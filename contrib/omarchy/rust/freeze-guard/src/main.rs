//! omarchy-freeze-guard: detect desktop freezes, thaw them without losing
//! windows, and only as a last resort restart Hyprland. (Rust port of the
//! Python daemon; same config, files and behavior.)
//!
//!   omarchy-freeze-guard daemon          watch in the background (systemd user service)
//!   omarchy-freeze-guard rescue [--hard] thaw now (SUPER+CTRL+SHIFT+ESCAPE; also from a TTY/ssh)
//!   omarchy-freeze-guard report          write a diagnostic report now
//!   omarchy-freeze-guard last            how the previous boot ended + latest reports
//!
//! A hard lockup of the kernel stops this daemon too; that case is covered by
//! the hardware watchdog (contrib/omarchy/stability) and reported here after
//! the reboot, with the panic text systemd-pstore saved.

mod config;
mod desktop;
mod guard;
mod report;

use config::Config;
use desktop::{run, Desktop, Paths, RealDesktop};
use guard::{Guard, Outcome};
use std::time::Duration;

const MIRROR_EVERY: f64 = 15.0;

fn clock(id: libc::clockid_t) -> f64 {
    let mut ts = libc::timespec { tv_sec: 0, tv_nsec: 0 };
    unsafe { libc::clock_gettime(id, &mut ts) };
    ts.tv_sec as f64 + ts.tv_nsec as f64 / 1e9
}

fn hhmmss() -> String {
    let t = unsafe { libc::time(std::ptr::null_mut()) };
    let mut tm: libc::tm = unsafe { std::mem::zeroed() };
    unsafe { libc::localtime_r(&t, &mut tm) };
    format!("{:02}:{:02}:{:02}", tm.tm_hour, tm.tm_min, tm.tm_sec)
}

fn load_config() -> Config {
    let home = std::env::var("HOME").unwrap_or_default();
    let dir = std::env::var("XDG_CONFIG_HOME").unwrap_or(format!("{home}/.config"));
    let path = format!("{dir}/omarchy-freeze-guard/config.json");
    let text = std::fs::read_to_string(&path).ok();
    Config::parse(text.as_deref(), |w| eprintln!("omarchy-freeze-guard: {path}: {w}"))
}

fn daemon() -> i32 {
    let cfg = load_config();
    let paths = Paths::from_env();
    let _ = std::fs::create_dir_all(&paths.state);
    if paths.instance.is_empty() {
        eprintln!("omarchy-freeze-guard: HYPRLAND_INSTANCE_SIGNATURE not set");
        return 1;
    }
    report::rotate_log(&paths);
    let mut d = RealDesktop { paths: Paths::from_env() };
    report::check_previous_boot(&paths, false, &mut |s, b| d.notify(s, b, true));
    let every = cfg.get("probeEvery");
    eprintln!(
        "omarchy-freeze-guard: watching {}: socket (report {} s, Xwayland {} s, restart {} s), frames every {} s (kick {} s, DPMS {} s, restart {} s), shell every {} s",
        paths.instance, cfg.get("reportAfter"), cfg.get("xwaylandAfter"), cfg.get("recoverAfter"), cfg.get("renderEvery"),
        cfg.get("renderKickAfter"), cfg.get("renderDpmsAfter"), cfg.get("renderRecoverAfter"), cfg.get("shellEvery")
    );
    let mut g = Guard::new(cfg);
    let (mut size, mut mirrored) = (-1i64, 0.0f64);
    let (mut mono, mut boot) = (clock(libc::CLOCK_MONOTONIC), clock(libc::CLOCK_BOOTTIME));
    loop {
        std::thread::sleep(Duration::from_secs_f64(every));
        let (m, b) = (clock(libc::CLOCK_MONOTONIC), clock(libc::CLOCK_BOOTTIME));
        let slept = (b - boot) - (m - mono) > 5.0; // the machine was suspended in between
        mono = m;
        boot = b;
        let Some(pid) = d.paths.hypr_pid() else {
            eprintln!("omarchy-freeze-guard: Hyprland is gone; stopping");
            return 0;
        };
        if slept {
            g.reset_after_sleep();
            continue;
        }
        // A panic in one check must not leave the desktop unwatched.
        let r = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| g.tick(&mut d, m, pid, &hhmmss())));
        match r {
            Ok(Outcome::Recovered) => return 0,
            Ok(_) => {}
            Err(_) => eprintln!("omarchy-freeze-guard: tick panicked; continuing"),
        }
        if g.since.is_none() && g.render_since.is_none() && m - mirrored >= MIRROR_EVERY {
            size = report::mirror_log(&d.paths, size);
            mirrored = m;
        }
    }
}

/// Manual thaw: everything that does not lose windows; --hard also restarts Hyprland.
fn rescue(hard: bool) -> i32 {
    let mut d = RealDesktop { paths: Paths::from_env() };
    let mut done: Vec<String> = vec![];
    let woke = d.thaw_stopped();
    if !woke.is_empty() {
        done.push(format!("SIGCONT to {}", woke.join(", ")));
    }
    let pid = d.paths.hypr_pid();
    let alive = pid.is_some() && d.probe(2.0).is_none();
    if alive {
        run(&["hyprctl", "dispatch", "hl.dsp.submap(\"reset\")"], 3.0, &[]);
        d.kick_renderer();
        done.push("submap reset, renderer reload, input capture released".into());
    }
    if let Some((xpid, disp)) = d.xwayland() {
        if !d.xwayland_ping(&disp) && d.restart_xwayland(xpid) {
            done.push(format!("Xwayland {disp} restarted (X11 apps closed)"));
        }
    }
    if alive || pid.is_none() {
        if !d.shell_ping() {
            let (ok, _) = d.restart_shell();
            done.push(format!("shell restarted{}", if ok { "" } else { " (still not answering)" }));
        }
        if !desktop::pids_named("nwg-dock-hyprla").is_empty() {
            run(&["pkill", "-x", "nwg-dock-hyprla"], 3.0, &[]);
            done.push("dock restarted".into());
        }
    } else if let Some(pid) = pid {
        std::thread::sleep(Duration::from_secs(5));
        if d.probe(2.0).is_none() {
            done.push("Hyprland answers again".into());
        } else if hard {
            done.push(format!("Hyprland restarted: {}", d.recover(pid)));
        } else {
            done.push("Hyprland still frozen: the daemon restarts it at 30 s, or run: omarchy-freeze-guard rescue --hard".into());
        }
    }
    let path = d.report("manual rescue", None, &[]);
    let actions = if done.is_empty() { "ACTION: nothing needed".to_string() } else { done.iter().map(|x| format!("ACTION: {x}")).collect::<Vec<_>>().join("\n") };
    d.note(&path, &actions);
    let summary = if done.is_empty() { "nothing was stuck".to_string() } else { done.join("; ") };
    println!("{summary}");
    if let Some(p) = &path {
        println!("{}", p.display());
    }
    if alive {
        d.notify("Desktop rescue", &format!("{summary}\nReport: {}", path.map(|p| p.display().to_string()).unwrap_or_default()), false);
    }
    0
}

fn last() -> i32 {
    let paths = Paths::from_env();
    let (_, out) = run(&["journalctl", "-b", "-1", "-n", "300", "-o", "short-precise", "-q", "--no-pager"], 20.0, &[]);
    let lines: Vec<String> = out.lines().map(String::from).collect();
    let (verdict, last) = report::classify(&lines);
    println!("previous boot: {}", format!("{verdict:?}").to_lowercase());
    for l in last.iter().rev().take(15).rev() {
        println!("  {l}");
    }
    for (prefix, suffix) in [("boot-", ".txt"), ("freeze-", ".txt"), ("hyprland-2", ".log")] {
        let mut v: Vec<String> = std::fs::read_dir(&paths.state)
            .map(|d| d.flatten().map(|e| e.file_name().to_string_lossy().into_owned()).filter(|n| n.starts_with(prefix) && n.ends_with(suffix)).collect())
            .unwrap_or_default();
        v.sort();
        match v.last() {
            Some(n) => println!("{prefix}*{suffix}: {}, latest {}", v.len(), paths.state.join(n).display()),
            None => println!("{prefix}*{suffix}: 0"),
        }
    }
    let ps = report::pstore_records(std::path::Path::new("/var/lib/systemd/pstore"));
    println!("pstore (kernel panics saved): {}", ps.len());
    0
}

fn main() {
    // Die quietly on a closed pipe (`omarchy-freeze-guard last | head`) like other CLI tools.
    unsafe { libc::signal(libc::SIGPIPE, libc::SIG_DFL) };
    let args: Vec<String> = std::env::args().collect();
    let cmd = args.get(1).map(String::as_str).unwrap_or("daemon");
    let code = match cmd {
        "daemon" => daemon(),
        "rescue" => rescue(args.iter().any(|a| a == "--hard")),
        "report" => {
            let mut d = RealDesktop { paths: Paths::from_env() };
            match d.report("manual report", None, &[]) {
                Some(p) => {
                    println!("{}", p.display());
                    0
                }
                None => 1,
            }
        }
        "last" => last(),
        _ => {
            eprintln!("usage: omarchy-freeze-guard daemon | rescue [--hard] | report | last");
            3
        }
    };
    std::process::exit(code);
}
