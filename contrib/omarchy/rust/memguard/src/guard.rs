//! The guard: one tick per sample. Made for an AI/ML workstation: high memory
//! use is normal and never "cleaned"; it acts only on sudden or runaway
//! spikes, and undoes everything once calm.

use crate::config::{never_freeze, Config, FREEZABLE};
use crate::nvml::Vram;
use crate::sample::{human, Kind, Scope, MB};
use crate::sys::Sys;
use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet, VecDeque};
use std::path::PathBuf;

/// One hour of samples for percentiles, and a short window for growth rates.
pub struct History {
    seconds: f64,
    samples: VecDeque<(f64, i64, Option<i64>)>,
}

impl History {
    pub fn new(seconds: f64) -> History {
        History { seconds, samples: VecDeque::new() }
    }
    pub fn add(&mut self, t: f64, ram: i64, vram: Option<i64>) {
        self.samples.push_back((t, ram, vram));
        while self.samples.front().map(|s| s.0 < t - self.seconds).unwrap_or(false) {
            self.samples.pop_front();
        }
    }
    fn val(s: &(f64, i64, Option<i64>), idx: usize) -> Option<i64> {
        if idx == 1 { Some(s.1) } else { s.2 }
    }
    pub fn percentile(&self, idx: usize, q: f64) -> Option<i64> {
        let mut v: Vec<i64> = self.samples.iter().filter_map(|s| Self::val(s, idx)).collect();
        if v.is_empty() {
            return None;
        }
        v.sort();
        let k = ((q / 100.0 * (v.len() - 1) as f64).round() as usize).min(v.len() - 1);
        Some(v[k])
    }
    /// Bytes per second over the last `window` seconds (positive = growing).
    pub fn rate(&self, idx: usize, t: f64, window: f64) -> f64 {
        let pts: Vec<(f64, i64)> = self.samples.iter().filter(|s| s.0 >= t - window).filter_map(|s| Self::val(s, idx).map(|v| (s.0, v))).collect();
        if pts.len() < 2 || pts[pts.len() - 1].0 == pts[0].0 {
            return 0.0;
        }
        (pts[pts.len() - 1].1 - pts[0].1) as f64 / (pts[pts.len() - 1].0 - pts[0].0)
    }
}

pub struct Guard {
    pub cfg: Config,
    pub hist: History,
    pub level: u8,
    pub vlevel: u8,
    calm_since: Option<f64>,
    pub frozen: BTreeMap<String, f64>,
    pub throttled: BTreeMap<String, (PathBuf, String)>,
    pub avoided: BTreeSet<String>,
    pub voxtype_offloaded: Option<f64>,
    voxtype_free_since: Option<f64>,
    reclaimed_at: HashMap<String, f64>,
    idle_since: HashMap<String, (i64, f64)>,
    last_scope_mem: HashMap<String, (f64, i64)>,
    scope_rate: HashMap<String, f64>,
    vram_seen: HashMap<i32, VecDeque<(f64, i64)>>,
    pub episode: bool,
    vepisode: bool,
    notified: u8,
    last_warn: f64,
    acted: bool,
    pub unloaded: Vec<String>,
    pub status: Value,
}

pub struct Mem {
    pub total: i64,
    pub avail: i64,
    pub swap_total: i64,
    pub swap_free: i64,
}

impl Mem {
    pub fn from_meminfo(mi: &BTreeMap<String, i64>) -> Mem {
        let total = *mi.get("MemTotal").unwrap_or(&1);
        let st = *mi.get("SwapTotal").unwrap_or(&0);
        Mem { total, avail: *mi.get("MemAvailable").unwrap_or(&total), swap_total: st, swap_free: *mi.get("SwapFree").unwrap_or(&st) }
    }
}

impl Guard {
    pub fn new(cfg: Config, applied: &Value) -> Guard {
        Guard {
            cfg,
            hist: History::new(3600.0),
            level: 0,
            vlevel: 0,
            calm_since: None,
            frozen: BTreeMap::new(),
            throttled: BTreeMap::new(),
            avoided: applied["avoided"].as_array().map(|a| a.iter().filter_map(|x| x.as_str().map(String::from)).collect()).unwrap_or_default(),
            voxtype_offloaded: applied["voxtypeOffloaded"].as_f64(),
            voxtype_free_since: None,
            reclaimed_at: HashMap::new(),
            idle_since: HashMap::new(),
            last_scope_mem: HashMap::new(),
            scope_rate: HashMap::new(),
            vram_seen: HashMap::new(),
            episode: false,
            vepisode: false,
            notified: 0,
            last_warn: -1e9,
            acted: false,
            unloaded: vec![],
            status: json!({}),
        }
    }

    fn event(&mut self, sys: &mut dyn Sys, text: &str) {
        sys.event(text);
    }

    pub fn applied_json(&self) -> String {
        let thr: serde_json::Map<String, Value> = self.throttled.iter().map(|(n, (p, prev))| (n.clone(), json!([p.to_string_lossy(), prev]))).collect();
        json!({"frozen": self.frozen.keys().collect::<Vec<_>>(), "throttled": thr, "avoided": self.avoided, "voxtypeOffloaded": self.voxtype_offloaded}).to_string()
    }

    fn save(&self, sys: &mut dyn Sys) {
        sys.save_applied(&self.applied_json());
    }

    /// (level, reasons, info) from RAM, swap, growth and pressure.
    pub fn ram_level(&self, t: f64, m: &Mem, psi: (f64, f64)) -> (u8, Vec<String>, Value) {
        let c = &self.cfg;
        let swap_used = m.swap_total - m.swap_free;
        let (some, full) = psi;
        let grow = self.hist.rate(1, t, 6.0);
        let tte = if grow > 64.0 * MB as f64 { m.avail as f64 / grow } else { f64::INFINITY };
        // Out of RAM with swap to spare only slows things down; the end of RAM *and*
        // swap crashes. Levels 2-3 need that, or processes already waiting.
        let tte_all = if grow > 64.0 * MB as f64 { (m.avail + m.swap_free) as f64 / grow } else { f64::INFINITY };
        let avail_pct = 100.0 * m.avail as f64 / m.total as f64;
        let swap_pct = if m.swap_total > 0 { 100.0 * swap_used as f64 / m.swap_total as f64 } else { 0.0 };
        let waiting = some > c.get("psiConfirm");
        let mut lvl = 0;
        if avail_pct < c.get("ramWarnAvailPct") || some > c.get("psiWarn") || tte < c.get("tteWarn") {
            lvl = 1;
        }
        if (avail_pct < c.get("ramCritAvailPct") && waiting) || some > c.get("psiCrit") || tte_all < c.get("tteCrit") || swap_pct > c.get("swapCritPct") {
            lvl = 2;
        }
        if (avail_pct < c.get("ramPanicAvailPct") && full > c.get("psiConfirm")) || full > c.get("psiCrit") || tte_all < c.get("ttePanic") {
            lvl = 3;
        }
        let mut reasons = vec![];
        if lvl > 0 {
            if avail_pct < c.get("ramWarnAvailPct") {
                reasons.push(format!("{avail_pct:.0} % RAM left"));
            }
            if some > c.get("psiWarn") {
                reasons.push(format!("waiting for memory {some:.0} % of the time"));
            }
            if tte < c.get("tteWarn") {
                let mut r = format!("growing {}/s, RAM full in {tte:.0} s", human(grow));
                if tte_all < c.get("tteWarn") {
                    r += &format!(", RAM+swap in {tte_all:.0} s");
                }
                reasons.push(r);
            }
            if swap_pct > c.get("swapCritPct") {
                reasons.push(format!("swap {swap_pct:.0} % used"));
            }
        }
        let r1 = |x: f64| (x * 10.0).round() / 10.0;
        let info = json!({"availPct": r1(avail_pct), "psiSome": some, "psiFull": full,
                          "tte": if tte.is_finite() { json!(r1(tte)) } else { Value::Null }, "swapPct": r1(swap_pct)});
        (lvl, reasons, info)
    }

    pub fn vram_level(&self, v: Option<&Vram>) -> u8 {
        let Some(v) = v else { return 0 };
        let free_mb = v.free as f64 / MB as f64;
        if free_mb < self.cfg.get("vramCritFreeMB") {
            2
        } else if free_mb < self.cfg.get("vramWarnFreeMB") {
            1
        } else {
            0
        }
    }

    fn status_json(&self, enabled: bool, lvl: u8, vlvl: u8, reasons: &[String], info: &Value, m: &Mem, vram: Option<&Vram>, scopes: &[Scope], wall: f64) -> Value {
        let mut ram = info.clone();
        ram["used"] = json!(m.total - m.avail);
        ram["total"] = json!(m.total);
        ram["p95"] = json!(self.hist.percentile(1, 95.0));
        ram["p99"] = json!(self.hist.percentile(1, 99.0));
        let v = vram.map(|v| json!({"used": v.used, "total": v.total, "free": v.free, "p95": self.hist.percentile(2, 95.0), "p99": self.hist.percentile(2, 99.0)}));
        let mut protected: Vec<&str> = scopes.iter().filter(|s| s.kind.is_some()).map(|s| s.name.as_str()).collect();
        protected.sort();
        let unl = &self.unloaded[self.unloaded.len().saturating_sub(5)..];
        json!({"t": wall, "enabled": enabled, "level": lvl, "vramLevel": vlvl, "reasons": reasons, "ram": ram, "vram": v,
               "frozen": if enabled { self.frozen.keys().cloned().collect::<Vec<_>>() } else { vec![] },
               "throttled": if enabled { self.throttled.keys().cloned().collect::<Vec<_>>() } else { vec![] },
               "protected": protected, "unloaded": unl,
               "dictation": if self.voxtype_offloaded.is_some() { "on demand (moved off the GPU)" } else { "resident" }})
    }

    pub fn tick(&mut self, sys: &mut dyn Sys, t: f64, mi: &Mem, psi: (f64, f64), vram: Option<&Vram>, scopes: &[Scope], wall: f64) -> u8 {
        if self.cfg.get("enabled") == 0.0 {
            return self.idle_tick(sys, t, mi, psi, vram, scopes, wall);
        }
        let used = mi.total - mi.avail;
        self.hist.add(t, used, vram.map(|v| v.used));
        let (lvl, reasons, info) = self.ram_level(t, mi, psi);
        let vlvl = self.vram_level(vram);
        self.protect(sys, scopes);
        self.track_idle(t, scopes);
        self.track_growth(t, scopes);
        self.track_vram(sys, t, vram);

        if lvl > 0 {
            self.calm_since = None;
            if lvl > self.level || !self.episode {
                self.event(sys, &format!("memory level {lvl}: {}", reasons.join("; ")));
            }
            if !self.episode {
                self.episode = true;
                self.notified = 0;
            }
            self.maybe_warn(sys, t, lvl, &reasons, &info, scopes);
            self.reclaim_idle(sys, t, scopes);
            if lvl >= 2 {
                self.freeze_batch(sys, t);
                self.throttle_runaway(sys, scopes, lvl >= 3);
            }
        } else if self.episode || !self.frozen.is_empty() || !self.throttled.is_empty() {
            let since = *self.calm_since.get_or_insert(t);
            if t - since >= self.cfg.get("calmSeconds") {
                self.release_calm(sys);
            }
        }
        self.level = lvl;

        self.maybe_reload_voxtype(sys, t, vram);
        if vlvl > 0 {
            if let Some(v) = vram {
                self.relieve_vram(sys, t, v, vlvl);
            }
        } else if self.vepisode {
            self.vepisode = false;
            self.event(sys, "VRAM back to normal");
        }
        self.vlevel = vlvl;

        // a job frozen for too long is thawed anyway: never stall work forever
        let limit = self.cfg.get("maxFreezeMin") * 60.0;
        for (unit, since) in self.frozen.clone() {
            if t - since >= limit {
                sys.thaw_unit(&unit);
                self.frozen.remove(&unit);
                self.event(sys, &format!("thawed {unit} after {} min (limit)", self.cfg.get("maxFreezeMin")));
                self.save(sys);
            }
        }
        self.status = self.status_json(true, lvl, vlvl, &reasons, &info, mi, vram, scopes, wall);
        lvl
    }

    /// Tell the user only what is worth an interruption: fast growth with swap to
    /// spare and nobody waiting is silent; low RAM or waiting warns at most every
    /// 15 min; level 2+ always, once per rise.
    fn maybe_warn(&mut self, sys: &mut dyn Sys, t: f64, lvl: u8, reasons: &[String], info: &Value, scopes: &[Scope]) {
        if lvl <= self.notified {
            return;
        }
        let real = info["availPct"].as_f64().unwrap_or(100.0) < self.cfg.get("ramWarnAvailPct") || info["psiSome"].as_f64().unwrap_or(0.0) > self.cfg.get("psiWarn");
        if lvl == 1 && (!real || t - self.last_warn < 900.0) {
            return;
        }
        self.notified = lvl;
        self.last_warn = t;
        let mut top: Vec<&Scope> = scopes.iter().collect();
        top.sort_by_key(|s| -s.current);
        let tops: Vec<String> = top.iter().take(5).map(|s| format!("{} {}", sys.label(s), human(s.current as f64))).collect();
        sys.notify(if lvl < 3 { "Memory is running low" } else { "Memory almost exhausted" }, &format!("{}\nTop: {}", reasons.join("; "), tops.join(", ")), lvl >= 2);
    }

    /// Switched off: undo what is applied, keep measuring for the widget, never act.
    fn idle_tick(&mut self, sys: &mut dyn Sys, t: f64, mi: &Mem, psi: (f64, f64), vram: Option<&Vram>, scopes: &[Scope], wall: f64) -> u8 {
        if !self.frozen.is_empty() || !self.throttled.is_empty() || self.episode {
            self.undo_all(sys, "memguard switched off");
        }
        self.hist.add(t, mi.total - mi.avail, vram.map(|v| v.used));
        let (_, _, info) = self.ram_level(t, mi, psi);
        self.status = self.status_json(false, 0, 0, &[], &info, mi, vram, scopes, wall);
        0
    }

    /// Mark protected scopes for oomd; unmark only the ones we marked.
    fn protect(&mut self, sys: &mut dyn Sys, scopes: &[Scope]) {
        let before = self.avoided.clone();
        for s in scopes {
            // Check the real mark every time: a remembered mark can be gone (the
            // unit was restarted under the same name), seen live on 2026-10-11.
            if s.kind.is_some() && !sys.has_avoid(&s.path) {
                if sys.set_avoid(&s.path, true) {
                    self.avoided.insert(s.name.clone());
                }
            } else if s.kind.is_none() && self.avoided.contains(&s.name) {
                sys.set_avoid(&s.path, false);
                self.avoided.remove(&s.name);
            }
        }
        let live: HashSet<&str> = scopes.iter().map(|s| s.name.as_str()).collect();
        self.avoided.retain(|n| live.contains(n.as_str()));
        if self.avoided != before {
            self.save(sys);
        }
    }

    fn track_idle(&mut self, t: f64, scopes: &[Scope]) {
        for s in scopes {
            let busy = match self.idle_since.get(&s.name) {
                None => true,
                Some(&(cpu, since)) => (s.cpu - cpu) as f64 > 2_000_000.0 * ((t - since) / 60.0).max(1.0), // > ~3 % of a core
            };
            if busy {
                self.idle_since.insert(s.name.clone(), (s.cpu, t));
            }
        }
        let live: HashSet<&str> = scopes.iter().map(|s| s.name.as_str()).collect();
        self.idle_since.retain(|n, _| live.contains(n.as_str()));
    }

    fn track_growth(&mut self, t: f64, scopes: &[Scope]) {
        self.scope_rate.clear();
        for s in scopes {
            let total = s.current + s.swap; // a capped scope keeps growing in swap
            if let Some(&(pt, pm)) = self.last_scope_mem.get(&s.name) {
                if t > pt {
                    self.scope_rate.insert(s.name.clone(), (total - pm) as f64 / (t - pt));
                }
            }
            self.last_scope_mem.insert(s.name.clone(), (t, total));
        }
        let live: HashSet<&str> = scopes.iter().map(|s| s.name.as_str()).collect();
        self.last_scope_mem.retain(|n, _| live.contains(n.as_str()));
    }

    /// Remember each non-Ollama process's VRAM, to tell who is asking for more.
    fn track_vram(&mut self, sys: &mut dyn Sys, t: f64, vram: Option<&Vram>) {
        let procs = vram.map(|v| v.procs.clone()).unwrap_or_default();
        for p in &procs {
            if sys.is_main_ollama(p.pid) || sys.comm_of(p.pid).starts_with("voxtype") {
                continue; // their own load/transcription is not competition
            }
            let h = self.vram_seen.entry(p.pid).or_default();
            h.push_back((t, p.mem));
            while h.front().map(|x| x.0 < t - 30.0).unwrap_or(false) {
                h.pop_front();
            }
        }
        let live: HashSet<i32> = procs.iter().map(|p| p.pid).collect();
        self.vram_seen.retain(|pid, _| live.contains(pid));
    }

    /// Bytes the fastest-growing non-Ollama GPU process added in the last 30 s.
    pub fn vram_competition(&self) -> i64 {
        self.vram_seen.values().filter(|h| h.len() >= 2).map(|h| h.back().unwrap().1 - h.iter().map(|x| x.1).min().unwrap()).max().unwrap_or(0)
    }

    fn reclaim_idle(&mut self, sys: &mut dyn Sys, t: f64, scopes: &[Scope]) {
        let idle_after = self.cfg.get("reclaimIdleMin") * 60.0;
        for s in scopes {
            if s.kind.is_some() || s.current < 64 * MB {
                continue;
            }
            let since = self.idle_since.get(&s.name).map(|x| x.1).unwrap_or(t);
            if t - since < idle_after || t - self.reclaimed_at.get(&s.name).copied().unwrap_or(-1e9) < 60.0 {
                continue;
            }
            self.reclaimed_at.insert(s.name.clone(), t);
            let before = s.current;
            sys.reclaim(&s.path, (self.cfg.get("reclaimMB") * MB as f64) as i64);
            let after = sys.memory_current(&s.path);
            if before - after > 16 * MB {
                let l = sys.label(s);
                self.event(sys, &format!("moved {} of idle {l} to zram", human((before - after) as f64)));
            }
        }
    }

    fn freeze_batch(&mut self, sys: &mut dyn Sys, t: f64) {
        let mut units: Vec<String> = FREEZABLE.iter().map(|s| s.to_string()).collect();
        for u in &self.cfg.freezable {
            if !units.contains(u) {
                units.push(u.clone());
            }
        }
        for unit in units {
            if self.frozen.contains_key(&unit) || never_freeze(&unit) || !sys.unit_active(&unit) {
                continue;
            }
            if sys.freeze_unit(&unit) {
                self.acted = true;
                self.frozen.insert(unit.clone(), t);
                self.event(sys, &format!("froze {unit} until memory is back to normal"));
                self.save(sys);
            }
        }
    }

    /// Cap the fastest-growing scope at its current size: it then reclaims from itself.
    fn throttle_runaway(&mut self, sys: &mut dyn Sys, scopes: &[Scope], protected_too: bool) {
        let mut best: Option<&Scope> = None;
        let mut best_rate = 0.0;
        for s in scopes {
            let Some(&rate) = self.scope_rate.get(&s.name) else { continue };
            if s.kind == Some(Kind::Desktop) || (s.kind.is_some() && !protected_too) || self.throttled.contains_key(&s.name) {
                continue;
            }
            if rate > best_rate {
                best = Some(s);
                best_rate = rate;
            }
        }
        let Some(b) = best else { return };
        if best_rate < 50.0 * MB as f64 {
            return;
        }
        let prev = sys.memory_high(&b.path);
        let cap = b.current.max(256 * MB);
        if sys.set_memory_high(&b.path, &cap.to_string()) {
            self.acted = true;
            self.throttled.insert(b.name.clone(), (b.path.clone(), prev));
            let l = sys.label(b);
            self.event(sys, &format!("slowed {l} (+{}/s): capped at {} until memory is back to normal", human(best_rate), human(cap as f64)));
            sys.notify("Runaway memory slowed down", &format!("{l} grew {}/s. It keeps running, capped at {} until there is room again. Nothing was closed.", human(best_rate), human(cap as f64)), true);
            self.save(sys);
        }
    }

    /// A full GPU is often intended: only act when someone else is asking for
    /// VRAM that an idle Ollama model (then the dictation model) is holding.
    fn relieve_vram(&mut self, sys: &mut dyn Sys, t: f64, v: &Vram, vlvl: u8) {
        if !self.vepisode {
            self.vepisode = true;
            self.event(sys, &format!("VRAM level {vlvl}: {} free", human(v.free as f64)));
            if vlvl >= 2 {
                let mut top = v.procs.clone();
                top.sort_by_key(|p| -p.mem);
                let users: Vec<String> = top.iter().take(5).map(|p| {
                    let c = sys.comm_of(p.pid);
                    format!("{} {}", if c.is_empty() { p.pid.to_string() } else { c }, human(p.mem as f64))
                }).collect();
                sys.notify("GPU memory is almost full", &format!("{} free of {}.\nUsing it: {}", human(v.free as f64), human(v.total as f64), users.join(", ")), true);
            }
        }
        if (self.vram_competition() as f64) < self.cfg.get("vramGrowMB") * MB as f64 || sys.ollama_generating(v) {
            return;
        }
        let mut freed = false;
        for model in sys.ollama_loaded() {
            if sys.ollama_unload(&model) {
                self.unloaded.push(model.clone());
                freed = true;
                self.event(sys, &format!("unloaded idle Ollama model {model} (it reloads on the next request)"));
            }
        }
        if freed || self.cfg.get("voxtypeOffload") == 0.0 || self.voxtype_offloaded.is_some() {
            return;
        }
        let held = sys.voxtype_resident(v);
        if held >= 256 * MB && sys.voxtype_idle() && sys.voxtype_set_isolation(true) {
            self.voxtype_offloaded = Some(t);
            self.voxtype_free_since = None;
            self.event(sys, &format!("dictation model moved off the GPU ({} freed); dictation keeps working, 1-2 s slower", human(held as f64)));
            sys.notify("Dictation moved off the GPU", &format!("{} of VRAM freed for your work. Dictation keeps working, 1-2 s slower, and goes back to instant when the VRAM is free again.", human(held as f64)), false);
            self.save(sys);
        }
    }

    /// Back to resident once the VRAM has been free for a while and nobody asks for more.
    fn maybe_reload_voxtype(&mut self, sys: &mut dyn Sys, t: f64, vram: Option<&Vram>) {
        let (Some(_), Some(v)) = (self.voxtype_offloaded, vram) else { return };
        let roomy = v.free as f64 >= self.cfg.get("voxtypeReloadFreeMB") * MB as f64 && (self.vram_competition() as f64) < self.cfg.get("vramGrowMB") * MB as f64;
        if !roomy {
            self.voxtype_free_since = None;
            return;
        }
        let since = *self.voxtype_free_since.get_or_insert(t);
        if t - since < self.cfg.get("voxtypeReloadCalmMin") * 60.0 || !sys.voxtype_idle() {
            return;
        }
        if sys.voxtype_set_isolation(false) {
            self.event(sys, "dictation model back on the GPU (instant dictation)");
            self.voxtype_offloaded = None;
            self.voxtype_free_since = None;
            self.save(sys);
        }
    }

    /// Calm long enough: thaw jobs and lift caps, except a cap on a scope still
    /// growing (into swap): lifting it would just restart the runaway.
    fn release_calm(&mut self, sys: &mut dyn Sys) {
        for unit in self.frozen.keys().cloned().collect::<Vec<_>>() {
            sys.thaw_unit(&unit);
            self.frozen.remove(&unit);
            self.event(sys, &format!("thawed {unit}"));
        }
        for (name, (path, prev)) in self.throttled.clone() {
            if self.scope_rate.get(&name).copied().unwrap_or(0.0) > 10.0 * MB as f64 {
                continue;
            }
            sys.set_memory_high(&path, if prev.is_empty() { "max" } else { &prev });
            self.throttled.remove(&name);
            self.event(sys, &format!("released the cap on {name}"));
        }
        self.save(sys);
        if self.throttled.is_empty() {
            self.undo_all(sys, "memory back to normal");
        }
    }

    pub fn undo_all(&mut self, sys: &mut dyn Sys, why: &str) {
        for unit in self.frozen.keys().cloned().collect::<Vec<_>>() {
            sys.thaw_unit(&unit);
            self.frozen.remove(&unit);
            self.event(sys, &format!("thawed {unit}"));
        }
        for (name, (path, prev)) in self.throttled.clone() {
            sys.set_memory_high(&path, if prev.is_empty() { "max" } else { &prev });
            self.throttled.remove(&name);
            self.event(sys, &format!("released the cap on {name}"));
        }
        if self.episode {
            self.event(sys, why);
            if self.acted {
                sys.notify("Memory is back to normal", "Everything that was paused or slowed down is running normally again.", false);
            }
        }
        self.acted = false;
        self.episode = false;
        self.calm_since = None;
        self.save(sys);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::nvml::GpuProc;
    use crate::sample::GB;
    use std::path::Path;

    #[derive(Default)]
    struct Fake {
        active: HashSet<String>,
        frozen: Vec<String>,
        thawed: Vec<String>,
        highs: HashMap<String, String>,
        reclaims: Vec<String>,
        notes: Vec<(String, bool)>,
        avoid: HashSet<String>,
        loaded: Vec<String>,
        unloaded: Vec<String>,
        ollama_sm: u32,
        vox_resident: i64,
        vox_idle: bool,
        vox_calls: Vec<bool>,
        currents: HashMap<String, i64>,
        events: Vec<String>,
    }

    fn name(p: &Path) -> String {
        p.file_name().unwrap().to_string_lossy().into_owned()
    }

    impl Sys for Fake {
        fn unit_active(&mut self, u: &str) -> bool { self.active.contains(u) }
        fn freeze_unit(&mut self, u: &str) -> bool { self.frozen.push(u.into()); true }
        fn thaw_unit(&mut self, u: &str) -> bool { self.thawed.push(u.into()); true }
        fn set_memory_high(&mut self, p: &Path, v: &str) -> bool { self.highs.insert(name(p), v.into()); true }
        fn memory_high(&mut self, _p: &Path) -> String { "max".into() }
        fn memory_current(&mut self, p: &Path) -> i64 { *self.currents.get(&name(p)).unwrap_or(&0) }
        fn reclaim(&mut self, p: &Path, _b: i64) -> bool { self.reclaims.push(name(p)); true }
        fn set_avoid(&mut self, p: &Path, on: bool) -> bool { if on { self.avoid.insert(name(p)); } else { self.avoid.remove(&name(p)); } true }
        fn has_avoid(&mut self, p: &Path) -> bool { self.avoid.contains(&name(p)) }
        fn ollama_loaded(&mut self) -> Vec<String> { self.loaded.clone() }
        fn ollama_unload(&mut self, m: &str) -> bool { self.loaded.retain(|x| x != m); self.unloaded.push(m.into()); true }
        fn ollama_generating(&mut self, _v: &Vram) -> bool { self.ollama_sm >= 5 }
        fn comm_of(&mut self, pid: i32) -> String { match pid { 900 | 901 => "ollama", 950 => "voxtype", _ => "python3" }.into() }
        fn is_main_ollama(&mut self, pid: i32) -> bool { pid == 900 }
        fn voxtype_resident(&mut self, _v: &Vram) -> i64 { self.vox_resident }
        fn voxtype_idle(&mut self) -> bool { self.vox_idle }
        fn voxtype_set_isolation(&mut self, on: bool) -> bool { self.vox_calls.push(on); self.vox_resident = if on { 0 } else { 1552 * MB }; true }
        fn notify(&mut self, s: &str, _b: &str, c: bool) { self.notes.push((s.into(), c)); }
        fn label(&mut self, s: &Scope) -> String { s.name.clone() }
        fn event(&mut self, l: &str) { self.events.push(l.into()); }
        fn save_applied(&mut self, _j: &str) {}
    }

    fn sc(name: &str, kind: Option<Kind>, mem: i64, busy: bool) -> (Scope, bool) {
        (Scope { name: name.into(), path: PathBuf::from("/fake").join(name), pids: vec![1], current: mem, swap: 0, cpu: 0,
                 pressure: (0.0, 0.0), limit: f64::INFINITY, kind, why: String::new() }, busy)
    }

    /// A typical session: a training run, the IDE, a terminal, the desktop, Chrome, Slack.
    fn ai_box(chrome_busy: bool) -> Vec<(Scope, bool)> {
        vec![sc("train.scope", Some(Kind::Ai), 8 * GB, true), sc("ide.scope", Some(Kind::Dev), 3 * GB, true),
             sc("foot.scope", Some(Kind::Dev), 200 * MB, false), sc("hyprland.scope", Some(Kind::Desktop), 300 * MB, true),
             sc("chrome.scope", None, 3 * GB, chrome_busy), sc("slack.scope", None, GB, false)]
    }

    struct Machine {
        total: i64,
        avail: i64,
        swap_total: i64,
        swap_free: i64,
        psi: (f64, f64),
        scopes: Vec<(Scope, bool)>,
        vram: Option<Vram>,
        sys: Fake,
    }

    impl Machine {
        fn new(scopes: Vec<(Scope, bool)>) -> Machine {
            let mut sys = Fake { vox_resident: 1552 * MB, vox_idle: true, loaded: vec!["qwen2.5-coder:7b".into()], ..Default::default() };
            sys.active.insert("gphotos-sync.service".into());
            Machine { total: 30 * GB, avail: 20 * GB, swap_total: 60 * GB, swap_free: 60 * GB, psi: (0.0, 0.0), scopes, vram: None, sys }
        }
        fn scope(&mut self, n: &str) -> &mut Scope {
            &mut self.scopes.iter_mut().find(|s| s.0.name == n).unwrap().0
        }
        /// Run `seconds`, calling `script(machine, t)` before each 2 s tick.
        fn run(&mut self, g: &mut Guard, from: f64, seconds: f64, mut script: impl FnMut(&mut Machine, f64)) -> Vec<u8> {
            let mut t = from;
            let mut levels = vec![];
            while t < from + seconds {
                t += 2.0;
                script(self, t);
                for (s, busy) in self.scopes.iter_mut() {
                    if *busy {
                        s.cpu += 2_000_000;
                    }
                }
                for (s, _) in &self.scopes {
                    self.sys.currents.insert(s.name.clone(), s.current);
                }
                let m = Mem { total: self.total, avail: self.avail.max(0), swap_total: self.swap_total, swap_free: self.swap_free };
                let scopes: Vec<Scope> = self.scopes.iter().map(|s| s.0.clone()).collect();
                let v = self.vram.clone();
                levels.push(g.tick(&mut self.sys, t, &m, self.psi, v.as_ref(), &scopes, t));
            }
            levels
        }
    }

    fn guard(cfg: &str) -> Guard {
        Guard::new(Config::parse(Some(cfg), |_| {}), &json!({}))
    }

    #[test]
    fn heavy_but_calm_ai_work_is_left_alone() {
        let mut m = Machine::new(ai_box(true));
        m.avail = 6 * GB; // 20 % free: busy, not in danger
        let mut g = guard("{}");
        let lv = m.run(&mut g, 0.0, 120.0, |_, _| {});
        assert!(lv.iter().all(|l| *l == 0));
        assert!(m.sys.frozen.is_empty() && m.sys.highs.is_empty() && m.sys.notes.is_empty() && m.sys.reclaims.is_empty());
    }

    #[test]
    fn protected_scopes_are_marked_and_unmarked() {
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 4.0, |_, _| {});
        assert!(m.sys.avoid.contains("train.scope") && m.sys.avoid.contains("hyprland.scope") && !m.sys.avoid.contains("chrome.scope"));
        m.scope("ide.scope").kind = None;
        m.run(&mut g, 4.0, 4.0, |_, _| {});
        assert!(!m.sys.avoid.contains("ide.scope"));
        // a mark systemd set itself is not ours to remove
        let mut m2 = Machine::new(vec![sc("own.scope", None, GB, false)]);
        m2.sys.avoid.insert("own.scope".into());
        let mut g2 = guard("{}");
        m2.run(&mut g2, 0.0, 6.0, |_, _| {});
        assert!(m2.sys.avoid.contains("own.scope"));
    }

    #[test]
    fn a_remembered_mark_that_is_gone_is_set_again() {
        let mut m = Machine::new(ai_box(true));
        let mut g = Guard::new(Config::default(), &json!({"avoided": ["train.scope"]}));
        m.run(&mut g, 0.0, 2.0, |_, _| {});
        assert!(m.sys.avoid.contains("train.scope"));
    }

    #[test]
    fn loading_a_dataset_fast_with_swap_to_spare_only_warns() {
        // +1 GB/s for 16 s from 20 GB free: RAM fills, but 60 GB of zram/swap remain and nobody waits.
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        let lv = m.run(&mut g, 0.0, 120.0, |m, t| {
            if t <= 16.0 {
                m.avail -= 2 * GB;
                m.scope("train.scope").current += 2 * GB;
            }
        });
        assert!(lv.iter().all(|l| *l <= 1), "{lv:?}");
        assert!(m.sys.frozen.is_empty() && m.sys.highs.is_empty(), "no pause, no cap for a legitimate load");
    }

    #[test]
    fn very_low_ram_without_anyone_waiting_does_not_act() {
        let mut m = Machine::new(ai_box(true));
        m.avail = 3 * GB / 2; // 5 %: below the critical line, but zram absorbs it and nobody waits
        let mut g = guard("{}");
        let lv = m.run(&mut g, 0.0, 20.0, |_, _| {});
        assert!(lv.iter().all(|l| *l == 1), "{lv:?}");
        assert!(m.sys.frozen.is_empty() && m.sys.highs.is_empty());
        let lv = m.run(&mut g, 20.0, 6.0, |m, _| m.psi = (8.0, 0.0)); // now work waits
        assert_eq!(*lv.last().unwrap(), 2);
    }

    #[test]
    fn short_pressure_blip_does_not_escalate() {
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        let lv = m.run(&mut g, 0.0, 30.0, |m, t| m.psi = if (6.0..=8.0).contains(&t) { (12.0, 1.0) } else { (0.0, 0.0) });
        assert!(lv.iter().all(|l| *l <= 1));
        assert!(m.sys.frozen.is_empty());
    }

    fn leak(m: &mut Machine, _t: f64) {
        m.avail -= 600 * MB;
        m.swap_free -= 2 * GB;
        m.psi = (30.0, 3.0);
        m.scope("chrome.scope").current += 600 * MB;
    }

    #[test]
    fn runaway_app_is_capped_batch_paused_work_untouched() {
        let mut m = Machine::new(ai_box(true));
        m.avail = 3 * GB;
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 10.0, leak);
        assert_eq!(m.sys.frozen, vec!["gphotos-sync.service"]);
        assert!(m.sys.highs.contains_key("chrome.scope"));
        assert!(!m.sys.highs.contains_key("train.scope") && !m.sys.highs.contains_key("hyprland.scope"));
        assert!(m.sys.notes.iter().any(|(s, c)| s == "Runaway memory slowed down" && *c));
    }

    #[test]
    fn everything_is_undone_once_calm_but_waits_for_a_real_calm() {
        let mut m = Machine::new(ai_box(true));
        m.avail = 3 * GB;
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 10.0, leak);
        m.avail = 12 * GB;
        m.swap_free = 50 * GB;
        m.psi = (0.0, 0.0);
        m.run(&mut g, 10.0, 20.0, |_, _| {});
        assert!(m.sys.thawed.is_empty(), "20 s of calm is not enough");
        m.run(&mut g, 30.0, 16.0, |_, _| {});
        assert_eq!(m.sys.thawed, vec!["gphotos-sync.service"]);
        assert_eq!(m.sys.highs["chrome.scope"], "max");
        assert!(m.sys.notes.iter().any(|(s, _)| s == "Memory is back to normal"));
    }

    #[test]
    fn cap_stays_while_the_capped_app_keeps_leaking_into_swap() {
        let mut m = Machine::new(ai_box(true));
        m.avail = 3 * GB;
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 10.0, leak);
        m.avail = 12 * GB;
        m.psi = (0.0, 0.0);
        m.run(&mut g, 10.0, 60.0, |m, _| m.scope("chrome.scope").swap += 100 * MB);
        assert_ne!(m.sys.highs["chrome.scope"], "max");
        assert!(g.throttled.contains_key("chrome.scope"));
    }

    #[test]
    fn protected_runaway_is_capped_only_when_the_machine_is_about_to_die() {
        let mut m = Machine::new(ai_box(true));
        m.avail = 3 * GB;
        let mut g = guard("{}");
        let grow = |m: &mut Machine, _t: f64| {
            m.avail -= 300 * MB;
            m.swap_free -= GB;
            m.psi = (30.0, 3.0);
            m.scope("train.scope").current += 600 * MB;
        };
        m.run(&mut g, 0.0, 8.0, grow);
        assert!(!m.sys.highs.contains_key("train.scope"), "level 2: your training keeps growing");
        m.psi = (60.0, 40.0);
        m.run(&mut g, 8.0, 4.0, |m, t| { grow(m, t); m.psi = (60.0, 40.0); });
        assert!(m.sys.highs.contains_key("train.scope"), "level 3: even protected work is slowed");
        assert!(!m.sys.highs.contains_key("hyprland.scope"));
    }

    #[test]
    fn desktop_is_never_capped() {
        let mut m = Machine::new(ai_box(true));
        m.avail = 2 * GB;
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 10.0, |m, _| { m.psi = (60.0, 40.0); m.avail -= 100 * MB; m.scope("hyprland.scope").current += GB; });
        assert!(!m.sys.highs.contains_key("hyprland.scope"));
    }

    #[test]
    fn frozen_job_is_thawed_after_the_limit_even_under_pressure() {
        let mut m = Machine::new(ai_box(true));
        m.avail = GB;
        let mut g = guard(r#"{"maxFreezeMin": 1}"#);
        m.run(&mut g, 0.0, 70.0, |m, _| m.psi = (40.0, 3.0));
        assert!(m.sys.thawed.contains(&"gphotos-sync.service".to_string()));
    }

    #[test]
    fn fuse_mounts_are_never_frozen() {
        let mut m = Machine::new(ai_box(true));
        m.avail = GB;
        m.sys.active.insert("rclone-gdrive.service".into());
        let mut g = guard(r#"{"freezable": ["rclone-gdrive.service"]}"#);
        m.run(&mut g, 0.0, 10.0, |m, _| m.psi = (40.0, 3.0));
        assert!(!m.sys.frozen.contains(&"rclone-gdrive.service".to_string()));
    }

    #[test]
    fn low_ram_warns_at_most_every_15_min() {
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 1800.0, |m, t| m.avail = if (t as i64) % 120 < 60 { 5 * GB / 2 } else { 15 * GB });
        let warns = m.sys.notes.iter().filter(|(s, _)| s.contains("running low")).count();
        assert_eq!(warns, 2, "{:?}", m.sys.notes);
        assert!(!m.sys.notes.iter().any(|(s, _)| s.contains("back to normal")), "nothing was paused or slowed");
    }

    #[test]
    fn escalation_warns_even_after_a_quiet_level_1_and_after_a_level_1_warning() {
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 16.0, |m, t| {
            m.avail -= 900 * MB;
            m.psi = (if t < 10.0 { 0.0 } else { 30.0 }, 0.0);
        });
        assert!(m.sys.notes.iter().any(|(_, c)| *c), "{:?}", m.sys.notes);
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 30.0, |m, t| {
            m.avail = 5 * GB / 2;
            m.psi = (if t < 20.0 { 0.0 } else { 30.0 }, 0.0);
        });
        let kinds: Vec<bool> = m.sys.notes.iter().map(|(_, c)| *c).take(2).collect();
        assert_eq!(kinds, vec![false, true], "{:?}", m.sys.notes);
    }

    #[test]
    fn only_idle_ordinary_apps_give_memory_to_zram_rate_limited() {
        let mut m = Machine::new(ai_box(false)); // chrome idle
        m.avail = 3 * GB;
        let mut g = guard(r#"{"reclaimIdleMin": 1}"#);
        m.run(&mut g, 0.0, 100.0, |_, _| {});
        assert!(m.sys.reclaims.contains(&"chrome.scope".to_string()) && m.sys.reclaims.contains(&"slack.scope".to_string()));
        assert!(!m.sys.reclaims.iter().any(|r| r == "train.scope" || r == "foot.scope"));
        let n = m.sys.reclaims.iter().filter(|r| *r == "chrome.scope").count();
        assert!(n <= 2, "at most once a minute, got {n}");
    }

    fn gpu(free: i64, procs: Vec<GpuProc>) -> Vram {
        Vram { total: 12 * GB, free, used: 12 * GB - free, procs }
    }
    fn gp(pid: i32, mem: i64, sm: u32) -> GpuProc {
        GpuProc { pid, mem, typ: "C".into(), sm }
    }

    #[test]
    fn a_big_model_filling_the_gpu_is_not_unloaded() {
        let mut m = Machine::new(ai_box(true));
        m.vram = Some(gpu(800 * MB, vec![gp(900, 10 * GB, 0)]));
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 30.0, |_, _| {});
        assert!(m.sys.unloaded.is_empty() && m.sys.vox_calls.is_empty());
    }

    #[test]
    fn training_that_needs_vram_gets_the_idle_model_out_then_dictation() {
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        let mut mem = 2 * GB;
        m.run(&mut g, 0.0, 10.0, |m, _| {
            mem += 300 * MB;
            m.vram = Some(gpu(1000 * MB, vec![gp(900, 5 * GB, 0), gp(1234, mem, 90)]));
        });
        assert_eq!(m.sys.unloaded, vec!["qwen2.5-coder:7b"]);
        m.run(&mut g, 10.0, 10.0, |m, _| {
            mem += 300 * MB;
            m.vram = Some(gpu(900 * MB, vec![gp(1234, mem, 90), gp(950, 1552 * MB, 0)]));
        });
        assert_eq!(m.sys.vox_calls, vec![true], "dictation goes second");
    }

    #[test]
    fn competition_rules() {
        // a second Ollama loading a model counts; the main one loading its own does not
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        let mut mem = GB;
        m.run(&mut g, 0.0, 10.0, |m, _| { mem += 400 * MB; m.vram = Some(gpu(1000 * MB, vec![gp(901, mem, 50)])); });
        assert!(!m.sys.unloaded.is_empty(), "test Ollama on 11435 is competition");
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        let mut mem = GB;
        m.run(&mut g, 0.0, 10.0, |m, _| { mem += 400 * MB; m.vram = Some(gpu(1000 * MB, vec![gp(900, mem, 0)])); });
        assert!(m.sys.unloaded.is_empty(), "the main Ollama loading is not competition");
        // dictating is not competition either
        let mut m = Machine::new(ai_box(true));
        let mut g = guard("{}");
        let mut mem = GB;
        m.run(&mut g, 0.0, 10.0, |m, _| { mem += 400 * MB; m.vram = Some(gpu(1000 * MB, vec![gp(950, mem, 80)])); });
        assert!(m.sys.unloaded.is_empty());
    }

    #[test]
    fn model_answering_a_request_is_never_unloaded_and_never_while_dictating() {
        let mut m = Machine::new(ai_box(true));
        m.sys.ollama_sm = 60;
        let mut g = guard("{}");
        let mut mem = GB;
        m.run(&mut g, 0.0, 10.0, |m, _| { mem += 400 * MB; m.vram = Some(gpu(1000 * MB, vec![gp(900, 5 * GB, 60), gp(1234, mem, 90)])); });
        assert!(m.sys.unloaded.is_empty());
        let mut m = Machine::new(ai_box(true));
        m.sys.loaded.clear();
        m.sys.vox_idle = false;
        let mut g = guard("{}");
        let mut mem = GB;
        m.run(&mut g, 0.0, 10.0, |m, _| { mem += 400 * MB; m.vram = Some(gpu(1000 * MB, vec![gp(1234, mem, 90)])); });
        assert!(m.sys.vox_calls.is_empty(), "never while dictating");
    }

    #[test]
    fn gpu_almost_full_warns_once() {
        let mut m = Machine::new(ai_box(true));
        m.vram = Some(gpu(400 * MB, vec![gp(1234, 11 * GB, 90)]));
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 30.0, |_, _| {});
        assert_eq!(m.sys.notes.iter().filter(|(s, _)| s == "GPU memory is almost full").count(), 1);
    }

    #[test]
    fn dictation_back_on_the_gpu_after_five_free_minutes_not_while_dictating() {
        let mut m = Machine::new(ai_box(true));
        m.sys.vox_resident = 0;
        let mut g = Guard::new(Config::default(), &json!({"voxtypeOffloaded": 1.0}));
        m.vram = Some(gpu(8 * GB, vec![]));
        m.run(&mut g, 0.0, 200.0, |_, _| {});
        assert!(m.sys.vox_calls.is_empty(), "not before 5 min");
        m.sys.vox_idle = false;
        m.run(&mut g, 200.0, 200.0, |_, _| {});
        assert!(m.sys.vox_calls.is_empty(), "not while dictating");
        m.sys.vox_idle = true;
        m.run(&mut g, 400.0, 10.0, |_, _| {});
        assert_eq!(m.sys.vox_calls, vec![false]);
        assert!(g.voxtype_offloaded.is_none());
    }

    #[test]
    fn dictation_offload_can_be_switched_off() {
        let mut m = Machine::new(ai_box(true));
        m.sys.loaded.clear();
        let mut g = guard(r#"{"voxtypeOffload": 0}"#);
        let mut mem = GB;
        m.run(&mut g, 0.0, 10.0, |m, _| { mem += 400 * MB; m.vram = Some(gpu(1000 * MB, vec![gp(1234, mem, 90)])); });
        assert!(m.sys.vox_calls.is_empty());
    }

    #[test]
    fn switched_off_undoes_and_never_acts() {
        let mut m = Machine::new(ai_box(true));
        m.avail = 3 * GB;
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 10.0, leak);
        assert!(!m.sys.frozen.is_empty());
        g.cfg = Config::parse(Some(r#"{"enabled": 0}"#), |_| {});
        m.run(&mut g, 10.0, 20.0, leak);
        assert!(g.frozen.is_empty() && g.throttled.is_empty());
        assert_eq!(m.sys.thawed, vec!["gphotos-sync.service"]);
        assert_eq!(g.status["enabled"], json!(false));
        assert_eq!(m.sys.frozen.len(), 1, "nothing new frozen while off");
    }

    #[test]
    fn status_has_what_the_widget_reads() {
        let mut m = Machine::new(ai_box(true));
        m.vram = Some(gpu(6 * GB, vec![]));
        let mut g = guard("{}");
        m.run(&mut g, 0.0, 4.0, |_, _| {});
        for k in ["t", "enabled", "level", "vramLevel", "reasons", "ram", "vram", "frozen", "throttled", "protected", "unloaded", "dictation"] {
            assert!(g.status.get(k).is_some(), "{k}");
        }
        for k in ["availPct", "psiSome", "psiFull", "tte", "swapPct", "used", "total", "p95", "p99"] {
            assert!(g.status["ram"].get(k).is_some(), "ram.{k}");
        }
        assert!(g.status["vram"]["p99"].is_number());
    }

    #[test]
    fn history_percentiles_and_rate() {
        let mut h = History::new(3600.0);
        for i in 0..100 {
            h.add(i as f64, i, None);
        }
        assert_eq!(h.percentile(1, 95.0), Some(94));
        assert_eq!(h.percentile(2, 95.0), None);
        assert!((h.rate(1, 99.0, 6.0) - 1.0).abs() < 1e-9);
        h.add(5000.0, 1, None);
        assert_eq!(h.samples.len(), 1, "older than an hour dropped");
    }
}
