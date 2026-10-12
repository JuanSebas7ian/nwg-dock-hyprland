package hwcheck

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var modprobeDirs = []string{"/etc/modprobe.d", "/run/modprobe.d", "/usr/local/lib/modprobe.d", "/usr/lib/modprobe.d"}

const (
	limineDropinDir = "/etc/limine-entry-tool.d"
	UpdateLog       = "/tmp/omarchy-update.log"
	ReplacedDir     = "/var/lib/omarchy/replaced"
	btReference     = "~/.claude/skills/omarchy-hardware/references/bluetooth-mt7925.md"
)

var limineCmdlineFiles = []string{"/etc/default/limine"}

type pciDev struct{ ID, Class, Driver, Name string }
type usbDev struct{ ID, Name string }
type rfkill struct {
	Type, Name string
	Hard, Soft bool
}

// Facts is everything the checks read, read once.
type Facts struct {
	sys        System
	Release    string
	Cmdline    []string
	Loaded     []string
	Builtin    []string
	Packages   map[string]string
	KernelLog  string
	KernelOK   bool
	PCI        map[string]pciDev
	USB        map[string]usbDev
	Net        map[string]string
	BTAdapters []string
	Rfkill     []rfkill
	Kernels    map[string]string // release -> package
	Dkms       []DkmsRow
}

func (f *Facts) read(p string) string { s, _ := f.sys.Read(p); return s }

func uniqSorted(in []string) []string {
	set := map[string]bool{}
	for _, x := range in {
		set[x] = true
	}
	return sortedKeys(set)
}

func NewFacts(s System) *Facts {
	f := &Facts{sys: s}
	f.Release = strings.TrimSpace(f.read("/proc/sys/kernel/osrelease"))
	f.Cmdline = strings.Fields(f.read("/proc/cmdline"))
	var loaded []string
	for _, l := range splitLines(f.read("/proc/modules")) {
		if w := strings.Fields(l); len(w) > 0 {
			loaded = append(loaded, normMod(w[0]))
		}
	}
	f.Loaded = uniqSorted(loaded)
	var builtin []string
	for _, l := range splitLines(f.read("/usr/lib/modules/" + f.Release + "/modules.builtin")) {
		if l = strings.TrimSpace(l); strings.HasSuffix(l, ".ko") {
			b := base(l)
			builtin = append(builtin, normMod(b[:len(b)-3]))
		}
	}
	f.Builtin = uniqSorted(builtin)
	f.Packages = map[string]string{}
	if rc, out := s.Run(60*time.Second, "pacman", "-Q"); rc == 0 {
		f.Packages = ParsePacmanQ(out)
	}
	if rc, out := s.Run(120*time.Second, "journalctl", "-k", "-b", "-o", "cat", "--no-pager"); rc == 0 {
		f.KernelLog = out
		f.KernelOK = strings.TrimSpace(out) != ""
	}
	f.PCI = f.pci()
	f.USB = f.usb()
	f.Net = map[string]string{}
	for _, iface := range s.ListDir("/sys/class/net") {
		if l, ok := s.Readlink("/sys/class/net/" + iface + "/device/driver"); ok && l != "" {
			f.Net[iface] = base(l)
		}
	}
	for _, d := range s.ListDir("/sys/class/bluetooth") {
		if strings.HasPrefix(d, "hci") && !strings.Contains(d, ":") { // hciN:H = a connection, not an adapter
			f.BTAdapters = append(f.BTAdapters, d)
		}
	}
	for _, r := range s.ListDir("/sys/class/rfkill") {
		b := "/sys/class/rfkill/" + r
		hard, soft := f.read(b+"/hard"), f.read(b+"/soft")
		f.Rfkill = append(f.Rfkill, rfkill{strings.TrimSpace(f.read(b + "/type")), strings.TrimSpace(f.read(b + "/name")),
			strings.TrimSpace(hard) == "1", strings.TrimSpace(soft) == "1"})
	}
	sort.SliceStable(f.Rfkill, func(i, j int) bool {
		if f.Rfkill[i].Type != f.Rfkill[j].Type {
			return f.Rfkill[i].Type < f.Rfkill[j].Type
		}
		return f.Rfkill[i].Name < f.Rfkill[j].Name
	})
	f.Kernels = map[string]string{}
	for _, rel := range s.ListDir("/usr/lib/modules") {
		vmlinuz := "/usr/lib/modules/" + rel + "/vmlinuz"
		if s.Exists(vmlinuz) {
			if rc, out := s.Run(60*time.Second, "pacman", "-Qqo", vmlinuz); rc == 0 && strings.TrimSpace(out) != "" {
				f.Kernels[rel] = strings.TrimSpace(out)
			}
		}
	}
	if rc, out := s.Run(60*time.Second, "dkms", "status"); rc == 0 {
		f.Dkms = ParseDkmsStatus(out)
	}
	return f
}

func (f *Facts) pci() map[string]pciDev {
	names := map[string]string{}
	if rc, out := f.sys.Run(60*time.Second, "lspci", "-mm"); rc == 0 {
		names = ParseLspciMM(out)
	}
	devs := map[string]pciDev{}
	for _, slot := range f.sys.ListDir("/sys/bus/pci/devices") {
		b := "/sys/bus/pci/devices/" + slot
		cls := strings.TrimSpace(f.read(b + "/class"))
		if len(cls) > 6 {
			cls = cls[:6]
		}
		drv := ""
		if l, ok := f.sys.Readlink(b + "/driver"); ok && l != "" {
			drv = base(l)
		}
		short := slot
		if strings.Count(slot, ":") == 2 {
			short = strings.SplitN(slot, ":", 2)[1]
		}
		hex := func(p string) string {
			v := strings.TrimSpace(f.read(b + p))
			if len(v) >= 2 {
				return v[2:]
			}
			return ""
		}
		devs[slot] = pciDev{hex("/vendor") + ":" + hex("/device"), cls, drv, names[short]}
	}
	return devs
}

func (f *Facts) usb() map[string]usbDev {
	devs := map[string]usbDev{}
	for _, d := range f.sys.ListDir("/sys/bus/usb/devices") {
		if strings.Contains(d, ":") {
			continue
		}
		b := "/sys/bus/usb/devices/" + d
		vid := strings.TrimSpace(f.read(b + "/idVendor"))
		if vid == "" {
			continue
		}
		devs[d] = usbDev{vid + ":" + strings.TrimSpace(f.read(b+"/idProduct")), strings.TrimSpace(f.read(b + "/product"))}
	}
	return devs
}

var udevModprobeRe = regexp.MustCompile(`modprobe\s+(?:-\S+\s+)*([A-Za-z0-9_-]+)`)

// IntentionalLoads: modules a udev rule loads explicitly (RUN+="/usr/bin/modprobe <mod>") -> rule file.
func (f *Facts) IntentionalLoads() map[string]string {
	found := map[string]string{}
	for _, d := range []string{"/usr/lib/udev/rules.d", "/etc/udev/rules.d"} {
		for _, name := range f.sys.ListDir(d) {
			if !strings.HasSuffix(name, ".rules") {
				continue
			}
			for _, line := range splitLines(f.read(d + "/" + name)) {
				if strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") || !strings.Contains(line, "RUN") {
					continue
				}
				for _, m := range udevModprobeRe.FindAllStringSubmatch(line, -1) {
					found[normMod(m[1])] = d + "/" + name
				}
			}
		}
	}
	return found
}

// ModprobeConfig: the effective modprobe.d files (a name in /etc hides the same name in /usr/lib).
func (f *Facts) ModprobeConfig() ([]modOpt, [][2]string) {
	chosen := map[string]string{}
	for _, d := range modprobeDirs {
		for _, name := range f.sys.ListDir(d) {
			if _, seen := chosen[name]; strings.HasSuffix(name, ".conf") && !seen {
				chosen[name] = d + "/" + name
			}
		}
	}
	var opts []modOpt
	var black [][2]string
	for _, name := range sortedKeys(chosen) {
		o, b := ParseModprobe(f.read(chosen[name]))
		for _, x := range o {
			opts = append(opts, modOpt{x[0], x[1], x[2], chosen[name]})
		}
		for _, m := range b {
			black = append(black, [2]string{m, chosen[name]})
		}
	}
	return opts, black
}

// LimineTokens: token -> file of the configured boot line (default + drop-ins).
func (f *Facts) LimineTokens() map[string]string {
	files := append([]string{}, limineCmdlineFiles...)
	for _, name := range f.sys.ListDir(limineDropinDir) {
		if strings.HasSuffix(name, ".conf") {
			files = append(files, limineDropinDir+"/"+name)
		}
	}
	tokens := map[string]string{}
	for _, p := range files {
		for _, line := range splitLines(f.read(p)) {
			m := kcmdRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if m[1] == "=" { // "=" replaces everything before it
				tokens = map[string]string{}
			}
			for _, t := range strings.Fields(m[2]) {
				if _, ok := tokens[t]; !ok {
					tokens[t] = p
				}
			}
		}
	}
	return tokens
}

func (f *Facts) has(pkg string) bool { _, ok := f.Packages[pkg]; return ok }

func (f *Facts) isLoaded(m string) bool {
	i := sort.SearchStrings(f.Loaded, m)
	return i < len(f.Loaded) && f.Loaded[i] == m
}

var _ = fmt.Sprint
