package compat

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var base = map[string]string{
	"linux": "7.2.3.arch1-3", "linux-headers": "7.2.3.arch1-3", "amd-ucode": "20260810-2",
	"nvidia-open-dkms": "610.57.04-1", "nvidia-utils": "610.57.04-1", "lib32-nvidia-utils": "610.57.04-1",
	"opencl-nvidia": "610.57.04-1", "cuda": "13.3.1-1", "cudnn": "9.25.1.1-1", "ollama-cuda": "0.33.3-1",
	"mesa": "1:26.2.2-1", "lib32-mesa": "1:26.2.2-1", "vulkan-radeon": "1:26.2.2-1", "lib32-vulkan-radeon": "1:26.2.2-1",
	"vulkan-icd-loader": "1.4.357.0-1", "lib32-vulkan-icd-loader": "1.4.357.0-1",
	"gamemode": "1.8.2-3", "lib32-gamemode": "1.8.2-1", "mangohud": "0.8.4-1", "lib32-mangohud": "0.8.4-1",
	"xpadneo-dkms": "0.10.4-1.1", "hyprland": "0.56.2-1", "quickshell": "0.3-1", "omarchy": "4.0.4-1",
}

func ctx(pending map[string]string, boot int, root float64) *Ctx {
	c := NewCtx(base, pending)
	c.BootFreeMB, c.RootFreeGB = &boot, &root
	return c
}

func cx(p map[string]string) *Ctx { return ctx(p, 649, 292) }

func level(res []Result, id string) []string {
	var out []string
	for _, r := range res {
		if r.ID == id {
			out = append(out, r.Level)
		}
	}
	return out
}

func is(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func put(t *testing.T, p, text string) {
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHelpers(t *testing.T) {
	if Upstream("1:26.2.2-1") != "1:26.2.2" {
		t.Fatal("upstream")
	}
	if mm, _ := MajorMinor("7.2.3.arch1-3"); mm != [2]int{7, 2} {
		t.Fatal(mm)
	}
	if m, _ := Major("610.57.04-1"); m != 610 {
		t.Fatal(m)
	}
	if !reflect.DeepEqual(ParseUpdates("a 1-1 -> 2-1\nb 1:3-1 -> 1:4-1\n"), map[string]string{"a": "2-1", "b": "1:4-1"}) {
		t.Fatal("parse")
	}
	found := false
	for _, d := range ElfDirs {
		found = found || d == "/usr/lib32/"
	}
	if !found {
		t.Fatal("lib32 not scanned")
	}
}

func TestFamilies(t *testing.T) {
	for _, r := range RuleFamilies(cx(nil)) {
		if r.Level != OK {
			t.Fatal(r)
		}
	}
	is(t, level(RuleFamilies(cx(map[string]string{"nvidia-utils": "611.1-1"})), "C01"), FAIL)
	all := map[string]string{}
	for _, k := range []string{"nvidia-utils", "lib32-nvidia-utils", "opencl-nvidia", "nvidia-open-dkms"} {
		all[k] = "611.1-1"
	}
	is(t, level(RuleFamilies(cx(all)), "C01"), OK)
	is(t, level(RuleFamilies(cx(map[string]string{"linux": "7.2.4.arch1-1"})), "C02"), FAIL)
	is(t, level(RuleFamilies(cx(map[string]string{"linux": "7.2.4.arch1-1", "linux-headers": "7.2.4.arch1-1"})), "C02"), OK)
	is(t, level(RuleFamilies(cx(map[string]string{"lib32-mesa": "1:26.3.0-1"})), "C07"), FAIL)
	r := RuleFamilies(cx(map[string]string{"lib32-vulkan-icd-loader": "1.5-1", "mangohud": "0.9-1"}))
	is(t, level(r, "C08a"), WARN)
	is(t, level(r, "C08c"), WARN)
	is(t, level(r, "C08b"), OK)
	is(t, level(RuleFamilies(cx(map[string]string{"gamemode": "1.8.2-9"})), "C08b"), OK) // pkgrel only
	inst := map[string]string{}
	for k, v := range base {
		if k != "lib32-mangohud" {
			inst[k] = v
		}
	}
	is(t, level(RuleFamilies(NewCtx(inst, nil)), "C08c"))
}

func TestKernelRules(t *testing.T) {
	is(t, level(RuleC03(cx(nil)), "C03"), OK)
	is(t, level(RuleC03(cx(map[string]string{"linux": "7.2.4.arch1-1"})), "C03"), OK)
	is(t, level(RuleC03(cx(map[string]string{"linux": "7.3.1.arch1-1"})), "C03"), WARN)
	is(t, level(RuleC03(cx(map[string]string{"linux": "7.3.1.arch1-1", "nvidia-open-dkms": "611.1-1"})), "C03"), OK)
	is(t, level(RuleC04(cx(nil)), "C04"), OK)
	r := RuleC04(cx(map[string]string{"linux": "7.3.1.arch1-1"}))
	if r[0].Level != WARN || !strings.Contains(r[0].Text, "xpadneo-dkms") {
		t.Fatal(r)
	}
	up := map[string]string{"linux": "7.2.4.arch1-1"}
	is(t, level(RuleC09(cx(nil)), "C09"), OK)
	is(t, level(RuleC09(ctx(up, 649, 292)), "C09"), OK)
	is(t, level(RuleC09(ctx(up, 150, 292)), "C09"), FAIL)
	is(t, level(RuleC09(ctx(map[string]string{"linux-firmware-nvidia": "2-1"}, 150, 292)), "C09"), FAIL)
	is(t, level(RuleC09(ctx(map[string]string{"vim": "9-1"}, 10, 292)), "C09"), OK)
	for _, p := range []string{"nvidia-open-dkms", "nvidia-utils", "mkinitcpio", "limine", "limine-snapper-sync", "amd-ucode"} {
		is(t, level(RuleC09(ctx(map[string]string{p: "99-1"}, 150, 292)), "C09"), FAIL)
	}
	is(t, level(RuleC10(cx(nil)), "C10"), OK)
	is(t, level(RuleC10(ctx(map[string]string{"vim": "9-1"}, 649, 50)), "C10"), OK)
	is(t, level(RuleC10(ctx(map[string]string{"vim": "9-1"}, 649, 2.5)), "C10"), WARN)
	is(t, level(RuleC11(cx(map[string]string{"vim": "9-1"})), "C11"), OK)
	for _, p := range []string{"hyprland", "quickshell", "omarchy", "omarchy-shell"} {
		is(t, level(RuleC11(cx(map[string]string{p: "99-1"})), "C11"), WARN)
	}
	is(t, level(RuleC11(cx(map[string]string{"omarchy-nvim": "99-1"})), "C11"), OK)
}

func TestCudaRules(t *testing.T) {
	is(t, level(RuleC05(cx(nil)), "C05"), OK)
	is(t, level(RuleC05(cx(map[string]string{"nvidia-utils": "570.1-1"})), "C05"), FAIL)
	is(t, level(RuleC05(cx(map[string]string{"cuda": "12.9.1-1", "nvidia-utils": "550.1-1"})), "C05"), OK)
	is(t, level(RuleC05(cx(map[string]string{"cuda": "12.9.1-1", "nvidia-utils": "500.1-1"})), "C05"), FAIL)
	is(t, level(RuleC05(cx(map[string]string{"cuda": "14.0-1"})), "C05"), WARN)
	is(t, level(RuleC06(cx(map[string]string{"cuda": "13.4.0-1"})), "C06"), OK)
	is(t, level(RuleC06(cx(map[string]string{"cuda": "14.0-1"})), "C06"), WARN)
	is(t, level(RuleC06(cx(map[string]string{"cuda": "14.0-1", "cudnn": "10-1", "ollama-cuda": "0.34-1"})), "C06"), OK)
}

// ---------- post ----------

type fake struct {
	t    *testing.T
	proc string
	over map[string]func([]string) (int, string)
	env  map[string]string
}

func good(t *testing.T) (*Ctx, *fake) {
	proc := t.TempDir()
	put(t, filepath.Join(proc, "driver/nvidia/params"), "PreserveVideoMemoryAllocations: 1\nUseKernelSuspendNotifiers: 1\nOther: 0\n")
	put(t, filepath.Join(proc, "driver/nvidia/version"), "NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  610.57.04  Release Build  (root@x)\nGCC version: gcc\n")
	f := &fake{t: t, proc: proc, over: map[string]func([]string) (int, string){}, env: map[string]string{}}
	c := NewCtx(map[string]string{"nvidia-utils": "610.57.04-1", "ollama": "0.33.3-1"}, nil)
	c.ProcRoot, c.Kernel, c.HasSession = proc, "7.2.3-arch1-3", true
	c.Run = func(cmd []string, _ time.Duration, env map[string]string) (int, string) {
		if fn, ok := f.over[cmd[0]]; ok {
			return fn(cmd)
		}
		switch cmd[0] {
		case "dkms":
			return 0, "nvidia/610.57.04, 7.2.3-arch1-3, x86_64: installed\nnvidia/609, 7.1.0-arch1-1, x86_64: built\n"
		case "nvidia-smi":
			return 0, "610.57.04\n"
		case "vulkaninfo":
			return 0, "GPU0: NVIDIA GeForce RTX 3060\nGPU1: AMD Radeon (RADV RAPHAEL_MENDOCINO)\n"
		case "vainfo":
			for k, v := range env {
				f.env[k] = v
			}
			return 0, "vainfo: Driver version: VA-API NVDEC driver\nVAProfileH264Main : VAEntrypointVLD\n"
		case "hyprctl", "systemctl":
			return 0, ""
		}
		return 127, ""
	}
	c.CuInit = func() int { return 0 }
	c.OllamaUp = func() bool { return true }
	return c, f
}

func levels(c *Ctx) map[string]string {
	m := map[string]string{}
	for _, r := range RuleC13(c) {
		m[r.ID] = r.Level
	}
	return m
}

func TestC13AllGood(t *testing.T) {
	c, f := good(t)
	t.Setenv("LIBVA_DRIVER_NAME", "")
	lv := levels(c)
	var ids []string
	for id, l := range lv {
		ids = append(ids, id)
		if l != OK {
			t.Errorf("%s %s", id, l)
		}
	}
	sort.Strings(ids)
	if strings.Join(ids, " ") != "C13a C13b C13c C13d C13e C13f C13g C13h C13i C13j" {
		t.Fatal(ids)
	}
	if f.env["LIBVA_DRIVER_NAME"] != "nvidia" {
		t.Fatal("LIBVA_DRIVER_NAME not set for vainfo")
	}
}

func TestC13Failures(t *testing.T) {
	c, f := good(t)
	c.Installed["nvidia-utils"] = "611.2-1"
	if levels(c)["C13a"] != FAIL {
		t.Fatal("mismatch")
	}
	os.Remove(filepath.Join(f.proc, "driver/nvidia/version"))
	if levels(c)["C13a"] != FAIL {
		t.Fatal("not loaded")
	}
	for name, out := range map[string]string{"dkms": "nvidia/610.57.04, 7.2.3-arch1-3, x86_64: built\n", "vulkaninfo": "GPU0: AMD RADV only\n",
		"vainfo": "vainfo: Mesa Gallium\nVAProfileH264Main\n", "hyprctl": "Config error in file x line 3\n", "systemctl": "foo.service loaded failed failed Foo\n"} {
		c, f := good(t)
		o := out
		f.over[name] = func([]string) (int, string) { return 0, o }
		id := map[string]string{"dkms": "C13b", "vulkaninfo": "C13e", "vainfo": "C13f", "hyprctl": "C13h", "systemctl": "C13i"}[name]
		if levels(c)[id] != FAIL {
			t.Errorf("%s should fail", id)
		}
	}
	c, f = good(t)
	f.over["nvidia-smi"] = func([]string) (int, string) { return 9, "" }
	c.CuInit = func() int { return 100 }
	c.OllamaUp = func() bool { return false }
	lv := levels(c)
	if lv["C13c"] != FAIL || lv["C13d"] != FAIL || lv["C13g"] != FAIL {
		t.Fatal(lv)
	}
	c, _ = good(t)
	c.HasSession = false
	if levels(c)["C13h"] != SKIP {
		t.Fatal("no session")
	}
	c, f = good(t)
	put(t, filepath.Join(f.proc, "driver/nvidia/params"), "PreserveVideoMemoryAllocations: 0\nUseKernelSuspendNotifiers: 1\n")
	if levels(c)["C13j"] != FAIL {
		t.Fatal("params")
	}
	os.Remove(filepath.Join(f.proc, "driver/nvidia/params"))
	if levels(c)["C13j"] != FAIL {
		t.Fatal("no params")
	}
}

func TestC12AURLibs(t *testing.T) {
	d := t.TempDir()
	elf, txt, own := filepath.Join(d, "usr/bin/tool"), filepath.Join(d, "usr/bin/script"), filepath.Join(d, "usr/lib/foo/libcef.so")
	put(t, elf, "\x7fELF fake")
	put(t, txt, "#!/bin/sh\n")
	put(t, own, "x")
	old := ElfDirs
	ElfDirs = []string{d + "/usr/"}
	defer func() { ElfDirs = old }()
	c := NewCtx(nil, nil)
	c.AUR = []string{"foo"}
	c.KnownGaps = map[string]map[string]string{}
	set := func(files []string, ldd string) {
		c.Run = func(cmd []string, _ time.Duration, _ map[string]string) (int, string) {
			if cmd[0] == "pacman" {
				var b strings.Builder
				for _, f := range files {
					b.WriteString("foo " + f + "\n")
				}
				return 0, b.String()
			}
			return 0, ldd
		}
	}
	set([]string{elf, txt}, "\tlibgone.so.1 => not found\n\tlibc.so.6 => /usr/lib/libc.so.6\n")
	if r := RuleC12(c); r[0].Level != FAIL || !strings.Contains(r[0].Text, "libgone.so.1") {
		t.Fatal(r)
	}
	set([]string{elf}, "\tlibc.so.6 => /usr/lib/libc.so.6\n")
	is(t, level(RuleC12(c), "C12"), OK)
	is(t, level(RuleC12(NewCtx(nil, nil)), "C12"), OK)
	set([]string{elf, own}, "\tlibcef.so => not found\n") // bundled by the package itself
	if r := RuleC12(c); r[0].Level != OK || !strings.Contains(r[0].Text, "incluidas") {
		t.Fatal(r)
	}
	set([]string{elf}, "\t/opt/foo/lib/libQt6Core.so.6: version `Qt_6.11' not found (required by /usr/lib/libQt6LabsAnimation.so.6)\n")
	if r := RuleC12(c); r[0].Level != WARN || !strings.Contains(r[0].Text, "Qt_6.11") {
		t.Fatal(r)
	}
	c.KnownGaps = map[string]map[string]string{"foo": {"libgone.so.1": "vendor slip"}}
	set([]string{elf}, "\tlibgone.so.1 => not found\n")
	if r := RuleC12(c); r[0].Level != WARN || !strings.Contains(r[0].Text, "vendor slip") {
		t.Fatal(r)
	}
	c.KnownGaps = map[string]map[string]string{"bar": {"libgone.so.1": "other package"}}
	is(t, level(RuleC12(c), "C12"), FAIL)
}

func TestC14AndFindPacnew(t *testing.T) {
	c := NewCtx(nil, nil)
	is(t, level(RuleC14(c), "C14"), OK)
	c.Pacnew = []string{"/etc/pacman.conf.pacnew"}
	is(t, level(RuleC14(c), "C14"), WARN)
	d := t.TempDir()
	etc, stable := filepath.Join(d, "etc"), filepath.Join(d, "stable")
	put(t, filepath.Join(etc, "a.pacnew"), "x")
	put(t, filepath.Join(etc, "b.conf"), "x")
	t.Setenv("ETC_PATH", etc)
	t.Setenv("STABLE_DIR", stable)
	if got := FindPacnew(); !reflect.DeepEqual(got, []string{filepath.Join(etc, "a.pacnew")}) {
		t.Fatal(got)
	}
	snap := filepath.Join(stable, "2026-10-04", "snapshots.txt")
	put(t, snap, "root=1\n")
	future := time.Now().Add(100 * time.Second)
	_ = os.Chtimes(snap, future, future)
	if got := FindPacnew(); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestKnownGapsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "g.json")
	put(t, p, `{"_doc": "x", "c12": {"zoom": {"libQt6Bodymovin.so.6": "slip"}}}`)
	if LoadKnownGaps(p)["zoom"]["libQt6Bodymovin.so.6"] != "slip" || len(LoadKnownGaps("/nonexistent")) != 0 {
		t.Fatal("known gaps")
	}
}
