//! omarchy-memguard: keep RAM and VRAM usable through sudden or runaway
//! spikes, without killing anything (Rust port; same config, files and JSON).
//!
//!   omarchy-memguard daemon          watch (systemd user service)
//!   omarchy-memguard status [--json] now, percentiles of the last hour, what it is doing
//!   omarchy-memguard explain         which apps are protected and why
//!   omarchy-memguard smoke           live, read-only self-check (PASS/WARN/FAIL lines)
//!   omarchy-memguard ui [--brief]    everything the bar widget shows, as JSON
//!   omarchy-memguard set KEY VALUE   change a setting (validated; the daemon reloads it live)
//!   omarchy-memguard add|remove LIST NAME   exceptions: protect, ordinary, freezable
//!   omarchy-memguard reset [KEY]     back to the default (all settings without KEY)

mod config;
mod guard;
mod nvml;
mod sample;
mod sys;

use config::Config;
use guard::{Guard, Mem};
use sample::{human, read};
use serde_json::{json, Value};
use std::collections::HashSet;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use sys::{run, wall, RealSys, Sys};

static STOP: AtomicBool = AtomicBool::new(false);

extern "C" fn on_term(_: libc::c_int) {
    STOP.store(true, Ordering::SeqCst);
}

struct Paths {
    state: PathBuf,
    config: PathBuf,
}

fn paths() -> Paths {
    let home = PathBuf::from(std::env::var("HOME").unwrap_or_else(|_| "/".into()));
    let state = std::env::var("XDG_STATE_HOME").map(PathBuf::from).unwrap_or_else(|_| home.join(".local/state"));
    let cfg = std::env::var("XDG_CONFIG_HOME").map(PathBuf::from).unwrap_or_else(|_| home.join(".config"));
    Paths { state: state.join("omarchy-memguard"), config: cfg.join("omarchy-memguard/config.json") }
}

fn load_applied(p: &Paths) -> Value {
    serde_json::from_str(&read(p.state.join("applied.json"))).unwrap_or_else(|_| json!({}))
}

/// A previous instance that died may have left jobs frozen or apps capped.
/// Its oomd marks stay: they are protection, and the new instance takes them over.
fn undo_leftovers(p: &Paths, sys: &mut RealSys) -> Vec<String> {
    let data = load_applied(p);
    let mut done = vec![];
    for u in data["frozen"].as_array().cloned().unwrap_or_default() {
        if let Some(u) = u.as_str() {
            if sys.thaw_unit(u) {
                done.push(format!("thawed {u}"));
            }
        }
    }
    if let Some(m) = data["throttled"].as_object() {
        for (name, v) in m {
            let path = PathBuf::from(v[0].as_str().unwrap_or(""));
            let prev = v[1].as_str().unwrap_or("max");
            if path.exists() && sys.set_memory_high(&path, if prev.is_empty() { "max" } else { prev }) {
                done.push(format!("released {name}"));
            }
        }
    }
    sys.save_applied(&json!({"frozen": [], "throttled": {}, "avoided": data["avoided"].clone(), "voxtypeOffloaded": data["voxtypeOffloaded"].clone()}).to_string());
    done
}

fn cuda_pids(nv: &mut Option<nvml::Nvml>) -> (Option<nvml::Vram>, HashSet<i32>) {
    let v = nv.as_mut().map(|n| n.sample());
    let c = v.as_ref().map(|v| v.procs.iter().filter(|p| p.typ.contains('C')).map(|p| p.pid).collect()).unwrap_or_default();
    (v, c)
}

fn mtime(p: &PathBuf) -> Option<std::time::SystemTime> {
    std::fs::metadata(p).and_then(|m| m.modified()).ok()
}

fn write_status(p: &Paths, st: &Value) {
    let _ = std::fs::create_dir_all(&p.state);
    let tmp = p.state.join("status.json.tmp");
    if std::fs::write(&tmp, st.to_string()).is_ok() {
        let _ = std::fs::rename(tmp, p.state.join("status.json"));
    }
}

fn mono() -> f64 {
    let mut ts = libc::timespec { tv_sec: 0, tv_nsec: 0 };
    unsafe { libc::clock_gettime(libc::CLOCK_MONOTONIC, &mut ts) };
    ts.tv_sec as f64 + ts.tv_nsec as f64 / 1e9
}

fn daemon() -> i32 {
    let p = paths();
    let mut cfg = Config::load(&p.config);
    let _ = std::fs::create_dir_all(&p.state);
    let mut sys = RealSys { state: p.state.clone() };
    for l in undo_leftovers(&p, &mut sys) {
        eprintln!("omarchy-memguard: left over from a previous run: {l}");
    }
    let mut nv = nvml::Nvml::open();
    unsafe {
        libc::signal(libc::SIGTERM, on_term as *const () as libc::sighandler_t);
        libc::signal(libc::SIGINT, on_term as *const () as libc::sighandler_t);
    }
    eprintln!("omarchy-memguard: watching RAM{} every {} s", if nv.is_some() { " and VRAM" } else { "" }, cfg.get("every"));
    let mut g = Guard::new(cfg.clone(), &load_applied(&p));
    let mut mt = mtime(&p.config);
    while !STOP.load(Ordering::SeqCst) {
        let t = mono();
        if mtime(&p.config) != mt {
            // the widget (or a hand edit) changed it: no restart needed
            mt = mtime(&p.config);
            cfg = Config::load(&p.config);
            g.cfg = cfg.clone();
            sys.event("settings reloaded");
        }
        // one bad sample never stops the watch
        let r = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            let (vram, cuda) = cuda_pids(&mut nv);
            let scopes = sample::app_scopes(&cuda, &cfg);
            let throttled: HashSet<String> = g.throttled.keys().cloned().collect();
            let psi = sample::effective_psi(&scopes, &throttled, sample::base_groups());
            let mi = Mem::from_meminfo(&sample::meminfo());
            g.tick(&mut sys, t, &mi, psi, vram.as_ref(), &scopes, wall());
            write_status(&p, &g.status);
        }));
        if r.is_err() {
            eprintln!("omarchy-memguard: tick failed; continuing");
        }
        let every = cfg.get("every").max(0.5);
        let end = mono() + every;
        while mono() < end && !STOP.load(Ordering::SeqCst) {
            std::thread::sleep(std::time::Duration::from_millis(100));
        }
    }
    g.undo_all(&mut sys, "memguard stopped");
    0
}

fn status_cmd(as_json: bool) -> i32 {
    let p = paths();
    let st: Value = serde_json::from_str(&read(p.state.join("status.json"))).unwrap_or_else(|_| json!({}));
    if as_json {
        println!("{}", serde_json::to_string_pretty(&st).unwrap());
        return 0;
    }
    if st.as_object().map(|o| o.is_empty()).unwrap_or(true) {
        println!("no status yet (is omarchy-memguard.service running?)");
        return 1;
    }
    let r = &st["ram"];
    let age = wall() - st["t"].as_f64().unwrap_or(0.0);
    let reasons: Vec<String> = st["reasons"].as_array().map(|a| a.iter().filter_map(|x| x.as_str().map(String::from)).collect()).unwrap_or_default();
    println!("level {} (VRAM {}), {age:.0} s ago{}", st["level"], st["vramLevel"], if reasons.is_empty() { String::new() } else { format!(": {}", reasons.join("; ")) });
    let f = |v: &Value| v.as_f64().unwrap_or(0.0);
    println!("RAM   {} of {}, {} % available, pressure on work {} %, p95 {}, p99 {}", human(f(&r["used"])), human(f(&r["total"])), r["availPct"], r["psiSome"], human(f(&r["p95"])), human(f(&r["p99"])));
    if let Some(v) = st["vram"].as_object() {
        println!("VRAM  {} of {}, p95 {}, p99 {}", human(f(&v["used"])), human(f(&v["total"])), human(f(&v["p95"])), human(f(&v["p99"])));
        println!("dictation model: {}", st["dictation"].as_str().unwrap_or("?"));
    }
    for k in ["frozen", "throttled", "unloaded"] {
        if let Some(a) = st[k].as_array().filter(|a| !a.is_empty()) {
            println!("{k}: {}", a.iter().filter_map(|x| x.as_str()).collect::<Vec<_>>().join(", "));
        }
    }
    println!("protected apps: {}  (omarchy-memguard explain)", st["protected"].as_array().map(|a| a.len()).unwrap_or(0));
    0
}

fn explain() -> i32 {
    let p = paths();
    let cfg = Config::load(&p.config);
    let mut nv = nvml::Nvml::open();
    let (_, cuda) = cuda_pids(&mut nv);
    let mut scopes = sample::app_scopes(&cuda, &cfg);
    scopes.sort_by_key(|s| -s.current);
    for s in &scopes {
        let mark = match s.kind.map(|k| k.as_str()) {
            Some("desktop") => "desktop",
            Some("ai") => "AI     ",
            Some("dev") => "dev    ",
            Some("user") => "yours  ",
            _ => "-      ",
        };
        println!("{mark} {:>9}  {:<52} {}", human(s.current as f64), sample::label(s), if s.kind.is_some() { format!("protected: {}", s.why) } else { String::new() });
    }
    0
}

fn smoke() -> i32 {
    let p = paths();
    let mut rows: Vec<(&str, String)> = vec![];
    let mi = sample::meminfo();
    let avail = *mi.get("MemAvailable").unwrap_or(&0);
    rows.push((if avail > 0 { "PASS" } else { "FAIL" }, format!("meminfo readable ({} available)", human(avail as f64))));
    rows.push((if read("/proc/pressure/memory").is_empty() { "FAIL" } else { "PASS" }, "memory pressure (PSI) readable".into()));
    let app = sample::user_cgroup().join("app.slice");
    let writable = |f: &str| {
        let c = std::ffi::CString::new(app.join(f).as_os_str().as_encoded_bytes()).unwrap();
        unsafe { libc::access(c.as_ptr(), libc::W_OK) == 0 }
    };
    rows.push((if writable("cgroup.freeze") { "PASS" } else { "FAIL" }, "app.slice is delegated (freeze writable)".into()));
    rows.push((if writable("memory.reclaim") { "PASS" } else { "FAIL" }, "memory.reclaim writable".into()));
    let cfg = Config::load(&p.config);
    let scopes = sample::app_scopes(&HashSet::new(), &cfg);
    rows.push((if scopes.is_empty() { "FAIL" } else { "PASS" }, format!("{} app scopes, {} protected", scopes.len(), scopes.iter().filter(|s| s.kind.is_some()).count())));
    let c = std::ffi::CString::new(app.as_os_str().as_encoded_bytes()).unwrap();
    let mut buf = vec![0u8; 4096];
    let xa = unsafe { libc::listxattr(c.as_ptr(), buf.as_mut_ptr() as *mut libc::c_char, buf.len()) } >= 0;
    rows.push((if xa { "PASS" } else { "FAIL" }, "cgroup xattrs available (oomd avoid marks)".into()));
    let nv = nvml::Nvml::open().is_some();
    rows.push((if nv { "PASS" } else { "WARN" }, if nv { "NVML available (VRAM)".into() } else { "NVML not available: VRAM not watched".into() }));
    let (_, out) = run(&["systemctl", "--user", "is-active", "omarchy-memguard.service"], 5.0);
    rows.push((if out == "active" { "PASS" } else { "FAIL" }, format!("omarchy-memguard.service {out}")));
    match serde_json::from_str::<Value>(&read(p.state.join("status.json"))) {
        Ok(st) => {
            let age = wall() - st["t"].as_f64().unwrap_or(0.0);
            rows.push((if age < 15.0 { "PASS" } else { "FAIL" }, format!("status.json updated {age:.0} s ago")));
            let applied = st["frozen"].as_array().map(|a| !a.is_empty()).unwrap_or(false) || st["throttled"].as_array().map(|a| !a.is_empty()).unwrap_or(false);
            let stuck = applied && st["level"] == json!(0);
            rows.push((if stuck { "WARN" } else { "PASS" }, if stuck { format!("calm but still applied: {} {}", st["frozen"], st["throttled"]) } else { "nothing paused or capped while calm".into() }));
        }
        Err(_) => rows.push(("FAIL", "no status.json".into())),
    }
    if p.config.exists() {
        let ok = serde_json::from_str::<Value>(&read(&p.config)).map(|v| v.is_object()).unwrap_or(false);
        rows.push((if ok { "PASS" } else { "WARN" }, format!("{} {}", p.config.display(), if ok { "valid" } else { "invalid: defaults in use" })));
    } else {
        rows.push(("PASS", "no config file: defaults".into()));
    }
    for (s, t) in &rows {
        println!("{s} {t}");
    }
    i32::from(rows.iter().any(|(s, _)| *s == "FAIL"))
}

fn ui_state(brief: bool) -> Value {
    let p = paths();
    let st: Value = serde_json::from_str(&read(p.state.join("status.json"))).unwrap_or_else(|_| json!({}));
    let (_, out) = run(&["systemctl", "--user", "is-active", "omarchy-memguard.service"], 5.0);
    let has = st.as_object().map(|o| !o.is_empty()).unwrap_or(false);
    let age = if has { json!(((wall() - st["t"].as_f64().unwrap_or(0.0)) * 10.0).round() / 10.0) } else { Value::Null };
    let mut state = json!({"status": st, "service": if out.is_empty() { "unknown".to_string() } else { out }, "age": age});
    if brief {
        return state;
    }
    let cfg = Config::load(&p.config);
    let mut nv = nvml::Nvml::open(); // classify exactly as the daemon does
    let (_, cuda) = cuda_pids(&mut nv);
    let mut scopes = sample::app_scopes(&cuda, &cfg);
    scopes.sort_by_key(|s| -(s.current + s.swap));
    let capped: HashSet<String> = st["throttled"].as_array().map(|a| a.iter().filter_map(|x| x.as_str().map(String::from)).collect()).unwrap_or_default();
    let apps: Vec<Value> = scopes.iter().map(|s| {
        let proc_ = sample::dominant(s);
        let m = if config::valid_name(&proc_) { proc_ } else { s.name.trim_end_matches(".scope").trim_end_matches(".service").to_string() };
        json!({"unit": s.name, "label": sample::label(s), "kind": s.kind.map(|k| k.as_str()), "why": s.why, "match": m,
               "mem": s.current, "swap": s.swap, "capped": capped.contains(&s.name)})
    }).collect();
    let log = read(p.state.join("events.log"));
    let lines: Vec<&str> = log.lines().collect();
    let events: Vec<Value> = lines[lines.len().saturating_sub(60)..].iter().rev().map(|l| {
        let (w, t) = if l.len() > 20 { (&l[..19], &l[20..]) } else { (&l[..], "") };
        json!({"when": w, "text": t})
    }).collect();
    state["apps"] = json!(apps);
    state["events"] = json!(events);
    state["config"] = json!({"effective": cfg.to_json(), "user": config::user_config(&p.config), "defaults": config::defaults_json(),
                             "schema": config::schema_json(&cfg), "builtinFreezable": config::FREEZABLE});
    state
}

fn main() {
    unsafe { libc::signal(libc::SIGPIPE, libc::SIG_DFL) };
    let args: Vec<String> = std::env::args().collect();
    let cmd = args.get(1).map(String::as_str).unwrap_or("daemon");
    let arg = |i: usize| args.get(i).map(String::as_str);
    let code = match cmd {
        "daemon" => daemon(),
        "status" => status_cmd(args.iter().any(|a| a == "--json")),
        "explain" => explain(),
        "smoke" => smoke(),
        "ui" => {
            println!("{}", ui_state(args.iter().any(|a| a == "--brief")));
            0
        }
        "set" | "add" | "remove" | "reset" => {
            if cmd != "reset" && (arg(2).is_none() || arg(3).is_none()) {
                println!("{}", json!({"ok": false, "error": format!("usage: omarchy-memguard {cmd} KEY VALUE")}));
                3
            } else {
                match config::edit(&paths().config, cmd, arg(2), arg(3)) {
                    Ok(()) => {
                        println!("{}", json!({"ok": true, "error": ""}));
                        0
                    }
                    Err(e) => {
                        println!("{}", json!({"ok": false, "error": e}));
                        1
                    }
                }
            }
        }
        _ => {
            eprintln!("usage: omarchy-memguard daemon | status [--json] | explain | smoke | ui [--brief] | set KEY VALUE | add|remove LIST NAME | reset [KEY]");
            3
        }
    };
    std::process::exit(code);
}
