package iphone

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var mediaExt = map[string]bool{".jpg": true, ".jpeg": true, ".heic": true, ".heif": true, ".png": true, ".gif": true,
	".dng": true, ".mov": true, ".mp4": true, ".m4v": true, ".hevc": true}

var bundleRe = regexp.MustCompile(`^[A-Za-z0-9.-]{3,200}$`)

// mountPoint is where ifuse mounts what of a device: "media" (the camera roll) or an app's Documents.
func (p Paths) mountPoint(udid, what string) string { return filepath.Join(p.Runtime, udid, what) }

// Mounts lists this package's live ifuse mounts (from /proc/self/mountinfo).
func Mounts(p Paths) []string {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return []string{}
	}
	defer f.Close()
	out := []string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 4 {
			mp := strings.ReplaceAll(fields[4], `\040`, " ")
			if strings.HasPrefix(mp, p.Runtime+"/") {
				out = append(out, mp)
			}
		}
	}
	return out
}

func mounted(p Paths, dir string) bool {
	for _, m := range Mounts(p) {
		if m == dir {
			return true
		}
	}
	return false
}

// Mount mounts the camera roll ("media") or an app's Documents ("app:<bundle id>") and returns the folder.
func Mount(run Runner, p Paths, udid, what string) (string, error) {
	args := []string{"-u", udid}
	name := "media"
	if strings.HasPrefix(what, "app:") {
		id := strings.TrimPrefix(what, "app:")
		if !bundleRe.MatchString(id) {
			return "", errors.New("bad app id")
		}
		args = append(args, "--documents", id)
		name = "app-" + id
	} else if what != "media" {
		return "", errors.New("mount media or app:<bundle id>")
	}
	dir := p.mountPoint(udid, name)
	if mounted(p, dir) {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if b, err := run("ifuse", append([]string{dir}, args...)...); err != nil {
		return "", fmt.Errorf("could not open the iPhone (%s)", strings.TrimSpace(string(b)))
	}
	return dir, nil
}

// ErrInUse: a manual eject while an import or a safety check reads the device.
var ErrInUse = errors.New("the device folder is in use by an import or a check: wait for it to finish")

// Eject is the widget's eject: it refuses while a job reads the camera roll.
func Eject(run Runner, p Paths, udid string) ([]string, error) {
	if p.locked("import") || p.locked("safe") {
		return nil, ErrInUse
	}
	return Unmount(run, p, udid), nil
}

// Unmount releases every mount of one device (or of all devices when udid is ""), in use or not
// (the device was unplugged).
func Unmount(run Runner, p Paths, udid string) []string {
	var done []string
	for _, m := range Mounts(p) {
		if udid != "" && !strings.HasPrefix(m, filepath.Join(p.Runtime, udid)+"/") {
			continue
		}
		if _, err := run("fusermount3", "-u", m); err != nil {
			_, _ = run("fusermount3", "-uz", m) // the device went away: lazy unmount
		}
		_ = os.Remove(m)
		done = append(done, m)
	}
	return done
}

// Apps lists the apps that share files (`ifuse --list-apps`): bundle id -> name.
func Apps(run Runner, udid string) ([]M, error) {
	b, err := run("ifuse", "--list-apps", "-u", udid)
	if err != nil {
		return nil, errors.New(strings.TrimSpace(string(b)))
	}
	return ParseApps(string(b)), nil
}

func ParseApps(out string) []M {
	apps := []M{}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.Contains(l, "CFBundleIdentifier") {
			continue
		}
		parts := strings.Split(strings.Trim(l, `"`), `","`)
		if len(parts) < 3 || !bundleRe.MatchString(parts[0]) {
			continue
		}
		apps = append(apps, M{"id": parts[0], "version": parts[1], "name": parts[2]})
	}
	sort.Slice(apps, func(i, j int) bool {
		return strings.ToLower(apps[i]["name"].(string)) < strings.ToLower(apps[j]["name"].(string))
	})
	return apps
}

// Ledger remembers what was imported from one device: DCIM path -> size.
type Ledger map[string]int64

func (p Paths) ledgerPath(udid string) string { return filepath.Join(p.State, udid, "imported.json") }

func (p Paths) LoadLedger(udid string) Ledger {
	l := Ledger{}
	readJSON(p.ledgerPath(udid), &l)
	return l
}

// MediaFile is one photo or video in the camera roll.
type MediaFile struct {
	Rel  string
	Size int64
	Mod  time.Time
}

// ScanDCIM lists the photos and videos under <root>/DCIM.
func ScanDCIM(root string) ([]MediaFile, error) {
	var files []MediaFile
	base := filepath.Join(root, "DCIM")
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == base {
				return err
			}
			return nil // one unreadable folder must not stop the import
		}
		if d.IsDir() || !mediaExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		files = append(files, MediaFile{Rel: rel, Size: info.Size(), Mod: info.ModTime()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, err
}

// Pending is what still has to be imported.
func Pending(files []MediaFile, l Ledger) []MediaFile {
	var out []MediaFile
	for _, f := range files {
		if size, ok := l[f.Rel]; !ok || size != f.Size {
			out = append(out, f)
		}
	}
	return out
}

// Destination: <photos>/<year>/<original name>; an existing file of the same size is the same photo
// (already there), a different one gets a numbered name.
func Destination(dir string, f MediaFile) (path string, already bool) {
	year := strconv.Itoa(f.Mod.Year())
	if f.Mod.IsZero() || f.Mod.Year() < 2000 {
		year = "sin-fecha"
	}
	name := filepath.Base(f.Rel)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		cand := filepath.Join(dir, year, name)
		if i > 0 {
			cand = filepath.Join(dir, year, fmt.Sprintf("%s-%d%s", stem, i, ext))
		}
		info, err := os.Stat(cand)
		if err != nil {
			return cand, false
		}
		if info.Size() == f.Size {
			return cand, true
		}
	}
}

// copyVerified copies a camera-roll file and checks the copy has the size the device reported; a short copy
// (a read cut off, or iCloud Photos still downloading the original) is retried once, then reported.
func copyVerified(src, dst string, want int64, mod time.Time) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = copyFile(src, dst, mod); err != nil {
			continue
		}
		st, serr := os.Stat(dst)
		if serr == nil && st.Size() == want {
			return nil
		}
		got := int64(-1)
		if serr == nil {
			got = st.Size()
		}
		os.Remove(dst)
		err = fmt.Errorf("incomplete copy of %s (%d of %d bytes)", filepath.Base(src), got, want)
		if now, e := os.Stat(src); e == nil && now.Size() != want {
			return fmt.Errorf("%s changed on the device while copying (iCloud Photos downloading it?): retried next time", filepath.Base(src))
		}
	}
	return err
}

func copyFile(src, dst string, mod time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if !mod.IsZero() {
		_ = os.Chtimes(tmp, mod, mod)
	}
	return os.Rename(tmp, dst)
}

// ImportResult is the summary of one import.
type ImportResult struct {
	Imported int    `json:"imported"`
	Already  int    `json:"already"`
	Failed   int    `json:"failed"`
	Bytes    int64  `json:"bytes"`
	Dir      string `json:"dir"`
	Error    string `json:"error,omitempty"`
}

// Import copies the new photos and videos of one device; progress(i, n, name) reports each file.
// The ledger is saved every 25 files, so an interrupted import resumes where it stopped.
func Import(run Runner, p Paths, udid, class string, progress func(done, total int, name string)) (ImportResult, error) {
	cfg := p.LoadConfig()
	dir := Folder(cfg, class)
	res := ImportResult{Dir: dir}
	release, err := p.lock("import")
	if err != nil {
		return res, err
	}
	defer release()
	root, err := Mount(run, p, udid, "media")
	if err != nil {
		return res, err
	}
	files, err := ScanDCIM(root)
	if err != nil {
		return res, fmt.Errorf("could not read the camera roll: %v", err)
	}
	l := p.LoadLedger(udid)
	todo := Pending(files, l)
	save := func() { _ = writeJSON(p.ledgerPath(udid), l) }
	for i, f := range todo {
		if progress != nil {
			progress(i, len(todo), filepath.Base(f.Rel))
		}
		dst, already := Destination(dir, f)
		if already {
			res.Already++
		} else if err := copyVerified(filepath.Join(root, "DCIM", f.Rel), dst, f.Size, f.Mod); err != nil {
			res.Failed++
			res.Error = err.Error()
			if _, serr := os.Stat(filepath.Join(root, "DCIM")); serr != nil {
				// the camera roll went away (unplugged, locked, released): stop, the rest is retried next time
				res.Failed = len(todo) - res.Imported - res.Already
				res.Error = "the device was disconnected or its folder released; the rest is imported next time"
				save()
				_ = writeJSON(filepath.Join(p.State, "last-import.json"), M{"time": time.Now().Format(time.RFC3339), "udid": udid,
					"imported": res.Imported, "already": res.Already, "failed": res.Failed, "bytes": res.Bytes, "error": res.Error})
				return res, errors.New(res.Error)
			}
			continue
		} else {
			res.Imported++
			res.Bytes += f.Size
		}
		l[f.Rel] = f.Size
		if i%25 == 24 {
			save()
		}
	}
	save()
	if progress != nil {
		progress(len(todo), len(todo), "")
	}
	var pendingBytes int64
	for _, f := range todo {
		if size, ok := l[f.Rel]; !ok || size != f.Size {
			pendingBytes += f.Size
		}
	}
	_ = writeJSON(filepath.Join(p.State, udid, "scan.json"), M{"udid": udid, "total": len(files), "pending": res.Failed,
		"pendingBytes": pendingBytes, "time": time.Now().Format(time.RFC3339)})
	last := M{"time": time.Now().Format(time.RFC3339), "udid": udid, "imported": res.Imported, "already": res.Already,
		"failed": res.Failed, "bytes": res.Bytes, "pending": res.Failed}
	_ = writeJSON(filepath.Join(p.State, "last-import.json"), last)
	return res, nil
}

// Scan counts the photos still to import without copying them.
func Scan(run Runner, p Paths, udid string) (M, error) {
	root, err := Mount(run, p, udid, "media")
	if err != nil {
		return nil, err
	}
	files, err := ScanDCIM(root)
	if err != nil {
		return nil, err
	}
	todo := Pending(files, p.LoadLedger(udid))
	var bytes int64
	for _, f := range todo {
		bytes += f.Size
	}
	r := M{"udid": udid, "total": len(files), "pending": len(todo), "pendingBytes": bytes, "time": time.Now().Format(time.RFC3339)}
	_ = writeJSON(filepath.Join(p.State, udid, "scan.json"), r)
	return r, nil
}

// PhotoStatus: last import, last scan of each device, whether an import runs now.
func PhotoStatus(p Paths, cfg Config) M {
	var last, scans M
	readJSON(filepath.Join(p.State, "last-import.json"), &last)
	scans = M{}
	ents, _ := os.ReadDir(p.State)
	for _, e := range ents {
		var s M
		if e.IsDir() && readJSON(filepath.Join(p.State, e.Name(), "scan.json"), &s) {
			scans[e.Name()] = s
		}
	}
	return M{"dir": cfg.PhotosDir, "autoImport": cfg.AutoImport, "last": last, "scans": scans, "running": p.locked("import")}
}

// lock makes sure one import (or backup) runs at a time; the lock holds the pid.
func (p Paths) lock(name string) (func(), error) {
	path := filepath.Join(p.State, name+".lock")
	if p.locked(name) {
		return nil, errors.New("already running")
	}
	if err := os.MkdirAll(p.State, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, err
	}
	return func() { os.Remove(path) }, nil
}

func (p Paths) locked(name string) bool {
	b, err := os.ReadFile(filepath.Join(p.State, name+".lock"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	_, err = os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}
