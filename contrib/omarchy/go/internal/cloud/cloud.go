// Package cloud is the backend of the juansebas7ian.cloud bar widget: Google
// Drive and iCloud Drive (rclone mounts with a remote-control socket), the
// Google Photos uploader and Dropbox, read in one process.
package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// M is a JSON object.
type M = map[string]any

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func home() string { h, _ := os.UserHomeDir(); return h }

func runtimeDir() string { return env("XDG_RUNTIME_DIR", fmt.Sprintf("/run/user/%d", os.Getuid())) }
func cacheHome() string  { return env("XDG_CACHE_HOME", filepath.Join(home(), ".cache")) }
func stateHome() string  { return env("XDG_STATE_HOME", filepath.Join(home(), ".local/state")) }

// RC calls rclone's remote control on a unix socket.
func RC(sock, method string, params M) (M, error) {
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
		}}}
	if params == nil {
		params = M{}
	}
	b, _ := json.Marshal(params)
	resp, err := hc.Post("http://localhost/"+method, "application/json", strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	data := M{}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, err
		}
	}
	if resp.StatusCode != 200 {
		if e, ok := data["error"].(string); ok && e != "" {
			return nil, fmt.Errorf("%s", e)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return data, nil
}

func run(timeout time.Duration, name string, args ...string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
		err = nil
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.String(), errb.String(), code, err
}

// ServiceState is `systemctl --user is-active`.
func ServiceState(unit string) string {
	out, _, _, _ := run(10*time.Second, "systemctl", "--user", "is-active", unit)
	if s := strings.TrimSpace(out); s != "" {
		return s
	}
	return "unknown"
}

// Systemctl starts or stops a user unit.
func Systemctl(action, unit string) M {
	_, errs, code, err := run(60*time.Second, "systemctl", "--user", action, unit)
	if err != nil {
		return M{"ok": false, "error": "timeout"}
	}
	return M{"ok": code == 0, "error": strings.TrimSpace(errs)}
}

// IsMount: the folder is on another device than its parent.
func IsMount(path string) bool {
	var a, b syscall.Stat_t
	if syscall.Lstat(path, &a) != nil || a.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return false
	}
	if syscall.Lstat(filepath.Join(path, ".."), &b) != nil {
		return false
	}
	return a.Dev != b.Dev || a.Ino == b.Ino
}

type fileInfo struct {
	mtime float64
	rel   string
	size  int64
}

// Recent lists the files rclone's VFS cache holds, newest first.
func Recent(cacheDir string, limit int) []M {
	out := []M{}
	if st, err := os.Stat(cacheDir); cacheDir == "" || err != nil || !st.IsDir() {
		return out
	}
	var files []fileInfo
	_ = filepath.WalkDir(cacheDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(cacheDir, p)
		files = append(files, fileInfo{float64(st.ModTime().UnixNano()) / 1e9, rel, st.Size()})
		return nil
	})
	// Python sorted (mtime, rel, size) tuples in reverse
	sortFiles(files)
	for i, f := range files {
		if i >= limit {
			break
		}
		dir := filepath.Dir(f.rel)
		if dir == "." {
			dir = ""
		}
		out = append(out, M{"path": f.rel, "name": filepath.Base(f.rel), "dir": dir, "mtime": int64(f.mtime), "size": f.size})
	}
	return out
}

func sortFiles(f []fileInfo) {
	less := func(a, b fileInfo) bool {
		if a.mtime != b.mtime {
			return a.mtime > b.mtime
		}
		if a.rel != b.rel {
			return a.rel > b.rel
		}
		return a.size > b.size
	}
	for i := 1; i < len(f); i++ {
		for j := i; j > 0 && less(f[j], f[j-1]); j-- {
			f[j], f[j-1] = f[j-1], f[j]
		}
	}
}

func get(m M, k string, def any) any {
	if v, ok := m[k]; ok && v != nil {
		return v
	}
	return def
}

// rcStats fills the shared transfer fields of a mount's status.
func rcStats(sock string, res M, queueOK bool) (M, error) {
	vfs, err := RC(sock, "vfs/stats", nil)
	if err != nil {
		return nil, err
	}
	res["rc"] = true
	cache, _ := vfs["diskCache"].(M)
	if cache == nil {
		cache = M{}
	}
	res["cache"] = cache
	stats, err := RC(sock, "core/stats", nil)
	if err != nil {
		return nil, err
	}
	res["speed"] = get(stats, "speed", 0)
	res["errors"] = get(stats, "errors", 0)
	res["lastError"] = get(stats, "lastError", "")
	transfers := []M{}
	if list, ok := stats["transferring"].([]any); ok {
		for _, x := range list {
			t, _ := x.(M)
			speed := get(t, "speedAvg", get(t, "speed", 0))
			transfers = append(transfers, M{"name": get(t, "name", ""), "size": get(t, "size", 0), "bytes": get(t, "bytes", 0),
				"percent": get(t, "percentage", 0), "speed": speed, "eta": t["eta"]})
		}
	}
	res["transfers"] = transfers
	return vfs, nil
}

func queue(sock string, limit int) ([]M, error) {
	q, err := RC(sock, "vfs/queue", nil)
	if err != nil {
		return nil, err
	}
	var out []M
	list, _ := q["queue"].([]any)
	for _, x := range list {
		m, _ := x.(M)
		out = append(out, m)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func queueRows(items []M) []M {
	rows := []M{}
	for _, q := range items {
		up, _ := q["uploading"].(bool)
		rows = append(rows, M{"name": get(q, "name", ""), "size": get(q, "size", 0), "uploading": up, "tries": get(q, "tries", 0)})
	}
	return rows
}

// cachedAbout runs `rclone about --json`, cached 10 min.
func cachedAbout(remote, path string, staleFallback bool) any {
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < 10*time.Minute {
		var v any
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &v) == nil {
			return v
		}
	}
	out, _, code, err := run(30*time.Second, "rclone", "about", remote, "--json")
	var data M
	if err != nil || code != 0 || json.Unmarshal([]byte(out), &data) != nil {
		if staleFallback {
			var old M
			if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &old) == nil {
				old["stale"] = true
				return old
			}
			return nil
		}
		data = M{}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.Marshal(data)
	_ = os.WriteFile(path, b, 0o644)
	return data
}
