// Package compat validates driver updates: preflight (will the pending
// updates break drivers or apps?), post (does everything work right now?)
// and the pacman PreTransaction hook. No root needed.
//
// Output: "LEVEL ID text" per rule (OK, WARN, FAIL, SKIP). Exit 0 all OK,
// 1 warnings, 2 something would break, 3 could not evaluate.
// Env: BOOT_PATH, ROOT_PATH, PROC_ROOT, STABLE_DIR, ETC_PATH (fakes for tests),
// OMARCHY_KNOWN_GAPS (default: known-gaps.json next to the executable).
package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	OK   = "OK"
	WARN = "WARN"
	FAIL = "FAIL"
	SKIP = "SKIP"
)

// Result is one rule outcome.
type Result struct{ Level, ID, Text string }

// Minimum NVIDIA driver (major branch) per CUDA major.
var CudaMinDriver = map[int]int{12: 525, 13: 580}

// Packages whose update rebuilds the UKI / writes to /boot.
var kernelTriggers = []string{"linux", "amd-ucode", "nvidia-open-dkms", "nvidia-utils", "mkinitcpio"}
var sessionPkgs = []string{"hyprland", "quickshell", "omarchy-shell", "omarchy"}

const (
	BootMinMB = 200
	RootMinGB = 5
)

// ElfDirs is where C12 looks for binaries of AUR packages.
var ElfDirs = []string{"/usr/bin/", "/usr/lib/", "/usr/lib32/", "/opt/"}

type family struct {
	id, level string
	members   []string
}

var families = []family{
	{"C01", FAIL, []string{"nvidia-utils", "lib32-nvidia-utils", "opencl-nvidia", "nvidia-open-dkms"}},
	{"C02", FAIL, []string{"linux", "linux-headers"}},
	{"C07", FAIL, []string{"mesa", "lib32-mesa", "vulkan-radeon", "lib32-vulkan-radeon"}},
	{"C08a", WARN, []string{"vulkan-icd-loader", "lib32-vulkan-icd-loader"}},
	{"C08b", WARN, []string{"gamemode", "lib32-gamemode"}},
	{"C08c", WARN, []string{"mangohud", "lib32-mangohud"}},
}

// Upstream drops the pkgrel: "1:26.2.2-1" -> "1:26.2.2".
func Upstream(v string) string {
	if i := strings.LastIndex(v, "-"); i >= 0 {
		return v[:i]
	}
	return v
}

var mmRe = regexp.MustCompile(`^(?:\d+:)?(\d+)\.(\d+)`)
var majRe = regexp.MustCompile(`^(?:\d+:)?(\d+)`)

// MajorMinor of a version, ok=false if it has none.
func MajorMinor(v string) ([2]int, bool) {
	m := mmRe.FindStringSubmatch(v)
	if m == nil {
		return [2]int{}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return [2]int{a, b}, true
}

func Major(v string) (int, bool) {
	m := majRe.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

// Runner runs a command: (rc, stdout+stderr); 127 if missing, 124 on timeout.
type Runner func(cmd []string, timeout time.Duration, env map[string]string) (int, string)

func Run(cmd []string, timeout time.Duration, env map[string]string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Env = append(os.Environ(), "LC_ALL=C")
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
	out, err := c.CombinedOutput()
	if ctx.Err() != nil {
		return 124, ""
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		}
		return 127, ""
	}
	return 0, string(out)
}

// Ctx is the system snapshot the rules work on; tests build it directly.
type Ctx struct {
	Installed, Pending, Final map[string]string
	BootFreeMB                *int
	RootFreeGB                *float64
	DkmsPkgs                  []string
	Run                       Runner
	CuInit                    func() int
	OllamaUp                  func() bool
	AUR, Pacnew               []string
	ProcRoot, Kernel          string
	HasSession                bool
	KnownGaps                 map[string]map[string]string // nil = read known-gaps.json
}

func NewCtx(installed, pending map[string]string) *Ctx {
	c := &Ctx{Installed: map[string]string{}, Pending: map[string]string{}, Final: map[string]string{}, Run: Run,
		CuInit: func() int { return -1 }, OllamaUp: OllamaUp, ProcRoot: envOr("PROC_ROOT", "/proc"), Kernel: kernelRelease(),
		HasSession: os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != ""}
	for k, v := range installed {
		c.Installed[k], c.Final[k] = v, v
		if strings.HasSuffix(k, "-dkms") && !strings.HasPrefix(k, "nvidia") {
			c.DkmsPkgs = append(c.DkmsPkgs, k)
		}
	}
	sort.Strings(c.DkmsPkgs)
	for k, v := range pending {
		c.Pending[k], c.Final[k] = v, v
	}
	return c
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func kernelRelease() string {
	var u syscall.Utsname
	if syscall.Uname(&u) != nil {
		return ""
	}
	var b []byte
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

func (c *Ctx) changed(name string) bool {
	v, ok := c.Pending[name]
	return ok && Upstream(v) != Upstream(c.Installed[name])
}

func in(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sortedPending(c *Ctx, keep func(string) bool) []string {
	var out []string
	for n := range c.Pending {
		if keep(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// ---------- preflight rules ----------

func RuleFamilies(c *Ctx) []Result {
	var out []Result
	for _, f := range families {
		var have []string
		for _, m := range f.members {
			if _, ok := c.Final[m]; ok {
				have = append(have, m)
			}
		}
		if len(have) < 2 {
			continue
		}
		vers := map[string]bool{}
		var detail []string
		for _, m := range have {
			vers[Upstream(c.Final[m])] = true
			detail = append(detail, m+" "+Upstream(c.Final[m]))
		}
		if len(vers) == 1 {
			out = append(out, Result{OK, f.id, fmt.Sprintf("%s: misma versión %s", strings.Join(have, ", "), Upstream(c.Final[have[0]]))})
		} else {
			out = append(out, Result{f.level, f.id, "versiones desparejas tras la actualización: " + strings.Join(detail, ", ")})
		}
	}
	return out
}

func RuleC03(c *Ctx) []Result {
	if !c.changed("linux") {
		return []Result{{OK, "C03", "el kernel no cambia"}}
	}
	if _, ok := c.Final["nvidia-open-dkms"]; !ok {
		return []Result{{OK, "C03", "sin nvidia-open-dkms"}}
	}
	if c.changed("nvidia-open-dkms") {
		return []Result{{OK, "C03", "kernel y nvidia-open-dkms cambian juntos; DKMS recompila"}}
	}
	old, _ := MajorMinor(c.Installed["linux"])
	nw, _ := MajorMinor(c.Final["linux"])
	if old != nw {
		return []Result{{WARN, "C03", fmt.Sprintf("salto de kernel %s -> %s sin nuevo nvidia-open-dkms %s: el módulo DKMS podría no compilar",
			c.Installed["linux"], c.Final["linux"], c.Final["nvidia-open-dkms"])}}
	}
	return []Result{{OK, "C03", fmt.Sprintf("kernel con el mismo %d.%d; DKMS recompilará nvidia", nw[0], nw[1])}}
}

func RuleC04(c *Ctx) []Result {
	if len(c.DkmsPkgs) == 0 {
		return []Result{{OK, "C04", "sin otros módulos DKMS"}}
	}
	if c.changed("linux") {
		return []Result{{WARN, "C04", fmt.Sprintf("el kernel cambia: tras actualizar revisa `dkms status` (%s)", strings.Join(c.DkmsPkgs, ", "))}}
	}
	return []Result{{OK, "C04", "el kernel no cambia; módulos DKMS: " + strings.Join(c.DkmsPkgs, ", ")}}
}

func RuleC05(c *Ctx) []Result {
	cudaV, drv := c.Final["cuda"], c.Final["nvidia-utils"]
	if cudaV == "" || drv == "" {
		return []Result{{OK, "C05", "sin cuda o sin nvidia-utils"}}
	}
	cm, _ := Major(cudaV)
	need, ok := CudaMinDriver[cm]
	if !ok {
		return []Result{{WARN, "C05", fmt.Sprintf("CUDA %s: sin dato de driver mínimo en la tabla", cudaV)}}
	}
	if dm, ok := Major(drv); !ok || dm < need {
		return []Result{{FAIL, "C05", fmt.Sprintf("CUDA %s exige driver >= %d y quedaría %s", cudaV, need, drv)}}
	}
	return []Result{{OK, "C05", fmt.Sprintf("CUDA %s con driver %s (mínimo %d)", cudaV, drv, need)}}
}

func RuleC06(c *Ctx) []Result {
	if _, ok := c.Installed["cuda"]; !ok {
		return []Result{{OK, "C06", "cuda no instalado"}}
	}
	a, _ := Major(c.Final["cuda"])
	b, _ := Major(c.Installed["cuda"])
	if a == b {
		return []Result{{OK, "C06", "cuda mantiene su versión mayor"}}
	}
	var stale []string
	for _, n := range []string{"cudnn", "ollama-cuda"} {
		if _, ok := c.Installed[n]; ok && !c.changed(n) {
			stale = append(stale, n)
		}
	}
	if len(stale) > 0 {
		return []Result{{WARN, "C06", fmt.Sprintf("cuda cambia a %s y no se actualizan: %s", c.Final["cuda"], strings.Join(stale, ", "))}}
	}
	return []Result{{OK, "C06", fmt.Sprintf("cuda cambia a %s y cudnn/ollama-cuda lo acompañan", c.Final["cuda"])}}
}

func kernelish(n string) bool {
	return in(kernelTriggers, n) || strings.HasPrefix(n, "linux-firmware") || strings.HasPrefix(n, "limine")
}

func RuleC09(c *Ctx) []Result {
	hit := sortedPending(c, func(n string) bool { return kernelish(n) && c.changed(n) })
	if len(hit) == 0 {
		return []Result{{OK, "C09", "ninguna actualización toca /boot"}}
	}
	if c.BootFreeMB == nil {
		return []Result{{WARN, "C09", "no se pudo medir /boot"}}
	}
	if *c.BootFreeMB < BootMinMB {
		return []Result{{FAIL, "C09", fmt.Sprintf("/boot tiene %d MB libres (< %d) y cambian: %s", *c.BootFreeMB, BootMinMB, strings.Join(hit, ", "))}}
	}
	return []Result{{OK, "C09", fmt.Sprintf("/boot con %d MB libres (>= %d)", *c.BootFreeMB, BootMinMB)}}
}

func RuleC10(c *Ctx) []Result {
	if len(c.Pending) == 0 {
		return []Result{{OK, "C10", "sin actualizaciones pendientes"}}
	}
	if c.RootFreeGB == nil {
		return []Result{{WARN, "C10", "no se pudo medir /"}}
	}
	if *c.RootFreeGB < RootMinGB {
		return []Result{{WARN, "C10", fmt.Sprintf("/ tiene %.1f GB libres (< %d)", *c.RootFreeGB, RootMinGB)}}
	}
	return []Result{{OK, "C10", fmt.Sprintf("/ con %.0f GB libres (>= %d)", *c.RootFreeGB, RootMinGB)}}
}

func RuleC11(c *Ctx) []Result {
	hit := sortedPending(c, func(n string) bool { return c.changed(n) && in(sessionPkgs, n) })
	if len(hit) == 0 {
		return []Result{{OK, "C11", "ni Hyprland, Quickshell ni Omarchy cambian"}}
	}
	return []Result{{WARN, "C11", fmt.Sprintf("cambian %s: reinicia la sesión y ejecuta `omarchy restart shell` (plugins propios de la barra)", strings.Join(hit, ", "))}}
}

type Rule func(*Ctx) []Result

var PreflightRules = []Rule{RuleFamilies, RuleC03, RuleC04, RuleC05, RuleC06, RuleC09, RuleC10, RuleC11}

// HookRules: preflight without C10 (free space on / is not urgent mid-transaction).
var HookRules = []Rule{RuleFamilies, RuleC03, RuleC04, RuleC05, RuleC06, RuleC09, RuleC11}

// ---------- post rules ----------

func OllamaUp() bool {
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://127.0.0.1:11434/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// KnownGapsFile: $OMARCHY_KNOWN_GAPS, else known-gaps.json next to the executable.
func KnownGapsFile() string {
	if p := os.Getenv("OMARCHY_KNOWN_GAPS"); p != "" {
		return p
	}
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "known-gaps.json")
}

// LoadKnownGaps reads {pkg: {lib: reason}} accepted for C12 (packaging slips of AUR apps).
func LoadKnownGaps(path string) map[string]map[string]string {
	var f struct {
		C12 map[string]map[string]string `json:"c12"`
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &f) != nil || f.C12 == nil {
		return map[string]map[string]string{}
	}
	return f.C12
}

func underElfDirs(p string) bool {
	for _, d := range ElfDirs {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}

func isELF(p string) bool {
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 4)
	n, _ := f.Read(b)
	return n == 4 && string(b) == "\x7fELF"
}

func setList(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func first(l []string, n int) []string {
	if len(l) > n {
		return l[:n]
	}
	return l
}

// RuleC12: AUR binaries whose libraries do not resolve. Apps with their own
// runtime load it through their launcher, which ldd does not see: a missing
// library that the same package contains is bundled, not missing. A
// symbol-version clash inside such an app only affects it (WARN); a library
// no package provides is a FAIL.
func RuleC12(c *Ctx) []Result {
	if len(c.AUR) == 0 {
		return []Result{{OK, "C12", "sin paquetes de AUR"}}
	}
	gaps := c.KnownGaps
	if gaps == nil {
		gaps = LoadKnownGaps(KnownGapsFile())
	}
	var bad, clash []string
	accepted := map[string]bool{}
	scanned, bundled := 0, 0
	for _, pkg := range c.AUR {
		_, out := c.Run([]string{"pacman", "-Ql", pkg}, 15*time.Second, nil)
		var paths []string
		own := map[string]bool{}
		for _, line := range strings.Split(out, "\n") {
			if line == "" {
				continue
			}
			p := line
			if i := strings.Index(line, " "); i >= 0 {
				p = line[i+1:]
			}
			paths = append(paths, p)
			own[filepath.Base(p)] = true
		}
		for _, path := range paths {
			if !underElfDirs(path) || !isELF(path) {
				continue
			}
			if scanned >= 300 {
				break
			}
			scanned++
			_, ldd := c.Run([]string{"ldd", path}, 10*time.Second, nil)
			miss, versions := map[string]bool{}, map[string]bool{}
			for _, l := range strings.Split(ldd, "\n") {
				if strings.Contains(l, "=> not found") {
					lib := strings.TrimSpace(strings.SplitN(l, "=>", 2)[0])
					switch {
					case own[lib]:
						bundled++
					case gaps[pkg][lib] != "":
						accepted[fmt.Sprintf("%s: %s (%s)", pkg, lib, gaps[pkg][lib])] = true
					default:
						miss[lib] = true
					}
				} else if strings.Contains(l, "version `") && strings.Contains(l, "not found") {
					v := strings.SplitN(strings.SplitN(l, "version `", 2)[1], "'", 2)[0]
					versions[v] = true
				}
			}
			if len(miss) > 0 {
				bad = append(bad, fmt.Sprintf("%s (%s): %s", pkg, filepath.Base(path), strings.Join(setList(miss), ", ")))
			}
			if len(versions) > 0 {
				clash = append(clash, fmt.Sprintf("%s (%s): necesita %s", pkg, filepath.Base(path), strings.Join(setList(versions), ", ")))
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return []Result{{FAIL, "C12", "librerías sin resolver en AUR: " + strings.Join(first(bad, 5), "; ")}}
	}
	note := ""
	if bundled > 0 {
		note = fmt.Sprintf(" (%d librerías incluidas en sus propios paquetes)", bundled)
	}
	if len(accepted) > 0 {
		var acc []string
		for _, a := range setList(accepted) {
			acc = append(acc, "excepción conocida, known-gaps.json: "+a)
		}
		clash = append(acc, clash...)
	}
	if len(clash) > 0 {
		return []Result{{WARN, "C12", fmt.Sprintf("apps de AUR con runtime propio, solo afecta a esa app%s: %s", note, strings.Join(first(clash, 5), "; "))}}
	}
	return []Result{{OK, "C12", fmt.Sprintf("%d binarios de %d paquetes de AUR resuelven sus librerías%s", scanned, len(c.AUR), note)}}
}

var (
	drvVerRe = regexp.MustCompile(`\b(\d+\.\d+(?:\.\d+)?)\b`)
	dkmsRe   = regexp.MustCompile(`^([^,]+), ([^,]+), [^:]+: (.*)`)
	paramRe  = regexp.MustCompile(`(?m)^(\w+):\s*(\S+)`)
)

func firstLine(s string) string {
	return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
}

func RuleC13(c *Ctx) []Result {
	var out []Result
	text, _ := os.ReadFile(filepath.Join(c.ProcRoot, "driver/nvidia/version"))
	var m []string
	if len(text) > 0 {
		m = drvVerRe.FindStringSubmatch(strings.SplitN(string(text), "\n", 2)[0])
	}
	pkg := c.Installed["nvidia-utils"]
	switch {
	case m == nil:
		out = append(out, Result{FAIL, "C13a", "el driver nvidia no está cargado (/proc/driver/nvidia/version)"})
	case pkg != "" && Upstream(pkg) != m[1]:
		out = append(out, Result{FAIL, "C13a", fmt.Sprintf("driver cargado %s != nvidia-utils %s: reinicia", m[1], Upstream(pkg))})
	default:
		out = append(out, Result{OK, "C13a", fmt.Sprintf("driver cargado %s = nvidia-utils", m[1])})
	}
	rc, dk := c.Run([]string{"dkms", "status"}, 15*time.Second, nil)
	var bad []string
	for _, line := range strings.Split(dk, "\n") {
		if mm := dkmsRe.FindStringSubmatch(strings.TrimSpace(line)); mm != nil && mm[2] == c.Kernel &&
			!(strings.HasPrefix(mm[3], "installed") && !strings.Contains(mm[3], "WARNING")) {
			bad = append(bad, mm[1]+": "+mm[3])
		}
	}
	switch {
	case rc == 127:
		out = append(out, Result{WARN, "C13b", "dkms no está instalado"})
	case len(bad) > 0:
		out = append(out, Result{FAIL, "C13b", fmt.Sprintf("DKMS no instalado para %s: %s", c.Kernel, strings.Join(bad, "; "))})
	default:
		out = append(out, Result{OK, "C13b", "todos los módulos DKMS instalados para " + c.Kernel})
	}
	rc, o := c.Run([]string{"nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader"}, 10*time.Second, nil)
	if rc == 0 && strings.TrimSpace(o) != "" {
		out = append(out, Result{OK, "C13c", fmt.Sprintf("nvidia-smi responde (%s)", firstLine(o))})
	} else {
		out = append(out, Result{FAIL, "C13c", fmt.Sprintf("nvidia-smi no responde (rc=%d)", rc)})
	}
	if r := c.CuInit(); r == 0 {
		out = append(out, Result{OK, "C13d", "cuInit = 0"})
	} else {
		out = append(out, Result{FAIL, "C13d", fmt.Sprintf("cuInit devolvió %d", r)})
	}
	rc, o = c.Run([]string{"vulkaninfo", "--summary"}, 20*time.Second, nil)
	low := strings.ToLower(o)
	if rc == 0 && strings.Contains(low, "nvidia") && strings.Contains(low, "radv") {
		out = append(out, Result{OK, "C13e", "vulkaninfo lista NVIDIA y RADV"})
	} else {
		out = append(out, Result{FAIL, "C13e", fmt.Sprintf("vulkaninfo no lista NVIDIA y RADV (rc=%d)", rc)})
	}
	rc, o = c.Run([]string{"vainfo"}, 20*time.Second, map[string]string{"LIBVA_DRIVER_NAME": envOr("LIBVA_DRIVER_NAME", "nvidia")})
	low = strings.ToLower(o)
	if strings.Contains(low, "nvdec") || (strings.Contains(low, "nvidia") && strings.Contains(low, "vaprofile")) {
		out = append(out, Result{OK, "C13f", "vainfo con NVDEC"})
	} else {
		out = append(out, Result{FAIL, "C13f", fmt.Sprintf("vainfo sin NVDEC (rc=%d)", rc)})
	}
	if _, ok := c.Installed["ollama"]; ok {
		if c.OllamaUp() {
			out = append(out, Result{OK, "C13g", "ollama responde en 127.0.0.1:11434"})
		} else {
			out = append(out, Result{FAIL, "C13g", "ollama no responde en 127.0.0.1:11434"})
		}
	}
	if c.HasSession {
		rc, o = c.Run([]string{"hyprctl", "configerrors"}, 15*time.Second, nil)
		if rc == 0 && strings.TrimSpace(o) == "" {
			out = append(out, Result{OK, "C13h", "hyprctl configerrors vacío"})
		} else {
			msg := firstLine(o)
			if strings.TrimSpace(o) == "" {
				msg = fmt.Sprintf("rc=%d", rc)
			}
			out = append(out, Result{FAIL, "C13h", "hyprctl configerrors: " + msg})
		}
	} else {
		out = append(out, Result{SKIP, "C13h", "sin sesión de Hyprland"})
	}
	params, _ := os.ReadFile(filepath.Join(c.ProcRoot, "driver/nvidia/params"))
	got := map[string]string{}
	for _, mm := range paramRe.FindAllStringSubmatch(string(params), -1) {
		if _, seen := got[mm[1]]; !seen {
			got[mm[1]] = mm[2]
		}
	}
	var badp []string
	for _, k := range []string{"PreserveVideoMemoryAllocations", "UseKernelSuspendNotifiers"} {
		if got[k] != "1" {
			v := got[k]
			if v == "" {
				v = "?"
			}
			badp = append(badp, k+"="+v)
		}
	}
	if len(badp) == 0 {
		out = append(out, Result{OK, "C13j", "parámetros NVIDIA de suspensión correctos (Preserve=1, Notifiers=1)"})
	} else {
		out = append(out, Result{FAIL, "C13j", "parámetros NVIDIA de suspensión incorrectos: " + strings.Join(badp, ", ")})
	}
	var failed []string
	for _, cmd := range [][]string{{"systemctl", "--failed", "--no-legend", "--plain"}, {"systemctl", "--user", "--failed", "--no-legend", "--plain"}} {
		_, o := c.Run(cmd, 15*time.Second, nil)
		for _, l := range strings.Split(o, "\n") {
			if f := strings.Fields(l); len(f) > 0 {
				failed = append(failed, f[0])
			}
		}
	}
	sort.Strings(failed)
	if len(failed) == 0 {
		out = append(out, Result{OK, "C13i", "sin servicios fallidos"})
	} else {
		out = append(out, Result{FAIL, "C13i", "servicios fallidos: " + strings.Join(failed, " ")})
	}
	return out
}

func RuleC14(c *Ctx) []Result {
	if len(c.Pacnew) == 0 {
		return []Result{{OK, "C14", "sin .pacnew nuevos en /etc"}}
	}
	p := append([]string{}, c.Pacnew...)
	sort.Strings(p)
	return []Result{{WARN, "C14", ".pacnew nuevos: " + strings.Join(first(p, 8), ", ")}}
}

var PostRules = []Rule{RuleC12, RuleC13, RuleC14}

// ---------- gathering ----------

func ReadInstalled(run Runner) map[string]string {
	_, out := run([]string{"pacman", "-Q"}, 15*time.Second, nil)
	inst := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if p := strings.Fields(line); len(p) == 2 {
			inst[p[0]] = p[1]
		}
	}
	return inst
}

var updRe = regexp.MustCompile(`^(\S+) (\S+) -> (\S+)`)

func ParseUpdates(text string) map[string]string {
	pend := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if m := updRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			pend[m[1]] = m[3]
		}
	}
	return pend
}

func free(path string) (float64, bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0, false
	}
	return float64(st.Bavail) * float64(st.Frsize), true
}

// FillSpace measures /boot (MB) and / (GB).
func (c *Ctx) FillSpace() {
	if f, ok := free(envOr("BOOT_PATH", "/boot")); ok {
		mb := int(f / (1 << 20))
		c.BootFreeMB = &mb
	}
	if f, ok := free(envOr("ROOT_PATH", "/")); ok {
		gb := f / (1 << 30)
		c.RootFreeGB = &gb
	}
}

// FindPacnew: .pacnew files newer than the last stable snapshot.
func FindPacnew() []string {
	home, _ := os.UserHomeDir()
	stable := envOr("STABLE_DIR", filepath.Join(home, ".local/state/omarchy-stable"))
	var ref time.Time
	if ents, err := os.ReadDir(stable); err == nil {
		for _, e := range ents {
			if st, err := os.Stat(filepath.Join(stable, e.Name(), "snapshots.txt")); err == nil && st.ModTime().After(ref) {
				ref = st.ModTime()
			}
		}
	}
	found := []string{}
	_ = filepath.WalkDir(envOr("ETC_PATH", "/etc"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".pacnew") {
			if st, err := os.Stat(p); err == nil && st.ModTime().After(ref) {
				found = append(found, p)
			}
		}
		return nil
	})
	sort.Strings(found)
	return found
}

func Evaluate(c *Ctx, rules []Rule) []Result {
	var res []Result
	for _, r := range rules {
		res = append(res, r(c)...)
	}
	return res
}

// Report prints the results and returns the exit code.
func Report(res []Result, asJSON bool, w func(string)) int {
	if asJSON {
		checks := []map[string]string{}
		for _, r := range res {
			checks = append(checks, map[string]string{"id": r.ID, "status": r.Level, "text": r.Text})
		}
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", " ")
		_ = enc.Encode(map[string]any{"checks": checks})
		w(strings.TrimRight(b.String(), "\n"))
	} else {
		for _, r := range res {
			w(r.Level + " " + r.ID + " " + r.Text)
		}
	}
	code := 0
	for _, r := range res {
		if r.Level == FAIL {
			return 2
		}
		if r.Level == WARN {
			code = 1
		}
	}
	return code
}

var siRe = regexp.MustCompile(`^(Name|Version)\s*:\s*(\S+)`)

// Hook evaluates the pacman targets; it only ever returns lines to print.
func Hook(targets []string, run Runner) []string {
	set := map[string]bool{}
	for _, t := range targets {
		if t = strings.TrimSpace(t); t != "" {
			set[t] = true
		}
	}
	names := setList(set)
	if len(names) == 0 {
		return nil
	}
	inst := ReadInstalled(run)
	_, out := run(append([]string{"pacman", "-Si"}, names...), 3*time.Second, nil)
	pend, seen := map[string]string{}, map[string]bool{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		m := siRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[1] == "Name" {
			cur = m[2]
			if seen[cur] { // same name in several repos: the first block wins (pacman's choice)
				cur = ""
			}
			seen[m[2]] = true
		} else if cur != "" {
			if inst[cur] != m[2] {
				pend[cur] = m[2]
			}
			cur = ""
		}
	}
	if len(seen) == 0 {
		return []string{"[omarchy-compat] no se pudo evaluar (pacman -Si sin datos; solo informativo)"}
	}
	c := NewCtx(inst, pend)
	c.FillSpace()
	var lines []string
	n := 0
	for _, r := range Evaluate(c, HookRules) {
		if r.Level != WARN && r.Level != FAIL {
			continue
		}
		n++
		tag := "aviso"
		if r.Level == FAIL {
			tag = "ROMPERÍA"
		}
		lines = append(lines, fmt.Sprintf("[omarchy-compat] %s %s: %s", tag, r.ID, r.Text))
	}
	return append(lines, fmt.Sprintf("[omarchy-compat] %d paquetes revisados, %d avisos (solo informativo; no detiene la transacción)", len(names), n))
}

// Preflight checks the pending updates (from text, or checkupdates when text
// is nil). w prints a line of output, info a note for stderr.
func Preflight(text *string, asJSON bool, w, info func(string)) int {
	if text == nil {
		rc, out := Run([]string{"checkupdates"}, 120*time.Second, nil)
		if rc != 0 && rc != 2 {
			w(fmt.Sprintf("WARN C00 checkupdates falló (rc=%d; ¿sin red o espejo caído?): no se pudo evaluar", rc))
			return 3
		}
		text = &out
	}
	c := NewCtx(ReadInstalled(Run), ParseUpdates(*text))
	c.FillSpace()
	if len(c.Pending) == 0 {
		info("OK C00 sin actualizaciones pendientes")
	}
	return Report(Evaluate(c, PreflightRules), asJSON, w)
}

// Post checks that drivers, CUDA, Vulkan, VA-API and the desktop work now.
func Post(asJSON bool, cuInit func() int, w func(string)) int {
	c := NewCtx(ReadInstalled(Run), nil)
	c.CuInit = cuInit
	if rc, out := Run([]string{"pacman", "-Qqm"}, 15*time.Second, nil); rc == 0 {
		c.AUR = strings.Fields(out)
		sort.Strings(c.AUR)
	}
	c.Pacnew = FindPacnew()
	return Report(Evaluate(c, PostRules), asJSON, w)
}
