package hw

// Peripherals: Bluetooth adapter and devices (BlueZ over D-Bus, with
// battery), reconnect-at-boot settings shared with bt-autopower, wireless
// batteries (power_supply and Logitech receivers through solaar), input
// devices and game controllers (pad-keepalive), the USB tree with this boot's
// kernel errors per port, cameras, and the default audio devices.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultReconnect = "0 20 60 180 600"

// BTConf is ~/.config/bt-autopower.conf (sourced by bash: keep it valid shell).
var BTConf = func() string { return filepath.Join(ConfigDir(), "bt-autopower.conf") }

func dv(props M, key string, def any) any {
	if it := m(props[key]); it != nil {
		if v, ok := it["data"]; ok {
			return v
		}
	}
	return def
}

func b(v any) bool { x, _ := v.(bool); return x }

// ParseBluez turns GetManagedObjects (busctl --json) into the adapter and known devices.
func ParseBluez(objects M) (M, []M) {
	var adapter M
	devices := []M{}
	paths := make([]string, 0, len(objects))
	for p := range objects {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		ifaces := m(objects[p])
		if a := m(ifaces["org.bluez.Adapter1"]); a != nil && adapter == nil {
			adapter = M{"path": p, "address": dv(a, "Address", ""), "name": dv(a, "Alias", ""), "powered": b(dv(a, "Powered", false)),
				"discoverable": b(dv(a, "Discoverable", false)), "pairable": b(dv(a, "Pairable", false)), "discovering": b(dv(a, "Discovering", false))}
		}
		d := m(ifaces["org.bluez.Device1"])
		if d == nil || (!b(dv(d, "Paired", false)) && !b(dv(d, "Connected", false))) {
			continue // only devices this PC knows, not every scan result
		}
		name := s(dv(d, "Alias", ""))
		if name == "" {
			name = s(dv(d, "Name", ""))
		}
		var battery any
		if bat := m(ifaces["org.bluez.Battery1"]); bat != nil {
			battery = dv(bat, "Percentage", nil)
		}
		devices = append(devices, M{"mac": dv(d, "Address", ""), "name": name, "icon": dv(d, "Icon", ""), "paired": b(dv(d, "Paired", false)),
			"trusted": b(dv(d, "Trusted", false)), "connected": b(dv(d, "Connected", false)), "blocked": b(dv(d, "Blocked", false)), "battery": battery})
	}
	sort.SliceStable(devices, func(i, j int) bool {
		ci, cj := devices[i]["connected"].(bool), devices[j]["connected"].(bool)
		if ci != cj {
			return ci
		}
		return strings.ToLower(s(devices[i]["name"])) < strings.ToLower(s(devices[j]["name"]))
	})
	return adapter, devices
}

var confLine = regexp.MustCompile(`^\s*([A-Z_]+)=(.*)$`)
var confKey = regexp.MustCompile(`^\s*([A-Z_]+)=`)

// shellWord returns the first word of a shell value ('…', "…" and \ escapes).
func shellWord(v string) string {
	var out strings.Builder
	v = strings.TrimLeft(v, " \t")
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == ' ' || c == '\t' || c == '#':
			if c == '#' && out.Len() > 0 {
				out.WriteByte(c)
				continue
			}
			return out.String()
		case c == '\'':
			j := strings.IndexByte(v[i+1:], '\'')
			if j < 0 {
				return out.String() + v[i+1:]
			}
			out.WriteString(v[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			for i++; i < len(v) && v[i] != '"'; i++ {
				if v[i] == '\\' && i+1 < len(v) && strings.ContainsRune(`"\$`+"`", rune(v[i+1])) {
					i++
				}
				out.WriteByte(v[i])
			}
		case c == '\\' && i+1 < len(v):
			i++
			out.WriteByte(v[i])
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

var safeShell = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

// shellQuote is Python's shlex.quote.
func shellQuote(v string) string {
	if v == "" {
		return "''"
	}
	if safeShell.MatchString(v) {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'"'"'`) + "'"
}

func ReadBTConf() map[string]string {
	conf := map[string]string{"DISCOVERABLE": "yes", "PAIRABLE": "yes", "AUTOCONNECT": "yes", "AUTOCONNECT_EXCLUDE": "", "RECONNECT_AFTER": DefaultReconnect}
	data, err := os.ReadFile(BTConf())
	if err != nil {
		return conf
	}
	for _, line := range strings.Split(string(data), "\n") {
		if mm := confLine.FindStringSubmatch(line); mm != nil {
			conf[mm[1]] = shellWord(mm[2])
		}
	}
	return conf
}

// WriteBTConf sets KEY=value lines, keeping comments and other keys.
func WriteBTConf(updates map[string]string) error {
	path := BTConf()
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	} else {
		lines = []string{"# bt-autopower settings (also edited from the Hardware widget, Devices tab)"}
	}
	seen := map[string]bool{}
	for i, line := range lines {
		if mm := confKey.FindStringSubmatch(line); mm != nil {
			if v, ok := updates[mm[1]]; ok {
				lines[i] = mm[1] + "=" + shellQuote(v)
				seen[mm[1]] = true
			}
		}
	}
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !seen[k] {
			lines = append(lines, k+"="+shellQuote(updates[k]))
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := strings.TrimSuffix(path, filepath.Ext(path)) + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func bluetooth() M {
	var objects M
	var raw M
	if json.Unmarshal([]byte(Run(5*time.Second, "busctl", "--system", "--json=short", "call", "org.bluez", "/", "org.freedesktop.DBus.ObjectManager", "GetManagedObjects")), &raw) == nil {
		if d := l(raw["data"]); len(d) > 0 {
			objects = m(d[0])
		}
	}
	adapter, devices := ParseBluez(objects)
	conf := ReadBTConf()
	excluded := map[string]bool{}
	for _, x := range strings.Fields(conf["AUTOCONNECT_EXCLUDE"]) {
		excluded[x] = true
	}
	for _, d := range devices {
		d["autoconnect"] = conf["AUTOCONNECT"] == "yes" && d["trusted"].(bool) && !excluded[s(d["mac"])]
	}
	var ad any
	if adapter != nil {
		ad = adapter
	}
	return M{"adapter": ad, "devices": devices, "blocked": strings.Contains(Run(5*time.Second, "rfkill", "list", "bluetooth"), "Soft blocked: yes"),
		"service":  strings.TrimSpace(Run(5*time.Second, "systemctl", "--user", "is-active", "bt-autopower.service")),
		"settings": M{"discoverable": conf["DISCOVERABLE"] == "yes", "pairable": conf["PAIRABLE"] == "yes", "autoconnect": conf["AUTOCONNECT"] == "yes", "reconnectAfter": conf["RECONNECT_AFTER"]}}
}

var (
	macRe      = regexp.MustCompile(`^[0-9A-F]{2}(:[0-9A-F]{2}){5}$`)
	btFailedRe = regexp.MustCompile(`Failed to \w+: (\S+)`)
)

// Bluetoothctl is swapped in tests.
var Bluetoothctl = func(timeout time.Duration, args ...string) (string, int) {
	o, e, code := RunFull(timeout, "bluetoothctl", args...)
	return o + e, code
}

// BTAction: connect | disconnect | autoconnect (per device) | visible (adapter).
func BTAction(action string, mac *string, on bool) M {
	if mac != nil && !macRe.MatchString(*mac) {
		return M{"ok": false, "error": "bad address"}
	}
	addr := ""
	if mac != nil {
		addr = *mac
	}
	switch action {
	case "connect", "disconnect":
		if mac == nil {
			return M{"ok": false, "error": "bad address"}
		}
		out, code := Bluetoothctl(20*time.Second, action, addr)
		if code == -1 {
			return M{"ok": false, "error": "timed out (is the device on and in range?)"}
		}
		ok := strings.Contains(out, "successful")
		e := ""
		if !ok {
			if mm := btFailedRe.FindStringSubmatch(out); mm != nil {
				e = mm[1]
			} else {
				e = strings.TrimSpace(out)
				if len(e) > 120 {
					e = e[len(e)-120:]
				}
			}
		}
		return M{"ok": ok, "error": e}
	case "autoconnect":
		if mac == nil {
			return M{"ok": false, "error": "bad address"}
		}
		var excluded []string
		for _, x := range strings.Fields(ReadBTConf()["AUTOCONNECT_EXCLUDE"]) {
			if x != addr {
				excluded = append(excluded, x)
			}
		}
		if on {
			Bluetoothctl(10*time.Second, "trust", addr) // bt-autopower only reconnects trusted devices
		} else {
			excluded = append(excluded, addr)
		}
		if err := WriteBTConf(map[string]string{"AUTOCONNECT_EXCLUDE": strings.Join(excluded, " ")}); err != nil {
			return M{"ok": false, "error": err.Error()}
		}
		return M{"ok": true}
	case "visible":
		v, onoff := "no", "off"
		if on {
			v, onoff = "yes", "on"
		}
		if err := WriteBTConf(map[string]string{"DISCOVERABLE": v, "PAIRABLE": v}); err != nil {
			return M{"ok": false, "error": err.Error()}
		}
		if on {
			Bluetoothctl(10*time.Second, "discoverable-timeout", "0")
		}
		Bluetoothctl(10*time.Second, "discoverable", onoff)
		Bluetoothctl(10*time.Second, "pairable", onoff)
		return M{"ok": true}
	}
	return M{"ok": false, "error": "unknown action " + action}
}

func powerSupplies() []M {
	out := []M{}
	for _, name := range ListDir("/sys/class/power_supply") {
		base := "/sys/class/power_supply/" + name
		if Read(base+"/scope") != "Device" {
			continue // the PC's own supplies, not peripherals
		}
		var pct any
		if n, err := strconv.Atoi(Read(base + "/capacity")); err == nil && n >= 0 {
			pct = n
		}
		model := Read(base + "/model_name")
		if model == "" {
			model = name
		}
		out = append(out, M{"name": model, "source": "kernel", "percent": pct, "level": Read(base + "/capacity_level"), "status": Read(base + "/status")})
	}
	return out
}

var (
	solaarDev = regexp.MustCompile(`^\s{2}\d+: (.+?)\s*$`)
	solaarBat = regexp.MustCompile(`Battery: (\d+)%(?:, (?:BatteryStatus\.)?(\w+))?`)
)

// ParseSolaar reads devices and batteries from `solaar show`.
func ParseSolaar(text string) []M {
	out := []M{}
	var cur M
	for _, line := range strings.Split(text, "\n") {
		if mm := solaarDev.FindStringSubmatch(line); mm != nil {
			cur = M{"name": mm[1], "source": "solaar", "percent": nil, "status": ""}
			out = append(out, cur)
			continue
		}
		if mm := solaarBat.FindStringSubmatch(line); mm != nil && cur != nil && cur["percent"] == nil {
			n, _ := strconv.Atoi(mm[1])
			cur["percent"], cur["status"] = n, strings.ToLower(mm[2])
		}
	}
	return out
}

// solaar runs at most every 5 min (6 s each); refresh=false only reads the last answer.
func solaar(refresh bool) []M {
	if !refresh {
		var c M
		if ReadJSON(filepath.Join(CacheDir(), "solaar.json"), &c) {
			var out []M
			for _, x := range l(c["devices"]) {
				out = append(out, m(x))
			}
			return out
		}
		return nil
	}
	c := Cached("solaar", 5*time.Minute, func() M {
		text := Run(30*time.Second, "solaar", "show")
		if text == "" {
			return M{"devices": []M{}, "error": "solaar not available"}
		}
		return M{"devices": ParseSolaar(text), "checkedAt": time.Now().Unix()}
	}, false)
	var out []M
	for _, x := range l(c["devices"]) {
		out = append(out, m(x))
	}
	return out
}

var (
	jsRe       = regexp.MustCompile(`\bjs\d`)
	inputSufRe = regexp.MustCompile(` (Mouse|Consumer Control|System Control)$`)
)

// ParseInputDevices: keyboards, pointers and game controllers, one row per physical device.
func ParseInputDevices(text string) []M {
	type dev struct{ name, phys, sysfs, handlers, ev string }
	var found []M
	var d dev
	flush := func() {
		if d.name != "" {
			kind := ""
			switch {
			case jsRe.MatchString(d.handlers):
				kind = "gamepad"
			case strings.Contains(d.handlers, "mouse"):
				kind = "pointer"
			case strings.Contains(d.handlers, "kbd") && d.ev == "120013":
				kind = "keyboard"
			}
			if kind != "" {
				found = append(found, M{"name": d.name, "kind": kind, "phys": d.phys,
					"virtual": strings.HasPrefix(d.phys, "pad-keepalive") || strings.Contains(d.sysfs, "/virtual/")})
			}
		}
		d = dev{}
	}
	after := func(v string) string { _, x, _ := strings.Cut(v, "="); return x }
	for _, line := range append(strings.Split(text, "\n"), "") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		tag, value, _ := strings.Cut(line, ": ")
		switch tag {
		case "N":
			d.name = strings.Trim(after(value), `"`)
		case "P":
			d.phys = after(value)
		case "S":
			d.sysfs = after(value)
		case "H":
			d.handlers = after(value)
		case "B":
			if strings.HasPrefix(value, "EV=") {
				d.ev = value[3:]
			}
		}
	}
	// A receiver or keyboard exposes several nodes ("... Mouse", "... Consumer Control"): merge them.
	rank := map[string]int{"gamepad": 0, "pointer": 1, "keyboard": 2}
	type key struct {
		base string
		virt bool
	}
	merged := map[key]M{}
	var order []key
	for _, x := range found {
		k := key{inputSufRe.ReplaceAllString(s(x["name"]), ""), x["virtual"].(bool)}
		if e, ok := merged[k]; ok {
			kinds := e["kinds"].([]string)
			has := false
			for _, kk := range kinds {
				has = has || kk == x["kind"]
			}
			if !has {
				e["kinds"] = append(kinds, x["kind"].(string))
			}
			continue
		}
		x["name"], x["kinds"] = k.base, []string{x["kind"].(string)}
		merged[k] = x
		order = append(order, k)
	}
	out := []M{}
	for _, k := range order {
		e := merged[k]
		kinds := e["kinds"].([]string)
		sort.SliceStable(kinds, func(i, j int) bool { return rank[kinds[i]] < rank[kinds[j]] })
		e["kind"] = kinds[0]
		out = append(out, e)
	}
	return out
}

var usbErr = regexp.MustCompile(`(?i)usb (\d+-[\d.]+):.*?(error -?\d+|failed.*|disabled by hub.*|device not accepting address.*|unable to enumerate.*|reset .* USB device.*|USB disconnect.*)`)

func usbErrors() map[string]M {
	out := map[string]M{}
	for _, line := range strings.Split(Run(10*time.Second, "journalctl", "-k", "-b", "--no-pager", "-o", "short-unix", "-g", `usb [0-9]+-[0-9.]+:`), "\n") {
		mm := usbErr.FindStringSubmatch(line)
		if mm == nil {
			continue
		}
		port, msg := mm[1], mm[2]
		low := strings.ToLower(msg)
		if strings.HasPrefix(low, "usb disconnect") || strings.HasPrefix(low, "reset") {
			continue
		}
		e, ok := out[port]
		if !ok {
			e = M{"count": 0, "last": "", "at": int64(0)}
			out[port] = e
		}
		e["count"] = e["count"].(int) + 1
		if len(msg) > 90 {
			msg = msg[:90]
		}
		e["last"] = msg
		if f := strings.Fields(line); len(f) > 0 {
			if t, err := strconv.ParseFloat(f[0], 64); err == nil {
				e["at"] = int64(t)
			}
		}
	}
	return out
}

var usbSpeeds = map[string]string{"1.5": "USB 1 low", "12": "USB 1", "480": "USB 2", "5000": "USB 3", "10000": "USB 3.1", "20000": "USB 3.2"}

func portKey(p string) []int {
	var out []int
	for _, x := range regexp.MustCompile(`[-.]`).Split(p, -1) {
		n, _ := strconv.Atoi(x)
		out = append(out, n)
	}
	return out
}

func usbTree() []M {
	errs := usbErrors()
	all := ListDir("/sys/bus/usb/devices")
	out := []M{}
	for _, name := range all {
		if strings.Contains(name, ":") || strings.HasPrefix(name, "usb") {
			continue
		}
		base := "/sys/bus/usb/devices/" + name
		drvSet := map[string]bool{}
		for _, i := range all {
			if strings.HasPrefix(i, name+":") {
				ue := Read("/sys/bus/usb/devices/" + i + "/uevent")
				if _, after, ok := strings.Cut(ue, "DRIVER="); ok {
					if d := strings.Split(after, "\n")[0]; d != "" {
						drvSet[d] = true
					}
				}
			}
		}
		drivers := []string{}
		for d := range drvSet {
			drivers = append(drivers, d)
		}
		sort.Strings(drivers)
		speed := Read(base + "/speed")
		if v, ok := usbSpeeds[speed]; ok {
			speed = v
		}
		prod := Read(base + "/product")
		if prod == "" {
			prod = "Unknown device"
		}
		var e any
		if v, ok := errs[name]; ok {
			e = v
		}
		out = append(out, M{"port": name, "id": Read(base+"/idVendor") + ":" + Read(base+"/idProduct"), "name": prod, "vendor": Read(base + "/manufacturer"),
			"speed": speed, "hub": Read(base+"/bDeviceClass") == "09", "depth": strings.Count(name, ".") + 1, "drivers": drivers, "errors": e})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := portKey(s(out[i]["port"])), portKey(s(out[j]["port"]))
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return out
}

func cameras() []string {
	names := []string{}
	seen := map[string]bool{}
	for _, v := range ListDir("/sys/class/video4linux") {
		n := strings.Split(Read("/sys/class/video4linux/"+v+"/name"), ":")[0]
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return names
}

var nodeDesc = regexp.MustCompile(`node\.description = "(.*)"`)

func audioNode(which string) string {
	for _, line := range strings.Split(Run(3*time.Second, "wpctl", "inspect", fmt.Sprintf("@DEFAULT_AUDIO_%s@", which)), "\n") {
		if mm := nodeDesc.FindStringSubmatch(line); mm != nil {
			return mm[1]
		}
	}
	return ""
}

// disabledAudio: devices switched off by a WirePlumber rule (e.g. the webcam mic).
func disabledAudio() []string {
	out := []string{}
	files, _ := filepath.Glob(filepath.Join(ConfigDir(), "wireplumber", "wireplumber.conf.d", "*.conf"))
	sort.Strings(files)
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "device.disabled") && strings.Contains(string(b), "true") {
			out = append(out, filepath.Base(f))
		}
	}
	return out
}

// CollectPeripherals gathers the Devices topic.
func CollectPeripherals(withSolaar bool) M {
	batteries := append(powerSupplies(), solaar(withSolaar)...)
	return M{
		"bluetooth":    bluetooth(),
		"batteries":    batteries,
		"input":        ParseInputDevices(Read("/proc/bus/input/devices")),
		"padKeepalive": strings.TrimSpace(Run(5*time.Second, "systemctl", "--user", "is-active", "pad-keepalive.service")),
		"usb":          usbTree(),
		"cameras":      cameras(),
		"audio":        M{"output": audioNode("SINK"), "input": audioNode("SOURCE"), "disabled": disabledAudio()},
		"checkedAt":    time.Now().Unix(),
	}
}
