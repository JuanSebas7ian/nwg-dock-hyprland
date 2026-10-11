package drivers

import (
	"strings"
	"testing"
)

func baseEnv() Env {
	return Env{
		Installed: map[string]string{"linux": "7.2.3.arch1-3", "linux-headers": "7.2.3.arch1-3", "nvidia-open-dkms": "610.57.04-1",
			"nvidia-utils": "610.57.04-1", "linux-firmware": "20260810-2", "mesa": "1:26.2.2-1", "google-chrome": "155-1"},
		RunningKernel: "7.2.3-arch1-3", LoadedNVIDIA: "610.57.04",
		DKMS: "nvidia/610.57.04, 7.2.3-arch1-3, x86_64: installed\nhid-xpadneo/v0.10.4, 7.2.3-arch1-3, x86_64: installed\n",
	}
}

func comp(st Status, name string) Component {
	for _, c := range st.Components {
		if c.Name == name {
			return c
		}
	}
	return Component{}
}

func TestAllUpToDate(t *testing.T) {
	e := baseEnv()
	e.Updates = []Pkg{{Name: "google-chrome", Installed: "155-1", Available: "155-2"}}
	st := Build(e)
	if st.State != "ok" || !strings.HasPrefix(st.Summary, "All drivers") || len(st.OtherUpdates) != 1 || st.TotalUpdates != 1 || !st.DKMSOk {
		t.Fatalf("%+v", st)
	}
	for _, c := range st.Components {
		if c.Status != "ok" || len(c.Packages) == 0 {
			t.Fatalf("%+v", c)
		}
	}
	if len(st.Components) != 4 { // kernel, nvidia, firmware, graphics (only installed groups)
		t.Fatalf("%d components", len(st.Components))
	}
}

func TestDriverUpdatesAndFirmware(t *testing.T) {
	e := baseEnv()
	e.Updates = []Pkg{{Name: "nvidia-open-dkms", Installed: "610.57.04-1", Available: "615.10-1"}, {Name: "linux", Installed: "7.2.3.arch1-3", Available: "7.2.4.arch1-1"}}
	e.Firmware = []Firmware{{Device: "WD_BLACK SN770", Current: "731100WD", Available: "731130WD"}}
	st := Build(e)
	if st.State != "update" || st.Summary != "2 driver packages and 1 firmware update available" {
		t.Fatalf("%q %q", st.State, st.Summary)
	}
	if c := comp(st, "NVIDIA (RTX 3060)"); c.Status != "update" || c.Packages[0].Available != "615.10-1" {
		t.Fatalf("%+v", c)
	}
	if len(st.OtherUpdates) != 0 {
		t.Fatal("driver updates counted as apps")
	}
}

func TestRebootPendingWhenLoadedIsOlder(t *testing.T) {
	e := baseEnv()
	e.RunningKernel = "7.2.2-arch1-1"
	e.LoadedNVIDIA = "605.1"
	st := Build(e)
	if st.State != "reboot" || comp(st, "Kernel").Status != "reboot" || !strings.Contains(comp(st, "NVIDIA (RTX 3060)").Note, "loaded 605.1") {
		t.Fatalf("%+v", st)
	}
}

func TestDKMSNotBuilt(t *testing.T) {
	e := baseEnv()
	e.DKMS = "nvidia/610.57.04, 7.2.3-arch1-3, x86_64: built\n"
	if st := Build(e); st.DKMSOk || st.State != "update" || !strings.Contains(st.Summary, "DKMS") {
		t.Fatalf("%+v", st)
	}
}

func TestBIOSInSummary(t *testing.T) {
	e := baseEnv()
	e.BIOS = &BIOS{Installed: "1402", Latest: "1804", Beta: true, Newer: true}
	if st := Build(e); !strings.HasSuffix(st.Summary, "BIOS 1804 (beta) available") || st.State != "ok" {
		t.Fatalf("%q", st.Summary)
	}
	if !newerVersion("1804", "1402") || newerVersion("1402", "1402") || newerVersion("0999", "1402") || newerVersion("", "1") || !newerVersion("10001", "9999") {
		t.Fatal("BIOS version compare")
	}
}

func TestParsers(t *testing.T) {
	up := ParseCheckupdates("aether 4.32.0-1 -> 4.32.0-2\nnoise line\nlinux 7.2.3.arch1-3 -> 7.2.4.arch1-1 [ignored]\n")
	if strings.Join(SortedNames(up), ",") != "aether,linux" || up[1].Available != "7.2.4.arch1-1" {
		t.Fatalf("%+v", up)
	}
	fw := ParseFwupd([]byte(`{"Devices":[{"Name":"SN770","Version":"1","Releases":[{"Version":"2","Summary":"fix"}]},{"Name":"none","Version":"1","Releases":[]}]}`))
	if len(fw) != 1 || fw[0].Available != "2" {
		t.Fatalf("%+v", fw)
	}
	if len(ParseFwupd([]byte("garbage"))) != 0 {
		t.Fatal("garbage parsed")
	}
	if kernelVersion("7.2.3.arch1-3") != "7.2.3-arch1-3" || pkgBase("610.57.04-1") != "610.57.04" {
		t.Fatal("version helpers")
	}
}
