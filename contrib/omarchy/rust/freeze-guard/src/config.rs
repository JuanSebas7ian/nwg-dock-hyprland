//! Settings: defaults overridden by the valid numbers in
//! ~/.config/omarchy-freeze-guard/config.json. A bad file or value is ignored,
//! never fatal: the guard must keep running whatever is there.

use serde_json::Value;
use std::collections::BTreeMap;

pub const DEFAULTS: &[(&str, f64)] = &[
    ("probeEvery", 2.0),
    ("probeTimeout", 2.0),
    ("reportAfter", 8.0),
    ("xwaylandAfter", 12.0),
    ("recoverAfter", 30.0),
    ("shellEvery", 5.0),
    ("shellFailures", 2.0),
    ("renderEvery", 4.0),
    ("renderTimeout", 3.0),
    ("renderKickAfter", 6.0),
    ("renderDpmsAfter", 12.0),
    ("renderRecoverAfter", 30.0),
    ("shellPanelEvery", 3.0),
    ("shellPanelFailures", 2.0),
    ("thawStopped", 1.0),
];

/// Keys that must stay above zero (a zero interval would spin).
const POSITIVE: &[&str] = &["probeEvery", "probeTimeout", "renderTimeout", "shellEvery", "shellPanelEvery"];

#[derive(Debug, Clone, PartialEq)]
pub struct Config(pub BTreeMap<String, f64>);

impl Default for Config {
    fn default() -> Self {
        Config(DEFAULTS.iter().map(|(k, v)| (k.to_string(), *v)).collect())
    }
}

impl Config {
    pub fn get(&self, key: &str) -> f64 {
        *self.0.get(key).unwrap_or(&0.0)
    }

    /// Parse the user's file over the defaults; `warn` receives every ignored value.
    pub fn parse(text: Option<&str>, mut warn: impl FnMut(String)) -> Config {
        let mut cfg = Config::default();
        let Some(text) = text else { return cfg };
        let user: Value = match serde_json::from_str(text) {
            Ok(v) => v,
            Err(_) => {
                warn("config is not valid JSON, using defaults".into());
                return cfg;
            }
        };
        let Some(obj) = user.as_object() else {
            warn("config is not a JSON object, using defaults".into());
            return cfg;
        };
        for (k, v) in obj {
            if !cfg.0.contains_key(k) {
                warn(format!("unknown key {k:?} ignored"));
                continue;
            }
            let f = match v {
                Value::Number(n) => n.as_f64(),
                Value::Bool(b) => Some(if *b { 1.0 } else { 0.0 }),
                _ => None,
            };
            match f {
                Some(f) if f.is_finite() && f >= 0.0 && !(POSITIVE.contains(&k.as_str()) && f <= 0.0) => {
                    cfg.0.insert(k.clone(), f);
                }
                _ => warn(format!("{k}={v} is not valid, keeping {}", cfg.get(k))),
            }
        }
        cfg
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn missing_file_gives_defaults() {
        assert_eq!(Config::parse(None, |_| {}), Config::default());
    }

    #[test]
    fn valid_values_override_and_bad_ones_are_ignored() {
        let mut warnings = vec![];
        let cfg = Config::parse(
            Some(r#"{"reportAfter": 5, "recoverAfter": 0, "probeEvery": 0, "shellEvery": "3", "nope": 1, "renderEvery": -1, "thawStopped": false}"#),
            |w| warnings.push(w),
        );
        assert_eq!(cfg.get("reportAfter"), 5.0);
        assert_eq!(cfg.get("recoverAfter"), 0.0, "0 turns a step off");
        assert_eq!(cfg.get("probeEvery"), 2.0, "an interval can't be 0");
        assert_eq!(cfg.get("shellEvery"), 5.0, "strings are rejected");
        assert_eq!(cfg.get("renderEvery"), 4.0, "negatives are rejected");
        assert_eq!(cfg.get("thawStopped"), 0.0);
        assert_eq!(warnings.len(), 4, "{warnings:?}");
    }

    #[test]
    fn broken_or_non_object_json_keeps_defaults() {
        assert_eq!(Config::parse(Some("{broken"), |_| {}), Config::default());
        assert_eq!(Config::parse(Some("[1,2]"), |_| {}), Config::default());
    }
}
