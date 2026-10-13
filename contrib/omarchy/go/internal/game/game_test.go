package game

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fake struct {
	running []string
	active  map[string]bool
	calls   []string
	xalia   int
}

func (f *fake) run(name string, args ...string) ([]byte, error) {
	cmd := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	switch {
	case strings.Contains(cmd, "list-units"):
		return []byte(strings.Join(f.running, " loaded active running x\n") + " loaded active running x\n"), nil
	case strings.Contains(cmd, "is-active"):
		if f.active[args[len(args)-1]] {
			return []byte("active\n"), nil
		}
		return []byte("inactive\n"), errors.New("inactive")
	case name == "pgrep":
		if f.xalia == 0 {
			return []byte("0\n"), errors.New("none")
		}
		return []byte("2\n"), nil
	}
	return nil, nil
}

func has(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func TestMatches(t *testing.T) {
	for _, u := range []string{"omarchy-iphone-drive-173403.service", "omarchy-iphone-backup-3EFA401E-203547.service", "gphotos-sync.service"} {
		if !Matches(u) {
			t.Fatal(u)
		}
	}
	for _, u := range []string{"omarchy-iphone.service", "voxtype.service", "spotifyd.service", "rclone-gdrive.service", "omarchy-iphone-x.timer", "localsearch-3.service"} {
		if Matches(u) {
			t.Fatal(u)
		}
	}
}

func TestOnFreezesAndOffResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	f := &fake{running: []string{"omarchy-iphone-drive-1.service", "omarchy-iphone.service", "gphotos-sync.service", "spotifyd.service"},
		active: map[string]bool{"gphotos-sync.timer": true, "omarchy-backup.timer": true}, xalia: 2}
	s, err := On(f.run, path, true)
	if err != nil || !s.On || !s.Auto || len(s.Frozen) != 2 || len(s.Stopped) != 2 || s.Xalia != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if !has(f.calls, "systemctl --user freeze omarchy-iphone-drive-1.service") || has(f.calls, "systemctl --user freeze spotifyd.service") ||
		has(f.calls, "systemctl --user freeze omarchy-iphone.service") || !has(f.calls, "pkill -x xalia.exe") {
		t.Fatal(f.calls)
	}
	if !IsOnAt(path) {
		t.Fatal("state not saved")
	}
	f.calls = nil
	if s, err := Off(f.run, path, true); err != nil || s.On {
		t.Fatal(s, err)
	}
	if !has(f.calls, "systemctl --user thaw gphotos-sync.service") || !has(f.calls, "systemctl --user start omarchy-backup.timer") {
		t.Fatal(f.calls)
	}
}

func TestManualModeSurvivesTheGameEnding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	f := &fake{active: map[string]bool{}}
	_, _ = On(f.run, path, false) // by hand
	if s, _ := Off(f.run, path, true); !s.On {
		t.Fatal("GameMode's end hook switched off a mode turned on by hand")
	}
	if s, _ := Off(f.run, path, false); s.On {
		t.Fatal("the button must switch it off")
	}
	_, _ = On(f.run, path, true)  // a game starts
	_, _ = On(f.run, path, false) // the user presses the button while playing: keep it after the game
	if s, _ := Off(f.run, path, true); !s.On {
		t.Fatal("pressed by hand during the game: should stay on")
	}
}

func TestProtonNoXalia(t *testing.T) {
	common := t.TempDir()
	for _, d := range []string{"Proton - Experimental", "Proton 9.0", "Proton Hotfix"} {
		_ = os.MkdirAll(filepath.Join(common, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(common, "Proton - Experimental", "proton"), []byte("x"), 0o755)
	_ = os.WriteFile(filepath.Join(common, "Proton 9.0", "proton"), []byte("x"), 0o755)
	_ = os.WriteFile(filepath.Join(common, "Proton 9.0", "user_settings.py"), []byte("mine"), 0o644)
	done := ProtonNoXalia(common)
	if len(done) != 1 || !strings.Contains(done[0], "Experimental") {
		t.Fatal(done)
	}
	if b, _ := os.ReadFile(filepath.Join(common, "Proton 9.0", "user_settings.py")); string(b) != "mine" {
		t.Fatal("an existing user_settings.py was overwritten")
	}
	if b, _ := os.ReadFile(done[0]); !strings.Contains(string(b), `"PROTON_USE_XALIA": "0"`) {
		t.Fatal(string(b))
	}
}

func TestPressure(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cpu")
	_ = os.WriteFile(f, []byte("some avg10=71.47 avg60=62.44 avg300=33.98 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	if Pressure(f) != 71.47 || Pressure("/nope") != -1 {
		t.Fatal(Pressure(f))
	}
}

func TestFallsBackToTheCgroupWhenSystemdRefuses(t *testing.T) {
	root := t.TempDir()
	CgroupRoot = root
	defer func() { CgroupRoot = "/sys/fs/cgroup" }()
	cg := "/user.slice/app.slice/gphotos-sync.service"
	_ = os.MkdirAll(filepath.Join(root, cg), 0o755)
	_ = os.WriteFile(filepath.Join(root, cg, "cgroup.freeze"), []byte("0"), 0o644)
	path := filepath.Join(t.TempDir(), "state.json")
	f := &fake{running: []string{"gphotos-sync.service"}, active: map[string]bool{"gphotos-sync.service": true}}
	run := func(name string, args ...string) ([]byte, error) {
		cmd := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(cmd, "freeze gphotos") || strings.Contains(cmd, "thaw gphotos"):
			return []byte("Unit has a pending job"), errors.New("exit 1")
		case strings.Contains(cmd, "show gphotos-sync.service -p ControlGroup"):
			return []byte(cg + "\n"), nil
		}
		return f.run(name, args...)
	}
	s, err := On(run, path, false)
	if err != nil || len(s.Frozen) != 1 {
		t.Fatal(s, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, cg, "cgroup.freeze")); string(b) != "1" {
		t.Fatal("cgroup not frozen")
	}
	if _, err := Off(run, path, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, cg, "cgroup.freeze")); string(b) != "0" {
		t.Fatal("cgroup not thawed")
	}
}

func TestServicesOthersTalkToAreSlowedNotFrozen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	f := &fake{running: []string{"localsearch-3.service", "gphotos-sync.service"}, active: map[string]bool{"localsearch-3.service": true}}
	s, err := On(f.run, path, true)
	if err != nil || len(s.Slowed) != 1 || has(f.calls, "systemctl --user freeze localsearch-3.service") ||
		!has(f.calls, "systemctl --user set-property --runtime localsearch-3.service CPUWeight=1 IOWeight=1") {
		t.Fatal(s, f.calls)
	}
	f.calls = nil
	_, _ = Off(f.run, path, true)
	if !has(f.calls, "systemctl --user set-property --runtime localsearch-3.service CPUWeight= IOWeight=") {
		t.Fatal(f.calls)
	}
}
