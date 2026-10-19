package hwcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const rel = "7.2.3-arch1-3"

// fakeSystem: fixed files, links and command outputs.
type fakeSystem struct {
	files, links map[string]string
	cmds         map[string]cmdOut
	home         string
}

type cmdOut struct {
	rc  int
	out string
}

func (s *fakeSystem) Home() string { return s.home }
func (s *fakeSystem) Read(p string) (string, bool) {
	v, ok := s.files[p]
	return v, ok
}
func (s *fakeSystem) all() []string {
	var k []string
	for x := range s.files {
		k = append(k, x)
	}
	for x := range s.links {
		k = append(k, x)
	}
	return k
}
func (s *fakeSystem) Exists(p string) bool {
	p = strings.TrimRight(p, "/")
	for _, k := range s.all() {
		if k == p || strings.HasPrefix(k, p+"/") {
			return true
		}
	}
	return false
}
func (s *fakeSystem) ListDir(p string) []string {
	prefix := strings.TrimRight(p, "/") + "/"
	set := map[string]bool{}
	for _, k := range s.all() {
		if strings.HasPrefix(k, prefix) {
			set[strings.SplitN(k[len(prefix):], "/", 2)[0]] = true
		}
	}
	return sortedKeys(set)
}
func (s *fakeSystem) Readlink(p string) (string, bool) { v, ok := s.links[p]; return v, ok }
func (s *fakeSystem) WalkFiles(top string, suffixes ...string) []string {
	prefix := strings.TrimRight(top, "/") + "/"
	var out []string
	for k := range s.files {
		if strings.HasPrefix(k, prefix) && hasSuffix(k, suffixes) && !strings.Contains(k, "/.git/") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
func (s *fakeSystem) Run(_ time.Duration, cmd ...string) (int, string) {
	if r, ok := s.cmds[strings.Join(cmd, " ")]; ok {
		return r.rc, r.out
	}
	return 127, ""
}

func pkgList(p map[string]string) string {
	var b strings.Builder
	for _, k := range sortedKeys(p) {
		fmt.Fprintf(&b, "%s %s\n", k, p[k])
	}
	return b.String()
}

// healthy: AMD + NVIDIA open (DKMS) + MT7925 with Bluetooth, like the real machine.
func healthy() *fakeSystem {
	var mods []string
	for _, m := range []string{"nvidia", "nvidia_drm", "amdgpu", "mt7925e", "btusb", "btmtk", "igc"} {
		mods = append(mods, m+" 16384 0 - Live 0x0")
	}
	files := map[string]string{
		"/proc/sys/kernel/osrelease":                       rel + "\n",
		"/proc/cmdline":                                    "root=/dev/mapper/root rw quiet zswap.enabled=0 btusb.enable_autosuspend=0\n",
		"/proc/modules":                                    strings.Join(mods, "\n") + "\n",
		"/usr/lib/modules/" + rel + "/modules.builtin":     "kernel/drivers/usb/core/usbcore.ko\nkernel/mm/zswap.ko\n",
		"/usr/lib/modules/" + rel + "/vmlinuz":             "",
		"/sys/module/nvidia/version":                       "610.57.04\n",
		"/usr/lib/firmware/nvidia/610.57.04/gsp_ga10x.bin": "",
		"/sys/module/zswap/parameters/enabled":             "N\n",
		"/sys/module/btusb/parameters/enable_autosuspend":  "N\n",
		"/sys/module/btusb/parameters/reset":               "Y\n",
		"/usr/lib/modprobe.d/bluetooth-usb.conf":           "options btusb reset=1\n",
		"/usr/lib/modprobe.d/nvidia-utils.conf":            "blacklist nouveau\n",
		"/etc/default/limine":                              "KERNEL_CMDLINE[default]+=\"root=/dev/mapper/root rw quiet zswap.enabled=0\"\n",
		"/etc/limine-entry-tool.d/claude-bt.conf":          "# x\nKERNEL_CMDLINE[default]+=\" btusb.enable_autosuspend=0\"\n",
		"/sys/bus/pci/devices/0000:00:00.0/class":          "0x060000\n",
		"/sys/bus/pci/devices/0000:00:00.0/vendor":         "0x1022\n",
		"/sys/bus/pci/devices/0000:00:00.0/device":         "0x14d8\n",
		"/sys/bus/pci/devices/0000:01:00.0/class":          "0x030000\n",
		"/sys/bus/pci/devices/0000:01:00.0/vendor":         "0x10de\n",
		"/sys/bus/pci/devices/0000:01:00.0/device":         "0x2504\n",
		"/sys/bus/pci/devices/0000:07:00.0/class":          "0x028000\n",
		"/sys/bus/pci/devices/0000:07:00.0/vendor":         "0x14c3\n",
		"/sys/bus/pci/devices/0000:07:00.0/device":         "0x7925\n",
		"/sys/bus/usb/devices/1-1/idVendor":                "046d\n",
		"/sys/bus/usb/devices/1-1/idProduct":               "c52b\n",
		"/sys/bus/usb/devices/6-11/idVendor":               "0489\n",
		"/sys/bus/usb/devices/6-11/idProduct":              "e13a\n",
		"/sys/class/bluetooth/hci0/x":                      "",
		"/sys/class/rfkill/rfkill0/type":                   "wlan\n",
		"/sys/class/rfkill/rfkill0/name":                   "phy0\n",
		"/sys/class/rfkill/rfkill0/hard":                   "0\n",
		"/sys/class/rfkill/rfkill0/soft":                   "0\n",
		"/sys/class/net/wlp7s0/x":                          "",
		"/sys/class/net/lo/x":                              "",
	}
	links := map[string]string{
		"/sys/bus/pci/devices/0000:01:00.0/driver": "../../../bus/pci/drivers/nvidia",
		"/sys/bus/pci/devices/0000:07:00.0/driver": "../../../bus/pci/drivers/mt7925e",
		"/sys/class/net/wlp7s0/device/driver":      "../../../bus/pci/drivers/mt7925e",
	}
	pk := map[string]string{"linux": "7.2.3.arch1-3", "linux-headers": "7.2.3.arch1-3", "dkms": "3.2.1-1",
		"linux-firmware": "20260810-2", "linux-firmware-amdgpu": "20260810-2", "linux-firmware-mediatek": "20260810-2",
		"nvidia-open-dkms": "610.57.04-1", "nvidia-utils": "610.57.04-1", "lib32-nvidia-utils": "610.57.04-1",
		"mesa": "1:26.2.3-1", "lib32-mesa": "1:26.2.3-1", "bluez": "5.84-1"}
	cmds := map[string]cmdOut{
		"pacman -Q":                          {0, pkgList(pk)},
		"journalctl -k -b -o cat --no-pager": {0, "Linux version 7.2.3\nusb 1-1: new full-speed USB device\n"},
		"lspci -mm":                          {0, `01:00.0 "VGA compatible controller" "NVIDIA Corporation" "GA106 [GeForce RTX 3060]"` + "\n"},
		"pacman -Qqo /usr/lib/modules/" + rel + "/vmlinuz": {0, "linux\n"},
		"dkms status": {0, "nvidia/610.57.04, " + rel + ", x86_64: installed\n"},
		"modinfo -k " + rel + " -F version nvidia":      {0, "610.57.04\n"},
		"systemctl is-enabled bluetooth.service":        {0, "enabled\n"},
		"systemctl --failed --no-legend --plain":        {0, ""},
		"systemctl --user --failed --no-legend --plain": {0, ""},
		"pgrep -x Hyprland":                             {0, "1234\n"},
		"pacman -Dk":                                    {0, "No database errors have been found!\n"},
	}
	return &fakeSystem{files, links, cmds, "/home/test"}
}

func scan(s System, online bool) map[string]*Finding {
	m := map[string]*Finding{}
	for _, x := range RunChecks(NewFacts(s), online, nil) {
		m[x.ID] = x
	}
	return m
}

func setPackage(s *fakeSystem, name, version string) {
	p := ParsePacmanQ(s.cmds["pacman -Q"].out)
	if version == "" {
		delete(p, name)
	} else {
		p[name] = version
	}
	s.cmds["pacman -Q"] = cmdOut{0, pkgList(p)}
}

func lv(t *testing.T, f *Finding, want string) {
	t.Helper()
	if f == nil || f.Level != want {
		t.Fatalf("want %s, got %+v", want, f)
	}
}

func TestHealthyAllOK(t *testing.T) {
	for id, x := range scan(healthy(), false) {
		if x.Level == WARN || x.Level == FAIL {
			t.Errorf("%s %s %s %v", id, x.Level, x.Title, x.Details)
		}
	}
}

func TestDeterministicJSON(t *testing.T) {
	var outs []string
	for i := 0; i < 3; i++ {
		var b bytes.Buffer
		Main([]string{"scan", "--json"}, healthy(), &b, &b)
		outs = append(outs, b.String())
	}
	if outs[0] != outs[1] || outs[1] != outs[2] {
		t.Fatal("not deterministic")
	}
	if ExitCode([]string{OK, INFO}) != 0 || ExitCode([]string{OK, WARN}) != 1 || ExitCode([]string{WARN, FAIL}) != 2 {
		t.Fatal("exit codes")
	}
}

func TestHardware(t *testing.T) {
	s := healthy()
	delete(s.links, "/sys/bus/pci/devices/0000:07:00.0/driver")
	f := scan(s, false)["HW01"]
	lv(t, f, WARN)
	if !strings.Contains(f.Details[0], "0000:07:00.0 [14c3:7925]") {
		t.Fatal(f.Details)
	}
	log := "usb 6-11: device descriptor read/64, error -110\nusb 6-11: device descriptor read/64, error -110\n" +
		"usb usb6-port11: unable to enumerate USB device\nusb 3-2.4: device not accepting address 9, error -71\n" +
		"usb 3-2-port4: unable to enumerate USB device\nusb 1-1: new full-speed USB device number 2\n"
	if !reflect.DeepEqual(UsbEnumErrors(log), map[string]int{"3-2.4": 2, "6-11": 3}) {
		t.Fatal(UsbEnumErrors(log))
	}
	s = healthy()
	s.cmds["journalctl -k -b -o cat --no-pager"] = cmdOut{0, "usb 6-11: device descriptor read/64, error -110\n"}
	if d := scan(s, false)["HW02"].Details; !reflect.DeepEqual(d, []string{"usb 6-11"}) { // counts grow over time
		t.Fatal(d)
	}
	s = healthy()
	s.cmds["journalctl -k -b -o cat --no-pager"] = cmdOut{0, "bluetooth hci0: Direct firmware load for mediatek/mt7925/BT_RAM_CODE.bin failed with error -2\n"}
	f = scan(s, false)["HW03"]
	if f.Level != FAIL || !reflect.DeepEqual(f.Details, []string{"mediatek/mt7925/BT_RAM_CODE.bin"}) {
		t.Fatal(f)
	}
	if len(FirmwareFailures("cfg80211: Direct firmware load for regulatory.db failed with error -2\n"+
		"iwlwifi 0000:00:14.3: Direct firmware load for iwlwifi-so-a0-gf-a0-89.ucode failed with error -2\n")) != 0 {
		t.Fatal("harmless firmware failures")
	}
	s = healthy()
	setPackage(s, "linux-firmware-mediatek", "")
	s.cmds["pacman -T linux-firmware-mediatek"] = cmdOut{127, "linux-firmware-mediatek\n"}
	f = scan(s, false)["HW04"]
	if f.Level != FAIL || !strings.Contains(f.Details[0], "linux-firmware-mediatek") {
		t.Fatal(f)
	}
	s = healthy()
	delete(s.files, "/usr/lib/firmware/nvidia/610.57.04/gsp_ga10x.bin")
	lv(t, scan(s, false)["HW05"], FAIL)
	s = healthy()
	delete(s.files, "/sys/class/bluetooth/hci0/x")
	lv(t, scan(s, false)["HW06"], WARN)
	s = healthy()
	s.files["/sys/class/bluetooth/hci0:50/x"] = "" // an ACL connection, not a second adapter
	if !reflect.DeepEqual(NewFacts(s).BTAdapters, []string{"hci0"}) || len(DiffStates(CollectState(NewFacts(healthy())), CollectState(NewFacts(s)))) != 0 {
		t.Fatal("hci0:50")
	}
	s = healthy()
	s.files["/sys/class/rfkill/rfkill0/hard"] = "1\n"
	lv(t, scan(s, false)["HW07"], WARN)
}

func TestUpdates(t *testing.T) {
	s := healthy()
	s.files["/proc/sys/kernel/osrelease"] = "7.1.0-arch1-1\n"
	lv(t, scan(s, false)["UP01"], WARN)
	s = healthy()
	setPackage(s, "linux-headers", "7.2.2.arch1-1")
	lv(t, scan(s, false)["UP02"], FAIL)
	s = healthy()
	nw := "7.2.4-arch1-1"
	s.files["/usr/lib/modules/"+nw+"/vmlinuz"] = ""
	s.cmds["pacman -Qqo /usr/lib/modules/"+nw+"/vmlinuz"] = cmdOut{0, "linux\n"}
	f := scan(s, false)["UP03"]
	if f.Level != FAIL || !reflect.DeepEqual(f.Details, []string{"nvidia para " + nw}) {
		t.Fatal(f)
	}
	rows := ParseDkmsStatus("hid-xpadneo/v0.10.4, 7.2.3-arch1-3, x86_64: installed\nnvidia/610.57.04: added\n")
	if !reflect.DeepEqual(rows, []DkmsRow{{"hid-xpadneo", "v0.10.4", "7.2.3-arch1-3", "installed"}, {"nvidia", "610.57.04", "", "added"}}) {
		t.Fatal(rows)
	}
	s = healthy()
	for _, p := range []string{"nvidia-utils", "lib32-nvidia-utils", "nvidia-open-dkms"} {
		setPackage(s, p, "615.10-1")
	}
	s.cmds["modinfo -k "+rel+" -F version nvidia"] = cmdOut{0, "615.10\n"}
	lv(t, scan(s, false)["UP04"], WARN)
	s = healthy()
	setPackage(s, "nvidia-utils", "615.10-1")
	setPackage(s, "lib32-nvidia-utils", "615.10-1")
	lv(t, scan(s, false)["UP04"], FAIL)
	s = healthy()
	s.files["/usr/lib/modules/"+nw+"/vmlinuz"] = ""
	s.cmds["pacman -Qqo /usr/lib/modules/"+nw+"/vmlinuz"] = cmdOut{0, "linux\n"}
	for _, p := range []string{"nvidia-utils", "lib32-nvidia-utils", "nvidia-open-dkms"} {
		setPackage(s, p, "615.10-1")
	}
	s.cmds["modinfo -k "+rel+" -F version nvidia"] = cmdOut{0, "615.10\n"}
	lv(t, scan(s, false)["UP04"], FAIL) // the new kernel has no NVIDIA module
}

func TestKernelAndNvidiaUpdatedTogether(t *testing.T) {
	// omarchy update brought a new kernel and NVIDIA; the old kernel still runs: reboot warnings, no failures.
	s := healthy()
	nw := "7.2.4-arch1-1"
	delete(s.files, "/usr/lib/modules/"+rel+"/vmlinuz")
	delete(s.files, "/usr/lib/modules/"+rel+"/modules.builtin")
	delete(s.files, "/usr/lib/firmware/nvidia/610.57.04/gsp_ga10x.bin")
	s.files["/usr/lib/modules/"+nw+"/vmlinuz"] = ""
	s.files["/usr/lib/firmware/nvidia/615.10/gsp_ga10x.bin"] = ""
	s.cmds["pacman -Qqo /usr/lib/modules/"+nw+"/vmlinuz"] = cmdOut{0, "linux\n"}
	s.cmds["dkms status"] = cmdOut{0, "nvidia/615.10, " + nw + ", x86_64: installed\n"}
	delete(s.cmds, "modinfo -k "+rel+" -F version nvidia")
	s.cmds["modinfo -k "+nw+" -F version nvidia"] = cmdOut{0, "615.10\n"}
	for p, v := range map[string]string{"linux": "7.2.4.arch1-1", "linux-headers": "7.2.4.arch1-1", "nvidia-utils": "615.10-1",
		"lib32-nvidia-utils": "615.10-1", "nvidia-open-dkms": "615.10-1"} {
		setPackage(s, p, v)
	}
	f := scan(s, false)
	got := map[string]string{}
	for _, id := range []string{"UP01", "UP02", "UP03", "UP04", "HW05"} {
		got[id] = f[id].Level
	}
	if !reflect.DeepEqual(got, map[string]string{"UP01": WARN, "UP02": OK, "UP03": OK, "UP04": WARN, "HW05": WARN}) {
		t.Fatal(got)
	}
	for _, x := range f {
		if x.Level == FAIL {
			t.Fatal(x)
		}
	}
}

func TestCmdlineAndLogs(t *testing.T) {
	s := healthy()
	s.files["/etc/limine-entry-tool.d/claude-bt.conf"] = "KERNEL_CMDLINE[linux-lts]+=\" foo=1\"\nKERNEL_CMDLINE[default]=\"root=/dev/mapper/root rw\"\n"
	lv(t, scan(s, false)["UP05"], OK)
	s = healthy()
	s.files["/etc/limine-entry-tool.d/claude-usb.conf"] = "KERNEL_CMDLINE[default]+=\" usbcore.initial_descriptor_timeout=1000\"\n"
	f := scan(s, false)["UP05"]
	if f.Level != WARN || !strings.Contains(f.Details[0], "usbcore.initial_descriptor_timeout=1000") {
		t.Fatal(f)
	}
	s = healthy()
	s.cmds["systemctl --failed --no-legend --plain"] = cmdOut{0, "foo.service loaded failed failed Foo\n"}
	f = scan(s, false)["UP06"]
	if f.Level != WARN || !reflect.DeepEqual(f.Details, []string{"sistema: foo.service"}) {
		t.Fatal(f)
	}
	s = healthy()
	s.files[UpdateLog] = "ok\n\x1b[0;31mSomething went wrong during the update!\x1b[0m\r\nerror: failed\n"
	lv(t, scan(s, false)["UP08"], FAIL)
	s = healthy()
	s.files[UpdateLog] = "error: failed retrieving file 'mesa.pkg.tar.zst' from mirror.example\n       error: missing 'libfoo' dependency for 'bar'\n"
	lv(t, scan(s, false)["UP08"], OK)
	s = healthy()
	s.cmds["checkupdates"] = cmdOut{0, "linux 7.2.3.arch1-3 -> 7.2.4.arch1-1\nfirefox 1 -> 2\n"}
	if _, ok := scan(s, false)["UP09"]; ok {
		t.Fatal("UP09 offline")
	}
	f = scan(s, true)["UP09"]
	if !reflect.DeepEqual(f.Details, []string{"linux 7.2.3.arch1-3 -> 7.2.4.arch1-1"}) || !strings.Contains(f.Hint, "ANTES de reiniciar") {
		t.Fatal(f)
	}
}

func TestConflicts(t *testing.T) {
	s := healthy()
	s.cmds["pacman -Dk"] = cmdOut{1, "error: missing 'libfoo' dependency for 'bar'\n"}
	lv(t, scan(s, false)["CF01"], FAIL)
	s = healthy()
	s.files["/etc/pacman.conf.pacnew"] = ""
	s.files["/etc/skel/.git/objects/pack/x.pacnew"] = ""
	if d := scan(s, false)["CF02"].Details; !reflect.DeepEqual(d, []string{"/etc/pacman.conf.pacnew"}) {
		t.Fatal(d)
	}
	s = healthy()
	s.files[ReplacedDir+"/etc/sddm.conf.d/x.conf"] = ""
	if d := scan(s, false)["CF03"].Details; !reflect.DeepEqual(d, []string{"/etc/sddm.conf.d/x.conf"}) {
		t.Fatal(d)
	}
	s = healthy()
	s.files["/etc/modprobe.d/zz.conf"] = "options btusb enable_autosuspend=1\n"
	f := scan(s, false)["CF04"]
	if f.Level != WARN || !strings.Contains(f.Details[0], "btusb.enable_autosuspend") {
		t.Fatal(f)
	}
	s = healthy()
	s.files["/etc/modprobe.d/zz.conf"] = "options btusb enable_autosuspend=N\n"
	lv(t, scan(s, false)["CF04"], OK)
	s = healthy()
	s.files["/etc/modprobe.d/bluetooth-usb.conf"] = "# vacío: anula el de /usr/lib\n"
	s.files["/sys/module/btusb/parameters/reset"] = "N\n"
	lv(t, scan(s, false)["CF06"], OK)
	s = healthy()
	s.files["/etc/modprobe.d/omarchy-usb-autosuspend.conf"] = "options usbcore autosuspend=-1\n"
	f = scan(s, false)["CF05"]
	if f.Level != WARN || !strings.Contains(f.Details[0], "usbcore.autosuspend=-1") {
		t.Fatal(f)
	}
	s.files["/proc/cmdline"] = strings.TrimRight(s.files["/proc/cmdline"], "\n") + " usbcore.autosuspend=-1\n"
	lv(t, scan(s, false)["CF05"], INFO)
	s.files["/proc/cmdline"] = strings.ReplaceAll(s.files["/proc/cmdline"], "usbcore.autosuspend=-1", "usbcore.autosuspend=5")
	lv(t, scan(s, false)["CF05"], WARN)
	s = healthy()
	s.files["/sys/module/btusb/parameters/enable_autosuspend"] = "Y\n"
	lv(t, scan(s, false)["CF06"], WARN)
	// array parameter: the first slot took the value, the rest stay at their default
	s = healthy()
	s.files["/etc/modprobe.d/zz-array.conf"] = "options btusb reset=0x1d6c\n"
	s.files["/sys/module/btusb/parameters/reset"] = "7532,-1,-1\n"
	lv(t, scan(s, false)["CF06"], OK)
	s.files["/sys/module/btusb/parameters/reset"] = "-1,7532,-1\n"
	lv(t, scan(s, false)["CF06"], WARN)
	s = healthy()
	s.files["/proc/modules"] += "nouveau 16384 0 - Live 0x0\n"
	lv(t, scan(s, false)["CF07"], FAIL)
	s = healthy()
	s.files["/etc/modprobe.d/blacklist-xpad.conf"] = "blacklist xpad\n"
	s.files["/proc/modules"] += "xpad 53248 0 - Live 0x0\n"
	lv(t, scan(s, false)["CF07"], FAIL)
	s.files["/etc/udev/rules.d/70-claude-xpad.rules"] = "# a comment that says modprobe nouveau does not count\n" +
		"ACTION==\"add\", SUBSYSTEM==\"usb\", RUN+=\"/usr/bin/modprobe xpad\"\n"
	lv(t, scan(s, false)["CF07"], INFO)
	s.files["/proc/modules"] += "nouveau 16384 0 - Live 0x0\n"
	s.files["/etc/modprobe.d/blacklist-nouveau.conf"] = "blacklist nouveau\n"
	lv(t, scan(s, false)["CF07"], FAIL)
	s = healthy()
	setPackage(s, "nvidia-dkms", "610.57.04-1")
	lv(t, scan(s, false)["CF08"], WARN)
	s = healthy()
	setPackage(s, "lib32-mesa", "1:26.1.0-1")
	f = scan(s, false)["CF09"]
	if f.Level != WARN || !strings.HasPrefix(f.Details[0], "mesa:") {
		t.Fatal(f)
	}
}

func TestBaselineDiff(t *testing.T) {
	st := CollectState(NewFacts(healthy()))
	if len(DiffStates(st, CollectState(NewFacts(healthy())))) != 0 {
		t.Fatal("changes on the same system")
	}
	s := healthy()
	delete(s.links, "/sys/bus/pci/devices/0000:07:00.0/driver")
	delete(s.links, "/sys/class/net/wlp7s0/device/driver")
	delete(s.files, "/sys/class/bluetooth/hci0/x")
	setPackage(s, "mesa", "1:26.3.0-1")
	changes := DiffStates(st, CollectState(NewFacts(s)))
	for _, want := range []Change{{FAIL, "PCI 0000:07:00.0 [14c3:7925]: driver mt7925e → -"}, {FAIL, "interfaz de red wlp7s0 (mt7925e) desapareció"},
		{FAIL, "adaptador Bluetooth hci0 desapareció"}, {INFO, "paquete mesa: 1:26.2.3-1 → 1:26.3.0-1"}} {
		found := false
		for _, c := range changes {
			found = found || c == want
		}
		if !found {
			t.Errorf("missing %v in %v", want, changes)
		}
	}
	var lvs []string
	for _, c := range changes {
		lvs = append(lvs, c.Level)
	}
	if ExitCode(lvs) != 2 {
		t.Fatal("exit")
	}
}

func TestBaselineRoundtrip(t *testing.T) {
	d := t.TempDir()
	t.Setenv("XDG_STATE_HOME", d)
	var b bytes.Buffer
	if Main([]string{"baseline"}, healthy(), &b, &b) != 0 || Main([]string{"baseline"}, healthy(), &b, &b) != 1 || Main([]string{"diff"}, healthy(), &b, &b) != 0 {
		t.Fatal(b.String())
	}
	raw, _ := os.ReadFile(filepath.Join(d, "omarchy-hwcheck", "baseline.json"))
	var st map[string]any
	if json.Unmarshal(raw, &st) != nil || st["kernel"] != rel {
		t.Fatal(string(raw))
	}
	if Main([]string{"bogus"}, healthy(), &b, &b) != 3 || Main([]string{"--only"}, healthy(), &b, &b) != 3 {
		t.Fatal("usage")
	}
}

func TestHelpers(t *testing.T) {
	if UpstreamVersion("1:26.2.3-1") != "26.2.3" || UpstreamVersion("610.57.04-1") != "610.57.04" {
		t.Fatal("upstream")
	}
	for _, c := range [][3]string{{"1", "Y", "t"}, {"0x10", "16", "t"}, {"010", "10", "f"}, {"-1", "-1", "t"}, {"abc", "abc", "t"}, {"yes", "N", "f"}} {
		if sameParamValue(c[0], c[1]) != (c[2] == "t") {
			t.Errorf("%v", c)
		}
	}
	if f, ok := shlexSplit(`01:00.0 "VGA compatible controller" "NVIDIA" "GA106 [x]" -ra1`); !ok || len(f) != 5 || f[1] != "VGA compatible controller" {
		t.Fatal(f)
	}
	if _, ok := shlexSplit(`a "unclosed`); ok {
		t.Fatal("unclosed quote")
	}
}
