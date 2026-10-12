// Package session saves the Hyprland session (windows, workspaces, floating
// geometry, active workspace) and reopens it at login.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var terminals = map[string]string{"foot": "foot", "footclient": "foot", "alacritty": "alacritty", "kitty": "kitty", "com.mitchellh.ghostty": "ghostty"}

// Config is ~/.config/omarchy-session/config.json.
type Config struct {
	Enabled bool     `json:"enabled"`
	Restore bool     `json:"restore"`
	Exclude []string `json:"exclude"`
	Rerun   []string `json:"rerun"`
}

func DefaultConfig() Config {
	return Config{Enabled: true, Restore: true,
		// started by autostart or not worth restoring
		Exclude: []string{"", "polkit-gnome-authentication-agent-1", "org.omarchy.osd", "xdg-desktop-portal-gtk", "Omarchy", "org.omarchy.menu", "zenity", "pinentry", "1Password"},
		Rerun:   []string{"btop", "htop", "nvtop", "nvim", "vim", "lazygit", "lazydocker", "yazi", "ranger", "mc", "tmux", "zellij", "ncdu", "impala", "wiremix", "bluetui"}}
}

func LoadConfig(path string) Config {
	c := DefaultConfig()
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// Launch is how a window comes back.
type Launch struct {
	Kind     string   `json:"kind"` // terminal | webapp | desktop | cmd
	Terminal string   `json:"terminal,omitempty"`
	Cwd      string   `json:"cwd,omitempty"`
	Run      []string `json:"run,omitempty"`
	URL      string   `json:"url,omitempty"`
	ID       string   `json:"id,omitempty"`
	Argv     []string `json:"argv,omitempty"`
}

// Window is one saved window (same JSON as the Python version).
type Window struct {
	Class          string  `json:"class"`
	Title          string  `json:"title"`
	Workspace      string  `json:"workspace"`
	Monitor        any     `json:"monitor"`
	Floating       bool    `json:"floating"`
	At             []int   `json:"at"`
	Size           []int   `json:"size"`
	Fullscreen     any     `json:"fullscreen"`
	Pinned         bool    `json:"pinned"`
	FocusHistoryID int     `json:"focusHistoryID"`
	Launch         *Launch `json:"launch"`
}

// Snapshot is session.json.
type Snapshot struct {
	Version         int              `json:"version"`
	SavedAt         int64            `json:"savedAt"`
	ActiveWorkspace string           `json:"activeWorkspace"`
	Workspaces      []map[string]any `json:"workspaces"`
	Windows         []Window         `json:"windows"`
}

var webappRe = regexp.MustCompile(`^(?:chrome|brave|msedge)-([^_]+)__(.*)-(?:Default|Profile_\d+)$`)

// WebappURL turns an Omarchy web app class (chrome-<host>__<path>-Default) into its URL.
func WebappURL(class string) string {
	m := webappRe.FindStringSubmatch(class)
	if m == nil {
		return ""
	}
	return strings.TrimRight("https://"+m[1]+"/"+strings.ReplaceAll(m[2], "_", "/"), "/") + "/"
}

var safeArg = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`)

// ShellJoin quotes like Python's shlex.join.
func ShellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a != "" && safeArg.MatchString(a) {
			out[i] = a
		} else {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'"'"'`) + "'"
		}
	}
	return strings.Join(out, " ")
}

// LaunchCommand is the command line that reopens a window ("" if none).
func LaunchCommand(l *Launch, home string) string {
	if l == nil {
		return ""
	}
	switch l.Kind {
	case "terminal":
		cwd := l.Cwd
		if cwd == "" {
			cwd = home
		}
		var cmd []string
		switch l.Terminal {
		case "foot":
			cmd = []string{"foot", "--working-directory", cwd}
		case "kitty":
			cmd = []string{"kitty", "--directory", cwd}
		case "ghostty":
			cmd = []string{"ghostty", "--working-directory=" + cwd}
		default:
			cmd = []string{l.Terminal, "--working-directory", cwd}
		}
		if len(l.Run) > 0 {
			cmd = append(append(cmd, "-e"), l.Run...)
		}
		return ShellJoin(cmd)
	case "webapp":
		return ShellJoin([]string{"omarchy-launch-webapp", l.URL})
	case "desktop":
		return l.ID // uwsm-app runs desktop entry ids
	case "cmd":
		return ShellJoin(l.Argv)
	}
	return ""
}

// DesktopEntry is the part of a .desktop file that matters here.
type DesktopEntry struct{ WMClass, Exec string }

// ParseDesktop reads [Desktop Entry]; ok is false for hidden entries without StartupWMClass.
func ParseDesktop(text string) (DesktopEntry, bool) {
	in := false
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "[") {
			in = l == "[Desktop Entry]"
			continue
		}
		if !in || l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if k, v, ok := strings.Cut(l, "="); ok {
			k = strings.TrimSpace(k)
			if _, seen := kv[k]; !seen {
				kv[k] = strings.TrimSpace(v)
			}
		}
	}
	if len(kv) == 0 {
		return DesktopEntry{}, false
	}
	if strings.EqualFold(kv["NoDisplay"], "true") && kv["StartupWMClass"] == "" {
		return DesktopEntry{}, false
	}
	return DesktopEntry{WMClass: kv["StartupWMClass"], Exec: kv["Exec"]}, true
}

// DesktopEntries reads the application folders (first one wins per file name).
func DesktopEntries(home string) map[string]DesktopEntry {
	out := map[string]DesktopEntry{}
	for _, d := range []string{filepath.Join(home, ".local/share/applications"), "/usr/share/applications",
		filepath.Join(home, ".local/share/flatpak/exports/share/applications"), "/var/lib/flatpak/exports/share/applications"} {
		files, _ := filepath.Glob(filepath.Join(d, "*.desktop"))
		sort.Strings(files)
		for _, f := range files {
			name := filepath.Base(f)
			if _, ok := out[name]; ok {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if e, ok := ParseDesktop(string(b)); ok {
				out[name] = e
			}
		}
	}
	return out
}

// DesktopFor finds the entry of a window class: StartupWMClass first, then the file name.
func DesktopFor(class string, entries map[string]DesktopEntry) string {
	low := strings.ToLower(class)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if e := entries[n]; e.WMClass != "" && strings.ToLower(e.WMClass) == low {
			return n
		}
	}
	for _, n := range names {
		stem := strings.ToLower(strings.TrimSuffix(n, ".desktop"))
		parts := strings.Split(stem, ".")
		if stem == low || parts[len(parts)-1] == low {
			return n
		}
	}
	return ""
}

// TerminalOf maps a window class to a known terminal ("" if not one).
func TerminalOf(class string) string { return terminals[strings.ToLower(class)] }

// PlannedLaunch is one command to run at restore, with the window it is for.
type PlannedLaunch struct {
	Cmd    string
	Window Window
}

// Plan decides what to launch: every terminal window is its own launch; other
// apps launch once per command, and not when already open or running.
// queues holds, per class, the saved places new windows are sent to.
func Plan(snap Snapshot, already map[string]bool, running map[string]bool, home string) ([]PlannedLaunch, map[string][]Window) {
	windows := append([]Window{}, snap.Windows...)
	sort.SliceStable(windows, func(i, j int) bool { return windows[i].FocusHistoryID > windows[j].FocusHistoryID })
	queues := map[string][]Window{}
	var launches []PlannedLaunch
	seen := map[string]bool{}
	for _, w := range windows {
		queues[w.Class] = append(queues[w.Class], w)
		cmd := LaunchCommand(w.Launch, home)
		if cmd == "" || seen[cmd] {
			continue
		}
		if w.Launch.Kind == "terminal" {
			launches = append(launches, PlannedLaunch{cmd, w})
			continue
		}
		seen[cmd] = true
		if already[w.Class] || (w.Launch.Kind == "cmd" && running[cmd]) {
			continue
		}
		launches = append(launches, PlannedLaunch{cmd, w})
	}
	return launches, queues
}

// LuaStr quotes a string for a Hyprland Lua dispatcher.
func LuaStr(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// PlaceCommands are the dispatchers that put a new window where it was.
func PlaceCommands(window, currentWS string, t Window) []string {
	var out []string
	if t.Workspace != "" && t.Workspace != currentWS {
		out = append(out, fmt.Sprintf("hl.dsp.window.move({ workspace = %s, follow = false, window = %s })", LuaStr(t.Workspace), LuaStr(window)))
	}
	if t.Floating {
		out = append(out, fmt.Sprintf(`hl.dsp.window.float({ action = "enable", window = %s })`, LuaStr(window)))
		if len(t.Size) == 2 {
			out = append(out, fmt.Sprintf("hl.dsp.window.resize({ x = %d, y = %d, window = %s })", t.Size[0], t.Size[1], LuaStr(window)))
		}
		if len(t.At) == 2 {
			out = append(out, fmt.Sprintf("hl.dsp.window.move({ x = %d, y = %d, window = %s })", t.At[0], t.At[1], LuaStr(window)))
		}
	}
	return out
}

// Saver decides when to save: 3 s after the last change, except during a mass
// close (Omarchy closes every window before logout/reboot): then saving is
// frozen 30 s so the session on disk stays the one from before.
type Saver struct {
	WindowsNow  int
	lastChange  float64
	hasChange   bool
	closes      []float64
	frozenUntil float64
}

// Event records a Hyprland event; returns true if it changes the session.
func (s *Saver) Event(name string, now float64) {
	switch name {
	case "openwindow", "closewindow", "movewindow", "movewindowv2", "changefloatingmode", "workspace", "workspacev2", "fullscreen", "windowtitlev2":
		if name == "closewindow" {
			s.closes = append(s.closes, now)
		}
		s.lastChange, s.hasChange = now, true
	}
}

// Due reports whether to save now; froze is true when a mass close began.
func (s *Saver) Due(now float64) (save, froze bool) {
	kept := s.closes[:0]
	for _, t := range s.closes {
		if now-t < 10 {
			kept = append(kept, t)
		}
	}
	s.closes = kept
	if len(s.closes) >= 3 && float64(len(s.closes)) >= 0.5*float64(max(1, s.WindowsNow)) {
		s.frozenUntil = now + 30
		s.closes = s.closes[:0]
		froze = true
	}
	if s.hasChange && now-s.lastChange >= 3 && now >= s.frozenUntil {
		s.hasChange = false
		return true, froze
	}
	return false, froze
}

// ParentMap reads /proc: pid -> children.
func ParentMap() map[int][]int {
	kids := map[int][]int{}
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + d.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		i := strings.LastIndex(s, ")")
		f := strings.Fields(s[i+1:])
		if len(f) < 2 {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		kids[ppid] = append(kids[ppid], pid)
	}
	for k := range kids {
		sort.Ints(kids[k])
	}
	return kids
}

// Cmdline of a process.
func Cmdline(pid int) []string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range strings.Split(string(b), "\x00") {
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

// Cwd of a process.
func Cwd(pid int) string {
	p, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	return p
}

// TerminalState: the directory of the shell inside a terminal and the program
// in front of it if it is re-runnable.
func TerminalState(pid int, kids map[int][]int, rerun map[string]bool) (string, []string) {
	shells := kids[pid]
	if len(shells) == 0 {
		return Cwd(pid), nil
	}
	shell := shells[0]
	cwd := Cwd(shell)
	for _, child := range kids[shell] {
		cmd := Cmdline(child)
		if len(cmd) > 0 && rerun[filepath.Base(cmd[0])] {
			if c := Cwd(child); c != "" {
				cwd = c
			}
			return cwd, cmd
		}
	}
	return cwd, nil
}
