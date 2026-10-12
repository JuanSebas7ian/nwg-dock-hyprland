package cloud

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const TrustDays = 30

var AuthError = regexp.MustCompile(`(?i)401|421|trust.?token|reauth|authenticat|2fa|session|PCS cookie|requestPCS|unauthori`)

// ICloud is the rclone-icloud mount at ~/iCloud (and the read-only Photos mount).
type ICloud struct {
	Remote, Service, PhotosService, Mount, PhotosMount, Sock, State, Cache string
}

func DefaultICloud() ICloud {
	return ICloud{env("ICLOUD_REMOTE", "icloud:"), env("ICLOUD_SERVICE", "rclone-icloud.service"),
		env("ICLOUD_PHOTOS_SERVICE", "rclone-icloud-photos.service"), env("ICLOUD_MOUNT", filepath.Join(home(), "iCloud")),
		env("ICLOUD_PHOTOS_MOUNT", filepath.Join(home(), "iCloudPhotos")), env("ICLOUD_SOCK", filepath.Join(runtimeDir(), "rclone-icloud.sock")),
		filepath.Join(stateHome(), "omarchy-icloud"), filepath.Join(cacheHome(), "omarchy-icloud-widget")}
}

// Configured: the remote is in rclone's config (no network).
func (c ICloud) Configured() bool {
	out, _, _, err := run(10*time.Second, "rclone", "listremotes")
	if err != nil {
		return false
	}
	for _, f := range strings.Fields(out) {
		if f == c.Remote {
			return true
		}
	}
	return false
}

// AuthInfo: days left on Apple's 30-day trust token.
func (c ICloud) AuthInfo() M {
	b, err := os.ReadFile(filepath.Join(c.State, "authenticated"))
	issued, perr := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil || perr != nil {
		return M{"issued": nil, "daysLeft": nil}
	}
	left := TrustDays - (float64(time.Now().UnixNano())/1e9-issued)/86400
	return M{"issued": int64(issued), "daysLeft": math.Round(left*10) / 10}
}

func (c ICloud) journalAuthError() string {
	out, _, _, err := run(10*time.Second, "journalctl", "--user", "-u", c.Service, "--since", "-1h", "-o", "cat", "-n", "40", "--no-pager")
	if err != nil {
		return ""
	}
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if AuthError.MatchString(l) && (strings.Contains(l, "ERROR") || strings.Contains(l, "CRITICAL") || strings.Contains(l, "Failed")) {
			l = strings.TrimSpace(l)
			if len(l) > 240 {
				l = l[len(l)-240:]
			}
			return l
		}
	}
	return ""
}

func (c ICloud) quota() any {
	v := cachedAbout(c.Remote, filepath.Join(c.Cache, "quota.json"), false)
	if m, ok := v.(M); !ok || len(m) == 0 {
		return nil
	}
	return v
}

func (c ICloud) cacheDir() string {
	vfs, err := RC(c.Sock, "vfs/stats", nil)
	if err != nil {
		return ""
	}
	dc, _ := vfs["diskCache"].(M)
	p, _ := dc["path"].(string)
	return p
}

// LocalState: cloud / partial / local, from the sparse file in rclone's VFS cache.
func LocalState(cacheDir, rel string, size int64) string {
	if cacheDir == "" {
		return "cloud"
	}
	var st syscall.Stat_t
	if syscall.Stat(filepath.Join(cacheDir, rel), &st) != nil {
		return "cloud"
	}
	if size == 0 || st.Blocks*512 >= size {
		return "local"
	}
	if st.Blocks > 0 {
		return "partial"
	}
	return "cloud"
}

func (c ICloud) Status() M {
	res := M{"configured": c.Configured(), "service": ServiceState(c.Service), "mounted": IsMount(c.Mount), "mountpoint": c.Mount,
		"photos": M{"service": ServiceState(c.PhotosService), "mounted": IsMount(c.PhotosMount), "mountpoint": c.PhotosMount},
		"auth":   c.AuthInfo(), "authError": "", "rc": false, "cache": M{}, "transfers": []M{}, "queue": []M{},
		"speed": 0, "errors": 0, "lastError": "", "quota": nil, "recent": []M{}}
	if !res["configured"].(bool) {
		return res
	}
	if exists(c.Sock) {
		if vfs, err := rcStats(c.Sock, res, true); err != nil {
			res["rcError"] = err.Error()
		} else {
			q, _ := queue(c.Sock, 20)
			res["queue"] = queueRows(q)
			dc, _ := vfs["diskCache"].(M)
			p, _ := dc["path"].(string)
			res["recent"] = Recent(p, 10)
		}
	}
	last := fmt.Sprint(res["lastError"])
	if res["service"] == "failed" || AuthError.MatchString(last) {
		if AuthError.MatchString(last) {
			res["authError"] = last
		} else {
			res["authError"] = c.journalAuthError()
		}
	}
	if res["mounted"].(bool) {
		res["quota"] = c.quota()
	}
	return res
}

var errOutside = errors.New("path outside ~/iCloud")

// SafeRel confines a path to the mount (symlinks included).
func (c ICloud) SafeRel(rel string) (string, string, error) {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return "", c.Mount, nil
	}
	full := filepath.Clean(filepath.Join(c.Mount, rel))
	inside := func(p, root string) bool { return p == root || strings.HasPrefix(p, root+"/") }
	if !inside(full, c.Mount) {
		return "", "", errOutside
	}
	if real, err := filepath.EvalSymlinks(full); err == nil {
		root, rerr := filepath.EvalSymlinks(c.Mount)
		if rerr != nil {
			root = c.Mount
		}
		if !inside(real, root) {
			return "", "", errOutside
		}
	}
	return rel, full, nil
}

func (c ICloud) Ls(rel string) M {
	rel, full, err := c.SafeRel(rel)
	if err != nil {
		return M{"ok": false, "error": err.Error()}
	}
	if !IsMount(c.Mount) {
		return M{"ok": false, "error": "not mounted", "dir": rel, "entries": []M{}}
	}
	qs := map[string]M{}
	if q, err := queue(c.Sock, 0); err == nil {
		for _, x := range q {
			qs[fmt.Sprint(get(x, "name", ""))] = x
		}
	}
	cache := c.cacheDir()
	ents, err := os.ReadDir(full)
	if err != nil {
		msg := err.Error()
		if pe, ok := err.(*os.PathError); ok {
			msg = pe.Err.Error()
			msg = strings.ToUpper(msg[:1]) + msg[1:]
		}
		return M{"ok": false, "error": msg, "dir": rel, "entries": []M{}}
	}
	type entry struct {
		dir  bool
		name string
		m    M
	}
	var list []entry
	for _, de := range ents {
		st, err := os.Stat(filepath.Join(full, de.Name()))
		if err != nil {
			continue
		}
		path := de.Name()
		if rel != "" {
			path = rel + "/" + de.Name()
		}
		if st.IsDir() {
			list = append(list, entry{true, de.Name(), M{"name": de.Name(), "path": path, "dir": true, "size": 0, "mtime": st.ModTime().Unix(), "state": "folder"}})
			continue
		}
		state := LocalState(cache, path, st.Size())
		if q, ok := qs[path]; ok {
			state = "queued"
			if up, _ := q["uploading"].(bool); up {
				state = "uploading"
			}
		}
		list = append(list, entry{false, de.Name(), M{"name": de.Name(), "path": path, "dir": false, "size": st.Size(), "mtime": st.ModTime().Unix(), "state": state}})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].dir != list[j].dir {
			return list[i].dir
		}
		return strings.ToLower(list[i].name) < strings.ToLower(list[j].name)
	})
	entries, counts := []M{}, M{}
	for _, e := range list {
		entries = append(entries, e.m)
		s := e.m["state"].(string)
		n, _ := counts[s].(int)
		counts[s] = n + 1
	}
	return M{"ok": true, "dir": rel, "entries": entries, "counts": counts}
}

// Keep reads the file(s) once in the background so rclone caches them.
func (c ICloud) Keep(rel string) error {
	rel, full, err := c.SafeRel(rel)
	if err != nil {
		return err
	}
	if rel == "" {
		return errors.New("pick a file or folder, not all of iCloud")
	}
	if !IsMount(c.Mount) {
		return errors.New("not mounted")
	}
	if !exists(full) {
		return errors.New("no such file")
	}
	cmd := exec.Command("find", full, "-type", "f", "-exec", "cat", "{}", "+")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (c ICloud) Refresh() error {
	_, err := RC(c.Sock, "vfs/refresh", M{"recursive": "false"})
	return err
}
