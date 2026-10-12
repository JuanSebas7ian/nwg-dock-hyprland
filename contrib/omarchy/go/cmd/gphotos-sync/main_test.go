package main

// End-to-end without Google: "gphotos:" and the Drive remote are rclone alias
// remotes (environment variables) pointing at temporary folders.

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var bin string

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("rclone"); err != nil {
		os.Exit(0) // these tests need rclone
	}
	dir, _ := os.MkdirTemp("", "gphotos-bin")
	bin = filepath.Join(dir, "gphotos-sync")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type box struct {
	t                            *testing.T
	root, photos, drive, gf, cam string
	pics                         string
	env                          []string
}

func newBox(t *testing.T) *box {
	r := t.TempDir()
	b := &box{t: t, root: r, photos: filepath.Join(r, "photos"), drive: filepath.Join(r, "drive"), gf: filepath.Join(r, "GoogleFotos"),
		cam: filepath.Join(r, "cam"), pics: filepath.Join(r, "Pictures")}
	for _, d := range []string{b.photos, b.drive, b.gf, b.cam, b.pics} {
		_ = os.MkdirAll(d, 0o755)
	}
	b.env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(r, "config"), "XDG_STATE_HOME="+filepath.Join(r, "state"),
		"XDG_CACHE_HOME="+filepath.Join(r, "cache"), "RCLONE_CONFIG="+filepath.Join(r, "rclone.conf"),
		"RCLONE_CONFIG_GPFAKE_TYPE=alias", "RCLONE_CONFIG_GPFAKE_REMOTE="+b.photos,
		"RCLONE_CONFIG_DRFAKE_TYPE=alias", "RCLONE_CONFIG_DRFAKE_REMOTE="+b.drive)
	cfg := map[string]any{"remote": "gpfake:", "paused": false, "bwlimit": "off", "transfers": 2, "drive_rescan_hours": 6,
		"sources": []any{
			map[string]any{"id": "googlefotos", "name": "GF", "path": b.gf, "album": "{top}", "root_album": "GoogleFotos"},
			map[string]any{"id": "screenshots", "name": "Shots", "path": b.pics, "recursive": false, "album": "Capturas", "root_album": "Capturas"},
			map[string]any{"id": "camera", "name": "Cam", "path": b.cam, "album": "Cámara {top}", "root_album": "Cámara"},
			map[string]any{"id": "drive", "name": "Drive", "remote": "drfake:", "album": "Drive · {top}", "root_album": "Drive", "exclude": []string{"Backups/"}}}}
	p := filepath.Join(r, "config", "gphotos-sync", "config.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	data, _ := json.Marshal(cfg)
	_ = os.WriteFile(p, data, 0o644)
	return b
}

func (b *box) put(p string, data ...byte) {
	if data == nil {
		data = []byte(strings.Repeat("x", 100))
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, data, 0o644)
}

func (b *box) sync(args ...string) string {
	cmd := exec.Command(bin, args...)
	cmd.Env = b.env
	out, err := cmd.CombinedOutput()
	if err != nil && args[0] != "source" {
		b.t.Fatalf("%v: %v %s", args, err, out)
	}
	return string(out)
}

func (b *box) status() map[string]any {
	cmd := exec.Command(bin, "status")
	cmd.Env = b.env
	out, _ := cmd.Output()
	var st map[string]any
	if err := json.Unmarshal(out, &st); err != nil {
		b.t.Fatalf("status: %v %s", err, out)
	}
	return st
}

func (b *box) album(name string) []string {
	ents, err := os.ReadDir(filepath.Join(b.photos, "album", name))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func TestSourcesLandInTheirAlbums(t *testing.T) {
	b := newBox(t)
	b.put(filepath.Join(b.gf, "suelta.jpg"))
	b.put(filepath.Join(b.gf, "Viaje", "a.jpg"))
	b.put(filepath.Join(b.pics, "screenshot-1.png"))
	b.put(filepath.Join(b.pics, "sub", "nested.png")) // screenshots are not recursive
	b.put(filepath.Join(b.cam, "2014", "b.mp4"))
	b.put(filepath.Join(b.drive, "Fotos", "x", "c.heic"))
	b.put(filepath.Join(b.drive, "notas.txt")) // not media
	b.sync("run")
	for album, want := range map[string][]string{"GoogleFotos": {"suelta.jpg"}, "Viaje": {"a.jpg"}, "Capturas": {"screenshot-1.png"}, "Cámara 2014": {"b.mp4"}, "Drive · Fotos": {"c.heic"}} {
		if got := b.album(album); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v", album, got)
		}
	}
	st := b.status()
	if st["state"] != "idle" || st["uploadedTotal"] != 5.0 || st["pendingTotal"] != 0.0 || len(st["recent"].([]any)) != 5 {
		t.Fatalf("%v", st)
	}
}

func TestNeverUploadsTwice(t *testing.T) {
	b := newBox(t)
	b.put(filepath.Join(b.gf, "a.jpg"))
	b.sync("run")
	_ = os.Remove(filepath.Join(b.photos, "album", "GoogleFotos", "a.jpg")) // Photos can't be asked: the ledger decides
	b.put(filepath.Join(b.gf, "b.jpg"))
	b.sync("run")
	if got := b.album("GoogleFotos"); !reflect.DeepEqual(got, []string{"b.jpg"}) {
		t.Fatal(got)
	}
}

func TestSameNameInDifferentFoldersOfOneAlbum(t *testing.T) {
	b := newBox(t)
	b.put(filepath.Join(b.drive, "Viajes", "2019", "IMG_1.jpg"), []byte(strings.Repeat("a", 10))...)
	b.put(filepath.Join(b.drive, "Viajes", "2020", "IMG_1.jpg"), []byte(strings.Repeat("b", 20))...)
	b.sync("run")
	for _, s := range b.status()["sources"].([]any) {
		m := s.(map[string]any)
		if m["id"] == "drive" && (m["uploaded"] != 2.0 || m["pending"] != 0.0) {
			t.Fatalf("%v", m)
		}
	}
}

func TestExcludedDriveFolder(t *testing.T) {
	b := newBox(t)
	b.put(filepath.Join(b.drive, "Backups", "x.jpg"))
	b.sync("run")
	if b.album("Drive · Backups") != nil {
		t.Fatal("excluded folder uploaded")
	}
}

func TestDisabledSourceAndPause(t *testing.T) {
	b := newBox(t)
	b.put(filepath.Join(b.cam, "2010", "a.jpg"))
	b.sync("source", "camera", "off")
	b.sync("run")
	if b.album("Cámara 2010") != nil {
		t.Fatal("disabled source uploaded")
	}
	b.sync("source", "camera", "on")
	p := filepath.Join(b.root, "config", "gphotos-sync", "config.json")
	var cfg map[string]any
	data, _ := os.ReadFile(p)
	_ = json.Unmarshal(data, &cfg)
	cfg["paused"] = true
	data, _ = json.Marshal(cfg)
	_ = os.WriteFile(p, data, 0o644)
	b.sync("run")
	if b.album("Cámara 2010") != nil || b.status()["state"] != "paused" {
		t.Fatal("paused but uploaded")
	}
}

func TestMissingRemoteAsksForSetup(t *testing.T) {
	b := newBox(t)
	var env []string
	for _, e := range b.env {
		if !strings.HasPrefix(e, "RCLONE_CONFIG_GPFAKE_TYPE=") {
			env = append(env, e)
		}
	}
	b.env = env
	b.put(filepath.Join(b.gf, "a.jpg"))
	b.sync("run")
	st := b.status()
	if st["state"] != "setup" || !strings.Contains(st["error"].(string), "gphotos-setup") {
		t.Fatalf("%v", st)
	}
}

func TestPlanListsAlbumsWithoutUploading(t *testing.T) {
	b := newBox(t)
	b.put(filepath.Join(b.cam, "2012", "a.jpg"))
	if out := b.sync("plan"); !strings.Contains(out, "Cámara 2012: 1") || b.album("Cámara 2012") != nil {
		t.Fatal(out)
	}
}

func TestThumbnailPrefersFreedesktopCache(t *testing.T) {
	b := newBox(t)
	img := filepath.Join(b.cam, "2011", "a b.jpg")
	b.put(img)
	sum := md5.Sum([]byte("file://" + strings.ReplaceAll(img, " ", "%20")))
	thumb := filepath.Join(b.root, "cache", "thumbnails", "large", hex.EncodeToString(sum[:])+".png")
	b.put(thumb)
	b.sync("run")
	if got := b.status()["recent"].([]any)[0].(map[string]any)["thumb"]; got != thumb {
		t.Fatalf("%v", got)
	}
}

func TestHelpers(t *testing.T) {
	if pyQuote("/home/u/a b/ñ.jpg") != "/home/u/a%20b/%C3%B1.jpg" {
		t.Fatal(pyQuote("/home/u/a b/ñ.jpg"))
	}
	if cleanAlbum(" Drive · a/b ") != "Drive · a-b" || cleanAlbum("  ") != "Omarchy" {
		t.Fatal("cleanAlbum")
	}
	if !acceptable("x.JPG", 10) || acceptable("x.jpg", 0) || acceptable("x.txt", 10) || acceptable("x.jpg", photoMax+1) || !acceptable("v.mkv", photoMax+1) {
		t.Fatal("acceptable")
	}
}
