package main

// The CLI and the pacman hook against stub pacman/checkupdates binaries,
// plus the hook's guarantees: never non-zero, never longer than 5 s.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

var bin string

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "compat-bin")
	bin = filepath.Join(dir, "omarchy-compat")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

var installed = map[string]string{
	"linux": "7.2.3.arch1-3", "linux-headers": "7.2.3.arch1-3", "nvidia-open-dkms": "610.57.04-1", "nvidia-utils": "610.57.04-1",
	"lib32-nvidia-utils": "610.57.04-1", "opencl-nvidia": "610.57.04-1", "cuda": "13.3.1-1", "hyprland": "0.56.2-1", "vulkan-tools": "1.4-1",
}

type box struct {
	t        *testing.T
	dir, bin string
	env      []string
}

func newBox(t *testing.T) *box {
	d := t.TempDir()
	b := &box{t: t, dir: d, bin: filepath.Join(d, "bin")}
	_ = os.MkdirAll(b.bin, 0o755)
	var names []string
	for k := range installed {
		names = append(names, k)
	}
	sort.Strings(names)
	var inst strings.Builder
	for _, k := range names {
		fmt.Fprintf(&inst, "%s %s\n", k, installed[k])
	}
	b.env = []string{"PATH=" + b.bin + ":" + os.Getenv("PATH"), "STUB_INSTALLED=" + inst.String(),
		"STUB_SYNC=linux 7.3.0.arch1-1\nnvidia-utils 611.1-1\nvulkan-tools 1.4-1\n", "BOOT_PATH=" + d, "ROOT_PATH=" + d, "HOME=" + d}
	// -Si prints a block only for the names asked for, like the real thing
	b.stub("pacman", `case "$1" in -Q) printf "%s" "$STUB_INSTALLED";; -Si) shift; for n in "$@"; do v=$(printf "%s" "$STUB_SYNC" | awk -v n="$n" '$1==n{print $2}'); [ -n "$v" ] && printf "Name            : %s\nVersion         : %s\n\n" "$n" "$v"; done;; *) exit 1;; esac`)
	return b
}

func (b *box) stub(name, body string) {
	_ = os.WriteFile(filepath.Join(b.bin, name), []byte("#!/bin/bash\n"+body+"\n"), 0o755)
}

func (b *box) run(stdin string, args ...string) (string, string, int) {
	cmd := exec.Command(bin, args...)
	cmd.Env = b.env
	cmd.Stdin = strings.NewReader(stdin)
	var o, e strings.Builder
	cmd.Stdout, cmd.Stderr = &o, &e
	_ = cmd.Run()
	return o.String(), e.String(), cmd.ProcessState.ExitCode()
}

func (b *box) pre(updates string, args ...string) (string, int) {
	f := filepath.Join(b.dir, "updates")
	_ = os.WriteFile(f, []byte(updates), 0o644)
	out, _, code := b.run("", append([]string{"preflight", "--from-file", f}, args...)...)
	return out, code
}

func TestPreflightExitCodes(t *testing.T) {
	b := newBox(t)
	if out, code := b.pre(""); code != 0 || strings.Contains(out, "FAIL") {
		t.Fatal(code, out)
	}
	if out, code := b.pre("nvidia-utils 610.57.04-1 -> 611.1-1\n"); code != 2 || !strings.Contains(out, "FAIL C01") {
		t.Fatal(code, out)
	}
	if out, code := b.pre("hyprland 0.56.2-1 -> 0.57-1\n"); code != 1 || !strings.Contains(out, "WARN C11") {
		t.Fatal(code, out)
	}
	a, _ := b.pre("hyprland 0.56.2-1 -> 0.57-1\n")
	c, _ := b.pre("hyprland 0.56.2-1 -> 0.57-1\n")
	if a != c {
		t.Fatal("not deterministic")
	}
	out, _ := b.pre("", "--json")
	var j struct{ Checks []struct{ ID string } }
	if json.Unmarshal([]byte(out), &j) != nil || len(j.Checks) == 0 || j.Checks[0].ID != "C01" {
		t.Fatal(out)
	}
}

func TestPreflightViaCheckupdates(t *testing.T) {
	b := newBox(t)
	b.stub("checkupdates", `echo "linux 7.2.3.arch1-3 -> 7.3.0.arch1-1"; exit 0`)
	if out, _, code := b.run("", "preflight"); code != 2 || !strings.Contains(out, "FAIL C02") {
		t.Fatal(code, out)
	}
	b.stub("checkupdates", "exit 1") // offline / mirror down: cannot evaluate, not a FAIL
	if out, _, code := b.run("", "preflight"); code != 3 || !strings.Contains(out, "WARN C00") || strings.Contains(out, "FAIL") {
		t.Fatal(code, out)
	}
}

func TestUsage(t *testing.T) {
	b := newBox(t)
	if _, e, code := b.run("", "preflight", "--from-file"); code != 3 || !strings.Contains(e, "uso:") {
		t.Fatal(code, e)
	}
	if _, _, code := b.run(""); code != 3 {
		t.Fatal(code)
	}
}

func TestHookWarnsButExitsZero(t *testing.T) {
	b := newBox(t)
	out, _, code := b.run("linux\nnvidia-utils\n", "hook")
	if code != 0 || !strings.Contains(out, "[omarchy-compat] ROMPERÍA C01") || !strings.Contains(out, "[omarchy-compat] ROMPERÍA C02") || !strings.Contains(out, "2 paquetes revisados") {
		t.Fatal(code, out)
	}
	if out, _, _ := b.run("vulkan-tools\n", "hook"); strings.Count(strings.TrimSpace(out), "\n") != 0 || !strings.Contains(out, "1 paquetes revisados, 0 avisos") {
		t.Fatal(out)
	}
	a, _, _ := b.run("linux\nnvidia-utils\n", "hook")
	c, _, _ := b.run("nvidia-utils\nlinux\n", "hook")
	if a != c {
		t.Fatal("order matters")
	}
}

func TestHookNeverFails(t *testing.T) {
	b := newBox(t)
	for _, in := range []string{"", "\n\n", "\x00\xff", "no-such-pkg\n"} {
		if _, _, code := b.run(in, "hook"); code != 0 {
			t.Fatalf("%q: %d", in, code)
		}
	}
	b.stub("pacman", `case "$1" in -Q) printf "%s" "$STUB_INSTALLED";; -Si) printf "Name : vulkan-tools\nVersion : 1.4-1\n\nName : vulkan-tools\nVersion : 9.9-1\n\n";; esac`)
	if out, _, _ := b.run("vulkan-tools\n", "hook"); !strings.Contains(out, "0 avisos") { // second repo's block ignored
		t.Fatal(out)
	}
	b.stub("pacman", `case "$1" in -Q) printf "%s" "$STUB_INSTALLED";; *) exit 1;; esac`)
	if out, _, code := b.run("linux\n", "hook"); code != 0 || !strings.Contains(out, "no se pudo evaluar") || strings.Contains(out, "0 avisos") {
		t.Fatal(code, out)
	}
	b.stub("pacman", "echo boom >&2; kill -9 $$")
	if _, _, code := b.run("linux\n", "hook"); code != 0 {
		t.Fatal(code)
	}
	b.stub("pacman", "exec sleep 30")
	t0 := time.Now()
	if _, _, code := b.run("linux\n", "hook"); code != 0 || time.Since(t0) > 5500*time.Millisecond {
		t.Fatal(code, time.Since(t0))
	}
}

func TestWrapperAndHookFile(t *testing.T) {
	drv := filepath.Join("..", "..", "..", "drivers", "pacman-hook")
	w, _ := os.ReadFile(filepath.Join(drv, "compat-hook"))
	h, _ := os.ReadFile(filepath.Join(drv, "90-omarchy-compat.hook"))
	ws, hs := string(w), string(h)
	if !strings.HasSuffix(strings.TrimSpace(ws), "exit 0") || !strings.Contains(ws, "timeout -k 1 5") || !strings.Contains(ws, "/usr/local/lib/omarchy/omarchy-compat hook") {
		t.Fatal(ws)
	}
	for _, want := range []string{"[Trigger]", "[Action]", "When = PreTransaction", "NeedsTargets", "Exec = /usr/local/lib/omarchy/compat-hook",
		"Operation = Install", "Operation = Upgrade", "Target = *", "Type = Package"} {
		if !strings.Contains(hs, want) {
			t.Error("hook file lacks " + want)
		}
	}
	if strings.Contains(hs, "AbortOnFail") || strings.Contains(hs, "Operation = Remove") {
		t.Fatal("hook must never abort and only sees installs/upgrades")
	}
	// the wrapper pointed at a missing binary still exits 0
	d := t.TempDir()
	bad := filepath.Join(d, "hook")
	_ = os.WriteFile(bad, []byte(strings.ReplaceAll(ws, "/usr/local/lib/omarchy/omarchy-compat", "/nonexistent/x")), 0o755)
	cmd := exec.Command(bad)
	cmd.Stdin = strings.NewReader("linux\n")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}
