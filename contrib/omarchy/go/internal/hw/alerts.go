package hw

// Alerts from every domain, with thresholds from config.json. Evaluate turns
// the latest data into {"id", "level": "crit"|"warn", "title", "detail", "tab"};
// Notifier sends a desktop notification when a critical alert (or a low
// battery) first appears and re-arms only after it has been clear for
// `rearm`, so a value hovering around a threshold does not spam.

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
)

// Alert is one active problem.
type Alert struct {
	ID     string `json:"id"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Tab    string `json:"tab"`
}

func m(v any) M { x, _ := v.(M); return x }
func l(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []M:
		out := make([]any, len(x))
		for i := range x {
			out[i] = x[i]
		}
		return out
	}
	return nil
}
func s(v any) string {
	if v == nil {
		return ""
	}
	if x, ok := v.(string); ok {
		return x
	}
	return fmt.Sprint(v)
}
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case M:
		return len(x) > 0
	}
	f, ok := toF(v)
	return !ok || f != 0
}

// fnum: a number or (0, false) for null/missing.
func fnum(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	return toF(v)
}

func shortHash(text string) string {
	h := fnv.New32a()
	h.Write([]byte(text))
	return fmt.Sprint(h.Sum32() % 1000000)
}

// Evaluate all domains; critical alerts first.
func Evaluate(cfg Config, live, drivers, storage, peripherals M) []Alert {
	out := []Alert{}
	add := func(id, level, title, detail, tab string) { out = append(out, Alert{id, level, title, detail, tab}) }
	if live != nil {
		cpu := m(live["cpu"])
		if t, ok := fnum(cpu["tctl"]); ok {
			if t >= cfg.F("cpuCrit") {
				add("cpu-hot", "crit", fmt.Sprintf("CPU at %d °C", iround(t)), fmt.Sprintf("Critical threshold %v °C.", cfg.F("cpuCrit")), "cpu")
			} else if t >= cfg.F("cpuWarn") {
				add("cpu-warm", "warn", fmt.Sprintf("CPU at %d °C", iround(t)), fmt.Sprintf("Warning threshold %v °C.", cfg.F("cpuWarn")), "cpu")
			}
		}
		for _, x := range l(live["fans"]) {
			f := m(x)
			rpm, _ := fnum(f["rpm"])
			if f["role"] == "pump" && rpm < cfg.F("pumpMinRpm") {
				add("pump", "crit", "AIO pump stopped", fmt.Sprintf("%s reads %v RPM. Check the pump cable (AIO_PUMP) before using the PC hard.", s(f["name"]), f["rpm"]), "cpu")
			}
		}
		for _, x := range l(live["gpus"]) {
			g := m(x)
			if t, ok := fnum(g["temp"]); ok && t >= cfg.F("gpuWarn") {
				add(fmt.Sprintf("gpu-hot-%v", g["index"]), "warn", fmt.Sprintf("GPU at %v °C", g["temp"]), fmt.Sprintf("Warning threshold %v °C.", cfg.F("gpuWarn")), "gpu")
			}
		}
		mem := m(live["mem"])
		total, _ := fnum(mem["total"])
		used, _ := fnum(mem["used"])
		if total > 0 && used/total >= cfg.F("ramWarn") {
			add("ram", "warn", fmt.Sprintf("RAM %d%% used", iround(100*used/total)), "Close something or check for a leak.", "cpu")
		}
	}
	if storage != nil {
		root := m(storage["root"])
		size, _ := fnum(root["size"])
		free, _ := fnum(root["free"])
		if size > 0 && free/size < cfg.F("diskFreeWarn") {
			add("disk-free", "warn", "System disk almost full", fmt.Sprintf("%d GB free.", iround(free/1e9)), "storage")
		}
		for _, x := range l(storage["disks"]) {
			d := m(x)
			h := m(d["health"])
			if len(h) == 0 {
				continue
			}
			media, _ := fnum(h["mediaErrors"])
			used, _ := fnum(h["percentUsed"])
			spare, hasSpare := fnum(h["spare"])
			thresh, _ := fnum(h["spareThreshold"])
			if truthy(h["criticalWarning"]) || media > 0 {
				add("smart-"+s(d["name"]), "crit", s(d["model"])+": SMART warning", "Back up now and check the Storage tab.", "storage")
			} else if used >= 80 || (hasSpare && spare <= thresh) {
				add("wear-"+s(d["name"]), "warn", fmt.Sprintf("%s: %v%% worn", s(d["model"]), h["percentUsed"]), "Plan a replacement.", "storage")
			}
		}
	}
	if drivers != nil {
		for _, x := range l(m(drivers["nvidia"])["issues"]) {
			add("nvidia-"+shortHash(s(x)), "crit", "NVIDIA driver", s(x), "drivers")
		}
		if cuda := m(drivers["cuda"]); len(cuda) > 0 && !truthy(cuda["works"]) {
			add("cuda", "warn", "CUDA not working", s(cuda["error"]), "gpu")
		}
		for _, x := range l(m(drivers["health"])["issues"]) {
			i := m(x)
			level := "warn"
			if i["level"] == "fail" || i["level"] == "regression" {
				level = "crit"
			}
			var det []string
			for _, d := range l(i["details"]) {
				det = append(det, s(d))
			}
			detail := strings.Join(det, "; ")
			if len(detail) > 160 {
				detail = detail[:160]
			}
			add("hwcheck-"+s(i["id"]), level, s(i["id"])+" "+s(i["title"]), detail, "drivers")
		}
	}
	if peripherals != nil {
		bt := m(peripherals["bluetooth"])
		if bt["adapter"] == nil && !truthy(bt["blocked"]) {
			add("bt-missing", "warn", "No Bluetooth adapter", "bt-guardian tries to recover it; after Windows, cut the power.", "devices")
		}
		for _, x := range l(bt["devices"]) {
			d := m(x)
			if b, ok := fnum(d["battery"]); ok && truthy(d["connected"]) && b <= cfg.F("batteryWarn") {
				add("battery-"+s(d["mac"]), "warn", fmt.Sprintf("%s battery %v%%", s(d["name"]), d["battery"]), "Charge it soon.", "devices")
			}
		}
		for _, x := range l(peripherals["batteries"]) {
			b := m(x)
			if p, ok := fnum(b["percent"]); ok && p <= cfg.F("batteryWarn") {
				add("battery-"+s(b["name"]), "warn", fmt.Sprintf("%s battery %v%%", s(b["name"]), b["percent"]), "Charge it soon.", "devices")
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level == "crit" && out[j].Level != "crit" })
	return out
}

// Notifier de-duplicates desktop notifications.
type Notifier struct {
	active map[string]time.Time // id -> last seen
	Rearm  time.Duration
}

func NewNotifier(rearm time.Duration) *Notifier { return &Notifier{map[string]time.Time{}, rearm} }

// Update returns the alerts notified now.
func (n *Notifier) Update(alerts []Alert, now time.Time) []Alert {
	sent := []Alert{}
	for _, a := range alerts {
		notifiable := a.Level == "crit" || strings.HasPrefix(a.ID, "battery-")
		if _, seen := n.active[a.ID]; notifiable && !seen {
			urg := "normal"
			if a.Level == "crit" {
				urg = "critical"
			}
			Notify(a.Title, a.Detail, urg)
			sent = append(sent, a)
		}
		n.active[a.ID] = now
	}
	for id, seen := range n.active {
		if now.Sub(seen) > n.Rearm {
			delete(n.active, id)
		}
	}
	return sent
}
