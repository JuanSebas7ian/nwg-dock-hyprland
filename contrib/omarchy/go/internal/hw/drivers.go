package hw

// Drivers, firmware and updates: omarchy-hwcheck health, NVIDIA driver and
// DKMS per kernel, CUDA, pending updates (one checkupdates call, cached an
// hour, shared with omarchy-drivers) and the installed BIOS against the newest
// one ASUS publishes (cached a day). New NVIDIA/CUDA/kernel updates raise one
// notification per version.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	driverPkgs = regexp.MustCompile(`^(linux(-lts|-zen|-hardened)?(-headers)?|linux-firmware.*|amd-ucode|intel-ucode|` +
		`nvidia.*|lib32-nvidia.*|opencl-nvidia|egl-wayland.*|mesa.*|lib32-mesa.*|vulkan-.*|lib32-vulkan-.*|` +
		`libva.*|bluez.*|sof-firmware|alsa-firmware|alsa-ucm-conf|wireless-regdb|` +
		`dkms|.*-dkms|hid-xpadneo.*|fwupd|systemd|limine.*)$`)
	gpuPkgs = regexp.MustCompile(`^(nvidia.*|lib32-nvidia.*|opencl-nvidia|libva-nvidia-driver|egl-wayland.*|cuda|cuda-tools|` +
		`cudnn|nccl|python-pytorch-cuda|ollama-cuda|linux|linux-headers|linux-lts|linux-lts-headers|dkms)$`)
)

func dmi(name string) string { return Read("/sys/class/dmi/id/" + name) }

func kernelRelease() string {
	var u syscall.Utsname
	if syscall.Uname(&u) != nil {
		return ""
	}
	b := make([]byte, 0, len(u.Release))
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

func pkgVersions(pattern string) map[string]string {
	re := regexp.MustCompile(pattern)
	out := map[string]string{}
	for _, line := range strings.Split(Run(60*time.Second, "pacman", "-Q"), "\n") {
		p := strings.Fields(line)
		if len(p) == 2 && re.MatchString(p[0]) {
			out[p[0]] = p[1]
		}
	}
	return out
}

func ParseCheckupdates(text string) []M {
	pkgs := []M{}
	for _, line := range strings.Split(text, "\n") {
		p := strings.Fields(line)
		if len(p) >= 4 && p[2] == "->" {
			pkgs = append(pkgs, M{"name": p[0], "from": p[1], "to": p[3]})
		}
	}
	return pkgs
}

func allUpdates() M {
	out, errs, code := RunFull(120*time.Second, "checkupdates")
	// checkupdates: 0 = updates, 2 = none, 1 = error
	if code == 1 || code == -1 {
		e := strings.TrimSpace(errs)
		if e == "" {
			e = "checkupdates failed"
		}
		if len(e) > 200 {
			e = e[:200]
		}
		return M{"error": e, "packages": []M{}}
	}
	return M{"packages": ParseCheckupdates(out), "checkedAt": time.Now().Unix()}
}

func SplitUpdates(data M) M {
	all := l(data["packages"])
	drv, gpu := []any{}, []any{}
	for _, x := range all {
		n := s(m(x)["name"])
		if driverPkgs.MatchString(n) {
			drv = append(drv, x)
		}
		if gpuPkgs.MatchString(n) {
			gpu = append(gpu, x)
		}
	}
	return M{"total": len(all), "drivers": drv, "gpu": gpu, "checkedAt": data["checkedAt"], "error": data["error"], "stale": data["stale"]}
}

func notifyNewGPU(gpu []any) {
	path := filepath.Join(CacheDir(), "notified-gpu.json")
	var seenList []string
	ReadJSON(path, &seenList)
	seen := map[string]bool{}
	for _, k := range seenList {
		seen[k] = true
	}
	var keys, fresh []string
	for _, x := range gpu {
		p := m(x)
		k := s(p["name"]) + "=" + s(p["to"])
		keys = append(keys, k)
		if !seen[k] {
			fresh = append(fresh, k)
		}
	}
	sort.Strings(keys)
	sort.Strings(fresh)
	if len(fresh) > 0 {
		var lines []string
		for i, k := range fresh {
			if i == 8 {
				break
			}
			lines = append(lines, strings.Replace(k, "=", " → ", 1))
		}
		body := strings.Join(lines, "\n") + "\nUpdate with `omarchy update`; omarchy-hwcheck checks the NVIDIA build before you reboot."
		Notify("NVIDIA / CUDA / kernel updates", body, "normal")
	}
	if keys == nil {
		keys = []string{}
	}
	_ = WriteJSON(path, keys)
}

func health() M {
	out := Run(60*time.Second, "omarchy-hwcheck", "--json")
	if strings.TrimSpace(out) == "" {
		return M{"error": "omarchy-hwcheck not available", "counts": M{}, "issues": []M{}}
	}
	var data M
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return M{"error": err.Error(), "counts": M{}, "issues": []M{}}
	}
	counts, issues := M{}, []M{}
	for _, x := range l(data["findings"]) {
		f := m(x)
		level := s(f["level"])
		if level == "" {
			level = "ok"
		}
		n, _ := counts[level].(int)
		counts[level] = n + 1
		if level != "ok" && level != "info" {
			det := l(f["details"])
			if len(det) > 3 {
				det = det[:3]
			}
			if det == nil {
				det = []any{}
			}
			issues = append(issues, M{"id": f["id"], "level": level, "title": s(f["title"]), "details": det, "hint": s(f["hint"])})
		}
	}
	return M{"counts": counts, "issues": issues}
}

var modesetRe = regexp.MustCompile(`nvidia[-_]drm[ .]modeset=1`)

func nvidia() M {
	verText := Read("/proc/driver/nvidia/version")
	loaded := Read("/sys/module/nvidia/version")
	flavor := ""
	if strings.Contains(verText, "Open Kernel Module") {
		flavor = "open"
	} else if verText != "" {
		flavor = "proprietary"
	}
	pkgs := pkgVersions(`^(nvidia.*|lib32-nvidia-utils|opencl-nvidia|libva-nvidia-driver)$`)
	utils := strings.Split(pkgs["nvidia-utils"], "-")[0]
	var names []string
	for k := range pkgs {
		names = append(names, k)
	}
	sort.Strings(names)
	modulePkg := ""
	for _, k := range names {
		if strings.Contains(k, "dkms") || k == "nvidia" || k == "nvidia-open" || k == "nvidia-lts" || k == "nvidia-open-lts" {
			modulePkg = k
			break
		}
	}
	var kernels []string
	if ents, err := os.ReadDir("/usr/lib/modules"); err == nil {
		for _, e := range ents {
			if _, err := os.Stat(filepath.Join("/usr/lib/modules", e.Name(), "vmlinuz")); err == nil {
				kernels = append(kernels, e.Name())
			}
		}
	}
	sort.Strings(kernels)
	dkmsOut := Run(60*time.Second, "dkms", "status", "nvidia")
	running := kernelRelease()
	kl := []M{}
	issues := []string{}
	if loaded == "" {
		issues = append(issues, "The nvidia module is not loaded")
	}
	if loaded != "" && utils != "" && loaded != utils {
		issues = append(issues, fmt.Sprintf("Loaded driver %s differs from nvidia-utils %s: reboot pending", loaded, utils))
	}
	for _, k := range kernels {
		built := false
		for _, line := range strings.Split(dkmsOut, "\n") {
			if strings.Contains(line, ", "+k+",") {
				built = strings.Contains(line, "installed")
				break
			}
		}
		kl = append(kl, M{"kernel": k, "built": built, "running": k == running})
		if !built && strings.HasSuffix(modulePkg, "dkms") {
			issues = append(issues, fmt.Sprintf("DKMS module not built for kernel %s: do not reboot into it", k))
		}
	}
	gsp := 0
	if loaded != "" {
		g, _ := filepath.Glob(filepath.Join("/usr/lib/firmware/nvidia", loaded, "gsp_*.bin*"))
		gsp = len(g)
		if gsp == 0 {
			issues = append(issues, "No GSP firmware for "+loaded)
		}
	}
	modeset := Read("/sys/module/nvidia_drm/parameters/modeset")
	if modeset == "" {
		conf := Run(10*time.Second, "bash", "-c", "cat /etc/modprobe.d/*.conf /usr/lib/modprobe.d/*.conf /proc/cmdline 2>/dev/null")
		modeset = "unknown"
		if modesetRe.MatchString(conf) {
			modeset = "Y (configured)"
		}
	}
	var card any
	smi := strings.TrimSpace(Run(10*time.Second, "nvidia-smi", "--query-gpu=name,vbios_version,pcie.link.gen.max,pcie.link.width.max,persistence_mode", "--format=csv,noheader,nounits"))
	if smi != "" {
		var p []string
		for _, x := range strings.Split(strings.Split(smi, "\n")[0], ",") {
			p = append(p, strings.TrimSpace(x))
		}
		if len(p) >= 5 {
			card = M{"name": strings.Replace(p[0], "NVIDIA GeForce ", "", 1), "vbios": p[1], "pcie": fmt.Sprintf("Gen%s x%s", p[2], p[3]), "persistence": p[4]}
		}
	}
	return M{"loaded": loaded, "flavor": flavor, "utils": utils, "modulePackage": modulePkg, "card": card,
		"kernels": kl, "gsp": gsp, "modeset": modeset, "issues": issues}
}

func verTuple(v string) ([2]int, bool) {
	p := strings.Split(v, ".")
	if len(p) < 2 {
		p = append(p, "0")
	}
	a, e1 := strconv.Atoi(p[0])
	b, e2 := strconv.Atoi(p[1])
	return [2]int{a, b}, e1 == nil && e2 == nil
}

func cuda() M {
	info := M{}
	if exe, err := os.Executable(); err == nil {
		_ = json.Unmarshal([]byte(Run(30*time.Second, exe, "cuda-probe")), &info)
	}
	if len(info) == 0 {
		info = M{"works": false, "error": "CUDA check did not answer"}
	}
	pk := pkgVersions(`^(cuda|cuda-tools|cudnn|nccl|python-pytorch-cuda|ollama-cuda)$`)
	info["toolkit"] = strings.Split(pk["cuda"], "-")[0]
	info["cudnn"] = strings.Split(pk["cudnn"], "-")[0]
	extras := M{}
	for k, v := range pk {
		if k != "cuda" && k != "cudnn" {
			extras[k] = strings.Split(v, "-")[0]
		}
	}
	info["extras"] = extras
	if tk, dc := s(info["toolkit"]), s(info["driverCuda"]); tk != "" && dc != "" {
		a, ok1 := verTuple(tk)
		b, ok2 := verTuple(dc)
		if ok1 && ok2 {
			info["compatible"] = a[0] < b[0] || (a[0] == b[0] && a[1] <= b[1])
		}
	}
	return info
}

var brRe = regexp.MustCompile(`<br\s*/?>`)

func latestBIOS() M {
	board, vendor := dmi("board_name"), dmi("board_vendor")
	if !strings.Contains(strings.ToUpper(vendor), "ASUS") {
		return M{"error": "only ASUS boards are supported", "board": board}
	}
	u := "https://www.asus.com/support/api/product.asmx/GetPDBIOS?" + url.Values{"website": {"us"}, "model": {board}}.Encode()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 omarchy-hardware-widget")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		e := err.Error()
		if len(e) > 200 {
			e = e[:200]
		}
		return M{"error": e, "board": board}
	}
	defer resp.Body.Close()
	var data M
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return M{"error": err.Error(), "board": board}
	}
	for _, x := range l(m(data["Result"])["Obj"]) {
		g := m(x)
		if g["Name"] != "BIOS" {
			continue
		}
		files := l(g["Files"])
		if len(files) == 0 {
			continue
		}
		f := m(files[0])
		notes := strings.TrimSpace(strings.Trim(brRe.ReplaceAllString(s(f["Description"]), "\n"), `"`))
		isRel := s(f["IsRelease"])
		if f["IsRelease"] == nil {
			isRel = "1"
		}
		return M{"board": board, "version": s(f["Version"]), "date": strings.ReplaceAll(s(f["ReleaseDate"]), "/", "-"), "notes": notes,
			"beta": isRel == "0", "page": "https://rog.asus.com/motherboards/rog-strix/" + strings.ReplaceAll(strings.ToLower(board), " ", "-") + "/helpdesk_bios/"}
	}
	return M{"error": "no BIOS listed", "board": board}
}

var nonDigit = regexp.MustCompile(`\D`)

func BIOSNewer(latest, installed string) bool {
	n := func(v string) int64 {
		x, _ := strconv.ParseInt(nonDigit.ReplaceAllString(v, ""), 10, 64)
		return x
	}
	return n(latest) > n(installed)
}

func loadedDrivers() []M {
	kernel := kernelRelease()
	rows := []M{{"label": "Kernel", "version": kernel}}
	for _, lm := range [][2]string{{"NVIDIA", "nvidia"}, {"Radeon (amdgpu)", "amdgpu"}, {"Wi-Fi (mt7925e)", "mt7925e"},
		{"Bluetooth (btusb)", "btusb"}, {"Ethernet (igc)", "igc"}, {"NVMe", "nvme"}, {"Xbox pad (xpad)", "xpad"}, {"Board sensors (nct6775)", "nct6775"}} {
		v := Read("/sys/module/" + lm[1] + "/version")
		if v != "" || exists("/sys/module/"+lm[1]) {
			if v == "" {
				v = "built-in / " + kernel
			}
			rows = append(rows, M{"label": lm[0], "version": v})
		}
	}
	return rows
}

// CollectDrivers gathers the Drivers topic.
func CollectDrivers(force bool) M {
	updates := Cached("updates", time.Hour, allUpdates, force)
	split := SplitUpdates(updates)
	if !truthy(updates["error"]) && !truthy(updates["stale"]) {
		notifyNewGPU(l(split["gpu"]))
	}
	bios := Cached("bios", 24*time.Hour, latestBIOS, force)
	installed := dmi("bios_version")
	b := M{}
	for k, v := range bios {
		b[k] = v
	}
	b["newer"] = BIOSNewer(s(bios["version"]), installed)
	return M{
		"board":     M{"vendor": dmi("board_vendor"), "name": dmi("board_name"), "bios": installed, "biosDate": dmi("bios_date")},
		"bios":      b,
		"updates":   split,
		"health":    health(),
		"nvidia":    nvidia(),
		"cuda":      cuda(),
		"loaded":    loadedDrivers(),
		"checkedAt": time.Now().Unix(),
	}
}
