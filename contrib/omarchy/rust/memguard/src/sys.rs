//! Everything the guard does to the machine (fake in tests).

use crate::nvml::Vram;
use crate::sample::{comm_of, cmdline_of, read, Scope};
use std::ffi::CString;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

pub trait Sys {
    fn unit_active(&mut self, unit: &str) -> bool;
    fn freeze_unit(&mut self, unit: &str) -> bool;
    fn thaw_unit(&mut self, unit: &str) -> bool;
    fn set_memory_high(&mut self, scope: &Path, value: &str) -> bool;
    fn memory_high(&mut self, scope: &Path) -> String;
    fn memory_current(&mut self, scope: &Path) -> i64;
    fn reclaim(&mut self, scope: &Path, bytes: i64) -> bool;
    fn set_avoid(&mut self, scope: &Path, on: bool) -> bool;
    fn has_avoid(&mut self, scope: &Path) -> bool;
    fn ollama_loaded(&mut self) -> Vec<String>;
    fn ollama_unload(&mut self, model: &str) -> bool;
    fn ollama_generating(&mut self, vram: &Vram) -> bool;
    fn comm_of(&mut self, pid: i32) -> String;
    fn is_main_ollama(&mut self, pid: i32) -> bool;
    fn voxtype_resident(&mut self, vram: &Vram) -> i64;
    fn voxtype_idle(&mut self) -> bool;
    fn voxtype_set_isolation(&mut self, on: bool) -> bool;
    fn notify(&mut self, summary: &str, body: &str, critical: bool);
    fn label(&mut self, s: &Scope) -> String;
    fn event(&mut self, line: &str);
    fn save_applied(&mut self, json: &str);
}

pub fn run(cmd: &[&str], timeout: f64) -> (i32, String) {
    let Ok(mut child) = Command::new(cmd[0]).args(&cmd[1..]).stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn() else {
        return (127, format!("{} not found", cmd[0]));
    };
    let start = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(st)) => {
                let mut out = String::new();
                if let Some(mut o) = child.stdout.take() {
                    let _ = o.read_to_string(&mut out);
                }
                if let Some(mut e) = child.stderr.take() {
                    let _ = e.read_to_string(&mut out);
                }
                return (st.code().unwrap_or(1), out.trim().to_string());
            }
            Ok(None) if start.elapsed().as_secs_f64() < timeout => std::thread::sleep(Duration::from_millis(20)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return (1, "timeout".into());
            }
        }
    }
}

/// Minimal HTTP/1.0 to the local Ollama (no TLS, no crates).
pub fn http(method: &str, path: &str, body: Option<&str>, timeout: f64) -> Option<String> {
    let mut s = TcpStream::connect_timeout(&"127.0.0.1:11434".parse().ok()?, Duration::from_secs(2)).ok()?;
    s.set_read_timeout(Some(Duration::from_secs_f64(timeout))).ok()?;
    let body = body.unwrap_or("");
    let req = format!("{method} {path} HTTP/1.0\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{body}", body.len());
    s.write_all(req.as_bytes()).ok()?;
    let mut resp = String::new();
    s.read_to_string(&mut resp).ok()?;
    let (head, rest) = resp.split_once("\r\n\r\n")?;
    head.split_whitespace().nth(1).filter(|c| c.starts_with('2'))?;
    Some(rest.to_string())
}

fn xattr_path(p: &Path) -> CString {
    CString::new(p.as_os_str().as_encoded_bytes()).unwrap_or_default()
}

pub struct RealSys {
    pub state: std::path::PathBuf,
}

impl Sys for RealSys {
    fn unit_active(&mut self, unit: &str) -> bool {
        let (_, out) = run(&["systemctl", "--user", "is-active", unit], 5.0);
        matches!(out.as_str(), "active" | "activating" | "reloading")
    }
    fn freeze_unit(&mut self, unit: &str) -> bool {
        run(&["systemctl", "--user", "freeze", unit], 10.0).0 == 0
    }
    fn thaw_unit(&mut self, unit: &str) -> bool {
        run(&["systemctl", "--user", "thaw", unit], 10.0).0 == 0
    }
    fn set_memory_high(&mut self, scope: &Path, value: &str) -> bool {
        std::fs::write(scope.join("memory.high"), value).is_ok()
    }
    fn memory_high(&mut self, scope: &Path) -> String {
        let v = read(scope.join("memory.high")).trim().to_string();
        if v.is_empty() { "max".into() } else { v }
    }
    fn memory_current(&mut self, scope: &Path) -> i64 {
        read(scope.join("memory.current")).trim().parse().unwrap_or(0)
    }
    /// Move up to `bytes` of cold memory to swap (zram). EAGAIN = less was reclaimable.
    fn reclaim(&mut self, scope: &Path, bytes: i64) -> bool {
        std::fs::write(scope.join("memory.reclaim"), bytes.to_string()).is_ok()
    }
    /// user.oomd_avoid: systemd-oomd picks this cgroup last.
    fn set_avoid(&mut self, scope: &Path, on: bool) -> bool {
        let p = xattr_path(scope);
        let name = CString::new("user.oomd_avoid").unwrap();
        unsafe {
            if on {
                libc::setxattr(p.as_ptr(), name.as_ptr(), b"1".as_ptr() as *const libc::c_void, 1, 0) == 0
            } else {
                libc::removexattr(p.as_ptr(), name.as_ptr()) == 0
            }
        }
    }
    fn has_avoid(&mut self, scope: &Path) -> bool {
        let p = xattr_path(scope);
        let mut buf = vec![0u8; 4096];
        let n = unsafe { libc::listxattr(p.as_ptr(), buf.as_mut_ptr() as *mut libc::c_char, buf.len()) };
        n > 0 && buf[..n as usize].split(|b| *b == 0).any(|x| x == b"user.oomd_avoid")
    }
    fn ollama_loaded(&mut self) -> Vec<String> {
        let Some(b) = http("GET", "/api/ps", None, 3.0) else { return vec![] };
        let v: serde_json::Value = serde_json::from_str(&b).unwrap_or_default();
        v["models"].as_array().map(|a| a.iter().filter_map(|m| m["name"].as_str().or(m["model"].as_str()).map(String::from)).collect()).unwrap_or_default()
    }
    fn ollama_unload(&mut self, model: &str) -> bool {
        let body = serde_json::json!({"model": model, "keep_alive": 0}).to_string();
        http("POST", "/api/generate", Some(&body), 15.0).is_some()
    }
    fn ollama_generating(&mut self, vram: &Vram) -> bool {
        vram.procs.iter().any(|p| self.is_main_ollama(p.pid) && p.sm >= 5)
    }
    fn comm_of(&mut self, pid: i32) -> String {
        comm_of(pid)
    }
    /// The Ollama memguard talks to (system ollama.service, port 11434); another
    /// instance (a test build on 11435) is just a process asking for VRAM.
    fn is_main_ollama(&mut self, pid: i32) -> bool {
        comm_of(pid).starts_with("ollama") && read(format!("/proc/{pid}/cgroup")).contains("/system.slice/ollama.service")
    }
    fn voxtype_resident(&mut self, vram: &Vram) -> i64 {
        vram.procs.iter().filter(|p| comm_of(p.pid).starts_with("voxtype") && !cmdline_of(p.pid).contains("osd")).map(|p| p.mem).sum()
    }
    /// Not recording or transcribing (never interrupt a dictation).
    fn voxtype_idle(&mut self) -> bool {
        let (rc, out) = run(&["voxtype", "status", "--format", "json"], 5.0);
        rc == 0 && out.lines().last().and_then(|l| serde_json::from_str::<serde_json::Value>(l).ok()).map(|v| v["alt"] == "idle").unwrap_or(false)
    }
    /// gpu_isolation = true: transcribe in a child process that frees the GPU afterwards.
    fn voxtype_set_isolation(&mut self, on: bool) -> bool {
        if run(&["voxtype", "config", "set", "whisper.gpu_isolation", if on { "true" } else { "false" }], 10.0).0 != 0 {
            return false;
        }
        run(&["systemctl", "--user", "restart", "voxtype.service"], 30.0).0 == 0
    }
    fn notify(&mut self, summary: &str, body: &str, critical: bool) {
        run(&["notify-send", "-a", "Omarchy", "-u", if critical { "critical" } else { "normal" }, summary, body], 5.0);
    }
    fn label(&mut self, s: &Scope) -> String {
        crate::sample::label(s)
    }
    fn event(&mut self, line: &str) {
        eprintln!("omarchy-memguard: {line}");
        let _ = std::fs::create_dir_all(&self.state);
        if let Ok(mut f) = std::fs::OpenOptions::new().append(true).create(true).open(self.state.join("events.log")) {
            let _ = writeln!(f, "{} {line}", local_time());
        }
    }
    fn save_applied(&mut self, json: &str) {
        let _ = std::fs::create_dir_all(&self.state);
        let tmp = self.state.join("applied.json.tmp");
        if std::fs::write(&tmp, json).is_ok() {
            let _ = std::fs::rename(tmp, self.state.join("applied.json"));
        }
    }
}

pub fn local_time() -> String {
    let t = unsafe { libc::time(std::ptr::null_mut()) };
    let mut tm: libc::tm = unsafe { std::mem::zeroed() };
    unsafe { libc::localtime_r(&t, &mut tm) };
    format!("{:04}-{:02}-{:02} {:02}:{:02}:{:02}", tm.tm_year + 1900, tm.tm_mon + 1, tm.tm_mday, tm.tm_hour, tm.tm_min, tm.tm_sec)
}

pub fn wall() -> f64 {
    std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_secs_f64()).unwrap_or(0.0)
}
