package hw

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, root, rel, text string) {
	p := filepath.Join(root, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeSystem: two threads, k10temp, an nct6799 with a pump and a CPU fan, one DDR5 module.
func fakeSystem(t *testing.T, root string, idle [2]int, pump int) {
	put(t, root, "/proc/stat", fmt.Sprintf("cpu  100 0 100 %d 0 0 0 0 0 0\ncpu0 50 0 50 %d 0 0 0 0 0 0\ncpu1 50 0 50 %d 0 0 0 0 0 0\nintr 1\n", idle[0]+idle[1], idle[0], idle[1]))
	put(t, root, "/proc/cpuinfo", "model name\t: AMD Ryzen 5 9600X 6-Core Processor\n")
	put(t, root, "/proc/meminfo", "MemTotal: 1000 kB\nMemAvailable: 400 kB\nCached: 100 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
	put(t, root, "/proc/diskstats", "259 0 nvme0n1 1 0 10 0 1 0 20 0 0 0 0\n")
	put(t, root, "/proc/net/dev", "h1\nh2\n  lo: 5 0 0 0 0 0 0 0 5 0\neth0: 1000 0 0 0 0 0 0 0 2000 0\n")
	for rel, v := range map[string]string{
		"hwmon0/name": "k10temp", "hwmon0/temp1_label": "Tctl", "hwmon0/temp1_input": "61250", "hwmon0/temp3_label": "Tccd1", "hwmon0/temp3_input": "50000",
		"hwmon1/name": "nct6799", "hwmon1/fan2_input": "900", "hwmon1/fan3_input": "0", "hwmon1/fan7_input": fmt.Sprint(pump),
		"hwmon1/temp1_label": "SYSTIN", "hwmon1/temp1_input": "31000", "hwmon2/name": "spd5118", "hwmon2/temp1_input": "40500"} {
		put(t, root, "/sys/class/hwmon/"+rel, v)
	}
	put(t, root, "/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq", "5200000")
	put(t, root, "/sys/devices/system/cpu/cpu1/cpufreq/scaling_cur_freq", "600000")
	put(t, root, "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor", "performance")
}

func base(t *testing.T) string {
	root := t.TempDir()
	old := SysRoot
	SysRoot = filepath.Join(root, "fs")
	t.Cleanup(func() { SysRoot = old })
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("HW_NO_NVML", "1")
	fakeSystem(t, SysRoot, [2]int{100, 100}, 5500)
	put(t, root, "config/omarchy-sysmon/fans.json", `{"7": "AIO pump", "2": "CPU fan", "_roles": {"pump": ["7"], "cpu": ["2"]}}`)
	return root
}

func TestPerThreadLoadTemperaturesAndFanRoles(t *testing.T) {
	base(t)
	s := NewSensors(false)
	put(t, SysRoot, "/proc/stat", "cpu  100 0 150 250 0 0 0 0 0 0\ncpu0 50 0 50 150 0 0 0 0 0 0\ncpu1 50 0 100 100 0 0 0 0 0 0\n")
	snap := s.Sample(false, false)
	cpu := snap["cpu"].(M)
	if !reflect.DeepEqual(cpu["threads"], []float64{0, 100}) || cpu["load"] != 50.0 || *cpu["tctl"].(*float64) != 61.25 {
		t.Fatal(cpu)
	}
	if ccd := cpu["ccd"].([]*float64); len(ccd) != 1 || *ccd[0] != 50 {
		t.Fatal(ccd)
	}
	if !reflect.DeepEqual(cpu["freqs"], []int64{5200, 600}) {
		t.Fatal(cpu["freqs"])
	}
	fans := map[string]M{}
	for _, f := range snap["fans"].([]M) {
		fans[f["id"].(string)] = f
	}
	if fans["7"]["role"] != "pump" || fans["2"]["role"] != "cpu" {
		t.Fatal(fans)
	}
	if _, ok := fans["3"]; ok {
		t.Fatal("a stopped case fan is hidden")
	}
	if !reflect.DeepEqual(snap["board"], []M{{"name": "Motherboard", "temp": 31.0}}) || !reflect.DeepEqual(snap["ram"], []float64{40.5}) {
		t.Fatal(snap["board"], snap["ram"])
	}
	if snap["mem"].(M)["used"] != int64(600*1024) {
		t.Fatal(snap["mem"])
	}
	if _, ok := snap["gpus"]; ok {
		t.Fatal("gpus without NVML")
	}
}

func TestStoppedPumpStaysListed(t *testing.T) {
	base(t)
	fakeSystem(t, SysRoot, [2]int{100, 100}, 0)
	var rpms []int64
	for _, f := range NewSensors(false).Sample(false, false)["fans"].([]M) {
		if f["role"] == "pump" {
			rpms = append(rpms, f["rpm"].(int64))
		}
	}
	if !reflect.DeepEqual(rpms, []int64{0}) {
		t.Fatal(rpms)
	}
}

func cfg() Config {
	c := Config{}
	for k, v := range Defaults {
		c[k] = v
	}
	return c
}

func live(tctl, pump, used, gpu float64) M {
	return M{"cpu": M{"tctl": tctl}, "fans": []M{{"id": "7", "name": "AIO pump", "rpm": pump, "role": "pump"}},
		"mem": M{"total": 100.0, "used": used * 100}, "gpus": []M{{"index": 0, "temp": gpu}}}
}

func ids(a []Alert) []string {
	out := []string{}
	for _, x := range a {
		out = append(out, x.ID)
	}
	return out
}

func TestAlertThresholds(t *testing.T) {
	c := cfg()
	for _, tc := range []struct {
		l    M
		want []string
	}{{live(50, 5500, .5, 40), []string{}}, {live(86, 5500, .5, 40), []string{"cpu-warm"}}, {live(91, 5500, .5, 40), []string{"cpu-hot"}},
		{live(50, 120, .5, 40), []string{"pump"}}, {live(50, 5500, .95, 40), []string{"ram"}}, {live(50, 5500, .5, 85), []string{"gpu-hot-0"}}} {
		if got := ids(Evaluate(c, tc.l, nil, nil, nil)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: %v", tc.want, got)
		}
	}
	out := Evaluate(c, live(86, 0, .5, 40), nil, nil, nil)
	if len(out) != 2 || out[0].ID != "pump" || out[0].Level != "crit" || out[1].ID != "cpu-warm" || out[1].Tab != "cpu" {
		t.Fatal(out)
	}
}

func TestAlertsStorageDriversDevices(t *testing.T) {
	storage := M{"root": M{"size": 100.0, "free": 5.0}, "disks": []any{M{"name": "nvme0n1", "model": "X", "health": M{"mediaErrors": 2.0}}}}
	drv := M{"nvidia": M{"issues": []any{"DKMS module not built for kernel 7.3"}}, "cuda": M{"works": true},
		"health": M{"issues": []any{M{"id": "CF07", "level": "fail", "title": "t", "details": []any{}}}}}
	per := M{"bluetooth": M{"adapter": M{}, "devices": []any{M{"mac": "AA", "name": "JBL", "connected": true, "battery": 9.0}}},
		"batteries": []any{M{"name": "M575S", "percent": 50.0}}}
	got := strings.Join(ids(Evaluate(cfg(), nil, drv, storage, per)), " ")
	for _, want := range []string{"disk-free", "smart-nvme0n1", "hwcheck-CF07", "battery-AA", "nvidia-"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s missing in %s", want, got)
		}
	}
	if strings.Contains(got, "battery-M575S") || strings.Contains(got, "cuda") {
		t.Fatal(got)
	}
	// The NVIDIA alert id must be stable between runs (it de-duplicates notifications).
	a, b := Evaluate(cfg(), nil, drv, nil, nil), Evaluate(cfg(), nil, drv, nil, nil)
	if a[0].ID != b[0].ID {
		t.Fatal("unstable id")
	}
}

func TestNotifierSendsOnceAndRearms(t *testing.T) {
	sentN := 0
	old := Notify
	Notify = func(string, string, string) { sentN++ }
	defer func() { Notify = old }()
	n := NewNotifier(60 * time.Second)
	crit := Evaluate(cfg(), live(50, 0, .5, 40), nil, nil, nil)
	at := func(s int) time.Time { return time.Unix(int64(s), 0) }
	if len(n.Update(crit, at(1000))) != 1 || len(n.Update(crit, at(1010))) != 0 {
		t.Fatal("repeat")
	}
	n.Update(nil, at(1100))
	if len(n.Update(crit, at(1101))) != 1 || sentN != 2 {
		t.Fatal("no re-arm")
	}
	if len(n.Update(Evaluate(cfg(), live(86, 5500, .5, 40), nil, nil, nil), at(1102))) != 0 {
		t.Fatal("warnings never notify")
	}
}

func TestCheckupdatesSplitOnce(t *testing.T) {
	text := "linux 7.2.3.arch1-3 -> 7.2.4.arch1-1\nnvidia-utils 610.57.04-1 -> 610.60.01-1\nfirefox 140-1 -> 141-1\ncuda 13.3.1-1 -> 13.3.2-1\n"
	split := SplitUpdates(M{"packages": ParseCheckupdates(text), "checkedAt": 1})
	names := func(k string) []string {
		var out []string
		for _, x := range l(split[k]) {
			out = append(out, s(m(x)["name"]))
		}
		return out
	}
	if split["total"] != 4 || !reflect.DeepEqual(names("drivers"), []string{"linux", "nvidia-utils"}) || !reflect.DeepEqual(names("gpu"), []string{"linux", "nvidia-utils", "cuda"}) {
		t.Fatal(split)
	}
}

func TestBIOSNewer(t *testing.T) {
	if !BIOSNewer("1804", "1402") || BIOSNewer("1402", "1402") || BIOSNewer("", "1402") {
		t.Fatal("BIOSNewer")
	}
}

func TestSamsungParse(t *testing.T) {
	page := `<a href="https://x.samsung.com/fw/Samsung_SSD_980_3B4QFXO7.iso">980</a> <a href="https://x.samsung.com/fw/Samsung_SSD_990_PRO_4B2QJXD7.iso">`
	got := ParseSamsung(page, "")
	if got["980"]["version"] != "3B4QFXO7" || got["990 PRO"]["iso"] != "https://x.samsung.com/fw/Samsung_SSD_990_PRO_4B2QJXD7.iso" {
		t.Fatal(got)
	}
}

func TestBluezObjects(t *testing.T) {
	v := func(x any) M { return M{"type": "x", "data": x} }
	objects := M{
		"/org/bluez/hci0":       M{"org.bluez.Adapter1": M{"Address": v("58:02"), "Alias": v("pc"), "Powered": v(true), "Discoverable": v(false), "Pairable": v(true)}},
		"/org/bluez/hci0/dev_A": M{"org.bluez.Device1": M{"Address": v("AA"), "Alias": v("JBL Grip"), "Paired": v(true), "Trusted": v(true), "Connected": v(false), "Icon": v("audio-card")}},
		"/org/bluez/hci0/dev_B": M{"org.bluez.Device1": M{"Address": v("BB"), "Alias": v("Keys"), "Paired": v(true), "Trusted": v(true), "Connected": v(true)},
			"org.bluez.Battery1": M{"Percentage": v(42.0)}},
		"/org/bluez/hci0/dev_C": M{"org.bluez.Device1": M{"Address": v("CC"), "Alias": v("Stranger"), "Paired": v(false), "Connected": v(false)}},
	}
	adapter, devices := ParseBluez(objects)
	if adapter["powered"] != true || adapter["discoverable"] != false {
		t.Fatal(adapter)
	}
	if len(devices) != 2 || devices[0]["name"] != "Keys" || devices[1]["name"] != "JBL Grip" || devices[0]["battery"] != 42.0 || devices[1]["battery"] != nil {
		t.Fatal(devices)
	}
}

func TestSolaarOutput(t *testing.T) {
	text := "Bolt Receiver\n  Device path  : /dev/hidraw6\n\n  1: ERGO M575S Trackball\n     Codename : x\n" +
		"         8: UNIFIED BATTERY        {1004} V2\n            Battery: 80%, BatteryStatus.DISCHARGING.\n  2: ERGO M575S\n     Codename : y\n"
	want := []M{{"name": "ERGO M575S Trackball", "source": "solaar", "percent": 80, "status": "discharging"},
		{"name": "ERGO M575S", "source": "solaar", "percent": nil, "status": ""}}
	if got := ParseSolaar(text); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestInputDevicesMergedPerDevice(t *testing.T) {
	text := "N: Name=\"Logitech USB Receiver\"\nP: Phys=usb-0:9/input0\nH: Handlers=sysrq kbd event5\nB: EV=120013\n\n" +
		"N: Name=\"Logitech USB Receiver Mouse\"\nP: Phys=usb-0:9/input1\nH: Handlers=mouse0 event6\nB: EV=17\n\n" +
		"N: Name=\"USB Keyboard\"\nP: Phys=usb-0:2.3/input0\nH: Handlers=sysrq kbd event3\nB: EV=120013\n\n" +
		"N: Name=\"Microsoft X-Box 360 pad\"\nP: Phys=usb-0:1/input0\nH: Handlers=event22 js0\nB: EV=20000b\n\n" +
		"N: Name=\"Microsoft X-Box 360 pad\"\nP: Phys=pad-keepalive/input0\nS: Sysfs=/devices/virtual/input/input99\nH: Handlers=event23 js1\nB: EV=20000b\n\n" +
		"N: Name=\"Power Button\"\nP: Phys=LNXPWRBN/button/input0\nH: Handlers=kbd event1\nB: EV=3\n"
	var rows []string
	for _, d := range ParseInputDevices(text) {
		rows = append(rows, fmt.Sprintf("%s|%v|%v", d["name"], d["kinds"], d["virtual"]))
	}
	want := []string{"Logitech USB Receiver|[pointer keyboard]|false", "USB Keyboard|[keyboard]|false",
		"Microsoft X-Box 360 pad|[gamepad]|false", "Microsoft X-Box 360 pad|[gamepad]|true"}
	if !reflect.DeepEqual(rows, want) {
		t.Fatal(rows)
	}
}

func TestBTConfEditKeepsOtherLinesAndStaysShell(t *testing.T) {
	base(t)
	conf := BTConf()
	put(t, filepath.Dir(conf), filepath.Base(conf), "# mine\nINTERVAL=5\nDISCOVERABLE=yes\n")
	if err := WriteBTConf(map[string]string{"DISCOVERABLE": "no", "AUTOCONNECT_EXCLUDE": "AA:BB:CC:DD:EE:FF 11:22:33:44:55:66"}); err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(conf)
	if !strings.Contains(string(text), "# mine\nINTERVAL=5\nDISCOVERABLE=no\n") {
		t.Fatal(string(text))
	}
	c := ReadBTConf()
	if c["AUTOCONNECT_EXCLUDE"] != "AA:BB:CC:DD:EE:FF 11:22:33:44:55:66" || c["RECONNECT_AFTER"] != DefaultReconnect {
		t.Fatal(c)
	}
	// bt-autopower sources this file with bash: it must stay valid shell
	out, _ := exec.Command("bash", "-c", `. "`+conf+`"; echo "$DISCOVERABLE|$AUTOCONNECT_EXCLUDE"`).Output()
	if strings.TrimSpace(string(out)) != "no|AA:BB:CC:DD:EE:FF 11:22:33:44:55:66" {
		t.Fatal(string(out))
	}
	// quoting round-trips awkward values too
	_ = WriteBTConf(map[string]string{"AUTOCONNECT_EXCLUDE": `it's "odd" $x`})
	if got := ReadBTConf()["AUTOCONNECT_EXCLUDE"]; got != `it's "odd" $x` {
		t.Fatal(got)
	}
}

func TestAutoconnectToggleAndBadAddresses(t *testing.T) {
	base(t)
	var calls [][]string
	old := Bluetoothctl
	Bluetoothctl = func(_ time.Duration, args ...string) (string, int) { calls = append(calls, args); return "", 0 }
	defer func() { Bluetoothctl = old }()
	mac := "AA:BB:CC:DD:EE:FF"
	if BTAction("autoconnect", &mac, false)["ok"] != true || ReadBTConf()["AUTOCONNECT_EXCLUDE"] != mac {
		t.Fatal("exclude")
	}
	if BTAction("autoconnect", &mac, true)["ok"] != true || ReadBTConf()["AUTOCONNECT_EXCLUDE"] != "" {
		t.Fatal("include")
	}
	if !reflect.DeepEqual(calls, [][]string{{"trust", mac}}) {
		t.Fatal(calls)
	}
	bad := "AA; rm -rf ~"
	calls = nil
	if BTAction("connect", &bad, false)["ok"] != false || calls != nil {
		t.Fatal("bad address reached bluetoothctl")
	}
}

func TestProtocolViewStreamsHistoryAndLive(t *testing.T) {
	base(t)
	t.Setenv("PATH", "/nonexistent") // no busctl, checkupdates, …: collectors fail soft
	pr, pw := io.Pipe()
	inR, inW := io.Pipe()
	d := NewDaemon(pw, false)
	d.Exit = func(int) { pw.Close() }
	go d.Run(inR)
	go func() { inW.Write([]byte(`{"cmd":"view","open":true,"tab":"cpu"}` + "\n")) }()
	types := map[string]M{}
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	deadline := time.After(10 * time.Second)
	lines := make(chan string)
	go func() {
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	for types["history"] == nil || types["live"] == nil {
		select {
		case line := <-lines:
			var msg struct {
				Type string `json:"type"`
				Data any    `json:"data"`
			}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Fatal(line)
			}
			md, _ := msg.Data.(map[string]any)
			if md == nil {
				md = M{}
			}
			types[msg.Type] = md
		case <-deadline:
			t.Fatalf("got %v", types)
		}
	}
	if tctl := types["live"]["cpu"].(map[string]any)["tctl"]; tctl != 61.25 {
		t.Fatal(tctl)
	}
	inW.Close()
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"": "''", "yes": "yes", "a b": "'a b'", "it's": `'it'"'"'s'`} {
		if got := shellQuote(in); got != want {
			t.Errorf("%q: %s", in, got)
		}
	}
}
