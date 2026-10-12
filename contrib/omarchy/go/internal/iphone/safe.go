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

// Freeing space on the iPhone is done in the Photos app (Select → Delete, then empty Recently Deleted): deleting
// files over AFC/PTP leaves the Photos library pointing at missing files. This check says which photos are safe to
// delete there: copied to this PC (same size, and a sample compared byte by byte) AND uploaded to Google Photos.

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

// LocalCopy finds the imported copy of one camera-roll file (same name rule and size as Destination).
func LocalCopy(dir string, f MediaFile) (string, bool) {
	p, already := Destination(dir, f)
	return p, already
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
	Time          string `json:"time"`
	UDID          string `json:"udid"`
	Total         int    `json:"total"`
	Safe          int    `json:"safe"`
	SafeBytes     int64  `json:"safeBytes"`
	NotCopied     int    `json:"notCopied"`
	NotUploaded   int    `json:"notUploaded"`
	SafeUntil     string `json:"safeUntil,omitempty"`     // every photo taken up to this day is safe
	OldestMissing string `json:"oldestMissing,omitempty"` // the oldest photo that is not safe yet
	Verified      int    `json:"verified"`                // sample compared byte by byte
	Mismatched    int    `json:"mismatched"`
	ICloudPhotos  string `json:"icloudPhotos"` // on | off | unknown
	Dir           string `json:"dir"`
}

// CheckSafe compares the camera roll mounted at root with the local copies in dir and the Google Photos ledger.
func CheckSafe(root, dir string, uploaded map[string]bool, sample int) (SafeReport, error) {
	r := SafeReport{Time: time.Now().Format(time.RFC3339), Dir: dir, ICloudPhotos: "unknown"}
	files, err := ScanDCIM(root)
	if err != nil {
		return r, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Mod.Before(files[j].Mod) })
	r.Total = len(files)
	var firstBad time.Time
	var safeFiles []MediaFile
	var safeLocal []string
	for _, f := range files {
		local, copied := LocalCopy(dir, f)
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

// Safe runs the whole check for one device and saves it for the widget.
func Safe(run Runner, p Paths, udid, class string) (SafeReport, error) {
	cfg := p.LoadConfig()
	release, err := p.lock("safe")
	if err != nil {
		return SafeReport{}, err
	}
	defer release()
	root, err := Mount(run, p, udid, "media")
	if err != nil {
		return SafeReport{}, err
	}
	ledger := filepath.Join(filepath.Dir(p.State), "gphotos-sync", "uploaded.jsonl")
	r, err := CheckSafe(root, Folder(cfg, class), UploadedPaths(ledger), 30)
	if err != nil {
		return r, err
	}
	r.UDID = udid
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
			out[e.Name()] = r
		}
	}
	return out
}
