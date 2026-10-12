package cloud

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func box(t *testing.T) ICloud {
	r := t.TempDir()
	_ = os.MkdirAll(filepath.Join(r, "mnt"), 0o755)
	return ICloud{Remote: "icloud:", Service: "none.service", PhotosService: "none2.service", Mount: filepath.Join(r, "mnt"),
		PhotosMount: filepath.Join(r, "photos"), Sock: filepath.Join(r, "none.sock"), State: filepath.Join(r, "state"), Cache: filepath.Join(r, "cache")}
}

func TestLocalStateFromSparseCache(t *testing.T) {
	cache := t.TempDir()
	_ = os.WriteFile(filepath.Join(cache, "full.txt"), []byte(strings.Repeat("x", 10000)), 0o644)
	f, _ := os.Create(filepath.Join(cache, "sparse.bin")) // 1 MB with only its first 4 KB present
	f.Write([]byte(strings.Repeat("y", 4096)))
	f.Truncate(1 << 20)
	f.Close()
	f, _ = os.Create(filepath.Join(cache, "hole.bin")) // nothing downloaded yet
	f.Truncate(1 << 20)
	f.Close()
	for _, c := range []struct {
		dir, rel string
		size     int64
		want     string
	}{{cache, "full.txt", 10000, "local"}, {cache, "sparse.bin", 1 << 20, "partial"}, {cache, "hole.bin", 1 << 20, "cloud"},
		{cache, "missing.txt", 5, "cloud"}, {"", "full.txt", 5, "cloud"}} {
		if got := LocalState(c.dir, c.rel, c.size); got != c.want {
			t.Errorf("%s: %s", c.rel, got)
		}
	}
}

func TestPathsCannotLeaveTheMount(t *testing.T) {
	c := box(t)
	_ = os.Mkdir(filepath.Join(c.Mount, "Docs"), 0o755)
	if rel, _, _ := c.SafeRel("/Docs/"); rel != "Docs" {
		t.Fatal(rel)
	}
	if _, full, _ := c.SafeRel(""); full != c.Mount {
		t.Fatal(full)
	}
	_ = os.Symlink("/etc", filepath.Join(c.Mount, "esc"))
	for _, bad := range []string{"..", "Docs/../../etc", "../mnt2", "esc"} {
		if _, _, err := c.SafeRel(bad); err == nil {
			t.Errorf("%s allowed", bad)
		}
	}
}

func TestKeepRefusesEverythingAndUnmounted(t *testing.T) {
	c := box(t)
	if c.Keep("") == nil || c.Keep("Docs") == nil { // a plain folder, not a mount
		t.Fatal("keep accepted")
	}
}

func TestSignInCountdown(t *testing.T) {
	c := box(t)
	if a := c.AuthInfo(); a["issued"] != nil || a["daysLeft"] != nil {
		t.Fatal(a)
	}
	stamp := filepath.Join(c.State, "authenticated")
	_ = os.MkdirAll(c.State, 0o755)
	_ = os.WriteFile(stamp, []byte(strconv.FormatInt(time.Now().Unix()-27*86400, 10)), 0o644)
	if d := c.AuthInfo()["daysLeft"].(float64); d < 2.9 || d > 3.1 {
		t.Fatal(d)
	}
	_ = os.WriteFile(stamp, []byte(strconv.FormatInt(time.Now().Unix()-31*86400, 10)), 0o644)
	if d := c.AuthInfo()["daysLeft"].(float64); d >= 0 {
		t.Fatal(d)
	}
}

func TestAuthErrorsRecognised(t *testing.T) {
	for _, l := range []string{"HTTP error 421 (Misdirected Request)", "Missing PCS cookies from the request", "trust token expired", "authentication failed"} {
		if !AuthError.MatchString(l) {
			t.Error(l)
		}
	}
	if AuthError.MatchString("couldn't upload: disk full") {
		t.Fatal("false positive")
	}
}

func TestStatusWithoutRemoteSkipsNetwork(t *testing.T) {
	c := box(t)
	c.Remote = "surely-not-a-remote-xyz:"
	st := c.Status()
	if st["configured"] != false || st["mounted"] != false || st["quota"] != nil {
		t.Fatal(st)
	}
	if _, err := os.Stat(c.Cache); err == nil {
		t.Fatal("quota was queried")
	}
}

func TestLsUnmounted(t *testing.T) {
	if e := box(t).Ls("")["error"]; e != "not mounted" {
		t.Fatal(e)
	}
}

func TestIsMount(t *testing.T) {
	if !IsMount("/") || IsMount(t.TempDir()) || IsMount("/nonexistent-xyz") {
		t.Fatal("IsMount")
	}
}

func TestRecentNewestFirst(t *testing.T) {
	d := t.TempDir()
	for i, n := range []string{"a/old.txt", "b/new.txt", "mid.txt"} {
		p := filepath.Join(d, n)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("x"), 0o644)
		ts := time.Now().Add(time.Duration([]int{-3, 0, -1}[i]) * time.Hour)
		_ = os.Chtimes(p, ts, ts)
	}
	r := Recent(d, 2)
	if len(r) != 2 || r[0]["path"] != "b/new.txt" || r[0]["dir"] != "b" || r[1]["name"] != "mid.txt" || r[1]["dir"] != "" {
		t.Fatal(r)
	}
	if len(Recent("", 5)) != 0 {
		t.Fatal("empty dir")
	}
}

func TestScanDropboxSkipsLinksAndKeepsNewest(t *testing.T) {
	d := t.TempDir()
	for i := 0; i < 5; i++ {
		p := filepath.Join(d, "f", strconv.Itoa(i)+".txt")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(strings.Repeat("x", 10)), 0o644)
		ts := time.Unix(1_700_000_000+int64(i)*60, 0)
		_ = os.Chtimes(p, ts, ts)
	}
	_ = os.Symlink(filepath.Join(d, "f", "0.txt"), filepath.Join(d, "link.txt"))
	total, rows := ScanDropbox(d, 3)
	if total != 50 || len(rows) != 3 || rows[0]["name"] != "4.txt" || rows[2]["name"] != "2.txt" || rows[0]["folder"] != "f" {
		t.Fatal(total, rows)
	}
}
