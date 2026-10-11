//! Everything the guard does to the machine, behind a trait so the rescue
//! ladder can be driven against a simulated desktop in tests.

use std::fs;
use std::io::{Read, Seek, SeekFrom, Write};
use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

pub trait Desktop {
    /// None if Hyprland answered its socket, else why not.
    fn probe(&mut self, timeout: f64) -> Option<String>;
    /// True if some monitor is lit: only then must frames come out.
    fn screens_on(&mut self) -> bool;
    /// None if Hyprland rendered a frame for a 1x1 screencopy.
    fn render_probe(&mut self, timeout: f64) -> Option<String>;
    /// SIGCONT stopped desktop processes; returns what it woke.
    fn thaw_stopped(&mut self) -> Vec<String>;
    fn hypr_state(&mut self, pid: i32) -> Option<char>;
    /// (pid, display) of this session's Xwayland.
    fn xwayland(&mut self) -> Option<(i32, String)>;
    fn xwayland_ping(&mut self, display: &str) -> bool;
    fn restart_xwayland(&mut self, pid: i32) -> bool;
    fn shell_ping(&mut self) -> bool;
    fn keyboard_panel_open(&mut self) -> bool;
    /// Kill and relaunch the shell; (answers again, details).
    fn restart_shell(&mut self) -> (bool, String);
    fn kick_renderer(&mut self);
    fn dpms_cycle(&mut self);
    /// Abort Hyprland (SIGABRT, then SIGKILL); returns what happened.
    fn recover(&mut self, pid: i32) -> String;
    /// Write a diagnostic report; returns its path.
    fn report(&mut self, reason: &str, pid: Option<i32>, failures: &[String]) -> Option<PathBuf>;
    fn note(&mut self, report: &Option<PathBuf>, text: &str);
    fn notify(&mut self, summary: &str, body: &str, critical: bool);
    fn log(&mut self, text: &str);
}

// ---------------------------------------------------------------- helpers

pub fn read(path: impl AsRef<Path>) -> String {
    fs::read(path).map(|b| String::from_utf8_lossy(&b).into_owned()).unwrap_or_default()
}

pub fn tail(path: impl AsRef<Path>, nbytes: u64) -> String {
    let Ok(mut f) = fs::File::open(path) else { return String::new() };
    let len = f.seek(SeekFrom::End(0)).unwrap_or(0);
    let _ = f.seek(SeekFrom::Start(len.saturating_sub(nbytes)));
    let mut buf = Vec::new();
    let _ = f.read_to_end(&mut buf);
    String::from_utf8_lossy(&buf).into_owned()
}

/// Atomic write that survives a reset a few seconds later.
pub fn write_durable(path: &Path, text: &str) -> std::io::Result<()> {
    if let Some(dir) = path.parent() {
        fs::create_dir_all(dir)?;
    }
    let tmp = path.with_extension("tmp");
    {
        let mut f = fs::File::create(&tmp)?;
        f.write_all(text.as_bytes())?;
        f.sync_all()?;
    }
    fs::rename(&tmp, path)?;
    if let Some(dir) = path.parent() {
        if let Ok(d) = fs::File::open(dir) {
            let _ = d.sync_all();
        }
    }
    Ok(())
}

pub fn append_durable(path: &Path, text: &str) {
    if let Ok(mut f) = fs::OpenOptions::new().append(true).create(true).open(path) {
        let _ = f.write_all(text.as_bytes());
        let _ = f.write_all(b"\n");
        let _ = f.sync_all();
    }
}

/// Run a command with a timeout; stdout+stderr, or a note when it hung.
pub fn run(cmd: &[&str], timeout: f64, env: &[(&str, &str)]) -> (Option<i32>, String) {
    let mut c = Command::new(cmd[0]);
    c.args(&cmd[1..]).stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped());
    for (k, v) in env {
        c.env(k, v);
    }
    let Ok(mut child) = c.spawn() else { return (None, format!("({} not found)", cmd[0])) };
    let start = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                let mut out = String::new();
                if let Some(mut s) = child.stdout.take() {
                    let _ = s.read_to_string(&mut out);
                }
                if let Some(mut s) = child.stderr.take() {
                    let _ = s.read_to_string(&mut out);
                }
                return (status.code(), out.trim_end().to_string());
            }
            Ok(None) if start.elapsed().as_secs_f64() < timeout => std::thread::sleep(Duration::from_millis(20)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return (None, format!("(did not answer in {timeout} s: {})", cmd.join(" ")));
            }
        }
    }
}

pub fn proc_state(pid: i32) -> Option<char> {
    let stat = read(format!("/proc/{pid}/stat"));
    let close = stat.rfind(')')?;
    stat[close + 1..].trim_start().chars().next()
}

pub fn uid() -> u32 {
    unsafe { libc::getuid() }
}

pub fn kill(pid: i32, sig: i32) -> bool {
    unsafe { libc::kill(pid, sig) == 0 }
}

pub fn alive(pid: i32) -> bool {
    Path::new(&format!("/proc/{pid}")).exists()
}

pub fn pids_named(name: &str) -> Vec<i32> {
    use std::os::unix::fs::MetadataExt;
    let mut out = vec![];
    let me = uid();
    if let Ok(dir) = fs::read_dir("/proc") {
        for e in dir.flatten() {
            let Ok(pid) = e.file_name().to_string_lossy().parse::<i32>() else { continue };
            if read(e.path().join("comm")).trim() == name && e.metadata().map(|m| m.uid() == me).unwrap_or(false) {
                out.push(pid);
            }
        }
    }
    out.sort();
    out
}

pub fn threads(pid: i32) -> String {
    let mut tids: Vec<i32> = fs::read_dir(format!("/proc/{pid}/task"))
        .map(|d| d.flatten().filter_map(|e| e.file_name().to_string_lossy().parse().ok()).collect())
        .unwrap_or_default();
    tids.sort();
    let mut rows = vec![];
    for t in tids {
        let base = format!("/proc/{pid}/task/{t}");
        let stat = read(format!("{base}/stat"));
        let (Some(o), Some(c)) = (stat.find('('), stat.rfind(')')) else { continue };
        let name = &stat[o + 1..c];
        let state = stat[c + 1..].trim_start().chars().next().unwrap_or('?');
        let wchan = read(format!("{base}/wchan"));
        let wchan = if wchan.is_empty() { "-".to_string() } else { wchan };
        rows.push(format!("  {t:>8} {state} {wchan:<32} {name}"));
    }
    rows.join("\n")
}

pub fn now_stamp() -> String {
    let (_, out) = run(&["date", "+%Y%m%d-%H%M%S"], 3.0, &[]);
    out.trim().to_string()
}

pub fn omarchy_bin(name: &str) -> String {
    for dir in std::env::var("PATH").unwrap_or_default().split(':') {
        let p = Path::new(dir).join(name);
        if p.exists() {
            return p.to_string_lossy().into_owned();
        }
    }
    format!("/usr/share/omarchy/bin/{name}")
}

// ---------------------------------------------------------------- the real desktop

pub struct Paths {
    pub state: PathBuf,
    pub session_dir: PathBuf,
    pub runtime: PathBuf,
    pub instance: String,
}

impl Paths {
    pub fn from_env() -> Paths {
        let home = PathBuf::from(std::env::var("HOME").unwrap_or_else(|_| "/".into()));
        let state_home = std::env::var("XDG_STATE_HOME").map(PathBuf::from).unwrap_or_else(|_| home.join(".local/state"));
        Paths {
            state: state_home.join("omarchy-freeze"),
            session_dir: state_home.join("omarchy-session"),
            runtime: std::env::var("XDG_RUNTIME_DIR").map(PathBuf::from).unwrap_or_else(|_| PathBuf::from(format!("/run/user/{}", uid()))),
            instance: std::env::var("HYPRLAND_INSTANCE_SIGNATURE").unwrap_or_default(),
        }
    }
    pub fn hypr_dir(&self) -> PathBuf {
        self.runtime.join("hypr").join(&self.instance)
    }
    pub fn hypr_pid(&self) -> Option<i32> {
        let pid: i32 = read(self.hypr_dir().join("hyprland.lock")).split_whitespace().next()?.parse().ok()?;
        alive(pid).then_some(pid)
    }
}

pub struct RealDesktop {
    pub paths: Paths,
}

pub const NOISE: &[&str] = &["UFW BLOCK", "xkbcomp", "keycode", "Virtual modifier", "Using 0, ignoring", "LFSH", "Using Lock", "not fatal to the X"];

pub fn noisy(line: &str) -> bool {
    NOISE.iter().any(|n| line.contains(n))
}

impl RealDesktop {
    fn hypr_request(&self, cmd: &[u8], timeout: f64) -> std::io::Result<Vec<u8>> {
        let mut s = UnixStream::connect(self.paths.hypr_dir().join(".socket.sock"))?;
        let t = Some(Duration::from_secs_f64(timeout));
        s.set_read_timeout(t)?;
        s.set_write_timeout(t)?;
        s.write_all(cmd)?;
        let mut got = vec![];
        s.read_to_end(&mut got)?;
        Ok(got)
    }

    fn shell_pids(&self) -> Vec<i32> {
        pids_named("quickshell").into_iter().filter(|p| read(format!("/proc/{p}/cmdline")).replace('\0', " ").contains("/shell")).collect()
    }

    fn session_locked(&self) -> bool {
        run(&[&omarchy_bin("omarchy-hyprland-session-locked")], 5.0, &[]).0 == Some(0)
    }

    pub fn save_session_copy(&self) {
        let src = self.paths.session_dir.join("session.json");
        if src.exists() {
            let _ = write_durable(&self.paths.session_dir.join("session.freeze.json"), &read(&src));
        }
    }

    pub fn prune(&self, prefix: &str, suffix: &str, keep: usize) {
        prune_dir(&self.paths.state, prefix, suffix, keep);
    }
}

/// Keep the `keep` newest (by name) files `prefix*suffix` in dir.
pub fn prune_dir(dir: &Path, prefix: &str, suffix: &str, keep: usize) {
    let mut files: Vec<PathBuf> = fs::read_dir(dir)
        .map(|d| {
            d.flatten()
                .map(|e| e.path())
                .filter(|p| p.file_name().map(|n| { let n = n.to_string_lossy(); n.starts_with(prefix) && n.ends_with(suffix) }).unwrap_or(false))
                .collect()
        })
        .unwrap_or_default();
    files.sort();
    if files.len() > keep {
        for f in &files[..files.len() - keep] {
            let _ = fs::remove_file(f);
        }
    }
}

impl Desktop for RealDesktop {
    fn probe(&mut self, timeout: f64) -> Option<String> {
        match self.hypr_request(b"j/version", timeout) {
            Ok(b) if !b.is_empty() => None,
            Ok(_) => Some("empty answer".into()),
            Err(e) if e.kind() == std::io::ErrorKind::WouldBlock || e.kind() == std::io::ErrorKind::TimedOut => Some(format!("no answer in {timeout} s")),
            Err(e) => Some(e.to_string()),
        }
    }

    fn screens_on(&mut self) -> bool {
        let Ok(b) = self.hypr_request(b"j/monitors", 2.0) else { return false };
        let Ok(serde_json::Value::Array(mons)) = serde_json::from_slice(&b) else { return false };
        mons.iter().any(|m| m["dpmsStatus"].as_bool().unwrap_or(false) && !m["disabled"].as_bool().unwrap_or(false))
    }

    fn render_probe(&mut self, timeout: f64) -> Option<String> {
        match run(&["grim", "-g", "0,0 1x1", "-t", "ppm", "-"], timeout, &[]) {
            (None, out) if out.starts_with("(did not answer") => Some(format!("no frame in {timeout} s")),
            _ => None, // a quick refusal (locked session, no grim) is not a freeze
        }
    }

    fn thaw_stopped(&mut self) -> Vec<String> {
        let mut pids: Vec<(i32, &str)> = vec![];
        if let Some(p) = self.paths.hypr_pid() {
            pids.push((p, "Hyprland"));
        }
        for name in ["Xwayland", "quickshell"] {
            for p in pids_named(name) {
                pids.push((p, name));
            }
        }
        let mut woke = vec![];
        for (pid, name) in pids {
            if matches!(proc_state(pid), Some('T') | Some('t')) && kill(pid, libc::SIGCONT) {
                woke.push(format!("{name} ({pid})"));
            }
        }
        woke
    }

    fn hypr_state(&mut self, pid: i32) -> Option<char> {
        proc_state(pid)
    }

    fn xwayland(&mut self) -> Option<(i32, String)> {
        for pid in pids_named("Xwayland") {
            let args = read(format!("/proc/{pid}/cmdline"));
            if let Some(d) = args.split('\0').find(|a| a.len() > 1 && a.starts_with(':') && a[1..].chars().all(|c| c.is_ascii_digit())) {
                return Some((pid, d.to_string()));
            }
        }
        None
    }

    fn xwayland_ping(&mut self, display: &str) -> bool {
        match run(&["xprop", "-root", "_NET_SUPPORTING_WM_CHECK"], 3.0, &[("DISPLAY", display)]) {
            (Some(code), _) => code == 0,
            (None, out) => !out.starts_with("(did not answer"), // no xprop: can't tell, don't touch Xwayland
        }
    }

    fn restart_xwayland(&mut self, pid: i32) -> bool {
        kill(pid, libc::SIGKILL) // Hyprland starts a new one on demand; only X11 apps are lost
    }

    fn shell_ping(&mut self) -> bool {
        run(&[&omarchy_bin("omarchy-shell"), "shell", "ping"], 6.0, &[("OMARCHY_SHELL_IPC_TIMEOUT", "3s")]).0 == Some(0)
    }

    fn keyboard_panel_open(&mut self) -> bool {
        self.hypr_request(b"j/layers", 2.0).map(|b| String::from_utf8_lossy(&b).contains("omarchy-keyboard-panel")).unwrap_or(false)
    }

    /// `omarchy restart shell` asks the old shell to quit over IPC, which a hung
    /// shell never reads, so kill it: omarchy-launch-shell relaunches it.
    fn restart_shell(&mut self) -> (bool, String) {
        let locked = self.session_locked();
        let old = self.shell_pids();
        let mut lines = vec![format!("shell pids {old:?}, session {}", if locked { "locked" } else { "unlocked" })];
        for &pid in &old {
            lines.push(format!("  {pid} state {:?}\n{}", proc_state(pid), threads(pid)));
            kill(pid, libc::SIGKILL);
        }
        let mut ok = false;
        for i in 0..30 {
            std::thread::sleep(Duration::from_millis(500));
            if i == 10 && self.shell_pids().is_empty() {
                run(&["hyprctl", "dispatch", "hl.dsp.exec_cmd(\"omarchy-launch-shell\")"], 5.0, &[]);
                lines.push("no supervisor relaunch: started omarchy-launch-shell".into());
            }
            if !self.shell_pids().is_empty() && self.shell_ping() {
                ok = true;
                break;
            }
        }
        if ok && locked {
            run(&[&omarchy_bin("omarchy-shell"), "lock", "lock"], 10.0, &[]);
            lines.push("session re-locked".into());
        }
        (ok, lines.join("\n"))
    }

    fn kick_renderer(&mut self) {
        run(&["hyprctl", "dispatch", "hl.dsp.force_renderer_reload()"], 5.0, &[]);
        run(&["hyprctl", "dispatch", "hl.dsp.release_input_capture()"], 5.0, &[]);
    }

    fn dpms_cycle(&mut self) {
        run(&["hyprctl", "dispatch", "hl.dsp.dpms({ action = \"disable\" })"], 5.0, &[]);
        std::thread::sleep(Duration::from_millis(1500));
        run(&["hyprctl", "dispatch", "hl.dsp.dpms({ action = \"enable\" })"], 5.0, &[]);
    }

    fn recover(&mut self, pid: i32) -> String {
        self.save_session_copy();
        unsafe { libc::sync() };
        self.log(&format!("Hyprland (pid {pid}) frozen: SIGABRT"));
        if !kill(pid, libc::SIGABRT) {
            return "gone before the signal".into();
        }
        for _ in 0..15 {
            std::thread::sleep(Duration::from_secs(1));
            if !alive(pid) {
                return "aborted (core dump: coredumpctl info Hyprland)".into();
            }
        }
        if !kill(pid, libc::SIGKILL) {
            return "aborted".into();
        }
        std::thread::sleep(Duration::from_secs(5));
        if alive(pid) {
            "still alive after SIGKILL: stuck in the kernel (state D), only SysRq or a reset helps".into()
        } else {
            "killed".into()
        }
    }

    fn report(&mut self, reason: &str, pid: Option<i32>, failures: &[String]) -> Option<PathBuf> {
        let path = crate::report::write(&self.paths, reason, pid.or_else(|| self.paths.hypr_pid()), failures)?;
        self.prune("freeze-", ".txt", crate::report::KEEP_REPORTS);
        Some(path)
    }

    fn note(&mut self, report: &Option<PathBuf>, text: &str) {
        if let Some(p) = report {
            append_durable(p, text);
        }
    }

    fn notify(&mut self, summary: &str, body: &str, critical: bool) {
        run(&["notify-send", "-a", "Omarchy", "-u", if critical { "critical" } else { "normal" }, summary, body], 5.0, &[]);
    }

    fn log(&mut self, text: &str) {
        eprintln!("omarchy-freeze-guard: {text}");
    }
}
