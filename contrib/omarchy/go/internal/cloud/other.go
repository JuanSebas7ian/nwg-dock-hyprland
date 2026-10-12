package cloud

import (
	"container/heap"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func statPath(p string) (os.FileInfo, error) { return os.Stat(p) }

// GPhotos runs `gphotos-sync status`.
func GPhotos() any {
	bin := env("GPHOTOS_SYNC", filepath.Join(home(), ".local/bin/gphotos-sync"))
	if !exists(bin) {
		return M{"state": "missing", "error": "gphotos-sync is not installed"}
	}
	out, _, _, err := run(15*time.Second, bin, "status")
	var v any
	if err != nil {
		return M{"error": trim(err.Error(), 300)}
	}
	if strings.TrimSpace(out) == "" {
		return M{}
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return M{"error": trim(err.Error(), 300)}
	}
	return v
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Base quota of each Dropbox plan (Omarchy's table; bonus space is unknown).
var planQuotas = map[string]int64{"basic": 2_000_000_000, "plus": 2_000_000_000_000, "pro": 3_000_000_000_000,
	"professional": 3_000_000_000_000, "essentials": 3_000_000_000_000}

type dbFile struct {
	ts  int64
	n   int
	row M
}
type dbHeap []dbFile

func (h dbHeap) Len() int { return len(h) }
func (h dbHeap) Less(i, j int) bool {
	if h[i].ts != h[j].ts {
		return h[i].ts < h[j].ts
	}
	return h[i].n < h[j].n
}
func (h dbHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *dbHeap) Push(x any)   { *h = append(*h, x.(dbFile)) }
func (h *dbHeap) Pop() any     { o := *h; x := o[len(o)-1]; *h = o[:len(o)-1]; return x }

// ScanDropbox: total size and the newest files (symlinks skipped), as Omarchy's helper.
func ScanDropbox(root string, limit int) (int64, []M) {
	var total int64
	h := &dbHeap{}
	n := 0
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || d.IsDir() {
			return nil
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil
		}
		total += st.Size()
		rel, _ := filepath.Rel(root, p)
		folder := filepath.Dir(rel)
		if folder == "." || folder == "" {
			folder = "/"
		}
		n++
		f := dbFile{st.ModTime().Unix(), n, M{"name": d.Name(), "path": p, "folder": folder, "modifiedTs": st.ModTime().Unix(), "sizeBytes": st.Size()}}
		if h.Len() < limit {
			heap.Push(h, f)
		} else if (*h)[0].ts < f.ts || ((*h)[0].ts == f.ts && (*h)[0].n < f.n) {
			heap.Pop(h)
			heap.Push(h, f)
		}
		return nil
	})
	rows := make([]M, h.Len())
	for i := len(rows) - 1; i >= 0; i-- {
		rows[i] = heap.Pop(h).(dbFile).row
	}
	return total, rows
}

// Dropbox reads the daemon state and the newest files.
func Dropbox(limit int) M {
	cli, err := exec.LookPath("dropbox-cli")
	if err != nil {
		return M{"installed": false}
	}
	var info map[string]any
	b, _ := os.ReadFile(filepath.Join(home(), ".dropbox", "info.json"))
	_ = json.Unmarshal(b, &info)
	var acct map[string]any
	for _, k := range []string{"personal", "business"} {
		if a, ok := info[k].(map[string]any); ok {
			acct = a
			break
		}
	}
	path, _ := acct["path"].(string)
	plan, _ := acct["subscription_type"].(string)
	quota := planQuotas[strings.ToLower(plan)]
	auth := path != "" && exists(path)
	out, errs, code, rerr := run(4*time.Second, cli, "status")
	text := strings.TrimSpace(out + errs)
	statusText := "Stopped"
	if rerr == nil && code == 0 && text != "" {
		statusText = text
	}
	low := strings.ToLower(statusText)
	stopped := strings.Contains(low, "not running") || strings.Contains(low, "isn't running") || low == "stopped"
	running := rerr == nil && code == 0 && text != "" && !stopped
	var used int64
	files := []M{}
	if auth {
		used, files = ScanDropbox(path, limit)
	}
	pct := 0.0
	if quota > 0 {
		pct = float64(used) / float64(quota) * 100
	}
	d := M{"ok": true, "installed": true, "running": running, "authenticated": auth, "statusText": statusText,
		"accountPath": path, "plan": plan, "usedBytes": used, "quotaBytes": quota, "usagePercent": pct, "quotaKnown": quota > 0, "files": files}
	if quota > 0 && used > quota {
		d["quotaKnown"] = false // bonus space or a plan the table does not know
	}
	d["syncing"] = false
	for _, w := range []string{"syncing", "uploading", "downloading", "indexing"} {
		if strings.Contains(low, w) {
			d["syncing"] = true
		}
	}
	return d
}

// Status of all four services, read in parallel.
func Status() M {
	type kv struct {
		k string
		v any
	}
	ch := make(chan kv, 4)
	go func() { ch <- kv{"gdrive", DefaultDrive().Status()} }()
	go func() { ch <- kv{"icloud", DefaultICloud().Status()} }()
	go func() { ch <- kv{"gphotos", GPhotos()} }()
	go func() { ch <- kv{"dropbox", Dropbox(12)} }()
	res := M{}
	for i := 0; i < 4; i++ {
		r := <-ch
		res[r.k] = r.v
	}
	res["t"] = time.Now().Unix()
	return res
}
