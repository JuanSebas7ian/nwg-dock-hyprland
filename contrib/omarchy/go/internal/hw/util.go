// Package hw is the backend of the juansebas7ian.hardware bar widget: live
// sensors, NVIDIA (NVML), drivers and updates, storage, peripherals and the
// alerts across them, streamed by omarchy-hardwared. Read-only, no root.
package hw

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// M is a JSON object.
type M = map[string]any

// SysRoot: tests point this at a fake tree containing sys/ and proc/.
var SysRoot = os.Getenv("HW_SYSROOT")

func Home() string { h, _ := os.UserHomeDir(); return h }

func envDir(k, rel string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return filepath.Join(Home(), rel)
}

func CacheDir() string  { return filepath.Join(envDir("XDG_CACHE_HOME", ".cache"), "omarchy-hardware") }
func ConfigDir() string { return envDir("XDG_CONFIG_HOME", ".config") }

func sysPath(p string) string {
	if SysRoot != "" && (strings.HasPrefix(p, "/sys/") || strings.HasPrefix(p, "/proc/")) {
		return SysRoot + p
	}
	return p
}

// Read a small file, trimmed ("" if missing).
func Read(p string) string {
	b, err := os.ReadFile(sysPath(p))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Num reads a number divided by scale (nil if missing).
func Num(p string, scale float64) *float64 {
	v, err := strconv.ParseFloat(Read(p), 64)
	if err != nil {
		return nil
	}
	v /= scale
	return &v
}

func ListDir(p string) []string {
	ents, err := os.ReadDir(sysPath(p))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func exists(p string) bool { _, err := os.Stat(sysPath(p)); return err == nil }

// Run returns stdout, or "" if the command fails to start or times out.
func Run(timeout time.Duration, name string, args ...string) string {
	out, _, _ := RunFull(timeout, name, args...)
	return out
}

// RunFull returns stdout, stderr and the exit code (-1 if it could not run).
func RunFull(timeout time.Duration, name string, args ...string) (string, string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var o, e strings.Builder
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", e.String(), -1
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return o.String(), e.String(), ee.ExitCode()
	}
	if err != nil {
		return "", err.Error(), -1
	}
	return o.String(), e.String(), 0
}

var Defaults = M{
	"cpuWarn":      85.0, // °C: icon turns red
	"cpuCrit":      90.0, // °C: critical alert + notification
	"gpuWarn":      83.0,
	"pumpMinRpm":   500.0, // below this the AIO pump counts as stopped
	"ramWarn":      0.90,  // fraction used
	"diskFreeWarn": 0.10,  // fraction free on /
	"batteryWarn":  15.0,  // % for Bluetooth / wireless devices
	"notify":       true,
}

// Config is the defaults overlaid with ~/.config/omarchy-hardware/config.json.
type Config map[string]any

func LoadConfig() Config {
	c := Config{}
	for k, v := range Defaults {
		c[k] = v
	}
	var user map[string]any
	if b, err := os.ReadFile(filepath.Join(ConfigDir(), "omarchy-hardware", "config.json")); err == nil && json.Unmarshal(b, &user) == nil {
		for k, v := range user {
			c[k] = v
		}
	}
	return c
}

func (c Config) F(k string) float64 {
	f, _ := toF(c[k])
	return f
}

func toF(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint32:
		return float64(x), true
	case *float64:
		if x != nil {
			return *x, true
		}
	}
	return 0, false
}

func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	tmp := strings.TrimSuffix(path, filepath.Ext(path)) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// Cached is producer() cached in CacheDir()/<name>.json for maxAge. A refresh
// that fails ("error" set) returns the last good answer marked "stale", so an
// offline check never blanks the panel.
func Cached(name string, maxAge time.Duration, producer func() M, force bool) M {
	path := filepath.Join(CacheDir(), name+".json")
	if !force {
		if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < maxAge {
			var m M
			if ReadJSON(path, &m) {
				return m
			}
		}
	}
	data := producer()
	if e, ok := data["error"]; ok && e != nil && e != "" {
		var old M
		if ReadJSON(path, &old) {
			old["stale"] = e
			return old
		}
	}
	_ = WriteJSON(path, data)
	return data
}

// Notify sends a desktop notification unless "notify" is off.
var Notify = func(title, body, urgency string) {
	if n, ok := LoadConfig()["notify"].(bool); ok && !n {
		return
	}
	Run(10*time.Second, "notify-send", "-u", urgency, "-a", "Hardware", title, body)
}

func round1(f float64) float64 { return float64(int64(f*10+sign(f)*0.5)) / 10 }
func round2(f float64) float64 { return float64(int64(f*100+sign(f)*0.5)) / 100 }
func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}
func iround(f float64) int64 { return int64(f + sign(f)*0.5) }
