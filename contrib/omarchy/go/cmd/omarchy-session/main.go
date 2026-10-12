// omarchy-session saves and restores the Hyprland session: open windows,
// their workspaces and floating geometry, and the active workspace.
//
//	omarchy-session daemon            restore the last session, then keep saving it (systemd user service)
//	omarchy-session save              snapshot now
//	omarchy-session restore [--dry-run]
//	omarchy-session show              what would be restored
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"omarchy/internal/session"
)

var home, state, sessionFile, configFile string

func init() {
	home, _ = os.UserHomeDir()
	st := os.Getenv("XDG_STATE_HOME")
	if st == "" {
		st = filepath.Join(home, ".local", "state")
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	state = filepath.Join(st, "omarchy-session")
	sessionFile = filepath.Join(state, "session.json")
	configFile = filepath.Join(cfg, "omarchy-session", "config.json")
}

func logf(f string, a ...any) { fmt.Fprintf(os.Stderr, "omarchy-session: "+f+"\n", a...) }

func hyprJSON(what string, out any) {
	b, err := exec.Command("hyprctl", what, "-j").Output()
	if err == nil {
		_ = json.Unmarshal(b, out)
	}
}

// hl.dsp.* returns a dispatcher ("ok"); plain hl.exec_cmd left "expected a dispatcher" in configerrors.
func dispatch(lua string) { _ = exec.Command("hyprctl", "dispatch", lua).Run() }

func set(list []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range list {
		m[x] = true
	}
	return m
}

func which(cmd string) bool {
	if strings.Contains(cmd, "/") {
		_, err := os.Stat(cmd)
		return err == nil
	}
	_, err := exec.LookPath(cmd)
	return err == nil
}

func snapshot() session.Snapshot {
	cfg := session.LoadConfig(configFile)
	exclude, rerun := set(cfg.Exclude), set(cfg.Rerun)
	kids := session.ParentMap()
	entries := session.DesktopEntries(home)
	var clients []struct {
		Mapped, Hidden, Floating, Pinned bool
		Class, InitialClass, Title       string
		PID                              int `json:"pid"`
		Workspace                        struct{ Name string }
		Monitor, Fullscreen              any
		At, Size                         []int
		FocusHistoryID                   int `json:"focusHistoryID"`
	}
	hyprJSON("clients", &clients)
	snap := session.Snapshot{Version: 1, SavedAt: time.Now().Unix(), Windows: []session.Window{}, Workspaces: []map[string]any{}}
	for _, c := range clients {
		if !c.Mapped || c.Hidden {
			continue
		}
		cls := c.Class
		if cls == "" {
			cls = c.InitialClass
		}
		if exclude[cls] {
			continue
		}
		w := session.Window{Class: cls, Title: c.Title, Workspace: c.Workspace.Name, Monitor: c.Monitor, Floating: c.Floating,
			At: c.At, Size: c.Size, Fullscreen: c.Fullscreen, Pinned: c.Pinned, FocusHistoryID: c.FocusHistoryID}
		if term := session.TerminalOf(cls); term != "" {
			cwd, front := session.TerminalState(c.PID, kids, rerun)
			w.Launch = &session.Launch{Kind: "terminal", Terminal: term, Cwd: cwd, Run: front}
		} else if url := session.WebappURL(cls); url != "" {
			w.Launch = &session.Launch{Kind: "webapp", URL: url}
		} else if d := session.DesktopFor(cls, entries); d != "" {
			w.Launch = &session.Launch{Kind: "desktop", ID: d}
		} else if cmd := session.Cmdline(c.PID); len(cmd) > 0 && which(cmd[0]) {
			w.Launch = &session.Launch{Kind: "cmd", Argv: cmd}
		}
		snap.Windows = append(snap.Windows, w)
	}
	var active struct{ Name string }
	hyprJSON("activeworkspace", &active)
	snap.ActiveWorkspace = active.Name
	if snap.ActiveWorkspace == "" {
		snap.ActiveWorkspace = "1"
	}
	var wss []struct {
		Name    string
		Monitor any
	}
	hyprJSON("workspaces", &wss)
	for _, ws := range wss {
		snap.Workspaces = append(snap.Workspaces, map[string]any{"name": ws.Name, "monitor": ws.Monitor})
	}
	return snap
}

// writeSession keeps the previous one as session.prev.json when windows changed,
// and fsyncs: after a freeze and a hard reset the last session must be on disk.
func writeSession(snap session.Snapshot) {
	_ = os.MkdirAll(state, 0o755)
	if b, err := os.ReadFile(sessionFile); err == nil {
		var old session.Snapshot
		if json.Unmarshal(b, &old) == nil {
			ob, _ := json.Marshal(old.Windows)
			nb, _ := json.Marshal(snap.Windows)
			if string(ob) != string(nb) {
				_ = os.Rename(sessionFile, filepath.Join(state, "session.prev.json"))
			}
		}
	}
	b, _ := json.MarshalIndent(snap, "", " ")
	tmp := sessionFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	_, _ = f.Write(b)
	_ = f.Sync()
	f.Close()
	_ = os.Rename(tmp, sessionFile)
}

func runningCmdlines() map[string]bool {
	out := map[string]bool{}
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		var pid int
		if _, err := fmt.Sscanf(d.Name(), "%d", &pid); err != nil {
			continue
		}
		if cmd := session.Cmdline(pid); len(cmd) > 0 {
			out[session.ShellJoin(cmd)] = true
			out[filepath.Base(cmd[0])] = true
		}
	}
	return out
}

func socket2() (net.Conn, error) {
	run := os.Getenv("XDG_RUNTIME_DIR")
	if run == "" {
		run = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return net.Dial("unix", filepath.Join(run, "hypr", os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"), ".socket2.sock"))
}

func restore(dry bool) {
	b, err := os.ReadFile(sessionFile)
	var snap session.Snapshot
	if err != nil || json.Unmarshal(b, &snap) != nil {
		logf("no saved session")
		return
	}
	var clients []struct{ Class string }
	hyprJSON("clients", &clients)
	already := map[string]bool{}
	for _, c := range clients {
		already[c.Class] = true
	}
	launches, queues := session.Plan(snap, already, runningCmdlines(), home)
	if dry {
		for _, l := range launches {
			fmt.Printf("ws %4s  %-28s %s\n", l.Window.Workspace, l.Window.Class, l.Cmd)
		}
		fmt.Printf("\nactive workspace: %s\n", snap.ActiveWorkspace)
		return
	}
	events, err := socket2()
	if err != nil {
		logf("no Hyprland event socket: %v", err)
	}
	for _, l := range launches {
		dispatch(fmt.Sprintf("hl.dsp.exec_cmd(%s, { workspace = %s })", session.LuaStr("uwsm-app -- "+l.Cmd), session.LuaStr(l.Window.Workspace+" silent")))
		time.Sleep(250 * time.Millisecond)
	}
	pending := 0
	for _, q := range queues {
		pending += len(q)
	}
	if events != nil {
		// Send each new window to the next saved place of its class.
		deadline := time.Now().Add(45 * time.Second)
		_ = events.SetReadDeadline(deadline)
		sc := bufio.NewScanner(events)
		for pending > 0 && sc.Scan() {
			ev, data, _ := strings.Cut(sc.Text(), ">>")
			if ev != "openwindow" {
				continue
			}
			f := strings.SplitN(data, ",", 4)
			for len(f) < 4 {
				f = append(f, "")
			}
			addr, ws, cls, title := f[0], f[1], f[2], f[3]
			q := queues[cls]
			if len(q) == 0 {
				continue
			}
			idx := 0
			for i, t := range q {
				if t.Title == title {
					idx = i
					break
				}
			}
			target := q[idx]
			queues[cls] = append(q[:idx], q[idx+1:]...)
			pending--
			for _, c := range session.PlaceCommands("address:0x"+addr, ws, target) {
				dispatch(c)
			}
		}
		events.Close()
	}
	dispatch(fmt.Sprintf("hl.dsp.focus({ workspace = %s })", session.LuaStr(snap.ActiveWorkspace)))
	logf("restored %d app(s)", len(launches))
}

func daemon() {
	cfg := session.LoadConfig(configFile)
	if !cfg.Enabled {
		logf("disabled in config")
		return
	}
	// Restore once per Hyprland instance: a service restart must not reopen everything.
	marker := filepath.Join(state, "restored-instance")
	instance := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	b, _ := os.ReadFile(marker)
	if cfg.Restore && strings.TrimSpace(string(b)) != instance {
		time.Sleep(4 * time.Second) // let autostart apps (and their windows) come up first
		restore(false)
	}
	_ = os.MkdirAll(state, 0o755)
	_ = os.WriteFile(marker, []byte(instance), 0o644)
	snap := snapshot()
	writeSession(snap)
	saver := &session.Saver{WindowsNow: len(snap.Windows)}
	conn, err := socket2()
	if err != nil {
		logf("no Hyprland event socket: %v", err)
		return
	}
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines) // Hyprland is gone: keep the last saved session
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	now := func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return
			}
			ev, _, _ := strings.Cut(l, ">>")
			saver.Event(ev, now())
		case <-tick.C:
		}
		save, froze := saver.Due(now())
		if froze {
			logf("mass close: keeping the saved session")
		}
		if save {
			s := snapshot()
			saver.WindowsNow = len(s.Windows)
			writeSession(s)
		}
	}
}

func main() {
	cmd := "show"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "daemon":
		daemon()
	case "save":
		s := snapshot()
		writeSession(s)
		fmt.Printf("saved %d window(s) to %s\n", len(s.Windows), sessionFile)
	case "restore":
		restore(len(os.Args) > 2 && os.Args[2] == "--dry-run")
	case "show":
		restore(true)
	default:
		fmt.Fprintln(os.Stderr, "usage: omarchy-session daemon | save | restore [--dry-run] | show")
		os.Exit(2)
	}
}
