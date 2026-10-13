// Package iphone connects an iPhone to this PC for the iPhone bar widget: device status (lockdown),
// photo import and app files (AFC through ifuse), local backups (idevicebackup2), USB tethering (ipheth),
// screen mirroring (UxPlay, AirPlay) and the KDE Connect companion. It drives the mature libimobiledevice
// command-line tools instead of a Go library, so the omarchy module keeps no third-party dependencies.
package iphone

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// M is a JSON object.
type M = map[string]any

// Runner runs a command and returns its combined output.
type Runner func(name string, args ...string) ([]byte, error)

// ExecRunner runs real commands with a timeout.
func ExecRunner(timeout time.Duration) Runner {
	return func(name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
}

// Paths of the files this package uses.
type Paths struct {
	Config, State, Runtime, Home string
}

func DefaultPaths() Paths {
	home, _ := os.UserHomeDir()
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	return Paths{
		Config:  filepath.Join(env("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "omarchy-iphone", "config.json"),
		State:   filepath.Join(env("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "omarchy-iphone"),
		Runtime: filepath.Join(env("XDG_RUNTIME_DIR", filepath.Join(os.TempDir(), "omarchy-iphone-"+os.Getenv("USER"))), "omarchy-iphone"),
		Home:    home,
	}
}

// Config is ~/.config/omarchy-iphone/config.json.
type Config struct {
	PhotosDir      string `json:"photosDir"`
	BackupDir      string `json:"backupDir"`
	AutoImport     bool   `json:"autoImport"`
	AutoBackupDays int    `json:"autoBackupDays"`
	AirPlayName    string `json:"airplayName"`
	DriveBackup    bool   `json:"driveBackup"` // upload each local backup to Google Drive (restic, encrypted)
}

func (p Paths) LoadConfig() Config {
	c := Config{PhotosDir: filepath.Join(p.Home, "Pictures"), BackupDir: filepath.Join(p.Home, "iPhoneBackup"),
		AutoImport: true, AutoBackupDays: 0, AirPlayName: "Omarchy", DriveBackup: true}
	if b, err := os.ReadFile(p.Config); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	c.PhotosDir = expand(c.PhotosDir, p.Home)
	// photosDir is the root: each device class gets its folder (Pictures/iPhone, Pictures/iPad); the first
	// version stored Pictures/iPhone itself
	if c.PhotosDir == filepath.Join(p.Home, "Pictures", "iPhone") {
		c.PhotosDir = filepath.Join(p.Home, "Pictures")
	}
	c.BackupDir = expand(c.BackupDir, p.Home)
	return c
}

func (p Paths) SaveConfig(c Config) error { return writeJSON(p.Config, c) }

// Set changes one setting from its string form.
func (p Paths) Set(key, value string) error {
	c := p.LoadConfig()
	switch key {
	case "photosDir":
		c.PhotosDir = value
	case "backupDir":
		c.BackupDir = value
	case "autoImport":
		c.AutoImport = value == "true" || value == "on" || value == "1"
	case "autoBackupDays":
		n := 0
		for _, r := range value {
			if r < '0' || r > '9' {
				return errors.New("autoBackupDays: a number of days (0 = off)")
			}
			n = n*10 + int(r-'0')
		}
		c.AutoBackupDays = n
	case "driveBackup":
		c.DriveBackup = value == "true" || value == "on" || value == "1"
	case "airplayName":
		if strings.TrimSpace(value) == "" {
			return errors.New("airplayName cannot be empty")
		}
		c.AirPlayName = strings.TrimSpace(value)
	default:
		return errors.New("unknown setting " + key)
	}
	return p.SaveConfig(c)
}

func expand(path, home string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return path
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// Long jobs (import, backup, drive, safe) run detached from the bar (systemd-run), so a bar restart does not kill
// them; they leave their progress in a file the status reads.
var Jobs = []string{"import", "backup", "drive", "safe", "free"}

// Every job, its lock, progress and history belong to one device (State/<udid>/), so an iPhone and an iPad
// back up and upload at the same time.
func (p Paths) devFile(udid, name string) string { return filepath.Join(p.State, udid, name) }

func jobKey(job, udid string) string { return job + "-" + udid }

// SaveProgress records a job's progress for one device (fraction -1 = unknown).
func (p Paths) SaveProgress(job, udid string, frac float64, line string) {
	_ = writeJSON(p.devFile(udid, "progress-"+job+".json"), M{"fraction": frac, "line": line, "time": time.Now().Format(time.RFC3339)})
}

// JobsStatus: which jobs of one device run now and how far they are.
func JobsStatus(p Paths, udid string) M {
	out := M{}
	for _, j := range Jobs {
		running := p.locked(jobKey(j, udid))
		var progress any // nil (JSON null) unless the job runs
		var prog M
		if running && readJSON(p.devFile(udid, "progress-"+j+".json"), &prog) {
			progress = prog
		} else if !running && p.locked(j) && legacyOwner(p, j, udid) && readJSON(filepath.Join(p.State, "progress-"+j+".json"), &prog) {
			// a job started by the first, single-device version (global lock) is still running
			running, progress = true, prog
		}
		out[j] = M{"running": running, "progress": progress}
	}
	return out
}

// legacyOwner: whose job a global lock of the first version is: the lock holder's child process (restic,
// idevicebackup2) names the device in its arguments.
func legacyOwner(p Paths, job, udid string) bool {
	b, err := os.ReadFile(filepath.Join(p.State, job+".lock"))
	if err != nil {
		return false
	}
	pid := strings.TrimSpace(string(b))
	procs, _ := filepath.Glob("/proc/[0-9]*/stat")
	for _, st := range procs {
		raw, err := os.ReadFile(st)
		if err != nil {
			continue
		}
		// /proc/<pid>/stat: pid (comm) state ppid ...
		f := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
		if len(f) < 2 || f[1] != pid {
			continue
		}
		cmd, _ := os.ReadFile(filepath.Join(filepath.Dir(st), "cmdline"))
		if strings.Contains(string(cmd), udid) {
			return true
		}
	}
	return false
}

// KnownDevices: every device with state here (connected now or not).
func KnownDevices(p Paths) []string {
	var out []string
	ents, _ := os.ReadDir(p.State)
	for _, e := range ents {
		if e.IsDir() && udidRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// MigrateState moves the first version's single-device files (State/last-*.json) into State/<udid>/.
func MigrateState(p Paths) {
	for _, name := range []string{"last-backup.json", "last-drive.json", "last-import.json"} {
		old := filepath.Join(p.State, name)
		var rec M
		if !readJSON(old, &rec) {
			continue
		}
		udid, _ := rec["udid"].(string)
		if !udidRe.MatchString(udid) {
			continue
		}
		dst := p.devFile(udid, name)
		if _, err := os.Stat(dst); err != nil {
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			_ = os.Rename(old, dst)
		} else {
			_ = os.Remove(old)
		}
	}
}

// PerDevice is each known device's jobs and history.
func PerDevice(p Paths, cfg Config) M {
	out := M{}
	for _, u := range KnownDevices(p) {
		var dev Device
		readJSON(p.devFile(u, "device.json"), &dev) // last known name/model, also when not connected now
		var apps M
		readJSON(p.devFile(u, "apps.json"), &apps)
		out[u] = M{"device": dev, "apps": apps, "jobs": JobsStatus(p, u), "backup": BackupStatus(p, cfg, u), "drive": DriveStatus(p, cfg, u), "photos": PhotoStatus(p, cfg, u), "free": FreeStatus(p, u)}
	}
	return out
}

// Device is one connected iPhone.
type Device struct {
	UDID        string `json:"udid"`
	Conn        string `json:"conn"`  // usb | network
	Class       string `json:"class"` // iPhone | iPad
	Trusted     bool   `json:"trusted"`
	Name        string `json:"name,omitempty"`
	Model       string `json:"model,omitempty"`
	ProductType string `json:"productType,omitempty"`
	IOS         string `json:"ios,omitempty"`
	Build       string `json:"build,omitempty"`
	Battery     *struct {
		Level    int64 `json:"level"`
		Charging bool  `json:"charging"`
		Full     bool  `json:"full"`
	} `json:"battery,omitempty"`
	Storage *struct {
		Total int64 `json:"total"`
		Free  int64 `json:"free"`
	} `json:"storage,omitempty"`
	BackupEncrypted *bool  `json:"backupEncrypted,omitempty"`
	Error           string `json:"error,omitempty"`
	// Unreachable: listed on USB but its services do not answer (locked/asleep, or the link is stuck); pairing
	// would not help
	Unreachable bool `json:"unreachable,omitempty"`
}

var udidRe = regexp.MustCompile(`^[0-9A-Fa-f-]{24,40}$`)

// ListUDIDs returns connected devices: USB first, then Wi-Fi (network) ones not also on USB.
func ListUDIDs(run Runner) []Device {
	var out []Device
	seen := map[string]bool{}
	for _, conn := range []string{"usb", "network"} {
		flag := "-l"
		if conn == "network" {
			flag = "-n"
		}
		b, err := run("idevice_id", flag)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			fields := strings.Fields(l)
			if len(fields) == 0 {
				continue
			}
			u := fields[0]
			if udidRe.MatchString(u) && !seen[u] {
				seen[u] = true
				out = append(out, Device{UDID: u, Conn: conn})
			}
		}
	}
	return out
}

// Describe fills a device's details; a device that does not trust this PC only gets Trusted=false.
func Describe(run Runner, d Device) Device {
	args := func(extra ...string) []string {
		a := []string{"-u", d.UDID}
		if d.Conn == "network" {
			a = append(a, "-n")
		}
		return append(a, extra...)
	}
	b, err := run("idevicepair", args("validate")...)
	d.Trusted = err == nil && strings.Contains(string(b), "SUCCESS")
	if !d.Trusted {
		if unreachable(string(b)) {
			d.Unreachable = true
			d.Error = "Connected but not answering over USB: unlock it; if that does not help, unplug the cable and plug it in again"
		} else {
			d.Error = pairingHint(string(b))
		}
		return d
	}
	if b, err := run("ideviceinfo", args("-x")...); err == nil {
		if m, err := PlistDict(b); err == nil {
			d.Name, d.ProductType, d.Class = str(m, "DeviceName"), str(m, "ProductType"), str(m, "DeviceClass")
			d.IOS, d.Build = str(m, "ProductVersion"), str(m, "BuildVersion")
			d.Model = ModelName(d.ProductType)
		}
	} else {
		d.Error = strings.TrimSpace(string(b))
	}
	if b, err := run("ideviceinfo", args("-q", "com.apple.mobile.battery", "-x")...); err == nil {
		if m, err := PlistDict(b); err == nil {
			d.Battery = &struct {
				Level    int64 `json:"level"`
				Charging bool  `json:"charging"`
				Full     bool  `json:"full"`
			}{num(m, "BatteryCurrentCapacity"), boolean(m, "BatteryIsCharging"), boolean(m, "FullyCharged")}
		}
	}
	if b, err := run("ideviceinfo", args("-q", "com.apple.disk_usage", "-x")...); err == nil {
		if m, err := PlistDict(b); err == nil && num(m, "TotalDiskCapacity") > 0 {
			free := num(m, "AmountDataAvailable")
			if free == 0 {
				free = num(m, "TotalDataAvailable")
			}
			d.Storage = &struct {
				Total int64 `json:"total"`
				Free  int64 `json:"free"`
			}{num(m, "TotalDiskCapacity"), free}
		}
	}
	if b, err := run("ideviceinfo", args("-q", "com.apple.mobile.backup", "-k", "WillEncrypt")...); err == nil {
		v := strings.TrimSpace(string(b)) == "true"
		d.BackupEncrypted = &v
	}
	return d
}

// unreachable: the device's lockdown service could not be reached at all (usbmuxd "Mux error"), which is not a
// pairing problem: a locked or sleeping iPad/iPhone refuses data connections, or the USB link is stuck.
func unreachable(out string) bool {
	return strings.Contains(out, "Mux error") || strings.Contains(out, "error code -8")
}

func pairingHint(out string) string {
	switch {
	case strings.Contains(out, "PasswordProtected") || strings.Contains(out, "passcode"):
		return "Unlock the iPhone, then pair"
	case strings.Contains(out, "PairingDialogResponsePending") || strings.Contains(out, "trust dialog"):
		return "Tap Trust on the iPhone and type its passcode"
	case strings.Contains(out, "UserDeniedPairing"):
		return "The iPhone said Don't Trust: unplug it, plug it in again and pair"
	}
	return "Not paired with this PC yet: pair it"
}

// Pair asks the iPhone to trust this PC (the user taps Trust and types the passcode).
func Pair(run Runner, udid string) (string, error) {
	b, err := run("idevicepair", "-u", udid, "pair")
	out := strings.TrimSpace(string(b))
	if err != nil || !strings.Contains(out, "SUCCESS") {
		return "", errors.New(pairingHint(out) + " (" + out + ")")
	}
	return out, nil
}

// Model names by ProductType; unknown types fall back to the type itself.
var models = map[string]string{
	"iPhone12,1": "iPhone 11", "iPhone12,3": "iPhone 11 Pro", "iPhone12,5": "iPhone 11 Pro Max", "iPhone12,8": "iPhone SE (2nd gen)",
	"iPhone13,1": "iPhone 12 mini", "iPhone13,2": "iPhone 12", "iPhone13,3": "iPhone 12 Pro", "iPhone13,4": "iPhone 12 Pro Max",
	"iPhone14,4": "iPhone 13 mini", "iPhone14,5": "iPhone 13", "iPhone14,2": "iPhone 13 Pro", "iPhone14,3": "iPhone 13 Pro Max",
	"iPhone14,6": "iPhone SE (3rd gen)", "iPhone14,7": "iPhone 14", "iPhone14,8": "iPhone 14 Plus", "iPhone15,2": "iPhone 14 Pro",
	"iPhone15,3": "iPhone 14 Pro Max", "iPhone15,4": "iPhone 15", "iPhone15,5": "iPhone 15 Plus", "iPhone16,1": "iPhone 15 Pro",
	"iPhone16,2": "iPhone 15 Pro Max", "iPhone17,3": "iPhone 16", "iPhone17,4": "iPhone 16 Plus", "iPhone17,1": "iPhone 16 Pro",
	"iPhone17,2": "iPhone 16 Pro Max", "iPhone17,5": "iPhone 16e",
	"iPad13,18": "iPad (10th gen)", "iPad13,19": "iPad (10th gen)", "iPad14,1": "iPad mini (6th gen)", "iPad14,2": "iPad mini (6th gen)",
	"iPad13,16": "iPad Air (5th gen)", "iPad13,17": "iPad Air (5th gen)", "iPad14,8": "iPad Air 11\" (M2)", "iPad14,9": "iPad Air 11\" (M2)",
	"iPad14,10": "iPad Air 13\" (M2)", "iPad14,11": "iPad Air 13\" (M2)", "iPad16,3": "iPad Pro 11\" (M4)", "iPad16,4": "iPad Pro 11\" (M4)",
	"iPad16,5": "iPad Pro 13\" (M4)", "iPad16,6": "iPad Pro 13\" (M4)", "iPad15,7": "iPad (A16)", "iPad15,8": "iPad (A16)",
}

func ModelName(productType string) string {
	if m, ok := models[productType]; ok {
		return m
	}
	if strings.HasPrefix(productType, "iPad") {
		return "iPad (" + productType + ")"
	}
	return productType
}

// Folder is where a device class's photos go: <photos>/iPhone, <photos>/iPad.
func Folder(cfg Config, class string) string {
	if class != "iPad" {
		class = "iPhone"
	}
	return filepath.Join(cfg.PhotosDir, class)
}

// Tether reports the USB tethering interface (ipheth driver) and its NetworkManager state.
func Tether(run Runner, sysClassNet string) M {
	ents, _ := os.ReadDir(sysClassNet)
	for _, e := range ents {
		drv, err := os.Readlink(filepath.Join(sysClassNet, e.Name(), "device", "driver"))
		if err != nil || filepath.Base(drv) != "ipheth" {
			continue
		}
		state := ""
		if b, err := run("nmcli", "-t", "-g", "GENERAL.STATE", "device", "show", e.Name()); err == nil {
			state = strings.TrimSpace(string(b))
		}
		return M{"iface": e.Name(), "state": state, "connected": strings.HasPrefix(state, "100")}
	}
	return M{"iface": "", "connected": false}
}

// AirPlay is the UxPlay receiver (user unit uxplay.service).
func AirPlay(run Runner, cfg Config) M {
	_, lookErr := exec.LookPath("uxplay")
	b, _ := run("systemctl", "--user", "is-active", "uxplay.service")
	return M{"installed": lookErr == nil, "running": strings.TrimSpace(string(b)) == "active", "name": cfg.AirPlayName}
}

// SetAirPlay starts or stops the receiver; the name goes to the unit's environment file.
func SetAirPlay(run Runner, p Paths, on bool) error {
	cfg := p.LoadConfig()
	env := filepath.Join(filepath.Dir(p.Config), "airplay.env")
	if err := os.MkdirAll(filepath.Dir(env), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(env, []byte("AIRPLAY_NAME="+strings.ReplaceAll(cfg.AirPlayName, "\n", " ")+"\n"), 0o644); err != nil {
		return err
	}
	verb := "stop"
	if on {
		verb = "restart"
	}
	if b, err := run("systemctl", "--user", verb, "uxplay.service"); err != nil {
		return errors.New(strings.TrimSpace(string(b)))
	}
	return nil
}

// KDEDevice is one device kdeconnectd knows (read over D-Bus: the text of `kdeconnect-cli -l` changes with
// the device state, e.g. "on 192.168.1.4 via LAN" for unpaired ones).
type KDEDevice struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"` // phone | tablet | desktop | laptop | tv
	Paired          bool   `json:"paired"`
	Reachable       bool   `json:"reachable"`
	RequestedByPeer bool   `json:"requestedByPeer"` // the device asks to pair: accept or reject here
	Requested       bool   `json:"requested"`       // this PC asked: accept on the device
}

const kdeBus, kdeIface = "org.kde.kdeconnect", "org.kde.kdeconnect.device"

func kdePath(id string) string { return "/modules/kdeconnect/devices/" + id }

// busctl --json=short answers {"type":"s","data":...}; for method calls "data" is the list of return values.
func busData(b []byte) any {
	var r struct {
		Data any `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return r.Data
}

func busFirst(b []byte) any {
	if l, ok := busData(b).([]any); ok && len(l) > 0 {
		return l[0]
	}
	return nil
}

// KDEDevices lists kdeconnectd's devices with their state.
func KDEDevices(run Runner) ([]KDEDevice, error) {
	b, err := run("busctl", "--user", "--json=short", "call", kdeBus, "/modules/kdeconnect", "org.kde.kdeconnect.daemon", "devices", "bb", "false", "false")
	if err != nil {
		return nil, errors.New("kdeconnectd is not running")
	}
	ids, _ := busFirst(b).([]any)
	out := []KDEDevice{}
	for _, v := range ids {
		id, _ := v.(string)
		if !kdeID.MatchString(id) {
			continue
		}
		d := KDEDevice{ID: id}
		prop := func(name string) any {
			b, err := run("busctl", "--user", "--json=short", "get-property", kdeBus, kdePath(id), kdeIface, name)
			if err != nil {
				return nil
			}
			return busData(b)
		}
		call := func(method string) bool {
			b, err := run("busctl", "--user", "--json=short", "call", kdeBus, kdePath(id), kdeIface, method)
			if err != nil {
				return false
			}
			v, _ := busFirst(b).(bool)
			return v
		}
		d.Name, _ = prop("name").(string)
		d.Type, _ = prop("type").(string)
		d.Reachable, _ = prop("isReachable").(bool)
		d.Paired = call("isPaired")
		d.RequestedByPeer = call("isPairRequestedByPeer")
		d.Requested = call("isPairRequested")
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		rank := func(d KDEDevice) int {
			switch {
			case d.RequestedByPeer:
				return 0
			case d.Reachable && d.Paired:
				return 1
			case d.Reachable:
				return 2
			}
			return 3
		}
		return rank(out[i]) < rank(out[j])
	})
	return out, nil
}

func KDEConnect(run Runner) M {
	if _, err := exec.LookPath("kdeconnect-cli"); err != nil {
		return M{"installed": false, "devices": []KDEDevice{}}
	}
	devs, err := KDEDevices(run)
	if devs == nil {
		devs = []KDEDevice{}
	}
	return M{"installed": true, "running": err == nil, "devices": devs}
}

var kdeID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// KDEAction runs one KDE Connect action: pair (request; accept on the device), accept / reject (a request the
// device made), unpair, ring, ping, clipboard (send this PC's clipboard), refresh (look for devices; no id).
func KDEAction(run Runner, action, id string) error {
	if action == "refresh" {
		_, _ = run("kdeconnect-cli", "--refresh")
		return nil
	}
	if !kdeID.MatchString(id) {
		return errors.New("bad device id")
	}
	var name string
	var args []string
	switch action {
	case "pair":
		name, args = "kdeconnect-cli", []string{"--pair", "-d", id}
	case "accept":
		name, args = "busctl", []string{"--user", "call", kdeBus, kdePath(id), kdeIface, "acceptPairing"}
	case "reject":
		name, args = "busctl", []string{"--user", "call", kdeBus, kdePath(id), kdeIface, "cancelPairing"}
	case "unpair":
		name, args = "kdeconnect-cli", []string{"--unpair", "-d", id}
	case "ring":
		name, args = "kdeconnect-cli", []string{"--ring", "-d", id}
	case "ping":
		name, args = "kdeconnect-cli", []string{"--ping-msg", "Hola desde Omarchy", "-d", id}
	case "clipboard":
		clip, err := run("wl-paste", "--no-newline")
		if err != nil || len(clip) == 0 {
			return errors.New("the clipboard is empty")
		}
		name, args = "kdeconnect-cli", []string{"--share-text", string(clip), "-d", id}
	default:
		return errors.New("unknown action " + action)
	}
	if b, err := run(name, args...); err != nil {
		return errors.New(strings.TrimSpace(string(b)))
	}
	return nil
}

// Status is everything the widget shows.
func Status(run Runner, p Paths, sysClassNet string) M {
	cfg := p.LoadConfig()
	devs := ListUDIDs(run)
	MigrateState(p)
	for i := range devs {
		cache := filepath.Join(p.State, devs[i].UDID, "device.json")
		var c Device
		if p.locked(jobKey("backup", devs[i].UDID)) && readJSON(cache, &c) {
			c.Conn = devs[i].Conn
			devs[i] = c // during a backup the iPhone answers other queries slowly: show the last known details
			continue
		}
		devs[i] = Describe(run, devs[i])
		if !devs[i].Trusted && readJSON(cache, &c) {
			// not answering or not paired now: still show which device it is
			devs[i].Name, devs[i].Model, devs[i].Class, devs[i].ProductType = c.Name, c.Model, c.Class, c.ProductType
		}
		if devs[i].Trusted {
			// a busy device (import running) may not answer every query: keep the last known values
			if readJSON(cache, &c) {
				if devs[i].Battery == nil {
					devs[i].Battery = c.Battery
				}
				if devs[i].Storage == nil {
					devs[i].Storage = c.Storage
				}
				if devs[i].Name == "" {
					devs[i].Name, devs[i].Model, devs[i].ProductType, devs[i].IOS, devs[i].Class = c.Name, c.Model, c.ProductType, c.IOS, c.Class
				}
			}
			if devs[i].Name != "" {
				_ = writeJSON(cache, devs[i])
			}
		}
	}
	if devs == nil {
		devs = []Device{}
	}
	return M{
		"devices":      devs,
		"perDevice":    PerDevice(p, cfg),
		"backupDir":    cfg.BackupDir,
		"localBackups": LocalBackups(p, cfg),
		"safe":         SafeStatus(p),
		"tether":       Tether(run, sysClassNet),
		"airplay":      AirPlay(run, cfg),
		"kdeconnect":   KDEConnect(run),
		"mounts":       Mounts(p),
		"gphotos":      GooglePhotosStatus(p, cfg),
		"config":       cfg,
	}
}
