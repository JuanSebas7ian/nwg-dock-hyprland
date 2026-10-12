// gphotos-sync uploads local and Google Drive photos to Google Photos with
// rclone (remote "gphotos:").
//
//	gphotos-sync run [--rescan]      upload everything not uploaded yet (the systemd service runs this)
//	gphotos-sync status              JSON for the bar widget: state, progress, sources, recent uploads
//	gphotos-sync pause|resume        stop/allow uploads (pause also stops a running sync)
//	gphotos-sync source ID on|off    enable or disable one source
//	gphotos-sync plan                what would be uploaded, per album (no uploads)
//
// Why a ledger: since 2025-03-31 the Google Photos API only shows media this
// app uploaded, so "already there?" can't be asked of Google. Each uploaded
// file is recorded once (source + relative path) in uploaded.jsonl and never
// sent again, even if renamed in Photos.
package main

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

var (
	home, _   = os.UserHomeDir()
	configDir = filepath.Join(env("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "gphotos-sync")
	stateDir  = filepath.Join(env("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "gphotos-sync")
	cacheDir  = env("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	configF   = filepath.Join(configDir, "config.json")
	ledgerF   = filepath.Join(stateDir, "uploaded.jsonl")
	statusF   = filepath.Join(stateDir, "status.json")
	recentF   = filepath.Join(stateDir, "recent.json")
	lockF     = filepath.Join(stateDir, "run.lock")
	rclone    = env("GPHOTOS_RCLONE", "rclone")
)

const service = "gphotos-sync.service"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// What Google Photos accepts (photos up to 200 MB, videos up to 20 GB).
var photoExt = set("jpg jpeg png heic heif webp gif bmp tif tiff ico avif dng cr2 cr3 nef arw orf rw2 raf")
var videoExt = set("mp4 mov m4v 3gp 3g2 avi mpg mpeg wmv mkv mts m2ts asf divx mod tod")

const photoMax = 200 << 20
const videoMax = 20 << 30

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, x := range strings.Fields(s) {
		m[x] = true
	}
	return m
}

var defaultConfig = `{"remote": "gphotos:", "paused": false, "bwlimit": "08:00,2.5M 23:30,off", "transfers": 4, "drive_rescan_hours": 6,
 "sources": [
  {"id": "googlefotos", "name": "~/GoogleFotos", "path": "~/GoogleFotos", "album": "{top}", "root_album": "GoogleFotos", "enabled": true},
  {"id": "screenshots", "name": "Capturas de pantalla", "path": "~/Pictures", "recursive": false, "album": "Capturas de pantalla", "root_album": "Capturas de pantalla", "enabled": true},
  {"id": "camera", "name": "Cargas de cámara (Dropbox)", "path": "~/Dropbox/Cargas de cámara", "album": "Cámara {top}", "root_album": "Cámara", "enabled": true},
  {"id": "drive", "name": "Fotos de Google Drive", "remote": "rclone:", "album": "Drive · {top}", "root_album": "Drive", "exclude": ["Backups/"], "enabled": true}]}`

var quotaErr = regexp.MustCompile(`(?i)quota|rate.?limit|RESOURCE_EXHAUSTED|429|too many requests`)
var authErr = regexp.MustCompile(`(?i)invalid_grant|unauthori[sz]ed|401|token (has been )?(expired|revoked)|couldn't find section|didn't find section|not found in config`)

// Source is one folder or remote mapped to albums.
type Source struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Path      string   `json:"path,omitempty"`
	Remote    string   `json:"remote,omitempty"`
	Recursive *bool    `json:"recursive,omitempty"`
	Album     string   `json:"album"`
	RootAlbum string   `json:"root_album,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
	Enabled   *bool    `json:"enabled,omitempty"`
}

func (s Source) enabled() bool   { return s.Enabled == nil || *s.Enabled }
func (s Source) recursive() bool { return s.Recursive == nil || *s.Recursive }

// Config is ~/.config/gphotos-sync/config.json (kept as a raw map so unknown keys survive).
type Config struct {
	raw     map[string]any
	Remote  string
	Paused  bool
	Bw      string
	Trans   int
	Rescan  float64
	Sources []Source
}

func loadConfig() Config {
	raw := map[string]any{}
	b, err := os.ReadFile(configF)
	if err != nil || json.Unmarshal(b, &raw) != nil || raw == nil {
		_ = json.Unmarshal([]byte(defaultConfig), &raw)
		writeJSON(configF, raw)
	}
	def := map[string]any{}
	_ = json.Unmarshal([]byte(defaultConfig), &def)
	for k, v := range def {
		if _, ok := raw[k]; !ok {
			raw[k] = v
		}
	}
	c := Config{raw: raw}
	c.Remote, _ = raw["remote"].(string)
	c.Paused, _ = raw["paused"].(bool)
	c.Bw, _ = raw["bwlimit"].(string)
	t, _ := raw["transfers"].(float64)
	c.Trans = int(t)
	c.Rescan, _ = raw["drive_rescan_hours"].(float64)
	sb, _ := json.Marshal(raw["sources"])
	_ = json.Unmarshal(sb, &c.Sources)
	return c
}

func (c *Config) save() {
	var srcs []any
	sb, _ := json.Marshal(c.Sources)
	_ = json.Unmarshal(sb, &srcs)
	c.raw["sources"], c.raw["paused"] = srcs, c.Paused
	writeJSON(configF, c.raw)
}

func writeJSON(path string, v any) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.Marshal(v)
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path))
	if err != nil {
		return
	}
	_, _ = tmp.Write(b)
	tmp.Close()
	_ = os.Rename(tmp.Name(), path)
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

func mediaKind(name string) string {
	ext := ""
	if i := strings.LastIndex(name, "."); i >= 0 {
		ext = strings.ToLower(name[i+1:])
	}
	switch {
	case photoExt[ext]:
		return "photo"
	case videoExt[ext]:
		return "video"
	}
	return ""
}

func acceptable(name string, size int64) bool {
	switch mediaKind(name) {
	case "photo":
		return size > 0 && size <= photoMax
	case "video":
		return size > 0 && size <= videoMax
	}
	return false
}

var spaces = regexp.MustCompile(`\s+`)

// cleanAlbum: "/" would be read by rclone as a sub-path of album/.
func cleanAlbum(name string) string {
	s := strings.TrimSpace(spaces.ReplaceAllString(strings.ReplaceAll(name, "/", "-"), " "))
	if s == "" {
		return "Omarchy"
	}
	return s
}

func albumFor(s Source, rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) == 1 {
		if s.RootAlbum != "" {
			return cleanAlbum(s.RootAlbum)
		}
		return cleanAlbum(s.Name)
	}
	return cleanAlbum(strings.ReplaceAll(s.Album, "{top}", parts[0]))
}

func excluded(s Source, rel string) bool {
	for _, e := range s.Exclude {
		pre := e
		if !strings.HasSuffix(e, "/") {
			pre = e + "/"
		}
		if rel == strings.TrimSuffix(e, "/") || strings.HasPrefix(rel, pre) {
			return true
		}
	}
	return false
}

// pyQuote is Python's urllib.parse.quote (keeps / and unreserved characters).
func pyQuote(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("_.-~/", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// thumbFor: the freedesktop thumbnail (Nautilus made them for Dropbox), else the image itself.
func thumbFor(path string) string {
	sum := md5.Sum([]byte("file://" + pyQuote(path)))
	digest := hex.EncodeToString(sum[:])
	for _, size := range []string{"large", "x-large", "normal"} {
		t := filepath.Join(cacheDir, "thumbnails", size, digest+".png")
		if _, err := os.Stat(t); err == nil {
			return t
		}
	}
	low := strings.ToLower(path)
	for _, e := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp"} {
		if strings.HasSuffix(low, e) {
			return path
		}
	}
	return ""
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// ---------------------------------------------------------------- ledger

type entry map[string]any

func loadLedger() map[string]entry {
	done := map[string]entry{}
	f, err := os.Open(ledgerF)
	if err != nil {
		return done
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			if k, ok := e["k"].(string); ok {
				done[k] = e
			}
		}
	}
	return done
}

func record(e entry) {
	_ = os.MkdirAll(stateDir, 0o755)
	f, err := os.OpenFile(ledgerF, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		b, _ := json.Marshal(e)
		_, _ = f.Write(append(b, '\n'))
		f.Close()
	}
	var recent []entry
	readJSON(recentF, &recent)
	recent = append([]entry{e}, recent...)
	if len(recent) > 24 {
		recent = recent[:24]
	}
	writeJSON(recentF, recent)
}

// ---------------------------------------------------------------- scanning

type file struct {
	Rel  string `json:"rel"`
	Size int64  `json:"size"`
}

func scanLocal(s Source) []file {
	root := expand(s.Path)
	var out []file
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return out
	}
	add := func(p string, name string) {
		if strings.HasPrefix(name, ".") {
			return
		}
		st, err := os.Stat(p)
		if err != nil {
			return
		}
		rel, _ := filepath.Rel(root, p)
		if acceptable(name, st.Size()) && !excluded(s, rel) {
			out = append(out, file{rel, st.Size()})
		}
	}
	if !s.recursive() {
		ents, _ := os.ReadDir(root)
		for _, e := range ents {
			if e.Type().IsRegular() {
				add(filepath.Join(root, e.Name()), e.Name())
			}
		}
		return out
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		add(p, d.Name())
		return nil
	})
	return out
}

func scanRemote(s Source, rescanHours float64, force bool) ([]file, error) {
	cache := filepath.Join(stateDir, "scan-"+s.ID+".json")
	var cached struct {
		T     int64  `json:"t"`
		Files []file `json:"files"`
	}
	if readJSON(cache, &cached) && !force && float64(time.Now().Unix()-cached.T) < rescanHours*3600 {
		return cached.Files, nil
	}
	var exts []string
	for e := range photoExt {
		exts = append(exts, e)
	}
	for e := range videoExt {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	all := append([]string{}, exts...)
	for _, e := range exts {
		all = append(all, strings.ToUpper(e))
	}
	out, err := exec.Command(rclone, "lsjson", "-R", "--files-only", "--no-mimetype", "--no-modtime", "--include", "*.{"+strings.Join(all, ",")+"}", s.Remote).Output()
	if err != nil {
		msg := "?"
		if ee, ok := err.(*exec.ExitError); ok {
			lines := strings.Split(strings.TrimSpace(string(ee.Stderr)), "\n")
			msg = lines[len(lines)-1]
		}
		return nil, fmt.Errorf("no se pudo listar %s: %s", s.Remote, msg)
	}
	var items []struct {
		Path, Name string
		Size       int64
	}
	if err := json.Unmarshal(out, &items); err != nil && len(out) > 0 {
		return nil, err
	}
	files := []file{}
	for _, e := range items {
		if acceptable(e.Name, e.Size) && !excluded(s, e.Path) {
			files = append(files, file{e.Path, e.Size})
		}
	}
	cached.T, cached.Files = time.Now().Unix(), files
	writeJSON(cache, cached)
	return files, nil
}

// Summary is one source in the status.
type Summary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Total        int    `json:"total"`
	Uploaded     int    `json:"uploaded"`
	Pending      int    `json:"pending"`
	PendingBytes int64  `json:"pendingBytes"`
	Error        string `json:"error"`
}

type groupKey struct{ album, dir string }

type pfile struct {
	name, rel string
	size      int64
}

type planned struct {
	src     Source
	summary *Summary
	groups  map[groupKey][]pfile
}

func plan(c Config, ledger map[string]entry, force, onlyEnabled bool) []planned {
	var res []planned
	for _, s := range c.Sources {
		if onlyEnabled && !s.enabled() {
			continue
		}
		sum := &Summary{ID: s.ID, Name: s.Name, Enabled: s.enabled()}
		p := planned{s, sum, map[groupKey][]pfile{}}
		var files []file
		var err error
		if s.Remote != "" {
			files, err = scanRemote(s, c.Rescan, force)
		} else {
			files = scanLocal(s)
		}
		if err != nil {
			e := err.Error()
			if len(e) > 300 {
				e = e[:300]
			}
			sum.Error = e
			res = append(res, p)
			continue
		}
		for _, f := range files {
			sum.Total++
			if _, ok := ledger[s.ID+":"+f.Rel]; ok {
				sum.Uploaded++
				continue
			}
			sum.Pending++
			sum.PendingBytes += f.Size
			dir, name := "", f.Rel
			if i := strings.LastIndex(f.Rel, "/"); i >= 0 {
				dir, name = f.Rel[:i], f.Rel[i+1:]
			}
			k := groupKey{albumFor(s, f.Rel), dir}
			p.groups[k] = append(p.groups[k], pfile{name, f.Rel, f.Size})
		}
		res = append(res, p)
	}
	return res
}

// ---------------------------------------------------------------- uploading

type status struct {
	data      map[string]any
	baseBytes int64
}

func newStatus(sums []*Summary) *status {
	var files int
	var bytes int64
	for _, s := range sums {
		files += s.Pending
		bytes += s.PendingBytes
	}
	st := &status{data: map[string]any{"state": "running", "started": time.Now().Unix(), "updated": time.Now().Unix(), "sources": sums,
		"filesDone": 0, "filesTotal": files, "bytesTotal": bytes, "bytesDone": int64(0), "speed": 0, "eta": nil, "album": "", "transferring": []any{}, "error": ""}}
	st.save(nil)
	return st
}

func (st *status) save(kv map[string]any) {
	for k, v := range kv {
		st.data[k] = v
	}
	st.data["updated"] = time.Now().Unix()
	writeJSON(statusF, st.data)
}

// uploadGroup runs one rclone copy for the files of one folder into one album.
func uploadGroup(c Config, s Source, album, dir string, files []pfile, st *status) (string, string) {
	var base, localRoot string
	if s.Remote != "" {
		base = s.Remote + dir
	} else {
		localRoot = expand(s.Path)
		base = localRoot
		if dir != "" {
			base = filepath.Join(localRoot, dir)
		}
	}
	byName := map[string]pfile{}
	var names []string
	for _, f := range files {
		byName[f.name] = f
		names = append(names, f.name)
	}
	lst, err := os.CreateTemp("", "gphotos-*.lst")
	if err != nil {
		return "error", err.Error()
	}
	_, _ = lst.WriteString(strings.Join(names, "\n") + "\n")
	lst.Close()
	defer os.Remove(lst.Name())
	args := []string{"copy", base, c.Remote + "album/" + album, "--files-from-raw", lst.Name(), "--no-traverse", "--no-check-dest",
		"--transfers", fmt.Sprint(c.Trans), "--use-json-log", "-v", "--stats", "2s", "--low-level-retries", "10", "--retries", "1"} // a full retry would re-send what already went up
	if c.Bw != "" && c.Bw != "off" {
		args = append(args, "--bwlimit", c.Bw)
	}
	cmd := exec.Command(rclone, args...)
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return "error", err.Error()
	}
	verdict, msg, errors := "ok", "", 0
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 1<<20), 4<<20)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		text, _ := ev["msg"].(string)
		obj, _ := ev["object"].(string)
		if stats, ok := ev["stats"].(map[string]any); ok {
			b, _ := stats["bytes"].(float64)
			sp, _ := stats["speed"].(float64)
			var tr []map[string]any
			if ts, ok := stats["transferring"].([]any); ok {
				for _, t := range ts {
					tm, _ := t.(map[string]any)
					p, _ := tm["percentage"].(float64)
					tsp, _ := tm["speed"].(float64)
					tr = append(tr, map[string]any{"name": tm["name"], "percent": int(p), "speed": int(tsp)})
				}
			}
			if tr == nil {
				tr = []map[string]any{}
			}
			st.save(map[string]any{"bytesDone": st.baseBytes + int64(b), "speed": int(sp), "eta": stats["eta"], "album": album, "transferring": tr})
		} else if strings.HasPrefix(text, "Copied") && obj != "" {
			name := obj[strings.LastIndex(obj, "/")+1:]
			f, ok := byName[name]
			if !ok {
				f, ok = byName[obj]
			}
			if ok {
				local := ""
				if localRoot != "" {
					local = filepath.Join(localRoot, f.rel)
				}
				thumb := ""
				if local != "" {
					thumb = thumbFor(local)
				}
				record(entry{"k": s.ID + ":" + f.rel, "t": time.Now().Unix(), "album": album, "src": s.ID, "rel": f.rel, "size": f.size,
					"kind": mediaKind(name), "path": local, "thumb": thumb})
				done, _ := st.data["filesDone"].(int)
				st.save(map[string]any{"filesDone": done + 1})
			}
		} else if ev["level"] == "error" {
			errors++
			msg = text
			if obj != "" {
				msg = obj + ": " + text
			}
			if quotaErr.MatchString(text) {
				verdict = "quota"
				_ = cmd.Process.Signal(syscall.SIGTERM)
			} else if authErr.MatchString(text) {
				verdict = "auth"
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
		}
	}
	werr := cmd.Wait()
	if b, ok := st.data["bytesDone"].(int64); ok {
		st.baseBytes = b
	}
	if verdict == "ok" && (werr != nil || errors > 0) {
		verdict = "error"
	}
	if len(msg) > 400 {
		msg = msg[:400]
	}
	return verdict, msg
}

func remoteConfigured(c Config) bool {
	out, _ := exec.Command(rclone, "listremotes").Output()
	for _, r := range strings.Fields(string(out)) {
		if r == c.Remote {
			return true
		}
	}
	return false
}

func cmdRun(force bool) int {
	_ = os.MkdirAll(stateDir, 0o755)
	lock, err := os.OpenFile(lockF, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 1
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		fmt.Println("gphotos-sync: ya hay una sincronización en marcha")
		return 0
	}
	c := loadConfig()
	prev := map[string]any{}
	readJSON(statusF, &prev)
	merge := func(kv map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range prev {
			out[k] = v
		}
		for k, v := range kv {
			out[k] = v
		}
		return out
	}
	if c.Paused {
		writeJSON(statusF, merge(map[string]any{"state": "paused", "updated": time.Now().Unix(), "transferring": []any{}}))
		fmt.Println("gphotos-sync: en pausa")
		return 0
	}
	if !remoteConfigured(c) {
		writeJSON(statusF, merge(map[string]any{"state": "setup", "updated": time.Now().Unix(), "transferring": []any{}, "error": "Falta conectar Google Fotos: ejecuta gphotos-setup"}))
		fmt.Printf("gphotos-sync: falta el remoto %s (gphotos-setup)\n", c.Remote)
		return 0
	}
	ledger := loadLedger()
	pl := plan(c, ledger, force, true)
	var sums []*Summary
	for _, p := range pl {
		sums = append(sums, p.summary)
	}
	st := newStatus(sums)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	final, errText := "idle", ""
loop:
	for _, p := range pl {
		keys := make([]groupKey, 0, len(p.groups))
		for k := range p.groups {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].album != keys[j].album {
				return keys[i].album < keys[j].album
			}
			return keys[i].dir < keys[j].dir
		})
		for _, k := range keys {
			select {
			case <-stop:
				final = "idle"
				if loadConfig().Paused {
					final = "paused"
				}
				break loop
			default:
			}
			v, m := uploadGroup(c, p.src, k.album, k.dir, p.groups[k], st)
			if v == "quota" || v == "auth" {
				final, errText = v, m
				break loop
			}
			if v == "error" {
				errText = m
			}
		}
	}
	ledger = loadLedger()
	for _, p := range pl {
		if p.summary.Error == "" {
			done := 0
			for k := range ledger {
				if strings.HasPrefix(k, p.src.ID+":") {
					done++
				}
			}
			p.summary.Uploaded = min(done, p.summary.Total)
			p.summary.Pending = max(0, p.summary.Total-p.summary.Uploaded)
		}
	}
	lastOk := prev["lastOk"]
	if final == "idle" && errText == "" {
		lastOk = time.Now().Unix()
	}
	st.save(map[string]any{"state": final, "error": errText, "transferring": []any{}, "speed": 0, "eta": nil, "sources": sums, "lastRun": time.Now().Unix(), "lastOk": lastOk})
	tail := ""
	if errText != "" {
		tail = ", " + errText
	}
	fmt.Printf("gphotos-sync: %v subidos, estado %s%s\n", st.data["filesDone"], final, tail)
	return 0
}

func serviceActive() bool {
	out, _ := exec.Command("systemctl", "--user", "is-active", service).Output()
	s := strings.TrimSpace(string(out))
	return s == "active" || s == "activating"
}

func cmdStatus() int {
	c := loadConfig()
	st := map[string]any{}
	readJSON(statusF, &st)
	running := serviceActive()
	if !running && st["state"] == "running" {
		st["state"] = "idle" // killed without a chance to write its final state
	}
	if c.Paused && !running {
		st["state"] = "paused"
	}
	if s, _ := st["state"].(string); s == "" {
		if remoteConfigured(c) {
			st["state"] = "idle"
		} else {
			st["state"] = "setup"
		}
	}
	known := map[string]map[string]any{}
	if srcs, ok := st["sources"].([]any); ok {
		for _, s := range srcs {
			if m, ok := s.(map[string]any); ok {
				id, _ := m["id"].(string)
				known[id] = m
			}
		}
	}
	var sources []map[string]any
	uploaded, pending := 0.0, 0.0
	for _, s := range c.Sources {
		m := known[s.ID]
		if m == nil {
			m = map[string]any{"id": s.ID, "total": 0, "uploaded": 0, "pending": 0, "pendingBytes": 0, "error": ""}
		}
		m["enabled"], m["name"] = s.enabled(), s.Name
		u, _ := m["uploaded"].(float64)
		p, _ := m["pending"].(float64)
		uploaded += u
		if s.enabled() {
			pending += p
		}
		sources = append(sources, m)
	}
	st["sources"] = sources
	st["running"], st["paused"] = running, c.Paused
	var recent []map[string]any
	readJSON(recentF, &recent)
	if len(recent) > 12 {
		recent = recent[:12]
	}
	for _, r := range recent {
		if p, _ := r["path"].(string); p != "" {
			if t, _ := r["thumb"].(string); t == "" {
				r["thumb"] = thumbFor(p)
			}
		}
	}
	if recent == nil {
		recent = []map[string]any{}
	}
	st["recent"] = recent
	st["uploadedTotal"], st["pendingTotal"] = int(uploaded), int(pending)
	b, _ := json.Marshal(st)
	fmt.Println(string(b))
	return 0
}

func setPaused(v bool) int {
	c := loadConfig()
	c.Paused = v
	c.save()
	if v {
		_ = exec.Command("systemctl", "--user", "stop", service).Run()
	} else {
		_ = exec.Command("systemctl", "--user", "start", "--no-block", service).Run()
	}
	return 0
}

func cmdSource(id string, on bool) int {
	c := loadConfig()
	for i := range c.Sources {
		if c.Sources[i].ID == id {
			c.Sources[i].Enabled = &on
			c.save()
			return 0
		}
	}
	fmt.Fprintln(os.Stderr, "fuente desconocida: "+id)
	return 3
}

func cmdPlan() int {
	c := loadConfig()
	for _, p := range plan(c, loadLedger(), false, false) {
		s := p.summary
		flag := ""
		if !s.Enabled {
			flag = " (desactivada)"
		}
		e := ""
		if s.Error != "" {
			e = " ERROR " + s.Error
		}
		fmt.Printf("%s%s: %d archivos, %d subidos, %d pendientes (%.1f GB)%s\n", s.Name, flag, s.Total, s.Uploaded, s.Pending, float64(s.PendingBytes)/1e9, e)
		albums := map[string]int{}
		for k, fs := range p.groups {
			albums[k.album] += len(fs)
		}
		names := make([]string, 0, len(albums))
		for a := range albums {
			names = append(names, a)
		}
		sort.Strings(names)
		for _, a := range names {
			fmt.Printf("    %s: %d\n", a, albums[a])
		}
	}
	return 0
}

func main() {
	cmd := "status"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	a := os.Args
	switch {
	case cmd == "run":
		os.Exit(cmdRun(len(a) > 2 && a[2] == "--rescan"))
	case cmd == "status":
		os.Exit(cmdStatus())
	case cmd == "pause":
		os.Exit(setPaused(true))
	case cmd == "resume":
		os.Exit(setPaused(false))
	case cmd == "source" && len(a) == 4 && (a[3] == "on" || a[3] == "off"):
		os.Exit(cmdSource(a[2], a[3] == "on"))
	case cmd == "plan":
		os.Exit(cmdPlan())
	}
	fmt.Fprintln(os.Stderr, "usage: gphotos-sync run [--rescan] | status | pause | resume | source ID on|off | plan")
	os.Exit(3)
}
