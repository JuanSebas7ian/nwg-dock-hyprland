package iphone

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Tested 2026-10-13 on iOS 26.7.1: the device accepts a PTP delete and stops offering the object, but the Photos
// library keeps the photo (DCIM file and ZASSET row untouched). So the widget does not offer it; deleting is done in
// the Photos app, guided by SafeRuns and the safe list. The code stays for devices where it works.
//
// Freeing space deletes photos and videos from the device over PTP: the same way Windows' "delete after import"
// does it, through the device's own Photos framework, so the Photos library stays consistent (deleting the files
// over AFC would leave the library pointing at missing files). Only what is proven safe is deleted:
//   - the copy on this PC has exactly the same content (whole file, SHA-256, read from the device for this check),
//   - that copy is in Google Photos (gphotos-sync's ledger),
//   - and every file of the photo is (a Live Photo's HEIC and MOV, an edited IMG_E… version) — never half of one.
// ifuse sometimes hands back a 128 KB block from elsewhere in the file (seen on 2 of 31 sampled copies), so a copy
// that differs is copied again, read twice to be sure, and sent to Google Photos again.

// SafeGroup is one photo or video on the device (all its files) that can be deleted.
type SafeGroup struct {
	Key   string   `json:"key"`   // folder/number: 100APPLE/0362
	Name  string   `json:"name"`  // IMG_0362
	Files []string `json:"files"` // DCIM paths (100APPLE/IMG_0362.HEIC, 100APPLE/IMG_0362.MOV)
	Bytes int64    `json:"bytes"`
	Taken string   `json:"taken"` // capture day
	At    string   `json:"at"`    // capture time (HH:MM), to find it in the Photos app
	Video bool     `json:"video"` // a video (not a Live Photo's motion part)
	Local string   `json:"local"` // a copy on this PC, to look at it
}

var groupRe = regexp.MustCompile(`^IMG_E?(\d+)`)

// GroupKey: files of one photo share their folder and number (IMG_0362.HEIC, IMG_0362.MOV, IMG_E0362.HEIC).
func GroupKey(rel string) string {
	dir, name := path.Split(rel)
	if m := groupRe.FindStringSubmatch(strings.ToUpper(name)); m != nil {
		return dir + m[1]
	}
	return rel
}

func isVideo(rel string) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".mov", ".mp4", ".m4v", ".hevc":
		return true
	}
	return false
}

// SHA256 of a whole file, as hex.
func fullHash(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyHashed copies src to dst (through a .part file) and returns the SHA-256 of what was written.
func copyHashed(src, dst string, mod time.Time) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if !mod.IsZero() {
		_ = os.Chtimes(tmp, mod, mod)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyResult is what the whole-file comparison found.
type VerifyResult struct {
	Verified int      `json:"verified"` // files whose copy is identical (now or in an earlier check)
	Repaired []string `json:"repaired"` // local copies that differed: copied again, to be uploaded again
	Unstable []string `json:"unstable"` // device files that read differently twice: left alone
}

// VerifyCopies compares, whole file, each candidate (copied and uploaded) with its copy and records the hash in the
// ledger; a copy that differs is replaced by a fresh one (read twice) and dropped from Google Photos' ledger so it
// is uploaded again. progress(done, total, name).
func VerifyCopies(root string, l Ledger, cands []MediaFile, locals []string, progress func(int, int, string)) (VerifyResult, []string) {
	var r VerifyResult
	var drop []string // local paths to upload again
	for i, f := range cands {
		if progress != nil {
			progress(i, len(cands), filepath.Base(f.Rel))
		}
		e := l[f.Rel]
		local := locals[i]
		st, err := os.Stat(local)
		if err != nil {
			continue
		}
		if e.Hash != "" && e.Size == f.Size && e.Local == local && e.LocalSize == st.Size() && e.LocalMod == st.ModTime().UnixNano() {
			r.Verified++ // proven earlier and neither file changed
			continue
		}
		src := filepath.Join(root, "DCIM", f.Rel)
		hd, err1 := fullHash(src)
		hl, err2 := fullHash(local)
		if err1 != nil || err2 != nil {
			continue
		}
		if hd != hl {
			// read the device again: the same answer twice means the device is right and the copy is not
			if again, err := fullHash(src); err != nil || again != hd {
				r.Unstable = append(r.Unstable, f.Rel)
				continue
			}
			got, err := copyHashed(src, local, st.ModTime())
			if err != nil || got != hd {
				os.Remove(local + ".part")
				r.Unstable = append(r.Unstable, f.Rel)
				continue
			}
			if err := os.Rename(local+".part", local); err != nil {
				continue
			}
			r.Repaired = append(r.Repaired, f.Rel)
			drop = append(drop, local)
			st, _ = os.Stat(local)
		}
		e.Size, e.Local, e.Hash = f.Size, local, hd
		e.LocalSize, e.LocalMod = st.Size(), st.ModTime().UnixNano()
		l[f.Rel] = e
		r.Verified++
	}
	if progress != nil {
		progress(len(cands), len(cands), "")
	}
	return r, drop
}

// SafeGroups lists the photos whose every file is verified and uploaded, oldest first.
func SafeGroups(files []MediaFile, l Ledger, uploaded map[string]bool) []SafeGroup {
	byKey := map[string][]MediaFile{}
	for _, f := range files {
		byKey[GroupKey(f.Rel)] = append(byKey[GroupKey(f.Rel)], f)
	}
	var out []SafeGroup
	for key, fs := range byKey {
		g := SafeGroup{Key: key}
		ok, video, still := true, false, false
		var taken time.Time
		for _, f := range fs {
			e, in := l[f.Rel]
			if !in || e.Hash == "" || e.Size != f.Size || !uploaded[e.Local] {
				ok = false
				break
			}
			g.Files = append(g.Files, f.Rel)
			g.Bytes += f.Size
			t := takenOf(f, l)
			if taken.IsZero() || t.Before(taken) {
				taken = t
			}
			if isVideo(f.Rel) {
				video = true
			} else {
				still = true
				g.Local = e.Local
			}
		}
		if !ok {
			continue
		}
		if g.Local == "" {
			g.Local = l[fs[0].Rel].Local
		}
		sort.Strings(g.Files)
		g.Name = strings.TrimSuffix(path.Base(g.Files[0]), path.Ext(g.Files[0]))
		g.Video = video && !still
		g.Taken = taken.Format("2006-01-02")
		g.At = taken.Format("15:04")
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Taken != out[j].Taken {
			return out[i].Taken < out[j].Taken
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// dropFromGooglePhotosLedger forgets uploads of the given local paths, so gphotos-sync uploads them again.
func dropFromGooglePhotosLedger(ledger string, paths []string) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	b, err := os.ReadFile(ledger)
	if err != nil {
		return 0, nil
	}
	var out bytes.Buffer
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && set[e.Path] {
			n++
			continue
		}
		out.Write(sc.Bytes())
		out.WriteByte('\n')
	}
	if n == 0 {
		return 0, nil
	}
	_ = os.WriteFile(ledger+".bak."+time.Now().Format("20060102-150405"), b, 0o644)
	tmp := ledger + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return 0, err
	}
	return n, os.Rename(tmp, ledger)
}

// ---- PTP ----

// ErrLocked: PTP shows no photos while the device is locked.
var ErrLocked = errors.New("the device does not offer its photos: unplug it, unlock it and plug it in again (accept \"Allow access to photos\"), then keep it unlocked")

// PTPPort finds the gphoto2 port (usb:BUS,DEV) of a device: its USB serial is the UDID without dashes.
func PTPPort(sys, udid string) (string, error) {
	want := strings.ReplaceAll(udid, "-", "")
	devs, _ := filepath.Glob(filepath.Join(sys, "*"))
	for _, d := range devs {
		b, err := os.ReadFile(filepath.Join(d, "serial"))
		if err != nil || !strings.EqualFold(strings.TrimSpace(string(b)), want) {
			continue
		}
		bus, e1 := os.ReadFile(filepath.Join(d, "busnum"))
		dev, e2 := os.ReadFile(filepath.Join(d, "devnum"))
		if e1 != nil || e2 != nil {
			continue
		}
		var nb, nd int
		fmt.Sscan(strings.TrimSpace(string(bus)), &nb)
		fmt.Sscan(strings.TrimSpace(string(dev)), &nd)
		return fmt.Sprintf("usb:%03d,%03d", nb, nd), nil
	}
	return "", errors.New("the device is not on USB")
}

var (
	ptpFolderRe = regexp.MustCompile(`folder '([^']+)'`)
	ptpFileRe   = regexp.MustCompile(`^#\d+\s+(\S+)`)
)

// ParsePTPList reads `gphoto2 --list-files`: PTP folder -> file names. The DCIM folders are matched by their last
// part (…/DCIM/100APPLE → 100APPLE).
func ParsePTPList(out string) map[string][]string {
	files := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		if m := ptpFolderRe.FindStringSubmatch(line); m != nil && strings.HasPrefix(line, "There") {
			cur = m[1]
			continue
		}
		if m := ptpFileRe.FindStringSubmatch(line); m != nil && cur != "" {
			files[cur] = append(files[cur], m[1])
		}
	}
	return files
}

// ptpPortFn finds the device on this PC; tests replace it.
var ptpPortFn = func(udid string) (string, error) { return PTPPort("/sys/bus/usb/devices", udid) }

// PTPLibDir holds a libgphoto2 2.5.33 (usr/lib of Arch's package), used when present: with 2.5.34 an iOS 26 device
// shows its storage empty (the photo folders are not listed). install.sh puts it there.
func PTPLibDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "lib", "omarchy-iphone", "libgphoto2")
}

// ptpEnv points gphoto2 at the bundled libgphoto2 and its camera/port drivers.
func ptpEnv(dir string) []string {
	cams, _ := filepath.Glob(filepath.Join(dir, "libgphoto2", "2.*"))
	ports, _ := filepath.Glob(filepath.Join(dir, "libgphoto2_port", "0.*"))
	if len(cams) == 0 || len(ports) == 0 {
		return nil
	}
	return append(os.Environ(), "LD_LIBRARY_PATH="+dir, "CAMLIBS="+cams[len(cams)-1], "IOLIBS="+ports[len(ports)-1])
}

// ptpRun runs gphoto2 (shell commands on stdin when script is not empty); tests replace it.
var ptpRun = func(port, script string, args ...string) ([]byte, error) {
	cmd := exec.Command("gphoto2", append([]string{"--port", port}, args...)...)
	cmd.Env = ptpEnv(PTPLibDir())
	if script != "" {
		cmd.Stdin = strings.NewReader(script)
	}
	return cmd.CombinedOutput()
}

// PTPPlan maps the DCIM files to delete to PTP objects. iOS lays PTP out by month (/store_…/202306_a) and offers a
// HEIC as JPG ("Transfer to Mac or PC: Automatic"), so a file is matched by its name without extension and by being
// a still or a video; when the name is in several folders, the one of its capture month (month[rel] = "200601")
// decides, and if that does not settle it the file is left alone. An edit's .AAE sidecar goes after its photo.
// owner says which DCIM files each PTP object stands for.
func PTPPlan(list map[string][]string, rels []string, month map[string]string) (plan map[string][]string, owner map[string][]string, missing []string) {
	type obj struct{ folder, name string }
	byStem := map[string][]obj{}
	for folder, names := range list {
		for _, n := range names {
			byStem[strings.ToUpper(strings.TrimSuffix(n, path.Ext(n)))] = append(byStem[strings.ToUpper(strings.TrimSuffix(n, path.Ext(n)))], obj{folder, n})
		}
	}
	plan, owner = map[string][]string{}, map[string][]string{}
	sidecars := map[string][]string{}
	for _, rel := range rels {
		dir, name := path.Split(rel)
		stem := strings.ToUpper(strings.TrimSuffix(name, path.Ext(name)))
		var cands []obj
		folders := map[string]bool{}
		for _, o := range byStem[stem] {
			if strings.EqualFold(path.Ext(o.name), ".aae") || isVideo(o.name) != isVideo(name) {
				continue
			}
			cands = append(cands, o)
			folders[o.folder] = true
		}
		if len(folders) > 1 {
			var keep []obj
			kf := map[string]bool{}
			for _, o := range cands {
				base := path.Base(o.folder)
				if (month[rel] != "" && strings.HasPrefix(base, month[rel])) || base == strings.TrimSuffix(dir, "/") {
					keep = append(keep, o)
					kf[o.folder] = true
				}
			}
			cands, folders = keep, kf
		}
		if len(cands) == 0 || len(folders) != 1 {
			missing = append(missing, rel)
			continue
		}
		for _, o := range cands {
			k := o.folder + "/" + o.name
			if !contains(plan[o.folder], o.name) {
				plan[o.folder] = append(plan[o.folder], o.name)
			}
			if !contains(owner[k], rel) {
				owner[k] = append(owner[k], rel)
			}
			for _, x := range byStem[stem] {
				if x.folder == o.folder && strings.EqualFold(path.Ext(x.name), ".aae") && !contains(sidecars[o.folder], x.name) {
					sidecars[o.folder] = append(sidecars[o.folder], x.name)
				}
			}
		}
	}
	for folder, names := range sidecars {
		plan[folder] = append(plan[folder], names...)
	}
	return plan, owner, missing
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// FreeRequest is what the widget asked to delete (State/<udid>/free-request.json).
type FreeRequest struct {
	All  bool     `json:"all"`
	Keys []string `json:"keys"`
}

// FreeResult is the outcome of a deletion.
type FreeResult struct {
	Time    string   `json:"time"`
	Groups  int      `json:"groups"`
	Deleted int      `json:"deleted"` // files
	Bytes   int64    `json:"bytes"`
	Missing []string `json:"missing,omitempty"` // not offered over PTP (already gone, or only in iCloud)
	Failed  []string `json:"failed,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Free deletes the requested safe photos from the device. It re-checks each one against the last safety check
// and the device as it is now (same size) before deleting.
func Free(run Runner, p Paths, udid string, req FreeRequest, progress func(done, total int, name string)) (FreeResult, error) {
	res := FreeResult{Time: time.Now().Format(time.RFC3339)}
	release, err := p.lock(jobKey("free", udid))
	if err != nil {
		return res, err
	}
	defer release()
	if p.locked(jobKey("import", udid)) || p.locked(jobKey("safe", udid)) {
		return res, ErrInUse
	}
	var rep SafeReport
	if !readJSON(filepath.Join(p.State, udid, "safe.json"), &rep) || rep.UDID != udid {
		return res, errors.New("run the safety check first")
	}
	want := map[string]bool{}
	for _, k := range req.Keys {
		want[k] = true
	}
	var groups []SafeGroup
	for _, g := range rep.Groups {
		if req.All || want[g.Key] {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return res, errors.New("nothing to delete: run the safety check again")
	}
	// the device now: every file still there with the size that was verified
	root, err := Mount(run, p, udid, "media")
	if err != nil {
		return res, err
	}
	files, err := ScanDCIM(root)
	Unmount(run, p, udid) // PTP and AFC together confuse some devices
	if err != nil {
		return res, err
	}
	now := map[string]int64{}
	for _, f := range files {
		now[f.Rel] = f.Size
	}
	l := p.LoadLedger(udid)
	uploaded := UploadedPaths(filepath.Join(p.gphotosState(), "uploaded.jsonl"))
	var rels []string
	var passed []SafeGroup
	sizes := map[string]int64{}
	for _, g := range groups {
		ok := true
		for _, rel := range g.Files {
			e := l[rel]
			if e.Hash == "" || now[rel] != e.Size || !uploaded[e.Local] {
				ok = false
			}
		}
		// a file of the photo that appeared after the check (an edit) was not verified: keep the photo
		for rel := range now {
			if GroupKey(rel) == g.Key && !contains(g.Files, rel) {
				ok = false
			}
		}
		if !ok {
			res.Failed = append(res.Failed, g.Name+" (changed since the check)")
			continue
		}
		res.Groups++
		passed = append(passed, g)
		for _, rel := range g.Files {
			rels = append(rels, rel)
			sizes[rel] = now[rel]
		}
	}
	port, err := ptpPortFn(udid)
	if err != nil {
		return res, err
	}
	b, err := ptpRun(port, "", "--list-files")
	if err != nil {
		return res, fmt.Errorf("PTP: %s", lastLine(string(b)))
	}
	list := ParsePTPList(string(b))
	if len(list) == 0 {
		return res, ErrLocked
	}
	month := map[string]string{}
	for _, rel := range rels {
		if t := l[rel].Taken; len(t) >= 7 {
			month[rel] = t[:4] + t[5:7]
		}
	}
	plan, owner, missing := PTPPlan(list, rels, month)
	res.Missing = missing
	folders := make([]string, 0, len(plan))
	total := 0
	for f, names := range plan {
		folders = append(folders, f)
		total += len(names)
	}
	sort.Strings(folders)
	done := 0
	failedKeys := map[string]bool{}
	for _, folder := range folders {
		names := plan[folder]
		for i := 0; i < len(names); i += 40 {
			batch := names[i:min(i+40, len(names))]
			var script strings.Builder
			fmt.Fprintf(&script, "cd %s\n", folder)
			for _, n := range batch {
				fmt.Fprintf(&script, "delete %s\n", n)
			}
			script.WriteString("exit\n")
			out, _ := ptpRun(port, script.String(), "--shell")
			// what is left decides what was deleted (the shell's own messages vary between versions)
			after, err := ptpRun(port, "", "--folder", folder, "--list-files")
			if err != nil {
				res.Error = lastLine(string(out))
				return res, fmt.Errorf("PTP stopped answering: %s", lastLine(string(after)))
			}
			left := ParsePTPList(string(after))[folder]
			for _, n := range batch {
				rels := owner[folder+"/"+n]
				if len(rels) == 0 {
					continue // an edit's sidecar: it usually goes with its photo
				}
				if contains(left, n) {
					res.Failed = append(res.Failed, path.Base(folder)+"/"+n)
					for _, rel := range rels {
						failedKeys[GroupKey(rel)] = true
					}
					continue
				}
				res.Deleted++
				for _, rel := range rels {
					res.Bytes += sizes[rel] // the stored size (a JPG offered over PTP is not it)
					delete(sizes, rel)
				}
			}
			done += len(batch)
			if progress != nil {
				progress(done, total, batch[len(batch)-1])
			}
		}
	}
	// photos none of whose files were offered over PTP were not deleted
	for _, rel := range missing {
		failedKeys[GroupKey(rel)] = true
	}
	appendJSONL(filepath.Join(p.State, udid, "deleted.jsonl"), res)
	// the deleted photos leave the list until the next check
	if res.Deleted > 0 {
		gone := map[string]bool{}
		for _, g := range passed {
			gone[g.Key] = !failedKeys[g.Key]
		}
		keep := rep.Groups[:0]
		for _, g := range rep.Groups {
			if !gone[g.Key] {
				keep = append(keep, g)
			}
		}
		rep.Groups = keep
		rep.Deletable, rep.DeletableBytes = len(keep), 0
		for _, g := range keep {
			rep.DeletableBytes += g.Bytes
		}
		_ = writeJSON(filepath.Join(p.State, udid, "safe.json"), rep)
	}
	_ = writeJSON(filepath.Join(p.State, udid, "last-free.json"), res)
	return res, nil
}

// StartFree saves what to delete and starts the deletion as a detached job.
func StartFree(p Paths, udid string, req FreeRequest) error {
	if err := writeJSON(filepath.Join(p.State, udid, "free-request.json"), req); err != nil {
		return err
	}
	return StartJob("free", udid)
}

// LoadFreeRequest reads (and consumes) what the widget asked to delete.
func LoadFreeRequest(p Paths, udid string) (FreeRequest, error) {
	f := filepath.Join(p.State, udid, "free-request.json")
	var req FreeRequest
	if !readJSON(f, &req) {
		return req, errors.New("nothing was asked to be deleted")
	}
	os.Remove(f)
	return req, nil
}

// SafeGroupsOf: the deletable photos of the last check.
func SafeGroupsOf(p Paths, udid string) []SafeGroup {
	var r SafeReport
	readJSON(filepath.Join(p.State, udid, "safe.json"), &r)
	if r.Groups == nil {
		return []SafeGroup{}
	}
	return r.Groups
}

// SaveFreeResult records a deletion that stopped with an error.
func SaveFreeResult(p Paths, udid string, res FreeResult) {
	_ = writeJSON(filepath.Join(p.State, udid, "last-free.json"), res)
}

// FreeStatus: the last deletion of one device.
func FreeStatus(p Paths, udid string) M {
	var last M
	readJSON(filepath.Join(p.State, udid, "last-free.json"), &last)
	return M{"last": last, "running": p.locked(jobKey("free", udid))}
}

func appendJSONL(file string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	if f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Write(append(b, '\n'))
		f.Close()
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// PTPReady says whether the device offers its photos over PTP now. Locked when plugged in, iOS shows only an empty
// read-only store (store_feedface) until it is unplugged and plugged in again unlocked.
func PTPReady(udid string) (bool, string) {
	port, err := ptpPortFn(udid)
	if err != nil {
		return false, err.Error()
	}
	b, err := ptpRun(port, "", "--storage-info")
	out := string(b)
	if err != nil {
		return false, "PTP: " + lastLine(out)
	}
	if !strings.Contains(out, "basedir=") || strings.Contains(out, "store_feedface") {
		return false, "unplug the device, unlock it and plug it in again (accept \"Allow access to photos\" if it asks)"
	}
	return true, ""
}

// SafeRun is a stretch of the camera roll, in capture order (the order of Photos › Library), where every photo is
// safe: in the Photos app it is selected in one drag, from the first to the last.
type SafeRun struct {
	From      string `json:"from"` // capture time of the first ("2006-01-02 15:04")
	To        string `json:"to"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	N         int    `json:"n"`
	Bytes     int64  `json:"bytes"`
}

// SafeRuns finds the stretches of safe photos (keys of the safe groups) in capture order, largest first.
func SafeRuns(files []MediaFile, l Ledger, safe map[string]bool, max int) []SafeRun {
	type item struct {
		key   string
		name  string
		taken time.Time
		bytes int64
	}
	byKey := map[string]*item{}
	for _, f := range files {
		k := GroupKey(f.Rel)
		it := byKey[k]
		t := takenOf(f, l)
		if it == nil {
			it = &item{key: k, name: strings.TrimSuffix(path.Base(f.Rel), path.Ext(f.Rel)), taken: t}
			byKey[k] = it
		}
		if t.Before(it.taken) {
			it.taken = t
		}
		it.bytes += f.Size
	}
	items := make([]*item, 0, len(byKey))
	for _, it := range byKey {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].taken.Equal(items[j].taken) {
			return items[i].taken.Before(items[j].taken)
		}
		return items[i].key < items[j].key
	})
	var runs []SafeRun
	var cur *SafeRun
	for _, it := range items {
		if !safe[it.key] {
			cur = nil
			continue
		}
		if cur == nil {
			runs = append(runs, SafeRun{From: it.taken.Format("2006-01-02 15:04"), FirstName: it.name})
			cur = &runs[len(runs)-1]
		}
		cur.To, cur.LastName = it.taken.Format("2006-01-02 15:04"), it.name
		cur.N++
		cur.Bytes += it.bytes
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Bytes > runs[j].Bytes })
	if len(runs) > max {
		runs = runs[:max]
	}
	return runs
}
