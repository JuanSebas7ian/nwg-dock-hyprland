//! Settings (~/.config/omarchy-memguard/config.json): numbers over DEFAULTS
//! plus three exception lists. A bad file or value is ignored, never fatal.

use serde_json::{json, Map, Value};
use std::collections::BTreeMap;
use std::path::Path;

pub const DEFAULTS: &[(&str, f64)] = &[
    ("enabled", 1.0),               // 0 = watch and show only, never act (what was applied is undone)
    ("every", 2.0),                 // seconds between samples
    ("ramWarnAvailPct", 12.0),      // MemAvailable below this % of RAM -> level 1
    ("ramCritAvailPct", 6.0),       // -> level 2
    ("ramPanicAvailPct", 3.0),      // -> level 3
    ("psiWarn", 10.0),              // memory "some" avg10 % -> level 1
    ("psiConfirm", 5.0),            // low RAM only escalates when processes already wait this much
    ("psiCrit", 25.0),              // -> level 2; "full" above this -> level 3
    ("tteWarn", 60.0),              // seconds until RAM runs out at the current rate -> level 1
    ("tteCrit", 25.0),              // seconds until RAM + swap run out -> level 2
    ("ttePanic", 10.0),             // -> level 3
    ("swapCritPct", 60.0),          // swap used % -> level 2
    ("vramWarnFreeMB", 1500.0),     // free VRAM below this while another process asks for more
    ("vramCritFreeMB", 600.0),      // -> also a critical warning with the VRAM users
    ("vramGrowMB", 200.0),          // "asks for more": a non-Ollama process grew this much in 30 s
    ("voxtypeOffload", 1.0),        // dictation model moved to on-demand when someone needs the room
    ("voxtypeReloadFreeMB", 4500.0), // free VRAM needed to load it back
    ("voxtypeReloadCalmMin", 5.0),  // ...for this long, with nobody asking for more
    ("calmSeconds", 30.0),          // this long back at level 0 before undoing everything
    ("reclaimIdleMin", 10.0),       // an app idle this long gives its cold memory to zram
    ("reclaimMB", 512.0),           // per app and per minute
    ("maxFreezeMin", 30.0),         // a frozen job is thawed after this anyway
];

pub const FREEZABLE: &[&str] = &["gphotos-sync.service", "omarchy-backup.service", "opencode-ollama-sync.service"];
pub const LISTS: &[&str] = &["protect", "ordinary", "freezable"];
pub const MAX_LIST: usize = 50;

/// key -> (group, label, unit, min, max, step) for the settings screen.
pub const SCHEMA: &[(&str, &str, &str, &str, f64, f64, f64)] = &[
    ("enabled", "General", "Act on spikes (off = watch only)", "bool", 0.0, 1.0, 1.0),
    ("voxtypeOffload", "GPU", "Move dictation off the GPU when work needs the VRAM", "bool", 0.0, 1.0, 1.0),
    ("ramWarnAvailPct", "RAM", "Warn below free RAM", "%", 1.0, 50.0, 1.0),
    ("ramCritAvailPct", "RAM", "Act below free RAM (with waiting)", "%", 1.0, 40.0, 1.0),
    ("ramPanicAvailPct", "RAM", "Emergency below free RAM", "%", 1.0, 20.0, 1.0),
    ("psiWarn", "Pressure", "Warn when work waits", "%", 1.0, 90.0, 1.0),
    ("psiConfirm", "Pressure", "Waiting that confirms low RAM", "%", 0.0, 50.0, 1.0),
    ("psiCrit", "Pressure", "Act when work waits", "%", 5.0, 95.0, 1.0),
    ("tteWarn", "Spikes", "Warn when RAM full in", "s", 5.0, 600.0, 5.0),
    ("tteCrit", "Spikes", "Act when RAM + swap full in", "s", 5.0, 600.0, 5.0),
    ("ttePanic", "Spikes", "Emergency when RAM + swap full in", "s", 2.0, 120.0, 1.0),
    ("swapCritPct", "Spikes", "Act when swap used above", "%", 10.0, 95.0, 5.0),
    ("vramWarnFreeMB", "GPU", "Free VRAM before helping a process that asks for more", "MB", 100.0, 8000.0, 100.0),
    ("vramCritFreeMB", "GPU", "Critical free VRAM", "MB", 100.0, 4000.0, 100.0),
    ("vramGrowMB", "GPU", "Growth that counts as asking for more (30 s)", "MB", 50.0, 4000.0, 50.0),
    ("voxtypeReloadFreeMB", "GPU", "Free VRAM to put dictation back", "MB", 1000.0, 11000.0, 250.0),
    ("voxtypeReloadCalmMin", "GPU", "...for this long", "min", 1.0, 120.0, 1.0),
    ("calmSeconds", "Undo", "Calm before undoing", "s", 5.0, 600.0, 5.0),
    ("maxFreezeMin", "Undo", "Longest pause of a background job", "min", 1.0, 240.0, 5.0),
    ("reclaimIdleMin", "Idle apps", "Idle before giving memory to zram", "min", 1.0, 240.0, 1.0),
    ("reclaimMB", "Idle apps", "Per app and minute", "MB", 64.0, 4096.0, 64.0),
    ("every", "General", "Sample every", "s", 1.0, 30.0, 1.0),
];

pub fn valid_name(v: &str) -> bool {
    !v.is_empty() && v.len() <= 64 && v.chars().all(|c| c.is_ascii_alphanumeric() || "_.@:+-".contains(c))
}

pub fn never_freeze(unit: &str) -> bool {
    ["rclone", "fuse", "sshfs", "gvfs", "dropbox", "pipewire", "wireplumber"].iter().any(|w| unit.contains(w))
}

fn default_of(key: &str) -> Option<f64> {
    DEFAULTS.iter().find(|(k, _)| *k == key).map(|(_, v)| *v)
}

#[derive(Debug, Clone, PartialEq)]
pub struct Config {
    pub vals: BTreeMap<String, f64>,
    pub protect: Vec<String>,
    pub ordinary: Vec<String>,
    pub freezable: Vec<String>,
}

impl Default for Config {
    fn default() -> Self {
        Config { vals: DEFAULTS.iter().map(|(k, v)| (k.to_string(), *v)).collect(), protect: vec![], ordinary: vec![], freezable: vec![] }
    }
}

impl Config {
    pub fn get(&self, k: &str) -> f64 {
        *self.vals.get(k).unwrap_or(&0.0)
    }

    pub fn parse(text: Option<&str>, mut warn: impl FnMut(String)) -> Config {
        let mut c = Config::default();
        let Some(text) = text else { return c };
        let Ok(Value::Object(user)) = serde_json::from_str::<Value>(text) else {
            warn("not a JSON object, using defaults".into());
            return c;
        };
        for (k, v) in &user {
            if LISTS.contains(&k.as_str()) {
                if let Value::Array(a) = v {
                    let items: Vec<String> = a.iter().filter_map(|x| x.as_str()).filter(|x| valid_name(x)).take(MAX_LIST).map(String::from).collect();
                    match k.as_str() {
                        "protect" => c.protect = items,
                        "ordinary" => c.ordinary = items,
                        _ => c.freezable = items,
                    }
                }
                continue;
            }
            if default_of(k).is_none() {
                warn(format!("unknown key {k:?} ignored"));
                continue;
            }
            match v.as_f64() {
                Some(f) if v.is_number() && f.is_finite() && f >= 0.0 && !(k == "every" && f < 0.5) => {
                    c.vals.insert(k.clone(), f);
                }
                _ => warn(format!("{k}={v} is not valid, keeping {}", c.get(k))),
            }
        }
        c
    }

    pub fn load(path: &Path) -> Config {
        let text = std::fs::read_to_string(path).ok();
        Config::parse(text.as_deref(), |w| eprintln!("omarchy-memguard: {}: {w}", path.display()))
    }

    /// The effective settings as the widget reads them.
    pub fn to_json(&self) -> Value {
        let mut m = Map::new();
        for (k, v) in &self.vals {
            m.insert(k.clone(), json!(v));
        }
        m.insert("protect".into(), json!(self.protect));
        m.insert("ordinary".into(), json!(self.ordinary));
        m.insert("freezable".into(), json!(self.freezable));
        Value::Object(m)
    }
}

pub fn defaults_json() -> Value {
    let mut m = Map::new();
    for (k, v) in DEFAULTS {
        m.insert(k.to_string(), if v.fract() == 0.0 { json!(*v as i64) } else { json!(v) });
    }
    Value::Object(m)
}

pub fn schema_json(c: &Config) -> Value {
    Value::Array(
        SCHEMA
            .iter()
            .map(|(k, g, l, u, lo, hi, st)| json!({"key": k, "group": g, "label": l, "unit": u, "min": lo, "max": hi, "step": st, "value": c.get(k), "default": default_of(k)}))
            .collect(),
    )
}

/// The file as the user wrote it (for the widget's "changed" marks).
pub fn user_config(path: &Path) -> Map<String, Value> {
    match std::fs::read_to_string(path).ok().and_then(|t| serde_json::from_str::<Value>(&t).ok()) {
        Some(Value::Object(m)) => m,
        _ => Map::new(),
    }
}

fn write_config(path: &Path, data: &Map<String, Value>) -> std::io::Result<()> {
    if let Some(d) = path.parent() {
        std::fs::create_dir_all(d)?;
    }
    let sorted: BTreeMap<&String, &Value> = data.iter().collect();
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, serde_json::to_string_pretty(&sorted).unwrap() + "\n")?;
    std::fs::rename(tmp, path)
}

/// set / add / remove / reset with validation.
pub fn edit(path: &Path, op: &str, key: Option<&str>, value: Option<&str>) -> Result<(), String> {
    let mut data = user_config(path);
    match op {
        "reset" => match key {
            None => data.retain(|k, _| LISTS.contains(&k.as_str())),
            Some(k) if default_of(k).is_some() || LISTS.contains(&k) => {
                data.remove(k);
            }
            Some(k) => return Err(format!("unknown setting {k}")),
        },
        "set" => {
            let k = key.unwrap_or("");
            let Some(def) = default_of(k) else { return Err(format!("unknown setting {k}")) };
            let v = value.unwrap_or("");
            let f: f64 = v.trim().parse().map_err(|_| format!("{k}: {v:?} is not a number"))?;
            let (lo, hi) = SCHEMA.iter().find(|s| s.0 == k).map(|s| (s.4, s.5)).unwrap_or((0.0, f64::INFINITY));
            if !(lo..=hi).contains(&f) {
                return Err(format!("{k}: {f} is outside {lo}-{hi}"));
            }
            if f == def {
                data.remove(k);
            } else if f.fract() == 0.0 {
                data.insert(k.into(), json!(f as i64));
            } else {
                data.insert(k.into(), json!(f));
            }
        }
        "add" | "remove" => {
            let k = key.unwrap_or("");
            if !LISTS.contains(&k) {
                return Err(format!("unknown list {k} (protect, ordinary, freezable)"));
            }
            let v = value.unwrap_or("");
            if !valid_name(v) {
                return Err(format!("{v:?}: use a process, app or unit name (letters, digits, . _ - @ : +)"));
            }
            let mut items: Vec<String> = data.get(k).and_then(|x| x.as_array()).map(|a| a.iter().filter_map(|s| s.as_str()).filter(|s| valid_name(s)).map(String::from).collect()).unwrap_or_default();
            if op == "add" {
                if k == "freezable" && never_freeze(v) {
                    return Err(format!("{v}: mounts and audio are never paused"));
                }
                if !items.iter().any(|x| x == v) {
                    if items.len() >= MAX_LIST {
                        return Err(format!("{k}: at most {MAX_LIST} names"));
                    }
                    items.push(v.into());
                }
                let other = match k {
                    "protect" => Some("ordinary"),
                    "ordinary" => Some("protect"),
                    _ => None,
                };
                if let Some(o) = other {
                    if let Some(Value::Array(a)) = data.get_mut(o) {
                        a.retain(|x| x.as_str() != Some(v)); // one name, one rule
                    }
                }
            } else {
                items.retain(|x| x != v);
            }
            if items.is_empty() {
                data.remove(k);
            } else {
                data.insert(k.into(), json!(items));
            }
        }
        _ => return Err(format!("unknown operation {op}")),
    }
    write_config(path, &data).map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(name: &str) -> std::path::PathBuf {
        let d = std::env::temp_dir().join(format!("mg-{}-{name}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        d.join("config.json")
    }

    #[test]
    fn bad_config_falls_back_to_defaults() {
        assert_eq!(Config::parse(Some("{nope"), |_| {}), Config::default());
        assert_eq!(Config::parse(Some("[]"), |_| {}), Config::default());
        let c = Config::parse(Some(r#"{"every": 0.1, "psiWarn": "x", "calmSeconds": -1, "nope": 1, "enabled": true}"#), |_| {});
        assert_eq!((c.get("every"), c.get("psiWarn"), c.get("calmSeconds"), c.get("enabled")), (2.0, 10.0, 30.0, 1.0));
    }

    #[test]
    fn valid_values_and_lists_apply() {
        let c = Config::parse(Some(r#"{"psiWarn": 20, "protect": ["blender", "bad name!", "x"], "freezable": ["foo.service"]}"#), |_| {});
        assert_eq!(c.get("psiWarn"), 20.0);
        assert_eq!(c.protect, vec!["blender", "x"]);
        assert_eq!(c.freezable, vec!["foo.service"]);
    }

    #[test]
    fn set_validates_range_and_type_and_default_removes_key() {
        let p = tmp("set");
        assert!(edit(&p, "set", Some("psiWarn"), Some("200")).is_err());
        assert!(edit(&p, "set", Some("psiWarn"), Some("abc")).is_err());
        assert!(edit(&p, "set", Some("nope"), Some("1")).is_err());
        edit(&p, "set", Some("psiWarn"), Some("20")).unwrap();
        assert_eq!(user_config(&p)["psiWarn"], json!(20));
        edit(&p, "set", Some("psiWarn"), Some("10")).unwrap();
        assert!(!user_config(&p).contains_key("psiWarn"));
    }

    #[test]
    fn lists_add_remove_one_rule_per_name_and_reset_keeps_exceptions() {
        let p = tmp("lists");
        edit(&p, "add", Some("protect"), Some("blender")).unwrap();
        edit(&p, "add", Some("ordinary"), Some("blender")).unwrap();
        let u = user_config(&p);
        assert!(!u.contains_key("protect") || u["protect"] == json!([]));
        assert_eq!(u["ordinary"], json!(["blender"]));
        assert!(edit(&p, "add", Some("freezable"), Some("rclone-gdrive.service")).is_err());
        assert!(edit(&p, "add", Some("protect"), Some("rm -rf")).is_err());
        assert!(edit(&p, "add", Some("nolist"), Some("x")).is_err());
        edit(&p, "set", Some("psiWarn"), Some("30")).unwrap();
        edit(&p, "reset", None, None).unwrap();
        let u = user_config(&p);
        assert!(!u.contains_key("psiWarn") && u.contains_key("ordinary"));
        edit(&p, "remove", Some("ordinary"), Some("blender")).unwrap();
        assert!(!user_config(&p).contains_key("ordinary"));
    }

    #[test]
    fn every_setting_is_on_the_settings_screen() {
        for (k, _) in DEFAULTS {
            assert!(SCHEMA.iter().any(|s| s.0 == *k), "{k} missing from SCHEMA");
        }
    }
}
