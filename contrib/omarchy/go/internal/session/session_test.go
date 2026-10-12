package session

import (
	"reflect"
	"strings"
	"testing"
)

func TestWebappURL(t *testing.T) {
	cases := map[string]string{
		"chrome-web.whatsapp.com__-Default":           "https://web.whatsapp.com/",
		"chrome-app.hey.com__calendar_week-Profile_2": "https://app.hey.com/calendar/week/",
		"brave-x.com__-Default":                       "https://x.com/",
		"firefox":                                     "",
	}
	for in, want := range cases {
		if got := WebappURL(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestShellJoinLikeShlex(t *testing.T) {
	if got := ShellJoin([]string{"foot", "--working-directory", "/home/u/My Docs", "it's", ""}); got != `foot --working-directory '/home/u/My Docs' 'it'"'"'s' ''` {
		t.Fatal(got)
	}
}

func TestLaunchCommand(t *testing.T) {
	cases := []struct {
		l    *Launch
		want string
	}{
		{&Launch{Kind: "terminal", Terminal: "foot", Cwd: "/src", Run: []string{"nvim", "x.go"}}, "foot --working-directory /src -e nvim x.go"},
		{&Launch{Kind: "terminal", Terminal: "kitty"}, "kitty --directory /home/u"},
		{&Launch{Kind: "terminal", Terminal: "ghostty", Cwd: "/a"}, "ghostty --working-directory=/a"},
		{&Launch{Kind: "terminal", Terminal: "alacritty", Cwd: "/a"}, "alacritty --working-directory /a"},
		{&Launch{Kind: "webapp", URL: "https://x.com/"}, "omarchy-launch-webapp https://x.com/"},
		{&Launch{Kind: "desktop", ID: "obsidian.desktop"}, "obsidian.desktop"},
		{&Launch{Kind: "cmd", Argv: []string{"/opt/app/app", "--flag"}}, "/opt/app/app --flag"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := LaunchCommand(c.l, "/home/u"); got != c.want {
			t.Errorf("%+v -> %q, want %q", c.l, got, c.want)
		}
	}
}

func TestDesktopParsingAndMatching(t *testing.T) {
	e, ok := ParseDesktop("[Desktop Entry]\nName=Obsidian\nExec=obsidian %U\nStartupWMClass=obsidian\n[Desktop Action new]\nExec=other\n")
	if !ok || e.WMClass != "obsidian" || e.Exec != "obsidian %U" {
		t.Fatalf("%+v %v", e, ok)
	}
	if _, ok := ParseDesktop("[Desktop Entry]\nNoDisplay=true\nExec=x\n"); ok {
		t.Fatal("hidden entry without a class accepted")
	}
	entries := map[string]DesktopEntry{"md.obsidian.Obsidian.desktop": {WMClass: "obsidian"}, "org.gnome.Nautilus.desktop": {}, "foot.desktop": {}}
	if DesktopFor("Obsidian", entries) != "md.obsidian.Obsidian.desktop" || DesktopFor("nautilus", entries) != "org.gnome.Nautilus.desktop" || DesktopFor("zzz", entries) != "" {
		t.Fatal("DesktopFor")
	}
}

func TestPlan(t *testing.T) {
	term := func(cwd string, focus int) Window {
		return Window{Class: "foot", Workspace: "1", FocusHistoryID: focus, Launch: &Launch{Kind: "terminal", Terminal: "foot", Cwd: cwd}}
	}
	chrome := func(ws string) Window {
		return Window{Class: "chromium", Workspace: ws, Launch: &Launch{Kind: "desktop", ID: "chromium.desktop"}}
	}
	snap := Snapshot{Windows: []Window{term("/a", 2), term("/a", 1), chrome("2"), chrome("3"),
		{Class: "slack", Launch: &Launch{Kind: "desktop", ID: "slack.desktop"}},
		{Class: "tool", Launch: &Launch{Kind: "cmd", Argv: []string{"tool"}}},
		{Class: "ghost"}}}
	launches, queues := Plan(snap, map[string]bool{"slack": true}, map[string]bool{"tool": true}, "/h")
	var cmds []string
	for _, l := range launches {
		cmds = append(cmds, l.Cmd)
	}
	want := []string{"foot --working-directory /a", "foot --working-directory /a", "chromium.desktop"}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("%v", cmds)
	}
	if len(queues["chromium"]) != 2 || len(queues["foot"]) != 2 {
		t.Fatalf("%v", queues)
	}
}

func TestPlaceCommands(t *testing.T) {
	c := PlaceCommands("address:0x1", "1", Window{Workspace: "3", Floating: true, Size: []int{800, 600}, At: []int{10, 20}})
	if len(c) != 4 || !strings.Contains(c[0], `workspace = "3"`) || !strings.Contains(c[2], "x = 800, y = 600") || !strings.Contains(c[3], "x = 10, y = 20") {
		t.Fatalf("%v", c)
	}
	if len(PlaceCommands("a", "2", Window{Workspace: "2"})) != 0 {
		t.Fatal("same workspace, tiled: nothing to do")
	}
	if LuaStr(`a"b\c`) != `"a\"b\\c"` {
		t.Fatal("LuaStr")
	}
}

func TestSaver(t *testing.T) {
	// a single change is saved 3 s later
	s := &Saver{WindowsNow: 5}
	s.Event("movewindow", 0)
	if ok, _ := s.Due(2); ok {
		t.Fatal("debounce")
	}
	if ok, _ := s.Due(3); !ok {
		t.Fatal("not saved after 3 s")
	}
	if ok, _ := s.Due(10); ok {
		t.Fatal("saved twice")
	}
	// shutdown closes everything: the saved session is kept
	s = &Saver{WindowsNow: 4}
	for i := 0; i < 4; i++ {
		s.Event("closewindow", float64(i)*0.2)
	}
	ok, froze := s.Due(1)
	if !froze || ok {
		t.Fatal("mass close must freeze saving")
	}
	if ok, _ := s.Due(20); ok {
		t.Fatal("saved during the freeze")
	}
	// a deliberate close after the freeze is saved
	s.Event("closewindow", 40)
	if ok, _ := s.Due(44); !ok {
		t.Fatal("deliberate close not saved after the freeze")
	}
	// ignore unrelated events
	s = &Saver{}
	s.Event("activewindow", 0)
	if ok, _ := s.Due(10); ok {
		t.Fatal("unrelated event saved")
	}
}
