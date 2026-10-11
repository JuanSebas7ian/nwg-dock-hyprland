// Package drivers says, per component, whether the drivers of this PC are up
// to date, whether a reboot is pending, and what can be updated (packages,
// firmware through fwupd, BIOS) — for the Hardware widget's Drivers tab.
package drivers

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Group is a set of packages that make up one driver component.
type Group struct {
	Name     string
	Packages []string
	Why      string
}

// Groups are the driver components of this machine, in display order.
var Groups = []Group{
	{"Kernel", []string{"linux", "linux-headers", "linux-lts", "linux-lts-headers"}, "every driver is built for it"},
	{"NVIDIA (RTX 3060)", []string{"nvidia-open-dkms", "nvidia-dkms", "nvidia", "nvidia-utils", "lib32-nvidia-utils", "libva-nvidia-driver", "egl-wayland", "nvidia-container-toolkit"}, "display, games, CUDA"},
	{"CUDA / AI", []string{"cuda", "cudnn", "ollama-cuda", "llama-cpp", "ggml-cuda"}, "local models"},
	{"Firmware & microcode", []string{"linux-firmware", "linux-firmware-mediatek", "linux-firmware-nvidia", "linux-firmware-amdgpu", "linux-firmware-intel", "linux-firmware-realtek", "amd-ucode", "sof-firmware"}, "Wi-Fi/Bluetooth MT7925, Radeon, CPU fixes"},
	{"Graphics (Radeon, Vulkan)", []string{"mesa", "lib32-mesa", "vulkan-radeon", "lib32-vulkan-radeon", "vulkan-icd-loader", "lib32-vulkan-icd-loader"}, "integrated GPU, Vulkan games"},
	{"Bluetooth & audio", []string{"bluez", "bluez-utils", "pipewire", "wireplumber", "alsa-ucm-conf"}, "speakers, headsets, pads"},
	{"Peripherals", []string{"xpadneo-dkms", "hid-xpadneo-dkms", "solaar", "fwupd"}, "Xbox pad, Logitech, firmware tool"},
	{"Boot", []string{"limine", "limine-mkinitcpio-hook", "mkinitcpio", "systemd", "dkms"}, "boot loader, initramfs, DKMS"},
}

// Pkg is one installed driver package.
type Pkg struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Available string `json:"available,omitempty"`
}

// Component is one row of the tab.
type Component struct {
	Name     string `json:"name"`
	Why      string `json:"why"`
	Status   string `json:"status"` // ok | update | reboot
	Packages []Pkg  `json:"packages"`
	Note     string `json:"note,omitempty"`
}

// Firmware is a device fwupd can update.
type Firmware struct {
	Device    string `json:"device"`
	Current   string `json:"current"`
	Available string `json:"available"`
	Summary   string `json:"summary,omitempty"`
}

// Status is the whole tab.
type Status struct {
	Summary      string      `json:"summary"`
	State        string      `json:"state"` // ok | update | reboot
	Components   []Component `json:"components"`
	Firmware     []Firmware  `json:"firmware"`
	FirmwareErr  string      `json:"firmwareError,omitempty"`
	DKMS         []string    `json:"dkms"`
	DKMSOk       bool        `json:"dkmsOk"`
	BIOS         *BIOS       `json:"bios,omitempty"`
	OtherUpdates []Pkg       `json:"otherUpdates"`
	TotalUpdates int         `json:"totalUpdates"`
	CheckedAt    int64       `json:"checkedAt"`
	UpdatesError string      `json:"updatesError,omitempty"`
}

// BIOS comes from the Hardware backend's daily ASUS check.
type BIOS struct {
	Installed string `json:"installed"`
	Latest    string `json:"latest"`
	Date      string `json:"date"`
	Beta      bool   `json:"beta"`
	Newer     bool   `json:"newer"`
	Page      string `json:"page"`
}

// Env is everything read from the system (fake in tests).
type Env struct {
	Installed     map[string]string // pacman -Q
	Updates       []Pkg             // checkupdates (Name, Installed=from, Available=to)
	UpdatesAt     int64
	UpdatesError  string
	RunningKernel string // uname -r
	LoadedNVIDIA  string // /sys/module/nvidia/version
	DKMS          string // dkms status
	Firmware      []Firmware
	FirmwareErr   string
	BIOS          *BIOS
}

// kernelVersion turns pacman's "7.2.3.arch1-3" into uname's "7.2.3-arch1-3".
func kernelVersion(pkg string) string {
	if i := strings.LastIndex(pkg, ".arch"); i >= 0 {
		return pkg[:i] + "-" + pkg[i+1:]
	}
	return pkg
}

func pkgBase(v string) string { // "610.57.04-1" -> "610.57.04"
	if i := strings.LastIndex(v, "-"); i > 0 {
		return v[:i]
	}
	return v
}

// Build turns the raw environment into the tab's status.
func Build(e Env) Status {
	st := Status{Components: []Component{}, Firmware: e.Firmware, DKMS: []string{}, OtherUpdates: []Pkg{},
		BIOS: e.BIOS, CheckedAt: e.UpdatesAt, UpdatesError: e.UpdatesError, FirmwareErr: e.FirmwareErr, TotalUpdates: len(e.Updates)}
	if st.Firmware == nil {
		st.Firmware = []Firmware{}
	}
	avail := map[string]string{}
	for _, u := range e.Updates {
		avail[u.Name] = u.Available
	}
	inGroup := map[string]bool{}
	pending, reboot := 0, false
	for _, g := range Groups {
		c := Component{Name: g.Name, Why: g.Why, Status: "ok", Packages: []Pkg{}}
		for _, name := range g.Packages {
			v, ok := e.Installed[name]
			if !ok {
				continue
			}
			inGroup[name] = true
			p := Pkg{Name: name, Installed: v, Available: avail[name]}
			if p.Available != "" {
				c.Status = "update"
				pending++
			}
			c.Packages = append(c.Packages, p)
		}
		if len(c.Packages) == 0 {
			continue
		}
		switch g.Name {
		case "Kernel":
			if v := e.Installed["linux"]; v != "" && e.RunningKernel != "" && kernelVersion(v) != e.RunningKernel {
				c.Note = "running " + e.RunningKernel + ", installed " + kernelVersion(v) + ": reboot to use it"
				reboot = true
			}
		case "NVIDIA (RTX 3060)":
			for _, n := range []string{"nvidia-open-dkms", "nvidia-dkms", "nvidia"} {
				if v := e.Installed[n]; v != "" && e.LoadedNVIDIA != "" && pkgBase(v) != e.LoadedNVIDIA {
					c.Note = "loaded " + e.LoadedNVIDIA + ", installed " + pkgBase(v) + ": reboot to use it"
					reboot = true
				}
			}
		}
		if c.Note != "" && c.Status == "ok" {
			c.Status = "reboot"
		}
		st.Components = append(st.Components, c)
	}
	for _, u := range e.Updates {
		if !inGroup[u.Name] {
			st.OtherUpdates = append(st.OtherUpdates, u)
		}
	}
	st.DKMSOk = true
	for _, l := range strings.Split(strings.TrimSpace(e.DKMS), "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		st.DKMS = append(st.DKMS, l)
		if !strings.HasSuffix(l, ": installed") && !strings.Contains(l, "installed (WARNING") {
			st.DKMSOk = false
		}
	}
	fw := len(st.Firmware)
	switch {
	case pending > 0 || fw > 0:
		st.State = "update"
		var parts []string
		if pending > 0 {
			parts = append(parts, plural(pending, "driver package"))
		}
		if fw > 0 {
			parts = append(parts, plural(fw, "firmware update"))
		}
		st.Summary = strings.Join(parts, " and ") + " available"
	case reboot:
		st.State = "reboot"
		st.Summary = "Drivers updated: reboot to load them"
	case !st.DKMSOk:
		st.State = "update"
		st.Summary = "A DKMS module is not built: open the report"
	default:
		st.State = "ok"
		st.Summary = "All drivers and firmware are up to date"
	}
	if e.BIOS != nil && e.BIOS.Newer {
		beta := ""
		if e.BIOS.Beta {
			beta = " (beta)"
		}
		st.Summary += " · BIOS " + e.BIOS.Latest + beta + " available"
	}
	return st
}

func plural(n int, s string) string {
	if n == 1 {
		return "1 " + s
	}
	return strconv.Itoa(n) + " " + s + "s"
}

// ---------------------------------------------------------------- reading the system

var updateLineRe = regexp.MustCompile(`^(\S+)\s+(\S+)\s+->\s+(\S+)`)

// ParseCheckupdates reads checkupdates output.
func ParseCheckupdates(out string) []Pkg {
	var pkgs []Pkg
	for _, l := range strings.Split(out, "\n") {
		if m := updateLineRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			pkgs = append(pkgs, Pkg{Name: m[1], Installed: m[2], Available: m[3]})
		}
	}
	return pkgs
}

// ParseFwupd reads `fwupdmgr get-updates --json`.
func ParseFwupd(data []byte) []Firmware {
	var r struct {
		Devices []struct {
			Name     string `json:"Name"`
			Version  string `json:"Version"`
			Releases []struct {
				Version string `json:"Version"`
				Summary string `json:"Summary"`
			} `json:"Releases"`
		} `json:"Devices"`
	}
	out := []Firmware{}
	if json.Unmarshal(data, &r) != nil {
		return out
	}
	for _, d := range r.Devices {
		if len(d.Releases) == 0 {
			continue
		}
		out = append(out, Firmware{Device: d.Name, Current: d.Version, Available: d.Releases[0].Version, Summary: d.Releases[0].Summary})
	}
	return out
}

// hwCache is the Hardware backend's cache folder, shared so checkupdates and
// the ASUS check run once for both.
func hwCache() string {
	if c := os.Getenv("XDG_CACHE_HOME"); c != "" {
		return filepath.Join(c, "omarchy-hardware")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "omarchy-hardware")
}

func output(name string, args ...string) string {
	out, _ := exec.Command(name, args...).Output()
	return string(out)
}

// ReadEnv reads the system; force re-runs checkupdates and fwupd.
func ReadEnv(force bool) Env {
	e := Env{Installed: map[string]string{}}
	for _, l := range strings.Split(output("pacman", "-Q"), "\n") {
		if f := strings.Fields(l); len(f) == 2 {
			e.Installed[f[0]] = f[1]
		}
	}
	e.RunningKernel = strings.TrimSpace(output("uname", "-r"))
	if b, err := os.ReadFile("/sys/module/nvidia/version"); err == nil {
		e.LoadedNVIDIA = strings.TrimSpace(string(b))
	}
	e.DKMS = output("dkms", "status")

	cache := filepath.Join(hwCache(), "updates.json")
	var cached struct {
		Packages  []struct{ Name, From, To string } `json:"packages"`
		CheckedAt int64                             `json:"checkedAt"`
	}
	fresh := false
	if b, err := os.ReadFile(cache); err == nil && json.Unmarshal(b, &cached) == nil && cached.CheckedAt > 0 {
		fresh = time.Since(time.Unix(cached.CheckedAt, 0)) < time.Hour
	}
	if force || !fresh {
		cmd := exec.Command("checkupdates")
		out, err := cmd.Output()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if err == nil || code == 2 { // 2 = no updates
			pk := ParseCheckupdates(string(out))
			cached.Packages = cached.Packages[:0]
			for _, p := range pk {
				cached.Packages = append(cached.Packages, struct{ Name, From, To string }{p.Name, p.Installed, p.Available})
			}
			cached.CheckedAt = time.Now().Unix()
			if b, err := json.Marshal(map[string]any{"packages": toHW(pk), "checkedAt": cached.CheckedAt}); err == nil {
				_ = os.MkdirAll(filepath.Dir(cache), 0o755)
				_ = os.WriteFile(cache, b, 0o644)
			}
		} else if cached.CheckedAt == 0 {
			e.UpdatesError = "checkupdates failed (offline?)"
		}
	}
	for _, p := range cached.Packages {
		e.Updates = append(e.Updates, Pkg{Name: p.Name, Installed: p.From, Available: p.To})
	}
	e.UpdatesAt = cached.CheckedAt

	fwCache := filepath.Join(hwCache(), "fwupd.json")
	if st, err := os.Stat(fwCache); force || err != nil || time.Since(st.ModTime()) > 6*time.Hour {
		out, err := exec.Command("fwupdmgr", "get-updates", "--json", "--no-unreported-check", "--no-metadata-check").Output()
		if err == nil || len(out) > 0 {
			_ = os.WriteFile(fwCache, out, 0o644)
		}
	}
	if b, err := os.ReadFile(fwCache); err == nil {
		e.Firmware = ParseFwupd(b)
	} else {
		e.FirmwareErr = "fwupd not checked yet"
	}

	var bios struct {
		Version string `json:"version"`
		Date    string `json:"date"`
		Beta    bool   `json:"beta"`
		Page    string `json:"page"`
	}
	if b, err := os.ReadFile(filepath.Join(hwCache(), "bios.json")); err == nil && json.Unmarshal(b, &bios) == nil && bios.Version != "" {
		inst, _ := os.ReadFile("/sys/class/dmi/id/bios_version")
		e.BIOS = &BIOS{Installed: strings.TrimSpace(string(inst)), Latest: bios.Version, Date: bios.Date, Beta: bios.Beta, Page: bios.Page}
		e.BIOS.Newer = newerVersion(bios.Version, e.BIOS.Installed)
	}
	return e
}

func toHW(pk []Pkg) []map[string]string {
	out := make([]map[string]string, 0, len(pk))
	for _, p := range pk {
		out = append(out, map[string]string{"name": p.Name, "from": p.Installed, "to": p.Available})
	}
	return out
}

var digitsRe = regexp.MustCompile(`\d+`)

// newerVersion compares ASUS BIOS numbers ("1804" > "1402").
func newerVersion(latest, installed string) bool {
	l, i := digitsRe.FindString(latest), digitsRe.FindString(installed)
	if l == "" || i == "" {
		return false
	}
	if len(l) != len(i) {
		return len(l) > len(i)
	}
	return l > i
}

// SortedNames is a helper for tests and stable output.
func SortedNames(pk []Pkg) []string {
	n := make([]string, 0, len(pk))
	for _, p := range pk {
		n = append(n, p.Name)
	}
	sort.Strings(n)
	return n
}
