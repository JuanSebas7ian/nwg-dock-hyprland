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

func kdeFake(tabletAsks bool) *fake {
	tab, ph := "0a79468d_782f_4bb8_9ade_24d1db995540", "2d4360c9_455b_44c6_bc6f_397a6d63809e"
	call := "busctl --user --json=short call org.kde.kdeconnect "
	get := "busctl --user --json=short get-property org.kde.kdeconnect "
	dev := func(id string) string { return "/modules/kdeconnect/devices/" + id + " org.kde.kdeconnect.device " }
	b := func(v bool) string {
		if v {
			return `{"type":"b","data":[true]}`
		}
		return `{"type":"b","data":[false]}`
	}
	return &fake{fail: map[string]bool{}, out: map[string]string{
		call + "/modules/kdeconnect org.kde.kdeconnect.daemon devices bb false false": `{"type":"as","data":[["` + ph + `","` + tab + `"]]}`,
		get + dev(tab) + "name":                   `{"type":"s","data":"JuanSebas7ian 2"}`,
		get + dev(tab) + "type":                   `{"type":"s","data":"tablet"}`,
		get + dev(tab) + "isReachable":            `{"type":"b","data":true}`,
		call + dev(tab) + "isPaired":              b(false),
		call + dev(tab) + "isPairRequestedByPeer": b(tabletAsks),
		call + dev(tab) + "isPairRequested":       b(false),
		get + dev(ph) + "name":                    `{"type":"s","data":"JuanSebas7ian"}`,
		get + dev(ph) + "type":                    `{"type":"s","data":"phone"}`,
		get + dev(ph) + "isReachable":             `{"type":"b","data":false}`,
		call + dev(ph) + "isPaired":               b(true),
		call + dev(ph) + "isPairRequestedByPeer":  b(false),
		call + dev(ph) + "isPairRequested":        b(false),
	}}
}

func TestKDEDevicesOverDBus(t *testing.T) {
	ds, err := KDEDevices(kdeFake(true).run)
	if err != nil || len(ds) != 2 {
		t.Fatal(ds, err)
	}
	// the request comes first: it expires in seconds
	if ds[0].Name != "JuanSebas7ian 2" || ds[0].Type != "tablet" || ds[0].Paired || !ds[0].Reachable || !ds[0].RequestedByPeer {
		t.Fatalf("%+v", ds[0])
	}
	if ds[1].Type != "phone" || !ds[1].Paired || ds[1].Reachable {
		t.Fatalf("%+v", ds[1])
	}
	if _, err := KDEDevices((&fake{}).run); err == nil {
		t.Fatal("no daemon should be an error")
	}
}

func TestWatchAnnouncesPairRequestsOnce(t *testing.T) {
	var notes []string
	Notify = func(title, body string) { notes = append(notes, title) }
	defer func() { Notify = func(string, string) {} }()
	f := kdeFake(true)
	f.out["idevice_id -l"], f.out["idevice_id -n"] = "", ""
	stop := make(chan struct{})
	ticks := 0
	f.hook = func(name string, args []string) {
		if name == "idevice_id" && args[0] == "-l" {
			if ticks++; ticks == 4 {
				close(stop)
			}
		}
	}
	Watch(f.run, testPaths(t), time.Millisecond, stop)
	if len(notes) != 1 || !strings.Contains(notes[0], "JuanSebas7ian 2 quiere emparejarse") {
		t.Fatal(notes)
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
	f.out["busctl"] = ""
	if err := KDEAction(f.run, "accept", "0a79468d_782f_4bb8_9ade_24d1db995540"); err != nil || !strings.Contains(f.calls[len(f.calls)-1], "acceptPairing") {
		t.Fatal(err, f.calls)
	}
	if err := KDEAction(f.run, "refresh", ""); err != nil {
		t.Fatal(err)
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
	res, err := Import(f.run, p, udid, "iPhone", func(done, total int, _ string) { last = [2]int{done, total} })
	if err != nil || res.Imported != 3 || res.Failed != 0 || last != [2]int{3, 3} {
		t.Fatalf("%+v %v %v", res, err, last)
	}
	dir := Folder(p.LoadConfig(), "iPhone")
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
	res, _ = Import(f.run, p, udid, "iPhone", nil)
	if res.Imported != 0 || res.Already != 0 {
		t.Fatalf("second import copied again: %+v", res)
	}
	roll["100APPLE/IMG_0003.HEIC"] = "three"
	f.hook = cameraRoll(roll)
	if res, _ = Import(f.run, p, udid, "iPhone", nil); res.Imported != 1 {
		t.Fatalf("%+v", res)
	}
	// the ledger is lost (new PC, same photos folder): files already there are recognised, not duplicated
	_ = os.Remove(p.ledgerPath(udid))
	if res, _ = Import(f.run, p, udid, "iPhone", nil); res.Imported != 0 || res.Already != 4 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "ifuse "+p.mountPoint(udid, "media")+" -u "+udid) {
		t.Fatal(f.calls)
	}
}

func TestImportRefusesToRunTwice(t *testing.T) {
	p := testPaths(t)
	release, err := p.lock(jobKey("import", udid))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := Import((&fake{}).run, p, udid, "iPhone", nil); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatal(err)
	}
	if !PhotoStatus(p, p.LoadConfig(), udid)["running"].(bool) {
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
	if BackupDue(p, cfg, udid, now) {
		t.Fatal("off by default")
	}
	cfg.AutoBackupDays = 7
	if !BackupDue(p, cfg, udid, now) {
		t.Fatal("never backed up")
	}
	_ = writeJSON(p.devFile(udid, "last-backup.json"), M{"time": now.Add(-48 * time.Hour).Format(time.RFC3339), "ok": true})
	if BackupDue(p, cfg, udid, now) {
		t.Fatal("2 days < 7")
	}
	_ = writeJSON(p.devFile(udid, "last-backup.json"), M{"time": now.Add(-48 * time.Hour).Format(time.RFC3339), "ok": false})
	if !BackupDue(p, cfg, udid, now) {
		t.Fatal("a failed backup is retried")
	}
}

func TestSettings(t *testing.T) {
	p := testPaths(t)
	if c := p.LoadConfig(); !c.AutoImport || !c.DriveBackup || c.PhotosDir != filepath.Join(p.Home, "Pictures") {
		t.Fatalf("%+v", c)
	}
	for _, kv := range [][2]string{{"autoImport", "false"}, {"autoBackupDays", "7"}, {"photosDir", "~/Fotos"}, {"airplayName", "Mi PC"}, {"driveBackup", "false"}} {
		if err := p.Set(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	c := p.LoadConfig()
	if c.AutoImport || c.AutoBackupDays != 7 || c.PhotosDir != filepath.Join(p.Home, "Fotos") || c.AirPlayName != "Mi PC" || c.DriveBackup {
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

func TestOldConfigAndIPadFolders(t *testing.T) {
	p := testPaths(t)
	_ = writeJSON(p.Config, M{"photosDir": filepath.Join(p.Home, "Pictures", "iPhone")})
	c := p.LoadConfig()
	if Folder(c, "iPhone") != filepath.Join(p.Home, "Pictures", "iPhone") || Folder(c, "iPad") != filepath.Join(p.Home, "Pictures", "iPad") || Folder(c, "") != filepath.Join(p.Home, "Pictures", "iPhone") {
		t.Fatal(Folder(c, "iPhone"), Folder(c, "iPad"))
	}
	if ModelName("iPad14,1") != "iPad mini (6th gen)" || ModelName("iPad99,1") != "iPad (iPad99,1)" {
		t.Fatal("iPad names")
	}
}

func TestDescribeIPad(t *testing.T) {
	f := trustedFake()
	f.out["ideviceinfo -u "+udid+" -x"] = string(plistXML(`<key>DeviceName</key><string>iPad de Juan</string><key>DeviceClass</key><string>iPad</string>
		<key>ProductType</key><string>iPad13,18</string><key>ProductVersion</key><string>26.1</string>`))
	if d := Describe(f.run, Device{UDID: udid, Conn: "usb"}); d.Class != "iPad" || d.Model != "iPad (10th gen)" {
		t.Fatalf("%+v", d)
	}
}

func TestResticProgress(t *testing.T) {
	out := `{"message_type":"status","percent_done":0.25,"total_bytes":4000000000,"bytes_done":1000000000}
{"message_type":"status","percent_done":1,"total_bytes":4000000000,"bytes_done":4000000000}
{"message_type":"summary","snapshot_id":"abc123","data_added":52428800,"total_bytes_processed":4000000000}
`
	var fr []float64
	sum, errLine := ResticProgress(strings.NewReader(out), func(f float64, line string) { fr = append(fr, f) })
	if len(fr) != 2 || fr[0] != 0.25 || sum["snapshot_id"] != "abc123" || errLine != "" {
		t.Fatal(fr, sum, errLine)
	}
	_, errLine = ResticProgress(strings.NewReader("Fatal: unable to open repository\n"), nil)
	if !strings.Contains(errLine, "unable to open") {
		t.Fatal(errLine)
	}
}

func TestResticEnvReadsOmarchyBackup(t *testing.T) {
	p := testPaths(t)
	conf := filepath.Join(filepath.Dir(filepath.Dir(p.Config)), "omarchy-backup")
	if _, err := ResticEnv(p); err == nil {
		t.Fatal("no config should fail")
	}
	_ = os.MkdirAll(conf, 0o700)
	_ = os.WriteFile(filepath.Join(conf, "config"), []byte("# x\nRESTIC_REPOSITORY=rclone:rclone:Backups/omarchy-restic\n"), 0o600)
	_ = os.WriteFile(filepath.Join(conf, "restic-password"), []byte("secret"), 0o600)
	env, err := ResticEnv(p)
	if err != nil || !strings.Contains(strings.Join(env, "\n"), "RESTIC_REPOSITORY=rclone:rclone:Backups/omarchy-restic") {
		t.Fatal(err)
	}
}

func TestDriveUploadNeedsALocalBackup(t *testing.T) {
	p := testPaths(t)
	if err := DriveUpload(p, udid, nil); err == nil || !strings.Contains(err.Error(), "local backup") {
		t.Fatal(err)
	}
}

func TestCheckSafe(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	put := func(base, rel, body string, mod time.Time) string {
		p := filepath.Join(base, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(body), 0o644)
		_ = os.Chtimes(p, mod, mod)
		return p
	}
	d := func(y, m, day int) time.Time { return time.Date(y, time.Month(m), day, 12, 0, 0, 0, time.Local) }
	put(root, "DCIM/100APPLE/IMG_1.HEIC", "aaaa", d(2024, 1, 5))
	put(root, "DCIM/100APPLE/IMG_2.HEIC", "bbbb", d(2024, 3, 9))
	put(root, "DCIM/100APPLE/IMG_3.HEIC", "cccc", d(2025, 6, 1)) // copied, not uploaded yet
	put(root, "DCIM/100APPLE/IMG_4.HEIC", "dddd", d(2026, 2, 1)) // not copied
	up := map[string]bool{}
	up[put(dir, "2024/IMG_1.HEIC", "aaaa", d(2024, 1, 5))] = true
	up[put(dir, "2024/IMG_2.HEIC", "bbbb", d(2024, 3, 9))] = true
	put(dir, "2025/IMG_3.HEIC", "cccc", d(2025, 6, 1))
	r, err := CheckSafe(root, dir, up, 10)
	if err != nil || r.Total != 4 || r.Safe != 2 || r.NotUploaded != 1 || r.NotCopied != 1 || r.SafeUntil != "2025-05-31" ||
		r.OldestMissing != "2025-06-01" || r.Verified != 2 || r.Mismatched != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	// a local copy with the right size but other content: nothing is declared safe
	put(dir, "2024/IMG_1.HEIC", "zzzz", d(2024, 1, 5))
	if r, _ := CheckSafe(root, dir, up, 10); r.Mismatched != 1 || r.SafeUntil != "" {
		t.Fatalf("%+v", r)
	}
}

func TestUploadedPaths(t *testing.T) {
	f := filepath.Join(t.TempDir(), "uploaded.jsonl")
	_ = os.WriteFile(f, []byte(`{"k":"iphone:2024/a.heic","path":"/home/u/Pictures/iPhone/2024/a.heic"}
not json
{"k":"x","path":"/home/u/b.png"}
`), 0o644)
	if u := UploadedPaths(f); len(u) != 2 || !u["/home/u/Pictures/iPhone/2024/a.heic"] {
		t.Fatal(u)
	}
}

func TestICloudPhotosFromTheDatabase(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	run := ExecRunner(10 * time.Second)
	for want, guid := range map[string]string{"on": "'A1B2'", "off": "NULL"} {
		root := t.TempDir()
		db := filepath.Join(root, "PhotoData", "Photos.sqlite")
		_ = os.MkdirAll(filepath.Dir(db), 0o755)
		if b, err := run("sqlite3", db, "CREATE TABLE ZASSET (Z_PK INTEGER, ZCLOUDASSETGUID TEXT); INSERT INTO ZASSET VALUES (1, "+guid+");"); err != nil {
			t.Fatal(string(b))
		}
		if got := ICloudPhotos(run, root, filepath.Join(t.TempDir(), "tmp")); got != want {
			t.Fatal(want, got)
		}
	}
	if ICloudPhotos(run, t.TempDir(), t.TempDir()) != "unknown" {
		t.Fatal("no database")
	}
}

func TestWatchRetriesUntilTrusted(t *testing.T) {
	var notes []string
	Notify = func(title, body string) { notes = append(notes, title+": "+body) }
	defer func() { Notify = func(string, string) {} }()
	p := testPaths(t)
	_ = p.Set("autoImport", "false")
	f := trustedFake()
	f.out["idevice_id -n"] = ""
	f.out["idevicepair -u "+udid+" validate"] = "ERROR: Please accept the trust dialog"
	f.fail["idevicepair -u "+udid+" validate"] = true
	f.out["ifuse"], f.out["fusermount3"] = "", ""
	f.hook = cameraRoll(map[string]string{"100APPLE/IMG_0001.HEIC": "one"})
	stop := make(chan struct{})
	ticks := 0
	orig := f.hook
	f.hook = func(name string, args []string) {
		if name == "idevice_id" && len(args) > 0 && args[0] == "-l" {
			ticks++
			if ticks == 3 { // the user taps Trust
				f.out["idevicepair -u "+udid+" validate"] = "SUCCESS: Validated pairing"
				delete(f.fail, "idevicepair -u "+udid+" validate")
			}
			if ticks == 6 {
				close(stop)
			}
		}
		orig(name, args)
	}
	Watch(f.run, p, time.Millisecond, stop)
	trust, newPhotos := 0, 0
	for _, n := range notes {
		if strings.Contains(n, "Trust") {
			trust++
		}
		if strings.Contains(n, "1 fotos y videos nuevos") {
			newPhotos++
		}
	}
	if trust != 1 || newPhotos != 1 {
		t.Fatal(notes)
	}
}

func TestGooglePhotosSources(t *testing.T) {
	p := testPaths(t)
	gp := p.gphotosConfig()
	_ = os.MkdirAll(filepath.Dir(gp), 0o755)
	_ = os.WriteFile(gp, []byte(`{"remote":"gphotos:","sources":[{"id":"camera","path":"~/Dropbox/Cargas"}]}`), 0o644)
	added, err := AddGooglePhotosSources(p)
	if err != nil || len(added) != 2 {
		t.Fatal(added, err)
	}
	if again, _ := AddGooglePhotosSources(p); len(again) != 0 {
		t.Fatal("added twice")
	}
	b, _ := os.ReadFile(gp)
	if !strings.Contains(string(b), `"path": "~/Pictures/iPhone"`) || !strings.Contains(string(b), `"album": "iPad {top}"`) || !strings.Contains(string(b), `"camera"`) {
		t.Fatal(string(b))
	}
	_ = os.MkdirAll(p.gphotosState(), 0o755)
	_ = os.WriteFile(filepath.Join(p.gphotosState(), "uploaded.jsonl"), []byte("{\"src\":\"iphone\"}\n{\"src\":\"iphone\"}\n{\"src\":\"camera\"}\n"), 0o644)
	st := GooglePhotosStatus(p, p.LoadConfig())
	if st["iphone"].(M)["uploaded"] != 2 || st["ipad"].(M)["source"] != true {
		t.Fatal(st)
	}
}

func TestJobsStatus(t *testing.T) {
	p := testPaths(t)
	release, _ := p.lock(jobKey("backup", udid))
	p.SaveProgress("backup", udid, 0.5, "50% Finished")
	js := JobsStatus(p, udid)
	if js["backup"].(M)["running"] != true || js["backup"].(M)["progress"].(M)["fraction"] != 0.5 || js["import"].(M)["running"] != false {
		t.Fatal(js)
	}
	release()
	if JobsStatus(p, udid)["backup"].(M)["progress"] != nil {
		t.Fatal("a finished job shows no progress")
	}
}

func TestEjectRefusesDuringImport(t *testing.T) {
	p := testPaths(t)
	release, _ := p.lock(jobKey("import", udid))
	if _, err := Eject((&fake{}).run, p, ""); err != ErrInUse {
		t.Fatal(err)
	}
	release()
	if _, err := Eject((&fake{}).run, p, ""); err != nil {
		t.Fatal(err)
	}
}

func TestImportStopsWhenTheDeviceGoesAway(t *testing.T) {
	p := testPaths(t)
	roll := map[string]string{}
	for i := 0; i < 50; i++ {
		roll[fmt.Sprintf("100APPLE/IMG_%04d.HEIC", i)] = "x"
	}
	f := &fake{out: map[string]string{"ifuse": "", "fusermount3": ""}, fail: map[string]bool{}, hook: cameraRoll(roll)}
	n := 0
	res, err := Import(f.run, p, udid, "iPhone", func(done, total int, _ string) {
		n++
		if done == 10 { // the cable is pulled mid-import
			_ = os.RemoveAll(filepath.Join(p.mountPoint(udid, "media"), "DCIM"))
		}
	})
	if err == nil || res.Imported != 10 || res.Failed != 40 || n > 12 {
		t.Fatalf("%+v %v calls=%d", res, err, n)
	}
	if l := p.LoadLedger(udid); len(l) != 10 {
		t.Fatal("only the copied files are recorded", len(l))
	}
}

func TestStatusKeepsLastKnownValuesWhenBusy(t *testing.T) {
	p := testPaths(t)
	f := trustedFake()
	f.out["idevice_id -n"] = ""
	sys := t.TempDir()
	if d := Status(f.run, p, sys)["devices"].([]Device)[0]; d.Storage == nil || d.Storage.Total != 128e9 {
		t.Fatalf("%+v", d)
	}
	f.fail["ideviceinfo -u "+udid+" -q com.apple.disk_usage -x"] = true // busy: the query fails
	if d := Status(f.run, p, sys)["devices"].([]Device)[0]; d.Storage == nil || d.Storage.Free != 32e9 {
		t.Fatalf("storage forgotten: %+v", d)
	}
}

func TestShortCopiesAreNotRecorded(t *testing.T) {
	p := testPaths(t)
	roll := map[string]string{"100APPLE/IMG_0001.HEIC": "complete", "100APPLE/IMG_0002.MOV": "0123456789"}
	f := &fake{out: map[string]string{"ifuse": "", "fusermount3": ""}, fail: map[string]bool{}, hook: cameraRoll(roll)}
	dir := Folder(p.LoadConfig(), "iPhone")
	res, err := Import(f.run, p, udid, "iPhone", func(done, total int, name string) {
		if name == "IMG_0002.MOV" { // shrink the source after it was listed: reads come back short
			_ = os.WriteFile(filepath.Join(p.mountPoint(udid, "media"), "DCIM", "100APPLE", "IMG_0002.MOV"), []byte("0123"), 0o644)
		}
	})
	if err != nil || res.Imported != 1 || res.Failed != 1 || !strings.Contains(res.Error, "changed on the device") {
		t.Fatalf("%+v %v", res, err)
	}
	if _, ok := p.LoadLedger(udid)["100APPLE/IMG_0002.MOV"]; ok {
		t.Fatal("a short copy was recorded as imported")
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "2024")); len(ents) != 1 {
		t.Fatal("the partial file was left behind", ents)
	}
}

func TestTwoDevicesBackUpAtTheSameTime(t *testing.T) {
	p := testPaths(t)
	ipad := "00008027-000A1B2C3D4E5F60"
	r1, err1 := p.lock(jobKey("backup", udid))
	r2, err2 := p.lock(jobKey("backup", ipad)) // a second device is not blocked by the first
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if _, err := p.lock(jobKey("backup", udid)); err == nil {
		t.Fatal("the same device twice")
	}
	p.SaveProgress("backup", udid, 0.3, "30%")
	p.SaveProgress("backup", ipad, 0.7, "70%")
	per := PerDevice(p, p.LoadConfig())
	a := per[udid].(M)["jobs"].(M)["backup"].(M)
	b := per[ipad].(M)["jobs"].(M)["backup"].(M)
	if a["running"] != true || b["running"] != true || a["progress"].(M)["fraction"] != 0.3 || b["progress"].(M)["fraction"] != 0.7 {
		t.Fatal(per)
	}
	r1()
	r2()
}

func TestMigrateStateMovesSingleDeviceFiles(t *testing.T) {
	p := testPaths(t)
	_ = writeJSON(filepath.Join(p.State, "last-backup.json"), M{"udid": udid, "ok": true, "time": "2026-10-12T17:33:20-05:00"})
	_ = writeJSON(filepath.Join(p.State, "last-drive.json"), M{"udid": "bad", "ok": true})
	MigrateState(p)
	if !BackupStatus(p, p.LoadConfig(), udid)["last"].(M)["ok"].(bool) {
		t.Fatal("backup history not migrated")
	}
	if _, err := os.Stat(filepath.Join(p.State, "last-backup.json")); err == nil {
		t.Fatal("old file left")
	}
}
