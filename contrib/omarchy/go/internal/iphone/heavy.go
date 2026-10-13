package iphone

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// What takes space, to decide what to clean: apps on the device (app + its data), the heaviest photos and
// videos (with whether each is safe to delete), and the device backups kept on this PC.

// AppSize is one app on the device.
type AppSize struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	App     int64  `json:"app"`  // the app itself (StaticDiskUsage)
	Data    int64  `json:"data"` // documents, caches, media it keeps (DynamicDiskUsage)
	Total   int64  `json:"total"`
}

// ParseAppSizes reads installation_proxy's answer (an XML plist array of dicts), biggest first.
func ParseAppSizes(xml []byte) ([]AppSize, error) {
	v, err := ParsePlist(xml)
	if err != nil {
		return nil, err
	}
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("unexpected answer from the device")
	}
	out := []AppSize{}
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		a := AppSize{ID: str(m, "CFBundleIdentifier"), Name: str(m, "CFBundleDisplayName"), Version: str(m, "CFBundleShortVersionString"),
			App: num(m, "StaticDiskUsage"), Data: num(m, "DynamicDiskUsage")}
		if a.Name == "" {
			a.Name = str(m, "CFBundleName")
		}
		if a.Name == "" {
			a.Name = a.ID
		}
		a.Total = a.App + a.Data
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	return out, nil
}

// AppSizes asks the device and keeps the answer (the query takes a few seconds).
func AppSizes(p Paths, udid string) (M, error) {
	xml, err := browseApps(udid)
	if err != nil {
		return nil, err
	}
	apps, err := ParseAppSizes(xml)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, a := range apps {
		total += a.Total
	}
	r := M{"time": time.Now().Format(time.RFC3339), "apps": apps, "total": total}
	_ = writeJSON(p.devFile(udid, "apps.json"), r)
	return r, nil
}

// LargeFile is one of the heaviest photos/videos on the device.
type LargeFile struct {
	Rel   string `json:"rel"`
	Size  int64  `json:"size"`
	Date  string `json:"date"`
	Video bool   `json:"video"`
	Local string `json:"local,omitempty"` // the copy on this PC, to look at it
	Safe  bool   `json:"safe"`            // on this PC and in Google Photos
	State string `json:"state"`           // safe | not uploaded | not copied
}

// Largest picks the n heaviest camera-roll files with their state.
func Largest(files []MediaFile, dir string, uploaded map[string]bool, l Ledger, n int) []LargeFile {
	sorted := append([]MediaFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Size > sorted[j].Size })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	out := []LargeFile{}
	for _, f := range sorted {
		ext := strings.ToLower(filepath.Ext(f.Rel))
		lf := LargeFile{Rel: f.Rel, Size: f.Size, Date: takenOf(f, l).Format("2006-01-02"), Video: ext == ".mov" || ext == ".mp4" || ext == ".m4v" || ext == ".hevc"}
		local, copied := LocalCopy(dir, f, l)
		switch {
		case !copied:
			lf.State = "not copied"
		case !uploaded[local]:
			lf.State, lf.Local = "not uploaded", local
		default:
			lf.State, lf.Local, lf.Safe = "safe", local, true
		}
		out = append(out, lf)
	}
	return out
}

// LocalBackup is one device backup kept on this PC.
type LocalBackup struct {
	UDID         string `json:"udid"`
	Name         string `json:"name"`
	Bytes        int64  `json:"bytes"`
	Time         string `json:"time"`
	InDrive      bool   `json:"inDrive"`   // uploaded after this backup was made
	DriveTime    string `json:"driveTime"` // last upload
	CanDelete    bool   `json:"canDelete"` // safe to remove locally: Google Drive has it
	WhyNotDelete string `json:"whyNotDelete,omitempty"`
}

// LocalBackups lists the backups under the backup folder.
func LocalBackups(p Paths, cfg Config) []LocalBackup {
	out := []LocalBackup{}
	ents, _ := os.ReadDir(cfg.BackupDir)
	for _, e := range ents {
		if !e.IsDir() || !udidRe.MatchString(e.Name()) {
			continue
		}
		u := e.Name()
		b := LocalBackup{UDID: u}
		var dev Device
		if readJSON(p.devFile(u, "device.json"), &dev) {
			b.Name = deviceLabel(dev)
		}
		var lb, ld M
		readJSON(p.devFile(u, "last-backup.json"), &lb)
		readJSON(p.devFile(u, "last-drive.json"), &ld)
		if v, ok := lb["bytes"].(float64); ok {
			b.Bytes = int64(v)
		}
		b.Time, _ = lb["time"].(string)
		b.DriveTime, _ = ld["time"].(string)
		bt, e1 := time.Parse(time.RFC3339, b.Time)
		dt, e2 := time.Parse(time.RFC3339, b.DriveTime)
		b.InDrive = e1 == nil && e2 == nil && ld["ok"] == true && !dt.Before(bt)
		switch {
		case p.locked(jobKey("backup", u)) || p.locked(jobKey("drive", u)):
			b.WhyNotDelete = "a backup or upload of this device is running"
		case !b.InDrive:
			b.WhyNotDelete = "not in Google Drive yet: upload it first"
		default:
			b.CanDelete = true
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

// DeleteLocalBackup removes one device's backup from this PC, only when Google Drive has a copy made after it.
// The next backup of that device starts from zero again (a full one; Google Drive still deduplicates it).
func DeleteLocalBackup(p Paths, udid string) (int64, error) {
	if !udidRe.MatchString(udid) {
		return 0, errors.New("bad device id")
	}
	cfg := p.LoadConfig()
	for _, b := range LocalBackups(p, cfg) {
		if b.UDID != udid {
			continue
		}
		if !b.CanDelete {
			return 0, fmt.Errorf("not deleted: %s", b.WhyNotDelete)
		}
		dir := filepath.Join(cfg.BackupDir, udid)
		if err := os.RemoveAll(dir); err != nil {
			return 0, err
		}
		// keep the backup's date: the weekly automatic backup counts from it (else it would refill the disk at once)
		_ = writeJSON(p.devFile(udid, "last-backup.json"), M{"udid": udid, "time": b.Time, "ok": true, "bytes": 0,
			"localDeleted": time.Now().Format(time.RFC3339)})
		return b.Bytes, nil
	}
	return 0, errors.New("there is no local backup of that device")
}
