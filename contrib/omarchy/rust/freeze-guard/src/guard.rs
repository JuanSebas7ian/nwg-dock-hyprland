//! The rescue ladder. One step per tick; time comes from the caller so tests
//! drive it against a simulated desktop.

use crate::config::Config;
use crate::desktop::Desktop;
use std::collections::HashSet;
use std::path::PathBuf;

pub const SHELL_RESTARTS: (usize, f64) = (3, 900.0); // at most 3 shell restarts in 15 min

#[derive(Debug, PartialEq)]
pub enum Outcome {
    Watching,
    Recovered,
}

pub struct Guard {
    pub cfg: Config,
    pub since: Option<f64>, // time Hyprland stopped answering
    pub failures: Vec<String>,
    pub reported: Option<PathBuf>,
    pub actions: Vec<String>,
    x_checked: bool,
    shell_ok_seen: bool,
    pub shell_fail: u32,
    shell_next: f64,
    shell_restarts: Vec<f64>,
    pub render_since: Option<f64>,
    render_report: Option<PathBuf>,
    render_steps: HashSet<&'static str>,
    render_actions: Vec<String>,
    render_next: f64,
}

fn join(v: &[String]) -> String {
    v.join("; ")
}

impl Guard {
    pub fn new(cfg: Config) -> Guard {
        Guard {
            cfg,
            since: None,
            failures: vec![],
            reported: None,
            actions: vec![],
            x_checked: false,
            shell_ok_seen: false,
            shell_fail: 0,
            shell_next: 0.0,
            shell_restarts: vec![],
            render_since: None,
            render_report: None,
            render_steps: HashSet::new(),
            render_actions: vec![],
            render_next: 0.0,
        }
    }

    fn act(&mut self, d: &mut dyn Desktop, text: String) {
        d.log(&text);
        d.note(&self.reported, &format!("ACTION: {text}"));
        self.actions.push(text);
    }

    /// After a suspend: forget everything measured before it.
    pub fn reset_after_sleep(&mut self) {
        self.since = None;
        self.failures.clear();
        self.shell_fail = 0;
        self.reset_render();
    }

    pub fn tick(&mut self, d: &mut dyn Desktop, t: f64, pid: i32, clock: &str) -> Outcome {
        if self.cfg.get("thawStopped") > 0.0 {
            let woke = d.thaw_stopped();
            if !woke.is_empty() {
                self.act(d, format!("SIGCONT to stopped {}", woke.join(", ")));
            }
        }
        match d.probe(self.cfg.get("probeTimeout")) {
            None => {
                if let Some(since) = self.since {
                    let frozen = t - since;
                    let steps = if self.actions.is_empty() { " by itself".to_string() } else { format!(" (steps: {})", join(&self.actions)) };
                    d.note(&self.reported, &format!("\nTHAWED after {frozen:.0} s{steps}"));
                    if self.reported.is_some() || !self.actions.is_empty() {
                        let mut body = format!("Back after {frozen:.0} s.");
                        if !self.actions.is_empty() {
                            body += &format!("\nDone: {}", join(&self.actions));
                        }
                        if let Some(r) = &self.reported {
                            body += &format!("\nReport: {}", r.display());
                        }
                        d.notify("The desktop was frozen", &body, false);
                    }
                }
                self.since = None;
                self.failures.clear();
                self.reported = None;
                self.actions.clear();
                self.x_checked = false;
                if self.check_render(d, t, pid) == Outcome::Recovered {
                    return Outcome::Recovered;
                }
                if self.render_since.is_none() {
                    self.check_shell(d, t);
                }
                Outcome::Watching
            }
            Some(why) => {
                let since = *self.since.get_or_insert(t - self.cfg.get("probeTimeout"));
                self.failures.push(format!("{clock} {why}"));
                let frozen = t - since;
                if self.reported.is_none() && frozen >= self.cfg.get("reportAfter") {
                    self.reported = d.report(&format!("Hyprland has not answered for {frozen:.0} s"), Some(pid), &self.failures);
                    d.log(&format!("freeze report: {:?}", self.reported));
                }
                let xa = self.cfg.get("xwaylandAfter");
                if xa > 0.0 && !self.x_checked && frozen >= xa {
                    self.x_checked = true;
                    self.try_xwayland(d, pid);
                }
                let ra = self.cfg.get("recoverAfter");
                if ra > 0.0 && frozen >= ra {
                    let result = d.recover(pid);
                    self.act(d, format!("Hyprland restarted after {frozen:.0} s: {result}"));
                    return Outcome::Recovered;
                }
                Outcome::Watching
            }
        }
    }

    fn try_xwayland(&mut self, d: &mut dyn Desktop, pid: i32) {
        if d.hypr_state(pid) == Some('D') {
            d.note(&self.reported, "Hyprland is in state D (inside the kernel): Xwayland is not the cause");
            return;
        }
        let Some((xpid, disp)) = d.xwayland() else { return };
        if d.xwayland_ping(&disp) {
            d.note(&self.reported, &format!("Xwayland ({disp}) answers: not the cause"));
            return;
        }
        if d.restart_xwayland(xpid) {
            self.act(d, format!("Xwayland {disp} did not answer either: restarted it (X11 apps closed)"));
        }
    }

    pub fn reset_render(&mut self) {
        self.render_since = None;
        self.render_report = None;
        self.render_steps.clear();
        self.render_actions.clear();
    }

    fn render_act(&mut self, d: &mut dyn Desktop, text: String) {
        d.log(&text);
        d.note(&self.render_report, &format!("ACTION: {text}"));
        self.render_actions.push(text);
    }

    fn check_render(&mut self, d: &mut dyn Desktop, t: f64, pid: i32) -> Outcome {
        let every = self.cfg.get("renderEvery");
        if every <= 0.0 || (self.render_since.is_none() && t < self.render_next) {
            return Outcome::Watching;
        }
        self.render_next = t + every;
        if !d.screens_on() {
            self.reset_render();
            return Outcome::Watching;
        }
        let Some(why) = d.render_probe(self.cfg.get("renderTimeout")) else {
            if let Some(rs) = self.render_since {
                if self.render_report.is_some() || !self.render_actions.is_empty() {
                    let stuck = t - rs;
                    let done = join(&self.render_actions);
                    let steps = if done.is_empty() { String::new() } else { format!(" (steps: {done})") };
                    d.note(&self.render_report, &format!("\nFRAMES AGAIN after {stuck:.0} s{steps}"));
                    let mut body = format!("Back after {stuck:.0} s.");
                    if !done.is_empty() {
                        body += &format!("\nDone: {done}");
                    }
                    if let Some(r) = &self.render_report {
                        body += &format!("\nReport: {}", r.display());
                    }
                    d.notify("The screen was frozen", &body, false);
                }
            }
            self.reset_render();
            return Outcome::Watching;
        };
        let rs = *self.render_since.get_or_insert(t - self.cfg.get("renderTimeout"));
        let stuck = t - rs;
        let (kick, dpms, rec) = (self.cfg.get("renderKickAfter"), self.cfg.get("renderDpmsAfter"), self.cfg.get("renderRecoverAfter"));
        if kick > 0.0 && stuck >= kick && self.render_steps.insert("kick") {
            self.render_report = d.report(&format!("Hyprland answers but has rendered no frame for {stuck:.0} s ({why})"), Some(pid), &[]);
            d.kick_renderer();
            self.render_act(d, "force_renderer_reload + release_input_capture".into());
        }
        if dpms > 0.0 && stuck >= dpms && self.render_steps.insert("dpms") {
            d.dpms_cycle();
            self.render_act(d, "screens off and on (DPMS)".into());
        }
        if rec > 0.0 && stuck >= rec {
            if self.render_report.is_none() {
                self.render_report = d.report(&format!("no frame for {stuck:.0} s"), Some(pid), &[]);
            }
            let r = d.recover(pid);
            self.render_act(d, format!("Hyprland restarted after {stuck:.0} s without frames: {r}"));
            return Outcome::Recovered;
        }
        Outcome::Watching
    }

    fn check_shell(&mut self, d: &mut dyn Desktop, t: f64) {
        let mut sf = self.cfg.get("shellFailures");
        if sf <= 0.0 || t < self.shell_next {
            return;
        }
        let panel = d.keyboard_panel_open();
        if panel && self.cfg.get("shellPanelFailures") > 0.0 {
            sf = self.cfg.get("shellPanelFailures");
        }
        self.shell_next = t + if panel { self.cfg.get("shellPanelEvery") } else { self.cfg.get("shellEvery") };
        if d.shell_ping() {
            self.shell_ok_seen = true;
            self.shell_fail = 0;
            return;
        }
        if !self.shell_ok_seen {
            return; // still starting, or not part of this session
        }
        self.shell_fail += 1;
        if (self.shell_fail as f64) < sf {
            return;
        }
        self.shell_restarts.retain(|r| t - r < SHELL_RESTARTS.1);
        if self.shell_restarts.len() >= SHELL_RESTARTS.0 {
            if self.shell_fail as f64 == sf {
                d.notify("The Omarchy shell keeps freezing", "Restarted 3 times in 15 min; leaving it alone.\nTry: omarchy restart shell", true);
            }
            return;
        }
        let reason = format!(
            "the Omarchy shell has not answered {} checks in a row{}",
            self.shell_fail,
            if panel { " with a panel open (keyboard and clicks captured)" } else { "" }
        );
        let path = d.report(&reason, None, &[]);
        let (ok, out) = d.restart_shell();
        self.shell_restarts.push(t);
        // Keep watching even if it did not come back: the next misses try again (rate limited).
        self.shell_fail = 0;
        self.shell_ok_seen = true;
        d.note(&path, &format!("ACTION: shell killed and relaunched -> {}\n{out}", if ok { "answers again" } else { "still not answering" }));
        d.log(&format!("shell restarted: {}", if ok { "ok" } else { "failed" }));
        let p = path.map(|p| p.display().to_string()).unwrap_or_default();
        d.notify("The bar/shell was frozen", &format!("{}\nReport: {p}", if ok { "Restarted it." } else { "Restart did not help." }), !ok);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    /// A simulated desktop: each fault is a flag the test flips.
    #[derive(Default)]
    struct Fake {
        hypr_frozen: bool,
        hypr_d: bool,
        no_frames: bool,
        screens_off: bool,
        stopped: Vec<String>,
        x_alive: bool,
        no_xwayland: bool,
        shell_down: bool,
        shell_never_up: bool,
        panel: bool,
        shell_restart_fixes: bool,
        // what happened
        reports: Vec<String>,
        notes: Vec<String>,
        notifies: Vec<(String, bool)>,
        xrestarts: u32,
        recovers: u32,
        kicks: u32,
        dpms: u32,
        shell_restarts: u32,
        thawed: u32,
    }

    impl Desktop for Fake {
        fn probe(&mut self, timeout: f64) -> Option<String> {
            self.hypr_frozen.then(|| format!("no answer in {timeout} s"))
        }
        fn screens_on(&mut self) -> bool {
            !self.screens_off
        }
        fn render_probe(&mut self, timeout: f64) -> Option<String> {
            self.no_frames.then(|| format!("no frame in {timeout} s"))
        }
        fn thaw_stopped(&mut self) -> Vec<String> {
            let w = std::mem::take(&mut self.stopped);
            self.thawed += w.len() as u32;
            w
        }
        fn hypr_state(&mut self, _pid: i32) -> Option<char> {
            Some(if self.hypr_d { 'D' } else { 'S' })
        }
        fn xwayland(&mut self) -> Option<(i32, String)> {
            (!self.no_xwayland).then(|| (77, ":1".to_string()))
        }
        fn xwayland_ping(&mut self, _d: &str) -> bool {
            self.x_alive
        }
        fn restart_xwayland(&mut self, _pid: i32) -> bool {
            self.xrestarts += 1;
            true
        }
        fn shell_ping(&mut self) -> bool {
            !self.shell_down && !self.shell_never_up
        }
        fn keyboard_panel_open(&mut self) -> bool {
            self.panel
        }
        fn restart_shell(&mut self) -> (bool, String) {
            self.shell_restarts += 1;
            if self.shell_restart_fixes {
                self.shell_down = false;
            }
            (!self.shell_down, "details".into())
        }
        fn kick_renderer(&mut self) {
            self.kicks += 1;
        }
        fn dpms_cycle(&mut self) {
            self.dpms += 1;
        }
        fn recover(&mut self, _pid: i32) -> String {
            self.recovers += 1;
            "aborted".into()
        }
        fn report(&mut self, reason: &str, _pid: Option<i32>, _f: &[String]) -> Option<PathBuf> {
            self.reports.push(reason.into());
            Some(PathBuf::from(format!("/tmp/freeze-{}.txt", self.reports.len())))
        }
        fn note(&mut self, r: &Option<PathBuf>, text: &str) {
            if r.is_some() {
                self.notes.push(text.into());
            }
        }
        fn notify(&mut self, s: &str, _b: &str, critical: bool) {
            self.notifies.push((s.into(), critical));
        }
        fn log(&mut self, _t: &str) {}
    }

    fn guard() -> Guard {
        Guard::new(Config::default())
    }

    /// Run the daemon loop: a tick every 2 s from t0 to t1.
    fn run(g: &mut Guard, d: &mut Fake, t0: f64, t1: f64) -> Outcome {
        let mut t = t0;
        while t <= t1 {
            if g.tick(d, t, 100, "12:00:00") == Outcome::Recovered {
                return Outcome::Recovered;
            }
            t += 2.0;
        }
        Outcome::Watching
    }

    #[test]
    fn healthy_desktop_does_nothing() {
        let (mut g, mut d) = (guard(), Fake::default());
        assert_eq!(run(&mut g, &mut d, 0.0, 120.0), Outcome::Watching);
        assert!(d.reports.is_empty() && d.notifies.is_empty() && d.recovers == 0 && d.shell_restarts == 0);
    }

    #[test]
    fn stopped_processes_are_thawed_at_once() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.stopped = vec!["Xwayland (77)".into()];
        g.tick(&mut d, 0.0, 100, "t");
        assert_eq!(d.thawed, 1);
        let mut g2 = Guard::new(Config::parse(Some(r#"{"thawStopped":0}"#), |_| {}));
        d.stopped = vec!["Hyprland (100)".into()];
        g2.tick(&mut d, 0.0, 100, "t");
        assert_eq!(d.thawed, 1, "thawStopped=0 leaves them for gdb");
    }

    #[test]
    fn short_hiccup_thaws_by_itself_quietly() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.hypr_frozen = true;
        run(&mut g, &mut d, 0.0, 2.0); // frozen 4 s: below the report
        d.hypr_frozen = false;
        run(&mut g, &mut d, 4.0, 6.0);
        assert!(d.reports.is_empty() && d.notifies.is_empty());
        assert!(g.since.is_none());
    }

    #[test]
    fn ladder_report_then_xwayland_then_restart() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.hypr_frozen = true;
        assert_eq!(run(&mut g, &mut d, 0.0, 4.0), Outcome::Watching);
        assert!(d.reports.is_empty(), "no report before 8 s");
        run(&mut g, &mut d, 6.0, 6.0);
        assert_eq!(d.reports.len(), 1, "report at 8 s (6 + probe timeout)");
        run(&mut g, &mut d, 8.0, 10.0);
        assert_eq!(d.xrestarts, 1, "Xwayland frozen too: restarted at 12 s");
        assert_eq!(run(&mut g, &mut d, 12.0, 40.0), Outcome::Recovered);
        assert_eq!(d.recovers, 1);
        assert_eq!(d.reports.len(), 1, "one report per freeze");
        assert_eq!(d.xrestarts, 1, "Xwayland is restarted once per freeze, not every tick");
    }

    #[test]
    fn xwayland_left_alone_when_it_answers_or_hyprland_is_in_the_kernel() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.hypr_frozen = true;
        d.x_alive = true;
        run(&mut g, &mut d, 0.0, 20.0);
        assert_eq!(d.xrestarts, 0);
        assert!(d.notes.iter().any(|n| n.contains("answers: not the cause")));
        let (mut g, mut d) = (guard(), Fake::default());
        d.hypr_frozen = true;
        d.hypr_d = true;
        run(&mut g, &mut d, 0.0, 20.0);
        assert_eq!(d.xrestarts, 0);
        assert!(d.notes.iter().any(|n| n.contains("state D")));
    }

    #[test]
    fn freeze_that_ends_before_restart_notifies_with_the_steps() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.hypr_frozen = true;
        run(&mut g, &mut d, 0.0, 14.0);
        d.hypr_frozen = false;
        run(&mut g, &mut d, 16.0, 16.0);
        assert_eq!(d.recovers, 0);
        assert!(d.notes.iter().any(|n| n.contains("THAWED after")));
        assert_eq!(d.notifies.iter().filter(|(s, _)| s == "The desktop was frozen").count(), 1);
    }

    #[test]
    fn steps_set_to_zero_are_off() {
        let mut g = Guard::new(Config::parse(Some(r#"{"recoverAfter":0,"xwaylandAfter":0}"#), |_| {}));
        let mut d = Fake::default();
        d.hypr_frozen = true;
        assert_eq!(run(&mut g, &mut d, 0.0, 200.0), Outcome::Watching);
        assert_eq!(d.recovers + d.xrestarts, 0);
    }

    #[test]
    fn frozen_picture_kick_dpms_then_restart() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.no_frames = true;
        run(&mut g, &mut d, 0.0, 2.0);
        assert_eq!(d.kicks, 0);
        run(&mut g, &mut d, 4.0, 4.0);
        assert_eq!(d.kicks, 1, "kick at 6 s without frames");
        run(&mut g, &mut d, 6.0, 10.0);
        assert_eq!(d.dpms, 1, "DPMS at 12 s");
        assert_eq!(run(&mut g, &mut d, 12.0, 40.0), Outcome::Recovered);
        assert_eq!((d.kicks, d.dpms, d.recovers), (1, 1, 1));
    }

    #[test]
    fn picture_back_after_kick_notifies_and_resets() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.no_frames = true;
        run(&mut g, &mut d, 0.0, 6.0);
        d.no_frames = false;
        run(&mut g, &mut d, 8.0, 8.0);
        assert!(g.render_since.is_none());
        assert!(d.notifies.iter().any(|(s, _)| s == "The screen was frozen"));
    }

    #[test]
    fn screens_off_is_not_a_frozen_picture() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.no_frames = true;
        d.screens_off = true;
        assert_eq!(run(&mut g, &mut d, 0.0, 100.0), Outcome::Watching);
        assert_eq!(d.kicks + d.dpms + d.recovers, 0);
    }

    #[test]
    fn hung_shell_is_replaced_after_two_misses() {
        let (mut g, mut d) = (guard(), Fake::default());
        run(&mut g, &mut d, 0.0, 4.0); // seen answering once
        d.shell_down = true;
        d.shell_restart_fixes = true;
        run(&mut g, &mut d, 6.0, 20.0);
        assert_eq!(d.shell_restarts, 1);
        assert!(d.notifies.iter().any(|(s, c)| s == "The bar/shell was frozen" && !c));
    }

    #[test]
    fn shell_never_seen_up_is_left_alone() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.shell_never_up = true;
        run(&mut g, &mut d, 0.0, 120.0);
        assert_eq!(d.shell_restarts, 0);
    }

    #[test]
    fn shell_with_a_panel_open_is_checked_faster() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.panel = true;
        run(&mut g, &mut d, 0.0, 0.0);
        d.shell_down = true;
        d.shell_restart_fixes = true;
        // panel: every 3 s, 2 misses -> replaced within ~6-9 s
        let mut t = 2.0;
        while d.shell_restarts == 0 && t < 20.0 {
            g.tick(&mut d, t, 100, "t");
            t += 1.0;
        }
        assert_eq!(d.shell_restarts, 1);
        assert!(t <= 9.0, "took {t} s");
    }

    #[test]
    fn shell_restarts_are_rate_limited_and_retried_after_a_failed_restart() {
        let (mut g, mut d) = (guard(), Fake::default());
        run(&mut g, &mut d, 0.0, 0.0);
        d.shell_down = true; // restarts never fix it
        run(&mut g, &mut d, 2.0, 200.0);
        assert_eq!(d.shell_restarts, 3, "3 per 15 min");
        assert!(d.notifies.iter().any(|(s, c)| s == "The Omarchy shell keeps freezing" && *c));
        run(&mut g, &mut d, 1000.0, 1100.0);
        assert!(d.shell_restarts > 3, "tries again after the window");
    }

    #[test]
    fn shell_not_checked_while_hyprland_is_frozen_or_picture_stuck() {
        let (mut g, mut d) = (guard(), Fake::default());
        run(&mut g, &mut d, 0.0, 0.0);
        d.shell_down = true;
        d.hypr_frozen = true;
        let mut g2 = Guard::new(Config::parse(Some(r#"{"recoverAfter":0}"#), |_| {}));
        run(&mut g2, &mut d, 0.0, 60.0);
        assert_eq!(d.shell_restarts, 0);
        let _ = &mut g;
    }

    #[test]
    fn shell_not_checked_while_the_picture_is_stuck() {
        let mut g = Guard::new(Config::parse(Some(r#"{"renderRecoverAfter":0}"#), |_| {}));
        let mut d = Fake::default();
        run(&mut g, &mut d, 0.0, 0.0);
        d.no_frames = true;
        d.shell_down = true;
        run(&mut g, &mut d, 2.0, 60.0);
        assert_eq!(d.shell_restarts, 0, "a frozen picture also stops the shell from answering: not its fault");
    }

    #[test]
    fn panel_uses_its_own_failure_count() {
        let mut g = Guard::new(Config::parse(Some(r#"{"shellFailures":4,"shellPanelFailures":1}"#), |_| {}));
        let mut d = Fake::default();
        d.panel = true;
        run(&mut g, &mut d, 0.0, 0.0);
        d.shell_down = true;
        d.shell_restart_fixes = true;
        g.tick(&mut d, 4.0, 100, "t");
        assert_eq!(d.shell_restarts, 1, "one miss is enough with a panel holding the keyboard");
    }

    #[test]
    fn sleep_forgets_the_freeze() {
        let (mut g, mut d) = (guard(), Fake::default());
        d.hypr_frozen = true;
        run(&mut g, &mut d, 0.0, 20.0);
        g.reset_after_sleep();
        d.hypr_frozen = false;
        assert!(g.since.is_none() && g.failures.is_empty());
        run(&mut g, &mut d, 600.0, 610.0);
        assert_eq!(d.recovers, 0);
    }
}
