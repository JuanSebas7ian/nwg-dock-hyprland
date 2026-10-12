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
}

func (p Paths) LoadConfig() Config {
	c := Config{PhotosDir: filepath.Join(p.Home, "Pictures", "iPhone"), BackupDir: filepath.Join(p.Home, "iPhoneBackup"),
		AutoImport: true, AutoBackupDays: 0, AirPlayName: "Omarchy"}
	if b, err := os.ReadFile(p.Config); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	c.PhotosDir = expand(c.PhotosDir, p.Home)
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

// Device is one connected iPhone.
type Device struct {
	UDID        string `json:"udid"`
	Conn        string `json:"conn"` // usb | network
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
		d.Error = pairingHint(string(b))
		return d
	}
	if b, err := run("ideviceinfo", args("-x")...); err == nil {
		if m, err := PlistDict(b); err == nil {
			d.Name, d.ProductType = str(m, "DeviceName"), str(m, "ProductType")
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
}

func ModelName(productType string) string {
	if m, ok := models[productType]; ok {
		return m
	}
	return productType
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

// KDEConnect lists the devices kdeconnectd knows.
type KDEDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Paired    bool   `json:"paired"`
	Reachable bool   `json:"reachable"`
}

var kdeLine = regexp.MustCompile(`^- (.+): (\S+) \((.+)\)$`)

func ParseKDEConnect(out string) []KDEDevice {
	var ds []KDEDevice
	for _, l := range strings.Split(out, "\n") {
		m := kdeLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		st := m[3]
		ds = append(ds, KDEDevice{ID: m[2], Name: m[1], Paired: strings.Contains(st, "paired") && !strings.Contains(st, "unpaired") && !strings.Contains(st, "not paired"),
			Reachable: strings.Contains(st, "reachable") && !strings.Contains(st, "unreachable") && !strings.Contains(st, "not reachable")})
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].Reachable && !ds[j].Reachable })
	return ds
}

func KDEConnect(run Runner) M {
	if _, err := exec.LookPath("kdeconnect-cli"); err != nil {
		return M{"installed": false, "devices": []KDEDevice{}}
	}
	b, err := run("kdeconnect-cli", "-l")
	devs := ParseKDEConnect(string(b))
	if devs == nil {
		devs = []KDEDevice{}
	}
	return M{"installed": true, "running": err == nil, "devices": devs}
}

var kdeID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// KDEAction runs one KDE Connect action on a device: pair, unpair, ring, ping, clipboard (send this PC's clipboard).
func KDEAction(run Runner, action, id string) error {
	if !kdeID.MatchString(id) {
		return errors.New("bad device id")
	}
	var args []string
	switch action {
	case "pair":
		args = []string{"--pair", "-d", id}
	case "unpair":
		args = []string{"--unpair", "-d", id}
	case "ring":
		args = []string{"--ring", "-d", id}
	case "ping":
		args = []string{"--ping-msg", "Hola desde Omarchy", "-d", id}
	case "clipboard":
		clip, err := run("wl-paste", "--no-newline")
		if err != nil || len(clip) == 0 {
			return errors.New("the clipboard is empty")
		}
		args = []string{"--share-text", string(clip), "-d", id}
	default:
		return errors.New("unknown action " + action)
	}
	if b, err := run("kdeconnect-cli", args...); err != nil {
		return errors.New(strings.TrimSpace(string(b)))
	}
	return nil
}

// Status is everything the widget shows.
func Status(run Runner, p Paths, sysClassNet string) M {
	cfg := p.LoadConfig()
	devs := ListUDIDs(run)
	for i := range devs {
		devs[i] = Describe(run, devs[i])
	}
	if devs == nil {
		devs = []Device{}
	}
	return M{
		"devices":    devs,
		"photos":     PhotoStatus(p, cfg),
		"backup":     BackupStatus(p, cfg),
		"tether":     Tether(run, sysClassNet),
		"airplay":    AirPlay(run, cfg),
		"kdeconnect": KDEConnect(run),
		"mounts":     Mounts(p),
		"config":     cfg,
	}
}
