//! Freeze reports, the durable copy of hyprland.log, and the verdict on how
//! the previous boot ended (with the kernel's last words from pstore).

use crate::desktop::{noisy, now_stamp, prune_dir, read, run, tail, threads, write_durable, Paths};
use std::fs;
use std::path::{Path, PathBuf};

pub const KEEP_REPORTS: usize = 20;
pub const KEEP_LOGS: usize = 5;
pub const LOG_TAIL_BYTES: u64 = 512 * 1024;

pub fn write(paths: &Paths, reason: &str, pid: Option<i32>, failures: &[String]) -> Option<PathBuf> {
    let (_, time) = run(&["date", "-Iseconds"], 3.0, &[]);
    let mut parts = vec![
        format!("omarchy-freeze-guard report: {reason}"),
        format!("time: {time}"),
        format!("boot: {}", read("/proc/sys/kernel/random/boot_id").trim()),
        format!("kernel: {}", read("/proc/sys/kernel/osrelease").trim()),
        format!("hyprland: pid {pid:?}, instance {}", paths.instance),
    ];
    if !failures.is_empty() {
        let last: Vec<String> = failures.iter().rev().take(10).rev().map(|f| format!("  {f}")).collect();
        parts.push(format!("probe failures (last 10):\n{}", last.join("\n")));
    }
    if let Some(pid) = pid {
        let status = read(format!("/proc/{pid}/status"));
        let state = status.lines().find_map(|l| l.strip_prefix("State:")).map(str::trim).unwrap_or("?");
        parts.push(format!("hyprland state: {state}  (D = stuck inside the kernel, usually the GPU driver; R/S = stuck in userspace)"));
        parts.push(format!("hyprland threads (tid, state, kernel wait channel, name):\n{}", threads(pid)));
    }
    parts.push(format!("load: {}", read("/proc/loadavg").trim()));
    for res in ["cpu", "memory", "io"] {
        parts.push(format!("pressure {res}:\n{}", read(format!("/proc/pressure/{res}")).trim_end()));
    }
    let avail = read("/proc/meminfo").lines().find_map(|l| l.strip_prefix("MemAvailable:").map(|v| v.trim().to_string())).unwrap_or("?".into());
    parts.push(format!("MemAvailable: {avail}"));
    let (_, smi) = run(&["nvidia-smi", "--query-gpu=name,driver_version,pstate,temperature.gpu,utilization.gpu,memory.used,memory.total,power.draw,clocks_event_reasons.active", "--format=csv"], 8.0, &[]);
    parts.push(format!("nvidia-smi:\n{smi}"));
    let (_, ps) = run(&["ps", "-eo", "pid,stat,wchan:28,etime,pcpu,rss,comm", "--sort=-pcpu"], 5.0, &[]);
    parts.push(format!("processes (top CPU):\n{}", ps.lines().take(26).collect::<Vec<_>>().join("\n")));
    let (_, journal) = run(&["journalctl", "-b", "--since", "-3min", "-o", "short-precise", "-q", "--no-pager"], 8.0, &[]);
    let j: Vec<&str> = journal.lines().filter(|l| !noisy(l)).collect();
    let j = j.join("\n");
    let j = if j.len() > 20000 { j[j.len() - 20000..].to_string() } else { j };
    parts.push(format!("journal, last 3 min (noise filtered):\n{j}"));
    parts.push(format!("hyprland.log tail:\n{}", tail(paths.hypr_dir().join("hyprland.log"), 24 * 1024)));
    let path = paths.state.join(format!("freeze-{}.txt", now_stamp()));
    write_durable(&path, &(parts.join("\n\n") + "\n")).ok()?;
    unsafe { libc::sync() };
    Some(path)
}

/// Copy the tail of hyprland.log to disk when it changed. Returns its size.
pub fn mirror_log(paths: &Paths, last: i64) -> i64 {
    let src = paths.hypr_dir().join("hyprland.log");
    let Ok(meta) = fs::metadata(&src) else { return last };
    let size = meta.len() as i64;
    if size != last {
        let _ = write_durable(&paths.state.join("hyprland-current.log"), &format!("# instance {}\n{}", paths.instance, tail(&src, LOG_TAIL_BYTES)));
    }
    size
}

/// A log left by another Hyprland instance is the one from before a crash/reset: keep it.
pub fn rotate_log(paths: &Paths) {
    let cur = paths.state.join("hyprland-current.log");
    let text = read(&cur);
    let first = text.lines().next().unwrap_or("");
    if !first.is_empty() && first != format!("# instance {}", paths.instance) {
        let (_, stamp) = run(&["date", "-r", &cur.to_string_lossy(), "+%Y%m%d-%H%M%S"], 3.0, &[]);
        let _ = fs::rename(&cur, paths.state.join(format!("hyprland-{}.log", stamp.trim())));
        prune_dir(&paths.state, "hyprland-2", ".log", KEEP_LOGS);
    }
}

#[derive(Debug, PartialEq, Clone, Copy)]
pub enum Verdict {
    Clean,
    Suspend,
    Reset,
    Unknown,
}

const CLEAN: &[&str] = &["Unmounted /", "System is powering down", "System is rebooting", "Journal stopped"];

fn clean_line(l: &str) -> bool {
    CLEAN.iter().any(|c| l.contains(c))
        || (l.contains("Reached target") && ["Shutdown", "Power-Off", "Reboot", "Final Step"].iter().any(|t| l.contains(t)))
}

/// Classify the last journal lines of the previous boot.
pub fn classify(lines: &[String]) -> (Verdict, Vec<String>) {
    if lines.is_empty() || lines[0].starts_with('(') {
        return (Verdict::Unknown, lines.to_vec());
    }
    let useful: Vec<String> = lines.iter().filter(|l| !noisy(l)).cloned().collect();
    let tail40: Vec<String> = useful.iter().rev().take(40).rev().cloned().collect();
    let last60 = &lines[lines.len().saturating_sub(60)..];
    if last60.iter().any(|l| clean_line(l)) {
        return (Verdict::Clean, tail40);
    }
    let last = lines[lines.len().saturating_sub(40)..].join("\n");
    if last.contains("PM: suspend entry") && !last.contains("PM: suspend exit") {
        return (Verdict::Suspend, tail40);
    }
    (Verdict::Reset, tail40)
}

/// Panic/oops records systemd-pstore saved from the previous boot (needs
/// stability/install.sh: systemd-pstore + panic on lockup).
pub fn pstore_records(dir: &Path) -> Vec<(String, String)> {
    let mut out = vec![];
    let Ok(entries) = fs::read_dir(dir) else { return out };
    let mut paths: Vec<PathBuf> = entries.flatten().map(|e| e.path()).collect();
    paths.sort();
    for p in paths.iter().rev().take(3) {
        let files: Vec<PathBuf> = if p.is_dir() { fs::read_dir(p).map(|d| d.flatten().map(|e| e.path()).collect()).unwrap_or_default() } else { vec![p.clone()] };
        for f in files {
            let text = read(&f);
            if !text.is_empty() {
                let lines: Vec<&str> = text.lines().collect();
                let keep = lines[lines.len().saturating_sub(60)..].join("\n");
                out.push((f.to_string_lossy().into_owned(), keep));
            }
        }
    }
    out
}

/// Once per boot: write a note (and notify) when the previous boot ended badly.
pub fn check_previous_boot(paths: &Paths, force: bool, notify: &mut dyn FnMut(&str, &str)) -> Verdict {
    let boot = read("/proc/sys/kernel/random/boot_id").trim().to_string();
    let marker = paths.state.join("checked-boot");
    if !force && read(&marker).trim() == boot {
        return Verdict::Unknown;
    }
    let (_, out) = run(&["journalctl", "-b", "-1", "-n", "300", "-o", "short-precise", "-q", "--no-pager"], 20.0, &[]);
    let lines: Vec<String> = out.lines().map(String::from).collect();
    let (verdict, last) = classify(&lines);
    let _ = fs::create_dir_all(&paths.state);
    let _ = fs::write(&marker, &boot);
    if matches!(verdict, Verdict::Clean | Verdict::Unknown) {
        return verdict;
    }
    let when = last.last().map(|l| l.chars().take(22).collect::<String>()).unwrap_or("?".into());
    let text = match verdict {
        Verdict::Suspend => format!("The previous boot went to sleep ({when}) and never woke up: a reset was needed."),
        _ => format!("The previous boot ended without a shutdown ({when}): reset, power loss or a hard lockup."),
    };
    let mut body = vec![text.clone(), String::new()];
    let newest = |prefix: &str, suffix: &str| -> Option<String> {
        let mut v: Vec<String> = fs::read_dir(&paths.state).ok()?.flatten().map(|e| e.file_name().to_string_lossy().into_owned()).filter(|n| n.starts_with(prefix) && n.ends_with(suffix)).collect();
        v.sort();
        v.pop().map(|n| paths.state.join(n).to_string_lossy().into_owned())
    };
    if let Some(r) = newest("freeze-", ".txt") {
        body.push(format!("Last freeze report: {r}"));
    }
    if let Some(l) = newest("hyprland-2", ".log") {
        body.push(format!("Hyprland log from before the reset: {l}"));
    }
    let pstore = pstore_records(Path::new("/var/lib/systemd/pstore"));
    if pstore.is_empty() {
        body.push("No kernel panic was saved (pstore empty): without the watchdog a hard lockup leaves no trace.".into());
    } else {
        body.push("\nKernel's last words (systemd-pstore):".into());
        for (f, t) in &pstore {
            body.push(format!("--- {f}\n{t}"));
        }
    }
    body.push(format!("\nLast journal lines of that boot:\n{}", last.join("\n")));
    let tag = if verdict == Verdict::Suspend { "suspend" } else { "reset" };
    let path = paths.state.join(format!("boot-{}-{tag}.txt", now_stamp()));
    let _ = write_durable(&path, &(body.join("\n") + "\n"));
    prune_dir(&paths.state, "boot-", ".txt", KEEP_REPORTS);
    notify("The last session ended badly", &format!("{text}\nDetails: {}", path.display()));
    verdict
}

#[cfg(test)]
mod tests {
    use super::*;

    fn l(v: &[&str]) -> Vec<String> {
        v.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn clean_shutdown() {
        let (v, _) = classify(&l(&["a", "systemd[1]: Reached target System Power Off.", "x"]));
        assert_eq!(v, Verdict::Reset, "only the listed targets count");
        let (v, _) = classify(&l(&["a", "systemd[1]: Reached target Power-Off.", "kernel: x"]));
        assert_eq!(v, Verdict::Clean);
        let (v, _) = classify(&l(&["a", "systemd-journald[1]: Journal stopped"]));
        assert_eq!(v, Verdict::Clean);
    }

    #[test]
    fn suspend_that_never_woke() {
        let (v, _) = classify(&l(&["a", "kernel: PM: suspend entry (deep)"]));
        assert_eq!(v, Verdict::Suspend);
        let (v, _) = classify(&l(&["kernel: PM: suspend entry (deep)", "kernel: PM: suspend exit", "rclone: x"]));
        assert_eq!(v, Verdict::Reset);
    }

    #[test]
    fn reset_drops_noise_and_unknown_when_no_journal() {
        let (v, last) = classify(&l(&["x kernel: [UFW BLOCK] IN=eno1", "rclone: ERROR token", "gcal ok"]));
        assert_eq!(v, Verdict::Reset);
        assert_eq!(last, l(&["rclone: ERROR token", "gcal ok"]));
        assert_eq!(classify(&[]).0, Verdict::Unknown);
        assert_eq!(classify(&l(&["(did not answer in 20 s)"])).0, Verdict::Unknown);
    }

    #[test]
    fn pstore_reads_newest_records() {
        let dir = std::env::temp_dir().join(format!("fg-pstore-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(dir.join("1791625913")).unwrap();
        fs::write(dir.join("1791625913/dmesg.txt"), "Kernel panic - not syncing: Hard LOCKUP\nCPU: 3").unwrap();
        let r = pstore_records(&dir);
        assert_eq!(r.len(), 1);
        assert!(r[0].1.contains("Hard LOCKUP"));
        assert!(pstore_records(&dir.join("missing")).is_empty());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn prune_keeps_the_newest() {
        let dir = std::env::temp_dir().join(format!("fg-prune-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        for i in 0..5 {
            fs::write(dir.join(format!("freeze-2026100{i}.txt")), "x").unwrap();
        }
        fs::write(dir.join("boot-x.txt"), "x").unwrap();
        prune_dir(&dir, "freeze-", ".txt", 2);
        let mut left: Vec<String> = fs::read_dir(&dir).unwrap().flatten().map(|e| e.file_name().to_string_lossy().into_owned()).collect();
        left.sort();
        assert_eq!(left, vec!["boot-x.txt", "freeze-20261003.txt", "freeze-20261004.txt"]);
        let _ = fs::remove_dir_all(&dir);
    }
}
