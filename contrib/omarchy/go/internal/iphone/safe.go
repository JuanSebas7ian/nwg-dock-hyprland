package iphone

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This check says which photos are safe to delete on the device: copied to this PC (the whole file compared with
// the device) AND uploaded to Google Photos. The deleting itself is in free.go (over PTP, never AFC).

// UploadedPaths reads gphotos-sync's ledger (uploaded.jsonl): the local paths already in Google Photos.
func UploadedPaths(ledger string) map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(ledger)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Path != "" {
			out[e.Path] = true
		}
	}
	return out
}

// LocalCopy finds the imported copy of one camera-roll file: where the ledger says it went (same size), else
// a file with its name and size in any year folder (copies made before the ledger recorded places).
func LocalCopy(dir string, f MediaFile, l Ledger) (string, bool) {
	if e, ok := l[f.Rel]; ok && e.Local != "" {
		if st, err := os.Stat(e.Local); err == nil && st.Size() == f.Size {
			return e.Local, true
		}
	}
	if p, already := Destination(dir, f); already {
		return p, true
	}
	name := filepath.Base(f.Rel)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	cands, _ := filepath.Glob(filepath.Join(dir, "*", stem+"*"+ext))
	for _, c := range cands {
		b := strings.TrimSuffix(filepath.Base(c), ext)
		if b != stem && !strings.HasPrefix(b, stem+"-") {
			continue
		}
		if st, err := os.Stat(c); err == nil && st.Size() == f.Size {
			return c, true
		}
	}
	return "", false
}

// takenOf is a file's capture date: the ledger's, else the device's file date.
func takenOf(f MediaFile, l Ledger) time.Time {
	if e, ok := l[f.Rel]; ok && e.Taken != "" {
		if t, err := time.Parse(time.RFC3339, e.Taken); err == nil {
			return t
		}
	}
	return f.Mod
}

func fileHash(path string, limit int64) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(fh, limit)); err != nil {
		return "", err
	}
	return string(h.Sum(nil)), nil
}

// SafeReport is the result of the check.
type SafeReport struct {
	Time          string      `json:"time"`
	UDID          string      `json:"udid"`
	Total         int         `json:"total"`
	Safe          int         `json:"safe"`
	SafeBytes     int64       `json:"safeBytes"`
	NotCopied     int         `json:"notCopied"`
	NotUploaded   int         `json:"notUploaded"`
	SafeUntil     string      `json:"safeUntil,omitempty"`     // every photo taken up to this day is safe
	OldestMissing string      `json:"oldestMissing,omitempty"` // the oldest photo that is not safe yet
	Verified      int         `json:"verified"`                // sample compared byte by byte
	Mismatched    int         `json:"mismatched"`
	ICloudPhotos  string      `json:"icloudPhotos"` // on | off | unknown
	Dir           string      `json:"dir"`
	Largest       []LargeFile `json:"largest"` // the heaviest photos/videos and whether each is safe to delete
	// whole-file check (see free.go): what can be deleted from the device
	Groups         []SafeGroup `json:"groups"`
	Deletable      int         `json:"deletable"`
	DeletableBytes int64       `json:"deletableBytes"`
	Repaired       []string    `json:"repaired"` // copies that differed: fixed and sent to Google Photos again
	Unstable       []string    `json:"unstable"`
}

// CheckSafe compares the camera roll mounted at root with the local copies in dir and the Google Photos ledger.
func CheckSafe(root, dir string, uploaded map[string]bool, l Ledger, sample int) (SafeReport, error) {
	r := SafeReport{Time: time.Now().Format(time.RFC3339), Dir: dir, ICloudPhotos: "unknown"}
	files, err := ScanDCIM(root)
	if err != nil {
		return r, err
	}
	r.Largest = Largest(files, dir, uploaded, l, 40)
	// order by when each was taken: that is the order of the Photos app, where the deleting happens
	for i := range files {
		files[i].Mod = takenOf(files[i], l)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Mod.Before(files[j].Mod) })
	r.Total = len(files)
	var firstBad time.Time
	var safeFiles []MediaFile
	var safeLocal []string
	for _, f := range files {
		local, copied := LocalCopy(dir, f, l)
		switch {
		case !copied:
			r.NotCopied++
		case !uploaded[local]:
			r.NotUploaded++
		default:
			r.Safe++
			r.SafeBytes += f.Size
			safeFiles = append(safeFiles, f)
			safeLocal = append(safeLocal, local)
			continue
		}
		if firstBad.IsZero() {
			firstBad = f.Mod
		}
	}
	if !firstBad.IsZero() {
		r.OldestMissing = firstBad.Format("2006-01-02")
		// everything strictly before the first unsafe photo's day is safe
		if day := firstBad.AddDate(0, 0, -1); len(files) > 0 && !files[0].Mod.After(day) {
			r.SafeUntil = day.Format("2006-01-02")
		}
	} else if len(files) > 0 {
		r.SafeUntil = files[len(files)-1].Mod.Format("2006-01-02")
	}
	// byte-by-byte comparison of a spread sample (first 8 MB of each file)
	if n := len(safeFiles); n > 0 && sample > 0 {
		step := max(1, n/sample)
		for i := 0; i < n; i += step {
			a, e1 := fileHash(filepath.Join(root, "DCIM", safeFiles[i].Rel), 8<<20)
			b, e2 := fileHash(safeLocal[i], 8<<20)
			if e1 != nil || e2 != nil {
				continue
			}
			r.Verified++
			if a != b {
				r.Mismatched++
			}
		}
	}
	if r.Mismatched > 0 {
		r.SafeUntil = "" // something does not match: trust nothing until it is understood
	}
	return r, nil
}

// ICloudPhotos reads a copy of the device's Photos database: assets with an iCloud id mean iCloud Photos is on
// (then deleting on the iPhone also deletes from iCloud and every other device).
func ICloudPhotos(run Runner, root, tmp string) string {
	src := filepath.Join(root, "PhotoData", "Photos.sqlite")
	if _, err := os.Stat(src); err != nil {
		return "unknown"
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return "unknown"
	}
	defer os.RemoveAll(tmp)
	for _, ext := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(src + ext); err == nil {
			if err := copyFile(src+ext, filepath.Join(tmp, "Photos.sqlite"+ext), time.Time{}); err != nil && ext == "" {
				return "unknown"
			}
		}
	}
	b, err := run("sqlite3", "-readonly", filepath.Join(tmp, "Photos.sqlite"),
		"SELECT COUNT(*) FROM ZASSET WHERE ZCLOUDASSETGUID IS NOT NULL AND ZCLOUDASSETGUID != '';")
	if err != nil {
		return "unknown"
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return "unknown"
	}
	if n > 0 {
		return "on"
	}
	return "off"
}

// Safe runs the whole check for one device and saves it for the widget: every copied and uploaded file is
// compared whole with the device (only once: the hash is kept), bad copies are fixed, and the photos that can be
// deleted are listed. progress(done, total, name) follows the comparison.
func Safe(run Runner, p Paths, udid, class string, progress func(int, int, string)) (SafeReport, error) {
	cfg := p.LoadConfig()
	release, err := p.lock(jobKey("safe", udid))
	if err != nil {
		return SafeReport{}, err
	}
	defer release()
	if p.locked(jobKey("import", udid)) || p.locked(jobKey("free", udid)) {
		return SafeReport{}, ErrInUse
	}
	root, err := Mount(run, p, udid, "media")
	if err != nil {
		return SafeReport{}, err
	}
	dir := Folder(cfg, class)
	gledger := filepath.Join(p.gphotosState(), "uploaded.jsonl")
	uploaded := UploadedPaths(gledger)
	l := p.LoadLedger(udid)
	files, err := ScanDCIM(root)
	if err != nil {
		return SafeReport{}, err
	}
	var cands []MediaFile
	var locals []string
	for _, f := range files {
		if local, ok := LocalCopy(dir, f, l); ok && uploaded[local] {
			cands = append(cands, f)
			locals = append(locals, local)
		}
	}
	v, drop := VerifyCopies(root, l, cands, locals, progress)
	if err := writeJSON(p.ledgerPath(udid), l); err != nil {
		return SafeReport{}, err
	}
	if _, err := dropFromGooglePhotosLedger(gledger, drop); err != nil {
		return SafeReport{}, err
	}
	uploaded = UploadedPaths(gledger)
	r, err := CheckSafe(root, dir, uploaded, l, 0)
	if err != nil {
		return r, err
	}
	r.UDID = udid
	r.Verified, r.Mismatched = v.Verified, len(v.Unstable)
	r.Repaired, r.Unstable = v.Repaired, v.Unstable
	r.Groups = SafeGroups(files, l, uploaded)
	r.Deletable = len(r.Groups)
	for _, g := range r.Groups {
		r.DeletableBytes += g.Bytes
	}
	r.ICloudPhotos = ICloudPhotos(run, root, filepath.Join(p.Runtime, "photosdb-"+udid))
	_ = writeJSON(filepath.Join(p.State, udid, "safe.json"), r)
	return r, nil
}

// SafeStatus: the last check of each device.
func SafeStatus(p Paths) M {
	out := M{}
	ents, _ := os.ReadDir(p.State)
	for _, e := range ents {
		var r SafeReport
		if e.IsDir() && readJSON(filepath.Join(p.State, e.Name(), "safe.json"), &r) {
			r.Groups = nil // the list is large: safe-list gives it
			out[e.Name()] = r
		}
	}
	return out
}
