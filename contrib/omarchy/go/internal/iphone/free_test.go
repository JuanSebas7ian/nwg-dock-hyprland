package iphone

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGroupKey(t *testing.T) {
	for rel, want := range map[string]string{
		"100APPLE/IMG_0362.HEIC":  "100APPLE/0362",
		"100APPLE/IMG_0362.MOV":   "100APPLE/0362",
		"100APPLE/IMG_E0362.HEIC": "100APPLE/0362",
		"101APPLE/IMG_0362.HEIC":  "101APPLE/0362",
		"100APPLE/FullSizeR.jpg":  "100APPLE/FullSizeR.jpg",
	} {
		if got := GroupKey(rel); got != want {
			t.Errorf("%s: %s, want %s", rel, got, want)
		}
	}
}

// a copy with the right size and one block from elsewhere (what ifuse did) is found, fixed and re-uploaded
func TestVerifyCopiesRepairs(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	put := func(p, body string) {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(body), 0o644)
	}
	put(filepath.Join(root, "DCIM/100APPLE/IMG_1.HEIC"), "good-one")
	put(filepath.Join(root, "DCIM/100APPLE/IMG_2.HEIC"), "good-two")
	put(filepath.Join(dir, "2024/IMG_1.HEIC"), "good-one")
	put(filepath.Join(dir, "2024/IMG_2.HEIC"), "good-owt") // same size, other content
	files := []MediaFile{{Rel: "100APPLE/IMG_1.HEIC", Size: 8}, {Rel: "100APPLE/IMG_2.HEIC", Size: 8}}
	locals := []string{filepath.Join(dir, "2024/IMG_1.HEIC"), filepath.Join(dir, "2024/IMG_2.HEIC")}
	l := Ledger{}
	r, drop := VerifyCopies(root, l, files, locals, nil)
	if r.Verified != 2 || len(r.Repaired) != 1 || r.Repaired[0] != "100APPLE/IMG_2.HEIC" || len(drop) != 1 || drop[0] != locals[1] {
		t.Fatalf("%+v %v", r, drop)
	}
	if b, _ := os.ReadFile(locals[1]); string(b) != "good-two" {
		t.Fatalf("not repaired: %s", b)
	}
	if l["100APPLE/IMG_1.HEIC"].Hash == "" || l["100APPLE/IMG_2.HEIC"].Hash == "" {
		t.Fatalf("%+v", l)
	}
	// a second check trusts the recorded hash while neither file changes
	_ = os.Remove(filepath.Join(root, "DCIM/100APPLE/IMG_1.HEIC"))
	if r, _ := VerifyCopies(root, l, files[:1], locals[:1], nil); r.Verified != 1 {
		t.Fatalf("%+v", r)
	}
	// the copy changed afterwards: it is checked again (and the device file is gone: not verified)
	put(locals[0], "changed!")
	if r, _ := VerifyCopies(root, l, files[:1], locals[:1], nil); r.Verified != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestSafeGroupsNeedEveryFile(t *testing.T) {
	up := map[string]bool{"/p/a.heic": true, "/p/a.mov": true, "/p/b.heic": true, "/p/v.mov": true}
	l := Ledger{
		"100APPLE/IMG_0001.HEIC": {Size: 10, Local: "/p/a.heic", Hash: "x", Taken: "2024-01-02T10:00:00Z"},
		"100APPLE/IMG_0001.MOV":  {Size: 20, Local: "/p/a.mov", Hash: "y", Taken: "2024-01-02T10:00:00Z"},
		"100APPLE/IMG_0002.HEIC": {Size: 10, Local: "/p/b.heic", Hash: "z"},
		"100APPLE/IMG_0002.MOV":  {Size: 20, Local: "/p/b.mov", Hash: "w"}, // not uploaded: the photo stays
		"100APPLE/IMG_0003.MOV":  {Size: 30, Local: "/p/v.mov", Hash: "v", Taken: "2023-05-01T10:00:00Z"},
		"100APPLE/IMG_0004.HEIC": {Size: 10, Local: "/p/a.heic"}, // never verified
	}
	files := []MediaFile{
		{Rel: "100APPLE/IMG_0001.HEIC", Size: 10}, {Rel: "100APPLE/IMG_0001.MOV", Size: 20},
		{Rel: "100APPLE/IMG_0002.HEIC", Size: 10}, {Rel: "100APPLE/IMG_0002.MOV", Size: 20},
		{Rel: "100APPLE/IMG_0003.MOV", Size: 30}, {Rel: "100APPLE/IMG_0004.HEIC", Size: 10},
	}
	g := SafeGroups(files, l, up)
	if len(g) != 2 || g[0].Key != "100APPLE/0003" || !g[0].Video || g[1].Key != "100APPLE/0001" || g[1].Video ||
		g[1].Bytes != 30 || len(g[1].Files) != 2 || g[1].Local != "/p/a.heic" || g[1].Name != "IMG_0001" {
		t.Fatalf("%+v", g)
	}
}

func TestPTPPortAndPlan(t *testing.T) {
	sys := t.TempDir()
	d := filepath.Join(sys, "1-2")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "serial"), []byte("00008110000435963EFA401E\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "busnum"), []byte("1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "devnum"), []byte("12\n"), 0o644)
	if p, err := PTPPort(sys, "00008110-000435963EFA401E"); err != nil || p != "usb:001,012" {
		t.Fatal(p, err)
	}
	out := `There is no file in folder '/'.
There are 3 files in folder '/store_00010001/202306_a'.
#1     IMG_0012.AAE               rd     2 KB application/x-unknown 1687295501
#2     IMG_0012.JPG               rd  5215 KB 3024x4032 image/jpeg 1687295501
#3     IMG_0013.MOV               rd  3000 KB video/quicktime 1687295501
There are 2 files in folder '/store_00010001/202307_a'.
#1     IMG_0013.JPG               rd  2000 KB image/jpeg 1687295501
#2     IMG_0014.HEIC              rd  2000 KB image/heif 1687295501
There are 1 files in folder '/store_00010001/202405_a'.
#1     IMG_0014.HEIC              rd  2000 KB image/heif 1687295501
`
	list := ParsePTPList(out)
	if len(list["/store_00010001/202306_a"]) != 3 {
		t.Fatalf("%v", list)
	}
	rels := []string{"100APPLE/IMG_0012.HEIC", "100APPLE/IMG_0013.HEIC", "100APPLE/IMG_0013.MOV", "100APPLE/IMG_0014.HEIC", "100APPLE/IMG_0099.HEIC"}
	month := map[string]string{"100APPLE/IMG_0012.HEIC": "202306", "100APPLE/IMG_0014.HEIC": "202405"}
	plan, owner, missing := PTPPlan(list, rels, month)
	if strings.Join(plan["/store_00010001/202306_a"], ",") != "IMG_0012.JPG,IMG_0013.MOV,IMG_0012.AAE" ||
		strings.Join(plan["/store_00010001/202307_a"], ",") != "IMG_0013.JPG" ||
		strings.Join(plan["/store_00010001/202405_a"], ",") != "IMG_0014.HEIC" || len(plan["/store_00010001/202307_a"]) != 1 {
		t.Fatalf("%v", plan)
	}
	if strings.Join(owner["/store_00010001/202306_a/IMG_0012.JPG"], ",") != "100APPLE/IMG_0012.HEIC" || len(missing) != 1 || missing[0] != "100APPLE/IMG_0099.HEIC" {
		t.Fatalf("%v %v", owner, missing)
	}
	// the same name in two folders and no capture month: left alone
	if _, _, missing := PTPPlan(list, []string{"100APPLE/IMG_0014.HEIC"}, nil); len(missing) != 1 {
		t.Fatal(missing)
	}
}

func TestFreeDeletesOnlyWhatIsStillSafe(t *testing.T) {
	p := testPaths(t)
	f := &fake{out: map[string]string{"ifuse": "", "fusermount3": ""}, fail: map[string]bool{}}
	f.hook = cameraRoll(map[string]string{"100APPLE/IMG_0001.HEIC": "one", "100APPLE/IMG_0002.HEIC": "two", "100APPLE/IMG_E0002.HEIC": "two-edit"})
	gl := filepath.Join(p.gphotosState(), "uploaded.jsonl")
	_ = os.MkdirAll(filepath.Dir(gl), 0o755)
	_ = os.WriteFile(gl, []byte(`{"path":"/p/1"}`+"\n"+`{"path":"/p/2"}`+"\n"), 0o644)
	_ = writeJSON(p.ledgerPath(udid), Ledger{
		"100APPLE/IMG_0001.HEIC": {Size: 3, Local: "/p/1", Hash: "h1"},
		"100APPLE/IMG_0002.HEIC": {Size: 3, Local: "/p/2", Hash: "h2"},
	})
	_ = writeJSON(filepath.Join(p.State, udid, "safe.json"), SafeReport{UDID: udid, Groups: []SafeGroup{
		{Key: "100APPLE/0001", Name: "IMG_0001", Files: []string{"100APPLE/IMG_0001.HEIC"}, Bytes: 3},
		{Key: "100APPLE/0002", Name: "IMG_0002", Files: []string{"100APPLE/IMG_0002.HEIC"}, Bytes: 3}, // edited since
	}})
	origPort, origRun := ptpPortFn, ptpRun
	defer func() { ptpPortFn, ptpRun = origPort, origRun }()
	ptpPortFn = func(string) (string, error) { return "usb:001,002", nil }
	left := []string{"IMG_0001.HEIC", "IMG_0002.HEIC", "IMG_E0002.HEIC"}
	var scripts []string
	ptpRun = func(port, script string, args ...string) ([]byte, error) {
		if script != "" {
			scripts = append(scripts, script)
			for _, line := range strings.Split(script, "\n") {
				if n, ok := strings.CutPrefix(line, "delete "); ok {
					var keep []string
					for _, x := range left {
						if x != n {
							keep = append(keep, x)
						}
					}
					left = keep
				}
			}
			return nil, nil
		}
		var b strings.Builder
		b.WriteString("There are 3 files in folder '/store_00010001/202412_a'.\n")
		for i, n := range left {
			b.WriteString("#" + string(rune('1'+i)) + "     " + n + "   rd 1 KB image/heif\n")
		}
		return []byte(b.String()), nil
	}
	res, err := Free(f.run, p, udid, FreeRequest{All: true}, nil)
	if err != nil || res.Deleted != 1 || res.Bytes != 3 || res.Groups != 1 || len(res.Failed) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(scripts) != 1 || !strings.Contains(scripts[0], "cd /store_00010001/202412_a\ndelete IMG_0001.HEIC\n") {
		t.Fatal(scripts)
	}
	if g := SafeGroupsOf(p, udid); len(g) != 1 || g[0].Key != "100APPLE/0002" {
		t.Fatalf("%+v", g)
	}
	// locked device: PTP lists nothing
	ptpRun = func(string, string, ...string) ([]byte, error) {
		return []byte("There is no file in folder '/store_feedface'.\n"), nil
	}
	if _, err := Free(f.run, p, udid, FreeRequest{Keys: []string{"100APPLE/0002"}}, nil); err == nil {
		t.Fatal("deleted an edited photo")
	}
}

func TestPTPReady(t *testing.T) {
	origPort, origRun := ptpPortFn, ptpRun
	defer func() { ptpPortFn, ptpRun = origPort, origRun }()
	ptpPortFn = func(string) (string, error) { return "usb:001,002", nil }
	answer := "[Storage 0]\nbasedir=/store_feedface\naccess=1 Read-Only\n"
	ptpRun = func(string, string, ...string) ([]byte, error) { return []byte(answer), nil }
	if ok, why := PTPReady("x"); ok || !strings.Contains(why, "unplug") {
		t.Fatal(ok, why)
	}
	answer = "[Storage 0]\nbasedir=/store_00010001\naccess=0 Read-Write\n"
	if ok, _ := PTPReady("x"); !ok {
		t.Fatal("ready device not seen")
	}
}

func TestSafeRuns(t *testing.T) {
	at := func(d int) string { return time.Date(2024, 1, d, 10, 0, 0, 0, time.UTC).Format(time.RFC3339) }
	l := Ledger{}
	var files []MediaFile
	for d := 1; d <= 7; d++ {
		rel := fmt.Sprintf("100APPLE/IMG_%04d.HEIC", d)
		l[rel] = LedgerEntry{Taken: at(d)}
		files = append(files, MediaFile{Rel: rel, Size: int64(d)})
	}
	files = append(files, MediaFile{Rel: "100APPLE/IMG_0002.MOV", Size: 100}) // the Live Photo part counts with its photo
	l["100APPLE/IMG_0002.MOV"] = LedgerEntry{Taken: at(2)}
	safe := map[string]bool{"100APPLE/0001": true, "100APPLE/0002": true, "100APPLE/0003": true, "100APPLE/0005": true, "100APPLE/0006": true}
	r := SafeRuns(files, l, safe, 10)
	if len(r) != 2 || r[0].FirstName != "IMG_0001" || r[0].LastName != "IMG_0003" || r[0].N != 3 || r[0].Bytes != 106 ||
		r[1].FirstName != "IMG_0005" || r[1].N != 2 || r[0].From != "2024-01-01 10:00" {
		t.Fatalf("%+v", r)
	}
}

func TestStorageReport(t *testing.T) {
	dir := t.TempDir()
	put := func(id string, n int) {
		p := filepath.Join(dir, id[:2], id)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, make([]byte, n), 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, "Manifest.db"), []byte("db"), 0o644)
	put("aa01", 100) // camera roll
	put("bb01", 500) // a WhatsApp video
	put("bb02", 300) // a WhatsApp document in a group
	put("bb03", 10)  // ChatStorage.sqlite
	put("cc01", 50)  // an app
	run := func(name string, args ...string) ([]byte, error) {
		q := args[len(args)-1]
		switch {
		case strings.Contains(q, "FROM Files"):
			return []byte(`[{"fileID":"aa01","domain":"CameraRollDomain","relativePath":"Media/DCIM/100APPLE/IMG_1.HEIC"},
{"fileID":"bb01","domain":"` + waDomain + `","relativePath":"Message/Media/5731@s.whatsapp.net/a/b/x.mp4"},
{"fileID":"bb02","domain":"` + waDomain + `","relativePath":"Message/Media/120@g.us/c/d/y.pdf"},
{"fileID":"bb03","domain":"` + waDomain + `","relativePath":"ChatStorage.sqlite"},
{"fileID":"cc01","domain":"AppDomain-com.spotify.client","relativePath":"Documents/z"},
{"fileID":"dd01","domain":"HomeDomain","relativePath":"missing blob"}]`), nil
		case strings.Contains(q, "ZWACHATSESSION"):
			return []byte(`[{"j":"5731@s.whatsapp.net","n":"Ana"},{"j":"120@g.us","n":"Familia"}]`), nil
		case strings.Contains(q, "ZWAMEDIAITEM"):
			return []byte(`[{"p":"Media/5731@s.whatsapp.net/a/b/x.mp4","d":"2025-06-18"}]`), nil
		}
		return nil, errors.New("unexpected " + q)
	}
	r, err := MakeStorageReport(run, dir, filepath.Join(t.TempDir(), "tmp"), map[string]string{"com.spotify.client": "Spotify"})
	if err != nil || r.Total != 960 || r.WhatsApp != 810 || r.Areas[0].Name != "WhatsApp" || r.Areas[2].Name != "Spotify" {
		t.Fatalf("%+v %v", r, err)
	}
	if len(r.Chats) != 2 || r.Chats[0].Name != "Ana" || r.Chats[1].Name != "Familia" || !r.Chats[1].Group ||
		r.Kinds["video"] != 500 || r.Kinds["document"] != 300 {
		t.Fatalf("%+v", r)
	}
	if len(r.Heavy) != 2 || r.Heavy[0].Chat != "Ana" || r.Heavy[0].Date != "2025-06-18" || r.Heavy[0].Kind != "video" || r.Heavy[1].Ext != "pdf" {
		t.Fatalf("%+v", r.Heavy)
	}
	if _, err := MakeStorageReport(run, t.TempDir(), t.TempDir(), nil); err == nil {
		t.Fatal("no backup: no error")
	}
}
