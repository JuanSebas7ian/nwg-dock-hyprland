// Package game is the one-button game mode: while playing, background work that competes with the game for CPU,
// disk and upload bandwidth is frozen (cgroup freeze: no CPU, no I/O, progress kept) and the timers that would
// start more of it are stopped; everything resumes afterwards. GameMode's start/end hooks switch it
// automatically for games launched with gamemoderun; the bar button switches it by hand.
package game

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Runner runs a command and returns its output.
type Runner func(name string, args ...string) ([]byte, error)

func ExecRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// Background services frozen while playing (prefixes match transient units like omarchy-iphone-drive-…).
var FreezePrefixes = []string{
	"omarchy-iphone-",        // device backups and their Google Drive uploads
	"gphotos-sync.service",   // Google Photos uploads
	"omarchy-backup.service", // the daily restic backup
	"localsearch-3.service",  // GNOME's file indexer
	"opencode-ollama-sync.service",
}

// Timers and path units that would start more background work; stopped while playing, started again after.
var StopUnits = []string{"gphotos-sync.timer", "gphotos-sync.path", "omarchy-backup.timer", "opencode-ollama-sync.path", "opencode-ollama-sync.timer"}

// State is ~/.local/state/omarchy-game/state.json.
type State struct {
	On      bool     `json:"on"`
	Auto    bool     `json:"auto"` // switched on by GameMode (a game started), not by hand
	Since   string   `json:"since,omitempty"`
	Frozen  []string `json:"frozen"`
	Stopped []string `json:"stopped"`
	Xalia   int      `json:"xaliaStopped"`
}

func StatePath() string {
	home, _ := os.UserHomeDir()
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return filepath.Join(s, "omarchy-game", "state.json")
	}
	return filepath.Join(home, ".local", "state", "omarchy-game", "state.json")
}

func Load(path string) State {
	var s State
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Matches says whether a unit is background work to freeze.
func Matches(unit string) bool {
	for _, p := range FreezePrefixes {
		if strings.HasSuffix(p, "-") && strings.HasPrefix(unit, p) && strings.HasSuffix(unit, ".service") {
			// never the iPhone watcher itself: it is idle and only starts jobs (and skips them in game mode)
			return unit != "omarchy-iphone.service"
		}
		if unit == p {
			return true
		}
	}
	return false
}

// running lists the user's running services.
func running(run Runner) []string {
	b, err := run("systemctl", "--user", "list-units", "--type=service", "--state=running,activating", "--no-legend", "--plain") // oneshot jobs (gphotos-sync) are "activating" while they work
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}

func active(run Runner, unit string) bool {
	b, _ := run("systemctl", "--user", "is-active", unit)
	return strings.TrimSpace(string(b)) == "active"
}

// On freezes background work. auto=true when GameMode starts a game.
func On(run Runner, path string, auto bool) (State, error) {
	s := Load(path)
	if s.On {
		if !auto && s.Auto {
			s.Auto = false // switched on by hand while a game runs: it stays on after the game
			return s, save(path, s)
		}
		return s, nil
	}
	s = State{On: true, Auto: auto, Since: time.Now().Format(time.RFC3339)}
	for _, u := range StopUnits {
		if active(run, u) {
			if _, err := run("systemctl", "--user", "stop", u); err == nil {
				s.Stopped = append(s.Stopped, u)
			}
		}
	}
	for _, u := range running(run) {
		if Matches(u) {
			if _, err := run("systemctl", "--user", "freeze", u); err == nil || freezeCgroup(run, u, true) == nil {
				s.Frozen = append(s.Frozen, u)
			}
		}
	}
	// Proton's UI-automation helper (gamepad navigation for launchers) costs CPU in games that do not need it
	if b, err := run("pgrep", "-c", "-x", "xalia.exe"); err == nil {
		n := 0
		for _, r := range strings.TrimSpace(string(b)) {
			if r >= '0' && r <= '9' {
				n = n*10 + int(r-'0')
			}
		}
		if n > 0 {
			_, _ = run("pkill", "-x", "xalia.exe")
			s.Xalia = n
		}
	}
	sort.Strings(s.Frozen)
	return s, save(path, s)
}

// Off resumes everything. auto=true when GameMode ends a game: a mode switched on by hand stays on.
func Off(run Runner, path string, auto bool) (State, error) {
	s := Load(path)
	if !s.On {
		return s, nil
	}
	if auto && !s.Auto {
		return s, nil
	}
	var errs []string
	for _, u := range s.Frozen {
		_, err := run("systemctl", "--user", "thaw", u)
		if cerr := freezeCgroup(run, u, false); err != nil && cerr != nil && active(run, u) {
			errs = append(errs, u)
		}
	}
	for _, u := range s.Stopped {
		if _, err := run("systemctl", "--user", "start", u); err != nil {
			errs = append(errs, u)
		}
	}
	s = State{}
	if err := save(path, s); err != nil {
		return s, err
	}
	if len(errs) > 0 {
		return s, errors.New("could not resume: " + strings.Join(errs, ", "))
	}
	return s, nil
}

// CgroupRoot is where the unified cgroup hierarchy is mounted; tests replace it.
var CgroupRoot = "/sys/fs/cgroup"

// freezeCgroup freezes or thaws a unit's cgroup directly: systemd refuses to freeze a unit with a pending job,
// which is the case of a oneshot service (gphotos-sync) while it works. The user's cgroups are delegated to it.
func freezeCgroup(run Runner, unit string, freeze bool) error {
	b, err := run("systemctl", "--user", "show", unit, "-p", "ControlGroup", "--value")
	cg := strings.TrimSpace(string(b))
	if err != nil || cg == "" {
		return errors.New("no cgroup for " + unit)
	}
	v := "0"
	if freeze {
		v = "1"
	}
	return os.WriteFile(filepath.Join(CgroupRoot, cg, "cgroup.freeze"), []byte(v), 0o644)
}

// IsOn is read by other tools (the iPhone watcher starts no backups while playing).
func IsOn() bool { return IsOnAt(StatePath()) }

func IsOnAt(path string) bool { return Load(path).On }

// ProtonNoXalia writes user_settings.py in each installed Proton so new games start without Xalia; an existing
// user_settings.py is left alone (returns the files written).
func ProtonNoXalia(steamCommon string) []string {
	var done []string
	dirs, _ := filepath.Glob(filepath.Join(steamCommon, "Proton*"))
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "proton")); err != nil {
			continue
		}
		f := filepath.Join(d, "user_settings.py")
		if _, err := os.Stat(f); err == nil {
			continue
		}
		body := "# written by omarchy-game: Xalia (gamepad navigation for game launchers) off, it costs CPU in games\n" +
			"# that do not need it. Delete this file to go back to Proton's default.\nuser_settings = {\n    \"PROTON_USE_XALIA\": \"0\",\n}\n"
		if os.WriteFile(f, []byte(body), 0o644) == nil {
			done = append(done, f)
		}
	}
	return done
}

// Pressure reads the share of time tasks waited for CPU (avg10 of /proc/pressure/cpu "some").
func Pressure(file string) float64 {
	b, err := os.ReadFile(file)
	if err != nil {
		return -1
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "some ") {
			for _, f := range strings.Fields(l) {
				if v, ok := strings.CutPrefix(f, "avg10="); ok {
					if x, err := strconv.ParseFloat(v, 64); err == nil {
						return x
					}
				}
			}
		}
	}
	return -1
}
