//! Reading the machine: /proc/meminfo, memory pressure, the user's app scopes
//! (cgroup v2) and which of them are protected work.

use crate::config::{Config, FREEZABLE};
use std::collections::{BTreeMap, HashSet};
use std::fs;
use std::path::{Path, PathBuf};

pub const GB: i64 = 1 << 30;
pub const MB: i64 = 1 << 20;

pub fn read(path: impl AsRef<Path>) -> String {
    fs::read(path).map(|b| String::from_utf8_lossy(&b).into_owned()).unwrap_or_default()
}

pub fn human(n: f64) -> String {
    if n.abs() >= GB as f64 {
        format!("{:.1} GB", n / GB as f64)
    } else {
        format!("{:.0} MB", n / MB as f64)
    }
}

pub fn meminfo() -> BTreeMap<String, i64> {
    parse_meminfo(&read("/proc/meminfo"))
}

pub fn parse_meminfo(text: &str) -> BTreeMap<String, i64> {
    let mut out = BTreeMap::new();
    for l in text.lines() {
        if let Some((k, v)) = l.split_once(':') {
            if let Some(n) = v.split_whitespace().next().and_then(|x| x.parse::<i64>().ok()) {
                out.insert(k.to_string(), n * 1024);
            }
        }
    }
    out
}

/// (some, full) avg10 of a pressure file.
pub fn parse_psi(text: &str) -> (f64, f64) {
    let (mut some, mut full) = (0.0, 0.0);
    for l in text.lines() {
        let val = l.split_whitespace().find_map(|w| w.strip_prefix("avg10=")).and_then(|x| x.parse().ok()).unwrap_or(0.0);
        if l.starts_with("some ") {
            some = val;
        } else if l.starts_with("full ") {
            full = val;
        }
    }
    (some, full)
}

pub fn user_cgroup() -> PathBuf {
    for l in read("/proc/self/cgroup").lines() {
        let path = l.rsplit("::").next().unwrap_or("");
        if let Some(i) = path.find("/user.slice/") {
            let rest = &path[i + 1..];
            // user.slice/user-1000.slice/user@1000.service[/...]
            let parts: Vec<&str> = rest.split('/').take(3).collect();
            if parts.len() == 3 && parts[2].starts_with("user@") {
                return Path::new("/sys/fs/cgroup").join(parts.join("/"));
            }
        }
    }
    let uid = unsafe { libc::getuid() };
    PathBuf::from(format!("/sys/fs/cgroup/user.slice/user-{uid}.slice/user@{uid}.service"))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Kind {
    Dev = 1,
    Ai = 2,
    User = 3,
    Desktop = 4,
}

impl Kind {
    pub fn as_str(self) -> &'static str {
        match self {
            Kind::Desktop => "desktop",
            Kind::User => "user",
            Kind::Ai => "ai",
            Kind::Dev => "dev",
        }
    }
}

const DESKTOP: &[&str] = &["Hyprland", "quickshell", "Xwayland", "pipewire", "pipewire-pulse", "wireplumber", "uwsm", "fcitx5", "hypridle",
    "hyprsunset", "swayosd-server", "mako", "walker", "elephant", "omarchy-freeze-", "omarchy-memguar", "omarchy-session", "xdg-desktop-por",
    "polkit-gnome-au", "gnome-keyring-d", "nwg-dock-hyprla", "rclone", "sshfs", "syncthing", "gvfsd", "gvfsd-fuse"];
const GPU_GUI: &[&str] = &["chrome", "chromium", "firefox", "electron", "Discord", "slack", "zoom", "telegram-deskto", "spotify", "obs", "mpv",
    "vlc", "steam", "steamwebhelper"];
const AI: &[&str] = &["ollama", "llama-server", "llama-cli", "llama-bench", "vllm", "tensorboard", "mlflow", "jupyter", "jupyter-lab",
    "jupyter-noteboo", "wandb", "wandb-service", "ray", "raylet", "gcs_server", "dockerd", "docker", "containerd", "containerd-shim",
    "nvidia-smi", "nvtop", "text-generation", "tgi", "triton", "uv", "pip", "conda", "mamba", "nvcc", "accelerate", "deepspeed", "torchrun",
    "huggingface-cli", "hf", "voxtype"];
const DEV: &[&str] = &["claude", "codex", "opencode", "gemini", "cursor-agent", "copilot", "aider", "antigravity-ide", "antigravity", "code",
    "cursor", "nvim", "vim", "emacs", "zed", "idea", "pycharm", "java", "node", "pyrefly", "pyright", "pyright-langse", "basedpyright", "ruff",
    "rust-analyzer", "gopls", "clangd", "jdtls", "cargo", "rustc", "gcc", "cc1", "cc1plus", "ld", "ld.lld", "make", "ninja", "cmake", "go",
    "gradle", "mvn", "latexmk", "pdflatex", "lualatex", "git", "ssh", "rsync", "tmux", "zellij", "foot", "alacritty", "kitty", "ghostty", "R",
    "rsession", "julia", "dbeaver", "terraform", "kubectl", "k9s", "pi"];
const AI_WORDS: &[&str] = &["torch", "tensorflow", "jax", "keras", "transformers", "unsloth", "accelerate", "deepspeed", "vllm", "lightning",
    "ipykernel", "jupyter", "langchain", "langgraph", "llama_index", "llama_cpp", "sentence_transformers", "diffusers", "peft", "trl",
    "bitsandbytes", "xgboost", "lightgbm", "sklearn", "ultralytics", "gradio", "streamlit", "mlflow", "tensorboard", "ray", "dask", "polars",
    "pandas", "duckdb", "chromadb", "qdrant", "litellm", "fastapi", "uvicorn"];

/// A word of AI_WORDS starting at a word boundary (like the Python \b(torch|...)\w*).
fn ai_cmdline(cmd: &str) -> bool {
    let b = cmd.as_bytes();
    AI_WORDS.iter().any(|w| {
        cmd.match_indices(w).any(|(i, _)| i == 0 || !(b[i - 1].is_ascii_alphanumeric() || b[i - 1] == b'_'))
    })
}

fn basename(cmdline: &str) -> &str {
    let first = cmdline.split(' ').next().unwrap_or("");
    first.rsplit('/').next().unwrap_or(first)
}

/// 'desktop', 'ai', 'dev' or None (an ordinary app).
pub fn classify(comm: &str, cmdline: &str, cuda: bool) -> Option<Kind> {
    let base = basename(cmdline);
    let is = |set: &[&str]| set.contains(&comm) || (!base.is_empty() && set.contains(&base));
    if is(DESKTOP) {
        return Some(Kind::Desktop);
    }
    if cuda && !is(GPU_GUI) && !cmdline.contains("--type=gpu-process") {
        return Some(Kind::Ai);
    }
    if is(AI) || comm.starts_with("pt_") {
        return Some(Kind::Ai);
    }
    let python = comm.starts_with("python") || base.starts_with("python");
    if python && ai_cmdline(cmdline) {
        return Some(Kind::Ai);
    }
    if is(DEV) || python {
        return Some(Kind::Dev); // a script someone is running: work until proven otherwise
    }
    None
}

#[derive(Debug, Clone)]
pub struct Scope {
    pub name: String,
    pub path: PathBuf,
    pub pids: Vec<i32>,
    pub current: i64,
    pub swap: i64,
    pub cpu: i64,
    pub pressure: (f64, f64),
    pub limit: f64,
    pub kind: Option<Kind>,
    pub why: String,
}

fn limit_of(p: &Path) -> f64 {
    read(p).trim().parse::<f64>().unwrap_or(f64::INFINITY)
}

impl Scope {
    pub fn read(path: &Path) -> Scope {
        let num = |f: &str| read(path.join(f)).trim().parse::<i64>().unwrap_or(0);
        let cpu = read(path.join("cpu.stat")).lines().find_map(|l| l.strip_prefix("usage_usec ").and_then(|v| v.trim().parse().ok())).unwrap_or(0);
        Scope {
            name: path.file_name().map(|n| n.to_string_lossy().into_owned()).unwrap_or_default(),
            path: path.to_path_buf(),
            pids: read(path.join("cgroup.procs")).split_whitespace().filter_map(|p| p.parse().ok()).collect(),
            current: num("memory.current"),
            swap: num("memory.swap.current"),
            cpu,
            pressure: parse_psi(&read(path.join("memory.pressure"))),
            limit: limit_of(&path.join("memory.max")).min(limit_of(&path.join("memory.high"))),
            kind: None,
            why: String::new(),
        }
    }
}

pub fn comm_of(pid: i32) -> String {
    read(format!("/proc/{pid}/comm")).trim().to_string()
}

pub fn cmdline_of(pid: i32) -> String {
    read(format!("/proc/{pid}/cmdline")).replace('\0', " ").trim().to_string()
}

fn unit_base(name: &str) -> &str {
    name.strip_suffix(".scope").or_else(|| name.strip_suffix(".service")).unwrap_or(name)
}

fn scope_matches(s: &Scope, names: &[String], comms: &HashSet<String>) -> bool {
    !names.is_empty() && names.iter().any(|n| *n == s.name || n == unit_base(&s.name) || comms.contains(n))
}

/// Classify a scope from its processes (comm, cmdline, cuda) and the user's exceptions.
pub fn classify_scope(s: &mut Scope, procs: &[(String, String, bool)], cfg: &Config) {
    let freezable: HashSet<&str> = FREEZABLE.iter().copied().chain(cfg.freezable.iter().map(String::as_str)).collect();
    if freezable.contains(s.name.as_str()) {
        s.why = "background job (pausable)".into();
        return;
    }
    let mut best: Option<(Kind, String)> = None;
    let mut comms = HashSet::new();
    for (comm, cmd, cuda) in procs {
        comms.insert(comm.clone());
        comms.insert(basename(cmd).to_string());
        if let Some(k) = classify(comm, cmd, *cuda) {
            if best.as_ref().map(|b| k > b.0).unwrap_or(true) {
                best = Some((k, comm.clone()));
            }
        }
    }
    comms.remove("");
    let desktop = best.as_ref().map(|b| b.0 == Kind::Desktop).unwrap_or(false);
    if scope_matches(s, &cfg.protect, &comms) && !desktop {
        best = Some((Kind::User, "your exception".into()));
    } else if scope_matches(s, &cfg.ordinary, &comms) && best.is_some() && !desktop {
        best = None;
        s.why = "your exception: ordinary".into();
    }
    if let Some((k, why)) = best {
        s.kind = Some(k);
        s.why = why;
    }
}

/// Every unit under app.slice, classified.
pub fn app_scopes(cuda: &HashSet<i32>, cfg: &Config) -> Vec<Scope> {
    let base = user_cgroup().join("app.slice");
    let Ok(dir) = fs::read_dir(&base) else { return vec![] };
    let mut paths: Vec<PathBuf> = dir.flatten().map(|e| e.path()).filter(|p| p.is_dir()).collect();
    paths.sort();
    let mut out = vec![];
    for p in paths {
        let n = p.file_name().unwrap().to_string_lossy().into_owned();
        if !(n.ends_with(".scope") || n.ends_with(".service")) {
            continue;
        }
        let mut s = Scope::read(&p);
        let procs: Vec<(String, String, bool)> = s.pids.iter().map(|&pid| (comm_of(pid), cmdline_of(pid), cuda.contains(&pid))).collect();
        classify_scope(&mut s, &procs, cfg);
        out.push(s);
    }
    out
}

/// Memory pressure felt by the innocent: the desktop, system services, and
/// every app that is not pressing against a limit of its own (global PSI also
/// counts a process thrashing inside its own cap, including the one memguard
/// capped, and would escalate for nothing).
pub fn effective_psi(scopes: &[Scope], throttled: &HashSet<String>, groups: Vec<(f64, f64)>) -> (f64, f64) {
    let mut g = groups;
    for s in scopes {
        if throttled.contains(&s.name) || (s.limit.is_finite() && s.current as f64 >= 0.9 * s.limit) {
            continue;
        }
        g.push(s.pressure);
    }
    if g.is_empty() {
        return parse_psi(&read("/proc/pressure/memory"));
    }
    (g.iter().map(|x| x.0).fold(0.0, f64::max), g.iter().map(|x| x.1).fold(0.0, f64::max))
}

pub fn base_groups() -> Vec<(f64, f64)> {
    vec![parse_psi(&read(user_cgroup().join("session.slice/memory.pressure"))), parse_psi(&read("/sys/fs/cgroup/system.slice/memory.pressure"))]
}

/// comm of the scope's biggest process.
pub fn dominant(s: &Scope) -> String {
    let mut best = (String::new(), -1i64);
    for &pid in &s.pids {
        let rss = read(format!("/proc/{pid}/status")).lines().find_map(|l| l.strip_prefix("VmRSS:").and_then(|v| v.split_whitespace().next()?.parse().ok())).unwrap_or(-1);
        if rss > best.1 {
            best = (comm_of(pid), rss);
        }
    }
    best.0
}

/// "app-Hyprland-foot-1234.scope" -> "foot".
pub fn short_name(unit: &str) -> String {
    let mut n = unit_base(unit);
    for p in ["app-Hyprland-", "app-flatpak-", "app-"] {
        if let Some(r) = n.strip_prefix(p) {
            n = r;
            break;
        }
    }
    let mut n = n.to_string();
    if let Some(i) = n.find('@') {
        n.truncate(i);
    }
    if let Some(i) = n.rfind('-') {
        let tail = &n[i + 1..];
        if tail.len() >= 6 && tail.chars().all(|c| c.is_ascii_hexdigit()) {
            n.truncate(i);
        }
    }
    n.replace("\\x2d", "-")
}

pub fn label(s: &Scope) -> String {
    let name = short_name(&s.name);
    let top = dominant(s);
    if !top.is_empty() && !name.to_lowercase().contains(&top.to_lowercase()) {
        format!("{name} ({top})")
    } else {
        name
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn kinds() {
        assert_eq!(classify("Hyprland", "/usr/bin/Hyprland", false), Some(Kind::Desktop));
        assert_eq!(classify("rclone", "rclone mount", false), Some(Kind::Desktop));
        assert_eq!(classify("python3", "python3 train.py --model x", true), Some(Kind::Ai), "CUDA user");
        assert_eq!(classify("chrome", "/opt/google/chrome/chrome --type=gpu-process", true), None, "video/WebGL is not AI");
        assert_eq!(classify("python3", "python3 -m ipykernel_launcher", false), Some(Kind::Ai));
        assert_eq!(classify("python3", "python3 -c import torch", false), Some(Kind::Ai));
        assert_eq!(classify("python3", "python3 myscript.py", false), Some(Kind::Dev));
        assert_eq!(classify("python3", "python3 -m mytorchx", false), Some(Kind::Dev), "torch inside a word does not count");
        assert_eq!(classify("pt_main_thread", "", false), Some(Kind::Ai));
        assert_eq!(classify("claude", "claude", false), Some(Kind::Dev));
        assert_eq!(classify("llama-server", "/usr/bin/llama-server --port 8080", false), Some(Kind::Ai));
        assert_eq!(classify("slack", "/usr/lib/slack/slack", false), None);
    }

    fn scope(name: &str) -> Scope {
        Scope { name: name.into(), path: PathBuf::from("/fake").join(name), pids: vec![1], current: GB, swap: 0, cpu: 0, pressure: (0.0, 0.0), limit: f64::INFINITY, kind: None, why: String::new() }
    }

    #[test]
    fn exceptions_by_process_unit_or_app_name() {
        let mut cfg = Config::default();
        cfg.protect = vec!["blender".into()];
        let mut s = scope("app-Hyprland-blender-abc123.scope");
        classify_scope(&mut s, &[("blender".into(), "/usr/bin/blender".into(), false)], &cfg);
        assert_eq!(s.kind, Some(Kind::User));
        cfg.protect = vec![];
        cfg.ordinary = vec!["code".into()];
        let mut s = scope("app-code.scope");
        classify_scope(&mut s, &[("code".into(), "/usr/bin/code".into(), false)], &cfg);
        assert_eq!(s.kind, None);
        assert_eq!(s.why, "your exception: ordinary");
        cfg.ordinary = vec!["Hyprland".into()];
        let mut s = scope("hypr.scope");
        classify_scope(&mut s, &[("Hyprland".into(), "Hyprland".into(), false)], &cfg);
        assert_eq!(s.kind, Some(Kind::Desktop), "the desktop can't be made ordinary");
        cfg.freezable = vec!["mine.service".into()];
        let mut s = scope("mine.service");
        classify_scope(&mut s, &[("python3".into(), "python3 x.py".into(), false)], &cfg);
        assert_eq!(s.kind, None, "a freezable job is not protected");
    }

    #[test]
    fn effective_psi_ignores_capped_scopes() {
        let mut capped = scope("leak.scope");
        capped.pressure = (90.0, 80.0);
        capped.limit = GB as f64;
        let mut ours = scope("ours.scope");
        ours.pressure = (70.0, 60.0);
        let mut victim = scope("victim.scope");
        victim.pressure = (20.0, 5.0);
        let throttled: HashSet<String> = ["ours.scope".to_string()].into();
        assert_eq!(effective_psi(&[capped.clone(), ours.clone(), victim], &throttled, vec![(1.0, 0.0)]), (20.0, 5.0));
        assert_eq!(effective_psi(&[capped, ours], &throttled, vec![(30.0, 2.0)]), (30.0, 2.0), "desktop/system count");
    }

    #[test]
    fn parsers_and_names() {
        assert_eq!(parse_psi("some avg10=12.50 avg60=1 avg300=0 total=1\nfull avg10=3.00 avg60=0 avg300=0 total=1\n"), (12.5, 3.0));
        let mi = parse_meminfo("MemTotal:       32000000 kB\nMemAvailable:   16000000 kB\n");
        assert_eq!(mi["MemTotal"], 32000000 * 1024);
        assert_eq!(short_name("app-Hyprland-foot-2b3c4d5e.scope"), "foot");
        assert_eq!(short_name("app-flatpak-com.slack.Slack@12345.scope"), "com.slack.Slack");
        assert_eq!(short_name("spotify-player.service"), "spotify-player");
        assert_eq!(short_name("app-uwsm\\x2dapp-x.scope"), "uwsm-app-x");
        assert_eq!(human(1.5 * GB as f64), "1.5 GB");
        assert_eq!(human(300.0 * MB as f64), "300 MB");
    }
}
