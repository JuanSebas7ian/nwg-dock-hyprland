package hw

// Storage: disks, partitions and NVMe health (UDisks2, no root), what uses the
// system disk with drill-down, Steam per game, disk firmware against the
// vendor's latest, and what can be reclaimed. du runs with nice/ionice; the
// slow parts run only when the Storage tab is open.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var systemAreas = [][2]string{
	{"Programs (/usr)", "/usr"}, {"Apps in /opt", "/opt"}, {"Ollama models", "/var/lib/ollama"},
	{"Flatpak (system)", "/var/lib/flatpak"}, {"Docker", "/var/lib/docker"}, {"Package cache", "/var/cache/pacman/pkg"},
	{"Logs", "/var/log"}, {"Swap file", "/swap"},
}

// Du is {path: bytes} for path (depth 0) or its children (depth 1), on one filesystem.
func Du(path string, depth int) map[string]int64 {
	args := []string{"-n", "15", "ionice", "-c3", "du", "-xsb", path}
	if depth > 0 {
		args = []string{"-n", "15", "ionice", "-c3", "du", "-xb", "--max-depth", strconv.Itoa(depth), path}
	}
	sizes := map[string]int64{}
	for _, line := range strings.Split(Run(300*time.Second, "nice", args...), "\n") {
		size, p, ok := strings.Cut(line, "\t")
		if n, err := strconv.ParseInt(size, 10, 64); ok && err == nil {
			sizes[p] = n
		}
	}
	return sizes
}

func duTotal(path string) int64 {
	var t int64
	for _, v := range Du(path, 0) {
		t += v
	}
	return t
}

func busJSON(args ...string) []any {
	out := Run(10*time.Second, "busctl", append([]string{"--system", "--json=short"}, args...)...)
	var vals []any
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v M
		if json.Unmarshal([]byte(line), &v) == nil {
			vals = append(vals, v["data"])
		}
	}
	return vals
}

func udisksDrives() []string {
	var out []string
	for _, obj := range strings.Fields(Run(60*time.Second, "busctl", "--system", "tree", "org.freedesktop.UDisks2", "--list")) {
		if strings.Contains(obj, "/drives/") {
			out = append(out, obj)
		}
	}
	return out
}

func driveSerial(obj string) string {
	v := busJSON("get-property", "org.freedesktop.UDisks2", obj, "org.freedesktop.UDisks2.Drive", "Serial")
	if len(v) > 0 {
		return s(v[0])
	}
	return ""
}

// Smart is per-drive NVMe health through UDisks2, by serial.
func Smart() map[string]M {
	drives := map[string]M{}
	for _, obj := range udisksDrives() {
		raw := busJSON("call", "org.freedesktop.UDisks2", obj, "org.freedesktop.UDisks2.NVMe.Controller", "SmartGetAttributes", "a{sv}", "0")
		if len(raw) == 0 {
			continue
		}
		first := l(raw[0])
		if len(first) == 0 {
			continue
		}
		attrs := M{}
		for k, v := range m(first[0]) {
			attrs[k] = m(v)["data"]
		}
		vals := busJSON("get-property", "org.freedesktop.UDisks2", obj, "org.freedesktop.UDisks2.NVMe.Controller", "SmartPowerOnHours", "SmartTemperature", "SmartCriticalWarning")
		h := M{"percentUsed": attrs["percent_used"], "spare": attrs["avail_spare"], "spareThreshold": attrs["spare_thresh"],
			"written": attrs["total_data_written"], "read": attrs["total_data_read"], "unsafeShutdowns": attrs["unsafe_shutdowns"],
			"mediaErrors": attrs["media_errors"], "powerOnHours": nil, "temp": nil, "criticalWarning": []any{}}
		if len(vals) > 0 {
			h["powerOnHours"] = vals[0]
		}
		if len(vals) > 1 {
			if t, ok := fnum(vals[1]); ok && t != 0 {
				h["temp"] = iround(t - 273.15)
			}
		}
		if len(vals) > 2 {
			h["criticalWarning"] = vals[2]
		}
		drives[driveSerial(obj)] = h
	}
	return drives
}

func statvfs(path string) (size, free, used int64) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return
	}
	bs := int64(st.Frsize)
	return int64(st.Blocks) * bs, int64(st.Bavail) * bs, int64(st.Blocks-st.Bfree) * bs
}

var btrfsRe = regexp.MustCompile(`^\s*(Device size|Device allocated|Device unallocated|Used|Free \(estimated\)):\s+(\d+)(?:\s+\(min: (\d+)\))?`)

func walkParts(node M, depth int, parts *[]M) {
	for _, x := range l(node["children"]) {
		c := m(x)
		mounts := []string{}
		for _, mp := range l(c["mountpoints"]) {
			if s(mp) != "" {
				mounts = append(mounts, s(mp))
			}
		}
		size := c["size"]
		if size == nil {
			size = 0
		}
		*parts = append(*parts, M{"name": c["name"], "size": size, "type": c["type"], "fstype": s(c["fstype"]), "label": s(c["label"]),
			"mounts": mounts, "used": c["fsused"], "avail": c["fsavail"], "fssize": c["fssize"], "depth": depth})
		walkParts(c, depth+1, parts)
	}
}

// Overview: disks, root usage, btrfs, swaps and Google Drive.
func Overview() M {
	var lsblk M
	_ = json.Unmarshal([]byte(Run(30*time.Second, "lsblk", "-J", "-b", "-e7", "-o", "NAME,SIZE,TYPE,FSTYPE,LABEL,MOUNTPOINTS,FSAVAIL,FSUSED,FSSIZE,MODEL,SERIAL,ROTA")), &lsblk)
	health := Smart()
	disks := []M{}
	for _, x := range l(lsblk["blockdevices"]) {
		dev := m(x)
		if dev["type"] != "disk" || strings.HasPrefix(s(dev["name"]), "zram") {
			continue
		}
		parts := []M{}
		walkParts(dev, 0, &parts)
		role, ntfs := "", false
		for _, p := range parts {
			for _, mp := range p["mounts"].([]string) {
				if mp == "/" {
					role = "Omarchy"
				}
			}
			ntfs = ntfs || p["fstype"] == "ntfs"
		}
		if role == "" && ntfs {
			role = "Windows"
		}
		size := dev["size"]
		if size == nil {
			size = 0
		}
		var h any
		if v, ok := health[s(dev["serial"])]; ok {
			h = v
		}
		disks = append(disks, M{"name": dev["name"], "model": strings.TrimSpace(s(dev["model"])), "size": size, "serial": s(dev["serial"]),
			"role": role, "partitions": parts, "health": h})
	}
	sort.SliceStable(disks, func(i, j int) bool {
		a, b := disks[i]["role"] != "Omarchy", disks[j]["role"] != "Omarchy"
		if a != b {
			return !a
		}
		return s(disks[i]["name"]) < s(disks[j]["name"])
	})
	size, free, used := statvfs("/")
	btrfs := M{}
	keys := map[string]string{"Device size": "size", "Device allocated": "allocated", "Device unallocated": "unallocated", "Used": "used", "Free (estimated)": "freeEstimated"}
	for _, line := range strings.Split(Run(30*time.Second, "btrfs", "filesystem", "usage", "-b", "/"), "\n") {
		if mm := btrfsRe.FindStringSubmatch(line); mm != nil {
			n, _ := strconv.ParseInt(mm[2], 10, 64)
			btrfs[keys[mm[1]]] = n
			if mm[3] != "" {
				n, _ := strconv.ParseInt(mm[3], 10, 64)
				btrfs["freeMin"] = n
			}
		}
	}
	swaps := []M{}
	lines := strings.Split(Read("/proc/swaps"), "\n")
	for i, line := range lines {
		p := strings.Fields(line)
		if i == 0 || len(p) < 4 {
			continue
		}
		sz, _ := strconv.ParseInt(p[2], 10, 64)
		us, _ := strconv.ParseInt(p[3], 10, 64)
		swaps = append(swaps, M{"name": p[0], "type": p[1], "size": sz * 1024, "used": us * 1024})
	}
	extra := []M{}
	gd := filepath.Join(Home(), "GoogleDrive")
	if isMount(gd) {
		var q M
		if ReadJSON(filepath.Join(Home(), ".cache/omarchy-gdrive-widget/quota.json"), &q) {
			u, _ := fnum(q["used"])
			o, _ := fnum(q["other"])
			extra = append(extra, M{"name": "Google Drive", "path": gd, "size": q["total"], "used": u + o, "free": q["free"]})
		}
	}
	return M{"disks": disks, "root": M{"size": size, "free": free, "used": used}, "btrfs": btrfs, "swaps": swaps, "extra": extra, "updatedAt": time.Now().Unix()}
}

func isMount(p string) bool {
	var a, b syscall.Stat_t
	if syscall.Stat(p, &a) != nil || syscall.Stat(filepath.Join(p, ".."), &b) != nil {
		return false
	}
	return a.Dev != b.Dev || a.Ino == b.Ino
}

func sortBySize(items []M) {
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := fnum(items[i]["size"])
		b, _ := fnum(items[j]["size"])
		return a > b
	})
}

// Breakdown: what uses the system disk.
func Breakdown() M {
	_, _, fsUsed := statvfs("/")
	home := Home()
	children := Du(home, 1)
	homeTotal, ok := children[home]
	delete(children, home)
	if !ok {
		for _, v := range children {
			homeTotal += v
		}
	}
	homeItems := []M{}
	for p, sz := range children {
		homeItems = append(homeItems, M{"name": filepath.Base(p), "path": p, "size": sz})
	}
	sortBySize(homeItems)
	if len(homeItems) > 14 {
		homeItems = homeItems[:14]
	}
	system := []M{}
	var inside, sysTotal int64
	for _, a := range systemAreas {
		if _, err := os.Stat(a[1]); err != nil {
			continue
		}
		if sz := duTotal(a[1]); sz > 0 {
			system = append(system, M{"name": a[0], "path": a[1], "size": sz})
			if strings.HasPrefix(a[1], "/var/lib/") {
				inside += sz
			}
		}
	}
	// /var/lib already contains Ollama, Flatpak and Docker; count the rest of it separately.
	if rest := duTotal("/var/lib") - inside; rest > 0 {
		system = append(system, M{"name": "Other system data (/var/lib)", "path": "/var/lib", "size": rest})
	}
	sortBySize(system)
	for _, e := range system {
		sysTotal += e["size"].(int64)
	}
	measured := homeTotal + sysTotal
	res := M{"fsUsed": fsUsed, "home": M{"total": homeTotal, "items": homeItems}, "system": M{"total": sysTotal, "items": system},
		"other": max(0, fsUsed-measured), "overMeasured": max(0, measured-fsUsed), // zstd compression makes du add up to more than is used
		"updatedAt": time.Now().Unix()}
	_ = WriteJSON(filepath.Join(CacheDir(), "breakdown.json"), res)
	return res
}

// Directory: sizes inside one folder, for the drill-down.
func Directory(path string) M {
	if strings.HasPrefix(path, "~") {
		path = Home() + path[1:]
	}
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	path, _ = filepath.Abs(path)
	children := Du(path, 1)
	total, ok := children[path]
	delete(children, path)
	if !ok {
		for _, v := range children {
			total += v
		}
	}
	items := []M{}
	for p, sz := range children {
		st, err := os.Stat(p)
		items = append(items, M{"name": filepath.Base(p), "path": p, "size": sz, "isDir": err == nil && st.IsDir()})
	}
	// Files directly inside (du --max-depth 1 lists folders only)
	if ents, err := os.ReadDir(path); err == nil {
		for _, e := range ents {
			if !e.Type().IsRegular() {
				continue
			}
			var st syscall.Stat_t
			p := filepath.Join(path, e.Name())
			if syscall.Lstat(p, &st) == nil {
				items = append(items, M{"name": e.Name(), "path": p, "size": st.Blocks * 512, "isDir": false})
			}
		}
	}
	sortBySize(items)
	if len(items) > 25 {
		items = items[:25]
	}
	return M{"path": path, "total": total, "items": items}
}

// Rated endurance (TBW) by model, from the manufacturers' spec sheets.
var tbw = map[string]int{"Samsung SSD 980 500GB": 300, "Samsung SSD 980 1TB": 600, "Samsung SSD 980 PRO 500GB": 300,
	"Samsung SSD 980 PRO 1TB": 600, "Samsung SSD 990 PRO 1TB": 600, "Samsung SSD 990 PRO 2TB": 1200,
	"WD_BLACK SN770 500GB": 300, "WD_BLACK SN770 1TB": 600, "WD_BLACK SN850X 1TB": 600}

const samsungTools = "https://semiconductor.samsung.com/consumer-storage/support/tools/"

var samsungISO = regexp.MustCompile(`(https://[^"\s]+/Samsung_SSD_([0-9A-Za-z_]+?)_([0-9A-Z]{8})\.iso)`)

// SamsungLatest: {family: {version, iso}} from Samsung's tools page, cached a day.
func SamsungLatest() map[string]M {
	path := filepath.Join(CacheDir(), "samsung-firmware.json")
	var found map[string]M
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < 24*time.Hour && ReadJSON(path, &found) {
		return found
	}
	req, _ := http.NewRequest(http.MethodGet, samsungTools, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		found = map[string]M{}
		ReadJSON(path, &found)
		return found
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return ParseSamsung(string(page), path)
}

func ParseSamsung(page, cachePath string) map[string]M {
	found := map[string]M{}
	for _, mm := range samsungISO.FindAllStringSubmatch(page, -1) {
		fam := strings.ReplaceAll(mm[2], "_", " ")
		if _, ok := found[fam]; !ok {
			found[fam] = M{"version": mm[3], "iso": mm[1]}
		}
	}
	if cachePath != "" {
		_ = WriteJSON(cachePath, found)
	}
	return found
}

func fwupdUpdates() map[string][]string {
	var data M
	_ = json.Unmarshal([]byte(Run(60*time.Second, "fwupdmgr", "get-updates", "--json")), &data)
	out := map[string][]string{}
	for _, x := range l(data["Devices"]) {
		d := m(x)
		key := s(d["Serial"])
		if key == "" {
			key = s(d["Name"])
		}
		var vs []string
		for _, r := range l(d["Releases"]) {
			vs = append(vs, s(m(r)["Version"]))
		}
		out[key] = vs
	}
	return out
}

func selftest(obj string) M {
	v := busJSON("get-property", "org.freedesktop.UDisks2", obj, "org.freedesktop.UDisks2.NVMe.Controller", "SmartSelftestStatus", "SmartSelftestPercentRemaining")
	res := M{"status": "", "remaining": -1}
	if len(v) > 0 {
		res["status"] = v[0]
	}
	if len(v) > 1 {
		res["remaining"] = v[1]
	}
	return res
}

var samsungFamily = regexp.MustCompile(`^Samsung SSD |\s*\d+(GB|TB)$`)

// Firmware of each NVMe drive against the vendor's latest.
func Firmware() M {
	health := Smart()
	samsung := SamsungLatest()
	fw := fwupdUpdates()
	objs := map[string]string{}
	for _, obj := range udisksDrives() {
		if ser := driveSerial(obj); ser != "" {
			objs[ser] = obj
		}
	}
	drives := []M{}
	for _, ctrl := range ListDir("/sys/class/nvme") {
		base := "/sys/class/nvme/" + ctrl
		model, serial, rev := Read(base+"/model"), Read(base+"/serial"), Read(base+"/firmware_rev")
		h := health[serial]
		if h == nil {
			h = M{}
		}
		vendor := strings.ReplaceAll(strings.Fields(model + " ?")[0], "_", " ")
		var t any
		if v, ok := tbw[model]; ok {
			t = v
		}
		e := M{"model": model, "serial": serial, "firmware": rev, "vendor": vendor, "tbw": t, "written": h["written"], "health": h,
			"udisks": objs[serial], "selftest": M{}}
		if o, ok := objs[serial]; ok {
			e["selftest"] = selftest(o)
		}
		if strings.HasPrefix(model, "Samsung") {
			info := samsung[strings.TrimSpace(samsungFamily.ReplaceAllString(model, ""))]
			dl := samsungTools
			if iso := s(info["iso"]); iso != "" {
				dl = iso
			}
			e["latest"], e["source"], e["download"] = s(info["version"]), "Samsung", dl
			e["note"] = "Samsung Magician has no Linux version for consumer SSDs; firmware ships as a bootable ISO."
		} else {
			e["latest"], e["source"], e["download"] = rev, "LVFS (fwupd)", ""
			if vs := fw[serial]; len(vs) > 0 {
				e["latest"], e["download"] = vs[0], "fwupdmgr update"
			}
		}
		e["upToDate"] = s(e["latest"]) == "" || e["latest"] == rev
		drives = append(drives, e)
	}
	return M{"drives": drives}
}

// StartSelftest asks UDisks (polkit) for a SMART self-test.
func StartSelftest(obj, kind string) M {
	_, errs, code := RunFull(120*time.Second, "busctl", "--system", "call", "org.freedesktop.UDisks2", obj,
		"org.freedesktop.UDisks2.NVMe.Controller", "SmartSelftestStart", "sa{sv}", kind, "0")
	return M{"ok": code == 0, "error": strings.TrimSpace(errs)}
}

var steamTools = regexp.MustCompile(`(?i)^(proton|steam linux runtime|steamworks common|steamvr)`)

func vdfValues(text, key string) []string {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s+"([^"]*)"`)
	var out []string
	for _, mm := range re.FindAllStringSubmatch(text, -1) {
		out = append(out, mm[1])
	}
	return out
}

func first(v []string, def string) string {
	if len(v) > 0 {
		return v[0]
	}
	return def
}

// Steam: installed games per library, with Proton prefix and shader cache.
func Steam() M {
	h := Home()
	roots := []string{filepath.Join(h, ".local/share/Steam"), filepath.Join(h, ".steam/steam"), filepath.Join(h, ".var/app/com.valvesoftware.Steam/.local/share/Steam")}
	libraries := []string{}
	seen := map[string]bool{}
	for _, r := range roots {
		if b, err := os.ReadFile(filepath.Join(r, "steamapps", "libraryfolders.vdf")); err == nil {
			for _, p := range vdfValues(string(b), "path") {
				if !seen[p] {
					seen[p] = true
					libraries = append(libraries, p)
				}
			}
		}
	}
	games, tools := []M{}, []M{}
	for _, lib := range libraries {
		apps := filepath.Join(lib, "steamapps")
		acfs, _ := filepath.Glob(filepath.Join(apps, "appmanifest_*.acf"))
		sort.Strings(acfs)
		for _, acf := range acfs {
			b, _ := os.ReadFile(acf)
			text := string(b)
			appid := first(vdfValues(text, "appid"), "")
			name := first(vdfValues(text, "name"), appid)
			size, _ := strconv.ParseInt(first(vdfValues(text, "SizeOnDisk"), "0"), 10, 64)
			last, _ := strconv.ParseInt(first(vdfValues(text, "LastPlayed"), "0"), 10, 64)
			var prefix, shaders int64
			if p := filepath.Join(apps, "compatdata", appid); appid != "" && dirExists(p) {
				prefix = duTotal(p)
			}
			if p := filepath.Join(apps, "shadercache", appid); appid != "" && dirExists(p) {
				shaders = duTotal(p)
			}
			e := M{"appid": appid, "name": name, "size": size, "prefix": prefix, "shaders": shaders, "total": size + prefix + shaders,
				"lastPlayed": last, "path": filepath.Join(apps, "common", first(vdfValues(text, "installdir"), "")), "library": lib}
			if steamTools.MatchString(name) {
				tools = append(tools, e)
			} else {
				games = append(games, e)
			}
		}
	}
	byTotal := func(x []M) {
		sort.SliceStable(x, func(i, j int) bool { return x[i]["total"].(int64) > x[j]["total"].(int64) })
	}
	byTotal(games)
	byTotal(tools)
	dir := ""
	for _, r := range roots {
		if dirExists(r) {
			dir = r
			break
		}
	}
	var total, listed int64
	if dir != "" {
		total = duTotal(dir)
	}
	for _, g := range append(append([]M{}, games...), tools...) {
		listed += g["total"].(int64)
	}
	return M{"libraries": libraries, "games": games, "tools": tools, "total": total, "client": max(0, total-listed), "path": dir}
}

func dirExists(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

var (
	paccacheRe = regexp.MustCompile(`(?s)(\d+) candidate packages? .*?\(disk space saved: ([\d.]+) ([KMGT]i?B)\)`)
	instSizeRe = regexp.MustCompile(`^Installed Size\s*:\s*([\d.]+) ([KMG]iB)`)
	journalRe  = regexp.MustCompile(`take up ([\d.]+)([KMGT])`)
	units      = map[string]float64{"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}
)

func unitMult(u string) float64 {
	if v, ok := units[u]; ok {
		return v
	}
	return 1
}

func pf(x string) float64 { v, _ := strconv.ParseFloat(x, 64); return v }

// Cleanup lists what can be reclaimed and how.
func Cleanup() M {
	items := []M{}
	pkgCache := duTotal("/var/cache/pacman/pkg")
	if mm := paccacheRe.FindStringSubmatch(Run(60*time.Second, "paccache", "-dk1")); mm != nil {
		items = append(items, M{"id": "paccache", "name": "Old package versions", "size": int64(pf(mm[2]) * unitMult(mm[3])),
			"detail": mm[1] + " packages · keeps the installed version", "command": "sudo paccache -rk1", "needsSudo": true})
	}
	if mm := paccacheRe.FindStringSubmatch(Run(60*time.Second, "paccache", "-dk0", "-u")); mm != nil {
		items = append(items, M{"id": "paccache-u", "name": "Cached packages no longer installed", "size": int64(pf(mm[2]) * unitMult(mm[3])),
			"detail": mm[1] + " packages", "command": "sudo paccache -ruk0", "needsSudo": true})
	}
	if len(items) == 0 && pkgCache > 0 {
		items = append(items, M{"id": "paccache-all", "name": "Package cache (installed versions)", "size": pkgCache,
			"detail": "only current versions; emptying it means no offline reinstall or downgrade", "command": "sudo paccache -rk0", "needsSudo": true})
	}
	if orphans := strings.Fields(Run(60*time.Second, "pacman", "-Qdtq")); len(orphans) > 0 {
		var size int64
		for _, line := range strings.Split(Run(60*time.Second, "pacman", append([]string{"-Qi"}, orphans...)...), "\n") {
			if mm := instSizeRe.FindStringSubmatch(line); mm != nil {
				size += int64(pf(mm[1]) * unitMult(mm[2]))
			}
		}
		shown := orphans
		if len(shown) > 6 {
			shown = shown[:6]
		}
		items = append(items, M{"id": "orphans", "name": "Orphan packages", "size": size, "detail": strings.Join(shown, ", "),
			"command": "sudo pacman -Rns $(pacman -Qdtq)", "needsSudo": true})
	}
	h := Home()
	items = append(items, M{"id": "cache", "name": "Your app caches (~/.cache)", "size": duTotal(filepath.Join(h, ".cache")),
		"detail": "Apps rebuild them; review before deleting", "open": filepath.Join(h, ".cache")})
	if t := filepath.Join(h, ".local/share/Trash"); dirExists(t) {
		if sz := duTotal(t); sz > 0 {
			items = append(items, M{"id": "trash", "name": "Trash", "size": sz, "detail": "", "command": "gio trash --empty"})
		}
	}
	if mm := journalRe.FindStringSubmatch(Run(30*time.Second, "journalctl", "--disk-usage")); mm != nil {
		items = append(items, M{"id": "journal", "name": "System journal", "size": int64(pf(mm[1]) * unitMult(mm[2])),
			"detail": "keep 2 weeks", "command": "sudo journalctl --vacuum-time=2weeks", "needsSudo": true})
	}
	sortBySize(items)
	return M{"items": items, "packageCache": pkgCache}
}
