package iphone

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const udid = "00008110-001A2B3C4D5E801E"

func plistXML(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>` + body + `</dict></plist>`)
}

// fake answers commands by their joined command line; unknown commands fail.
type fake struct {
	out   map[string]string
	fail  map[string]bool
	calls []string
	hook  func(cmd string, args []string)
}

func (f *fake) run(name string, args ...string) ([]byte, error) {
	cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, cmd)
	if f.hook != nil {
		f.hook(name, args)
	}
	for k, v := range f.out {
		if cmd == k || strings.HasPrefix(cmd, k+" ") {
			if f.fail[k] {
				return []byte(v), errors.New("exit 1")
			}
			return []byte(v), nil
		}
	}
	return nil, errors.New("not found: " + cmd)
}

func trustedFake() *fake {
	return &fake{out: map[string]string{
		"idevice_id -l":                        udid + "\n",
		"idevice_id -n":                        udid + " (Network)\n",
		"idevicepair -u " + udid + " validate": "SUCCESS: Validated pairing with device " + udid,
		"ideviceinfo -u " + udid + " -x": string(plistXML(`<key>DeviceName</key><string>iPhone de Juan</string>
			<key>ProductType</key><string>iPhone15,4</string><key>ProductVersion</key><string>26.1</string><key>BuildVersion</key><string>23B85</string>`)),
		"ideviceinfo -u " + udid + " -q com.apple.mobile.battery -x": string(plistXML(`<key>BatteryCurrentCapacity</key><integer>87</integer>
			<key>BatteryIsCharging</key><true/><key>FullyCharged</key><false/>`)),
		"ideviceinfo -u " + udid + " -q com.apple.disk_usage -x": string(plistXML(`<key>TotalDiskCapacity</key><integer>128000000000</integer>
			<key>AmountDataAvailable</key><integer>32000000000</integer>`)),
		"ideviceinfo -u " + udid + " -q com.apple.mobile.backup -k WillEncrypt": "true\n",
	}, fail: map[string]bool{}}
}

func testPaths(t *testing.T) Paths {
	d := t.TempDir()
	return Paths{Config: filepath.Join(d, "config", "config.json"), State: filepath.Join(d, "state"), Runtime: filepath.Join(d, "run"), Home: filepath.Join(d, "home")}
}

func TestParsePlist(t *testing.T) {
	m, err := PlistDict(plistXML(`<key>A</key><string>x &amp; y</string><key>N</key><integer>-5</integer><key>R</key><real>1.5</real>
		<key>T</key><true/><key>F</key><false/><key>D</key><data>aGk=</data><key>W</key><date>2026-10-12T10:00:00Z</date>
		<key>L</key><array><string>a</string><dict><key>k</key><integer>1</integer></dict></array>`))
	if err != nil {
		t.Fatal(err)
	}
	if str(m, "A") != "x & y" || num(m, "N") != -5 || m["R"] != 1.5 || !boolean(m, "T") || boolean(m, "F") || string(m["D"].([]byte)) != "hi" {
		t.Fatalf("%v", m)
	}
	if l := m["L"].([]any); len(l) != 2 || l[1].(map[string]any)["k"] != int64(1) {
		t.Fatalf("%v", m["L"])
	}
	if _, err := PlistDict([]byte("ERROR: no device")); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestListUDIDsDeduplicatesUSBAndNetwork(t *testing.T) {
	f := trustedFake()
	f.out["idevice_id -n"] = udid + " (Network)\nAAAABBBBCCCCDDDDEEEEFFFF0000111122223333 (Network)\n"
	ds := ListUDIDs(f.run)
	if len(ds) != 2 || ds[0].Conn != "usb" || ds[1].Conn != "network" {
		t.Fatalf("%+v", ds)
	}
}

func TestDescribeTrusted(t *testing.T) {
	d := Describe(trustedFake().run, Device{UDID: udid, Conn: "usb"})
	if !d.Trusted || d.Name != "iPhone de Juan" || d.Model != "iPhone 15" || d.IOS != "26.1" || d.Battery.Level != 87 || !d.Battery.Charging ||
		d.Storage.Free != 32e9 || d.BackupEncrypted == nil || !*d.BackupEncrypted {
		t.Fatalf("%+v", d)
	}
}

func TestDescribeUntrustedGivesAHint(t *testing.T) {
	f := trustedFake()
	f.out["idevicepair -u "+udid+" validate"] = "ERROR: Please accept the trust dialog on the screen of device " + udid
	f.fail["idevicepair -u "+udid+" validate"] = true
	d := Describe(f.run, Device{UDID: udid, Conn: "usb"})
	if d.Trusted || !strings.Contains(d.Error, "Trust") || d.Name != "" {
		t.Fatalf("%+v", d)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "ideviceinfo") {
			t.Fatal("asked an untrusted device for details")
		}
	}
	if !strings.Contains(pairingHint("ERROR: Device is locked, PasswordProtected"), "Unlock") {
		t.Fatal("locked hint")
	}
}

func TestPair(t *testing.T) {
	f := trustedFake()
	f.out["idevicepair -u "+udid+" pair"] = "SUCCESS: Paired with device " + udid
	if _, err := Pair(f.run, udid); err != nil {
		t.Fatal(err)
	}
	f.out["idevicepair -u "+udid+" pair"] = "ERROR: Please accept the trust dialog on the screen of device"
	f.fail["idevicepair -u "+udid+" pair"] = true
	if _, err := Pair(f.run, udid); err == nil || !strings.Contains(err.Error(), "Trust") {
		t.Fatal(err)
	}
}

func TestModelNameFallsBack(t *testing.T) {
	if ModelName("iPhone17,1") != "iPhone 16 Pro" || ModelName("iPhone99,9") != "iPhone99,9" {
		t.Fatal("model names")
	}
}

func TestParseKDEConnect(t *testing.T) {
	ds := ParseKDEConnect("- MacBook: 1234abcd5678 (paired)\n- iPhone de Juan: a1b2c3d4e5f6a7b8 (paired and reachable)\n- Pixel: ffffeeee1111 (reachable)\n2 devices found\n")
	if len(ds) != 3 || ds[0].Name != "iPhone de Juan" || !ds[0].Paired || !ds[0].Reachable {
		t.Fatalf("%+v", ds)
	}
	for _, d := range ds {
		if d.Name == "Pixel" && (d.Paired || !d.Reachable) || d.Name == "MacBook" && (!d.Paired || d.Reachable) {
			t.Fatalf("%+v", d)
		}
	}
}

func TestKDEActionValidates(t *testing.T) {
	f := &fake{out: map[string]string{"kdeconnect-cli": "", "wl-paste --no-newline": "hola"}, fail: map[string]bool{}}
	if err := KDEAction(f.run, "ring", "a1b2c3d4e5f6a7b8"); err != nil {
		t.Fatal(err)
	}
	if err := KDEAction(f.run, "clipboard", "a1b2c3d4e5f6a7b8"); err != nil || !strings.Contains(f.calls[len(f.calls)-1], "--share-text hola") {
		t.Fatal(err, f.calls)
	}
	if KDEAction(f.run, "ring", "x; rm -rf /") == nil || KDEAction(f.run, "explode", "a1b2c3d4e5f6a7b8") == nil {
		t.Fatal("bad input accepted")
	}
}

func TestParseApps(t *testing.T) {
	apps := ParseApps("\"CFBundleIdentifier\",\"CFBundleVersion\",\"CFBundleDisplayName\"\n\"org.videolan.vlc-ios\",\"3.6.4\",\"VLC\"\n\"com.readdle.ReaddleDocsIPad\",\"8.9\",\"Documents\"\n")
	if len(apps) != 2 || apps[0]["name"] != "Documents" || apps[1]["id"] != "org.videolan.vlc-ios" {
		t.Fatalf("%v", apps)
	}
}

func TestDestination(t *testing.T) {
	dir := t.TempDir()
	mod := time.Date(2025, 7, 3, 10, 0, 0, 0, time.Local)
	f := MediaFile{Rel: "100APPLE/IMG_0001.HEIC", Size: 4, Mod: mod}
	p, already := Destination(dir, f)
	if already || p != filepath.Join(dir, "2025", "IMG_0001.HEIC") {
		t.Fatal(p)
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("same"), 0o644)
	if _, already := Destination(dir, f); !already {
		t.Fatal("same size should count as the same photo")
	}
	f.Size = 9
	if p, already := Destination(dir, f); already || filepath.Base(p) != "IMG_0001-1.HEIC" {
		t.Fatal(p)
	}
	if p, _ := Destination(dir, MediaFile{Rel: "x/IMG_2.JPG", Size: 1}); !strings.Contains(p, "sin-fecha") {
		t.Fatal(p)
	}
}

// the fake ifuse "mounts" a camera roll by writing it into the mount point
func cameraRoll(files map[string]string) func(string, []string) {
	return func(name string, args []string) {
		if name != "ifuse" || len(args) == 0 || strings.HasPrefix(args[0], "--") {
			return
		}
		for rel, body := range files {
			p := filepath.Join(args[0], "DCIM", rel)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if _, err := os.Stat(p); err == nil && body == "" {
				continue
			}
			_ = os.WriteFile(p, []byte(body), 0o644)
			mod := time.Date(2024, 12, 24, 20, 0, 0, 0, time.Local)
			if strings.Contains(rel, "2026") {
				mod = time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
			}
			_ = os.Chtimes(p, mod, mod)
		}
	}
}

func TestImportIsIncrementalAndResumes(t *testing.T) {
	p := testPaths(t)
	roll := map[string]string{"100APPLE/IMG_0001.HEIC": "one", "100APPLE/IMG_0002.MOV": "video", "100APPLE/IMG_0002.AAE": "edit",
		"101APPLE/IMG_2026.JPG": "new"}
	f := &fake{out: map[string]string{"ifuse": "", "fusermount3": ""}, fail: map[string]bool{}, hook: cameraRoll(roll)}
	var last [2]int
	res, err := Import(f.run, p, udid, func(done, total int, _ string) { last = [2]int{done, total} })
	if err != nil || res.Imported != 3 || res.Failed != 0 || last != [2]int{3, 3} {
		t.Fatalf("%+v %v %v", res, err, last)
	}
	dir := p.LoadConfig().PhotosDir
	for _, want := range []string{"2024/IMG_0001.HEIC", "2024/IMG_0002.MOV", "2026/IMG_2026.JPG"} {
		info, err := os.Stat(filepath.Join(dir, want))
		if err != nil {
			t.Fatal(want, err)
		}
		if want == "2024/IMG_0001.HEIC" && info.ModTime().Year() != 2024 {
			t.Fatal("capture time not kept")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "2024", "IMG_0002.AAE")); err == nil {
		t.Fatal("edit sidecars are not media")
	}
	res, _ = Import(f.run, p, udid, nil)
	if res.Imported != 0 || res.Already != 0 {
		t.Fatalf("second import copied again: %+v", res)
	}
	roll["100APPLE/IMG_0003.HEIC"] = "three"
	f.hook = cameraRoll(roll)
	if res, _ = Import(f.run, p, udid, nil); res.Imported != 1 {
		t.Fatalf("%+v", res)
	}
	// the ledger is lost (new PC, same photos folder): files already there are recognised, not duplicated
	_ = os.Remove(p.ledgerPath(udid))
	if res, _ = Import(f.run, p, udid, nil); res.Imported != 0 || res.Already != 4 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "ifuse "+p.mountPoint(udid, "media")+" -u "+udid) {
		t.Fatal(f.calls)
	}
}

func TestImportRefusesToRunTwice(t *testing.T) {
	p := testPaths(t)
	release, err := p.lock("import")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := Import((&fake{}).run, p, udid, nil); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatal(err)
	}
	if !PhotoStatus(p, p.LoadConfig())["running"].(bool) {
		t.Fatal("status should say running")
	}
}

func TestMountRejectsBadAppIDs(t *testing.T) {
	p := testPaths(t)
	f := &fake{out: map[string]string{"ifuse": ""}, fail: map[string]bool{}}
	if _, err := Mount(f.run, p, udid, "app:../../etc"); err == nil {
		t.Fatal("path in app id accepted")
	}
	if d, err := Mount(f.run, p, udid, "app:org.videolan.vlc-ios"); err != nil || !strings.HasSuffix(d, "app-org.videolan.vlc-ios") ||
		!strings.Contains(f.calls[0], "--documents org.videolan.vlc-ios") {
		t.Fatal(d, err, f.calls)
	}
}

func TestBackupProgressParsesRedraws(t *testing.T) {
	out := "Backup will be encrypted.\nReceiving files\r[=          ]  10% Finished\r[=====      ]  50% Finished\r[=====      ]  50% Finished\r[===========] 100% Finished\nBackup Successful.\n"
	var pcts []int
	var lines []string
	BackupProgress(strings.NewReader(out), func(pct int, line string) {
		if pct >= 0 {
			pcts = append(pcts, pct)
		} else {
			lines = append(lines, line)
		}
	})
	if len(pcts) != 3 || pcts[2] != 100 || lines[len(lines)-1] != "Backup Successful." {
		t.Fatal(pcts, lines)
	}
}

func TestBackupDue(t *testing.T) {
	p := testPaths(t)
	cfg := p.LoadConfig()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	if BackupDue(p, cfg, now) {
		t.Fatal("off by default")
	}
	cfg.AutoBackupDays = 7
	if !BackupDue(p, cfg, now) {
		t.Fatal("never backed up")
	}
	_ = writeJSON(filepath.Join(p.State, "last-backup.json"), M{"time": now.Add(-48 * time.Hour).Format(time.RFC3339), "ok": true})
	if BackupDue(p, cfg, now) {
		t.Fatal("2 days < 7")
	}
	_ = writeJSON(filepath.Join(p.State, "last-backup.json"), M{"time": now.Add(-48 * time.Hour).Format(time.RFC3339), "ok": false})
	if !BackupDue(p, cfg, now) {
		t.Fatal("a failed backup is retried")
	}
}

func TestSettings(t *testing.T) {
	p := testPaths(t)
	if c := p.LoadConfig(); !c.AutoImport || c.PhotosDir != filepath.Join(p.Home, "Pictures", "iPhone") {
		t.Fatalf("%+v", c)
	}
	for _, kv := range [][2]string{{"autoImport", "false"}, {"autoBackupDays", "7"}, {"photosDir", "~/Fotos/iPhone"}, {"airplayName", "Mi PC"}} {
		if err := p.Set(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	c := p.LoadConfig()
	if c.AutoImport || c.AutoBackupDays != 7 || c.PhotosDir != filepath.Join(p.Home, "Fotos", "iPhone") || c.AirPlayName != "Mi PC" {
		t.Fatalf("%+v", c)
	}
	if p.Set("autoBackupDays", "-1") == nil || p.Set("airplayName", " ") == nil || p.Set("nope", "1") == nil {
		t.Fatal("bad settings accepted")
	}
}

func TestTetherFindsIpheth(t *testing.T) {
	sys := t.TempDir()
	drv := filepath.Join(sys, "drivers", "ipheth")
	_ = os.MkdirAll(drv, 0o755)
	_ = os.MkdirAll(filepath.Join(sys, "net", "enp0s1", "device"), 0o755)
	_ = os.Symlink(drv, filepath.Join(sys, "net", "enp0s1", "device", "driver"))
	_ = os.MkdirAll(filepath.Join(sys, "net", "eno1"), 0o755)
	f := &fake{out: map[string]string{"nmcli -t -g GENERAL.STATE device show enp0s1": "100 (connected)\n"}, fail: map[string]bool{}}
	tt := Tether(f.run, filepath.Join(sys, "net"))
	if tt["iface"] != "enp0s1" || tt["connected"] != true {
		t.Fatalf("%v", tt)
	}
	if Tether(f.run, filepath.Join(sys, "nothing"))["connected"] != false {
		t.Fatal("no iPhone")
	}
}

func TestOnConnect(t *testing.T) {
	var notes []string
	Notify = func(title, body string) { notes = append(notes, title+": "+body) }
	defer func() { Notify = func(string, string) {} }()
	p := testPaths(t)
	f := &fake{out: map[string]string{"ifuse": "", "fusermount3": ""}, fail: map[string]bool{},
		hook: cameraRoll(map[string]string{"100APPLE/IMG_0001.HEIC": "one"})}
	OnConnect(f.run, p, Device{UDID: udid, Trusted: false, Error: "Tap Trust on the iPhone and type its passcode"})
	if len(notes) != 1 || !strings.Contains(notes[0], "Trust") || len(f.calls) != 0 {
		t.Fatal(notes, f.calls)
	}
	OnConnect(f.run, p, Device{UDID: udid, Trusted: true, Name: "iPhone de Juan"})
	if len(notes) != 2 || !strings.Contains(notes[1], "1 fotos") {
		t.Fatal(notes)
	}
	_ = p.Set("autoImport", "false")
	f.hook = cameraRoll(map[string]string{"100APPLE/IMG_0001.HEIC": "", "100APPLE/IMG_0009.HEIC": "nine"})
	OnConnect(f.run, p, Device{UDID: udid, Trusted: true, Name: "iPhone de Juan"})
	if len(notes) != 3 || !strings.Contains(notes[2], "1 fotos y videos nuevos para importar") {
		t.Fatal(notes)
	}
}
