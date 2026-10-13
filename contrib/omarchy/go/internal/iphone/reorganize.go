package iphone

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Reorganize moves already imported photos into the folder of the year they were taken (the first imports used
// the device's file dates), gives each file its capture date, records the place in the ledger and updates
// gphotos-sync's ledger so moved files are not uploaded to Google Photos again.
type ReorgResult struct {
	Checked int    `json:"checked"`
	Moved   int    `json:"moved"`
	Dated   int    `json:"dated"`   // files whose capture date came from the metadata
	Missing int    `json:"missing"` // in the ledger but not found on this PC
	Updated int    `json:"updated"` // gphotos-sync ledger entries rewritten
	DryRun  bool   `json:"dryRun"`
	Backup  string `json:"backup,omitempty"`
}

func Reorganize(run Runner, p Paths, udid, class string, dryRun bool, progress func(done, total int)) (ReorgResult, error) {
	cfg := p.LoadConfig()
	dir := Folder(cfg, class)
	l := p.LoadLedger(udid)
	// the device's file dates say exactly where the first imports put each file (year of the file date)
	onDevice := map[string]MediaFile{}
	if root, err := Mount(run, p, udid, "media"); err == nil {
		if files, err := ScanDCIM(root); err == nil {
			for _, f := range files {
				onDevice[f.Rel] = f
			}
		}
	}
	res := ReorgResult{DryRun: dryRun}
	moves := map[string]string{} // old path -> new path
	i := 0
	for rel, e := range l {
		i++
		if progress != nil && i%50 == 0 {
			progress(i, len(l))
		}
		res.Checked++
		f := MediaFile{Rel: rel, Size: e.Size}
		if df, ok := onDevice[rel]; ok {
			f.Mod = df.Mod
		}
		local, ok := "", false
		if e.Local == "" && !f.Mod.IsZero() {
			local, ok = Destination(dir, f) // where the first version put it
		}
		if !ok {
			local, ok = LocalCopy(dir, f, l)
		}
		if !ok {
			res.Missing++
			continue
		}
		taken, fromMeta := CaptureDate(local)
		if fromMeta {
			res.Dated++
		}
		year := strconv.Itoa(taken.Year())
		if !fromMeta || taken.Year() < 2000 {
			// no capture date in the metadata: leave the file where it is
			e.Local = local
			l[rel] = e
			continue
		}
		e.Taken = taken.Format(time.RFC3339)
		target := local
		if filepath.Base(filepath.Dir(local)) != year {
			dated := MediaFile{Rel: rel, Size: e.Size, Mod: taken}
			dst, already := Destination(dir, dated)
			if already && dst != local && !sameContent(dst, local) {
				// same name and size but another photo: keep both, this one under a numbered name
				dst = freeName(dst)
				already = false
			}
			if already && dst != local {
				// the same photo is already in the right folder (an earlier import): keep that one
				target = dst
				if !dryRun {
					_ = os.Remove(local)
				}
			} else {
				target = dst
				if !dryRun {
					if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
						return res, err
					}
					if err := os.Rename(local, dst); err != nil {
						return res, err
					}
				}
			}
			moves[local] = target
			res.Moved++
		}
		if !dryRun {
			_ = os.Chtimes(target, taken, taken)
		}
		e.Local = target
		l[rel] = e
	}
	if dryRun {
		return res, nil
	}
	if err := writeJSON(p.ledgerPath(udid), l); err != nil {
		return res, err
	}
	removeEmptyDirs(dir)
	n, backup, err := rewriteGooglePhotosLedger(filepath.Join(p.gphotosState(), "uploaded.jsonl"), dir, moves)
	res.Updated, res.Backup = n, backup
	return res, err
}

// rewriteGooglePhotosLedger points gphotos-sync's entries at the moved files: same source id, new relative path,
// so the uploader sees them as already uploaded.
func rewriteGooglePhotosLedger(path, root string, moves map[string]string) (int, string, error) {
	if len(moves) == 0 {
		return 0, "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, "", nil // gphotos-sync not set up: nothing to rewrite
	}
	var out bytes.Buffer
	updated := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var e map[string]any
		if json.Unmarshal(line, &e) == nil {
			if old, _ := e["path"].(string); moves[old] != "" {
				nw := moves[old]
				rel, rerr := filepath.Rel(root, nw)
				src, _ := e["src"].(string)
				if rerr == nil && src != "" {
					e["path"], e["rel"], e["k"] = nw, rel, src+":"+rel
					if b, err := json.Marshal(e); err == nil {
						line = b
						updated++
					}
				}
			}
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	f.Close()
	if err := sc.Err(); err != nil {
		return 0, "", err
	}
	backup := path + ".bak." + time.Now().Format("20060102-150405")
	if b, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(backup, b, 0o644)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return 0, "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, "", fmt.Errorf("could not update gphotos-sync's ledger: %v", err)
	}
	return updated, backup, nil
}

func sameContent(a, b string) bool {
	ha, e1 := fileHash(a, 1<<62)
	hb, e2 := fileHash(b, 1<<62)
	return e1 == nil && e2 == nil && ha == hb
}

// freeName is the first "<stem>-N<ext>" that does not exist next to path.
func freeName(path string) string {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(c); err != nil {
			return c
		}
	}
}

func removeEmptyDirs(dir string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			_ = os.Remove(filepath.Join(dir, e.Name())) // fails (kept) when not empty
		}
	}
}
