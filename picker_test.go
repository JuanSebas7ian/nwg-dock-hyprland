package main

import (
	"os"
	"path/filepath"
	"testing"
)

const firefoxEntry = `[Desktop Entry]
Name=Firefox
Name[es]=Navegador Firefox
Icon=firefox
Type=Application
Exec=firefox %u

[Desktop Action new-window]
Name=New Window
NoDisplay=true
`

func TestParseDesktopEntry(t *testing.T) {
	cases := []struct {
		name, content, lang string
		wantName, wantIcon  string
		wantVisible         bool
	}{
		{"plain", firefoxEntry, "", "Firefox", "firefox", true},
		{"localized", firefoxEntry, "es", "Navegador Firefox", "firefox", true},
		{"other language", firefoxEntry, "fr", "Firefox", "firefox", true},
		{"hidden", "[Desktop Entry]\nName=X\nType=Application\nNoDisplay=true\n", "", "X", "", false},
		{"deleted", "[Desktop Entry]\nName=X\nType=Application\nHidden=true\n", "", "X", "", false},
		{"link", "[Desktop Entry]\nName=X\nType=Link\n", "", "X", "", false},
		{"no name", "[Desktop Entry]\nType=Application\n", "", "", "", false},
		{"spaces and comments", "# comment\n[Desktop Entry]\nName = Spaced \nType=Application\n", "", "Spaced", "", true},
	}
	for _, c := range cases {
		name, icon, visible := parseDesktopEntry(c.content, c.lang)
		if name != c.wantName || icon != c.wantIcon || visible != c.wantVisible {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", c.name, name, icon, visible, c.wantName, c.wantIcon, c.wantVisible)
		}
	}
}

func writeDesktop(t *testing.T, dir, id, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id+".desktop"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDesktopApps(t *testing.T) {
	user, system := t.TempDir(), t.TempDir()
	writeDesktop(t, system, "firefox", firefoxEntry)
	writeDesktop(t, system, "zed", "[Desktop Entry]\nName=zed editor\nType=Application\n")
	writeDesktop(t, system, "hidden", "[Desktop Entry]\nName=Hidden\nType=Application\nNoDisplay=true\n")
	writeDesktop(t, system, "org.gnome.Nautilus", "[Desktop Entry]\nName=Files\nIcon=nautilus\nType=Application\n")
	// a user desktop file overrides the system one with the same id
	writeDesktop(t, user, "org.gnome.Nautilus", "[Desktop Entry]\nName=My Files\nType=Application\n")
	os.WriteFile(filepath.Join(system, "notes.txt"), []byte("not a desktop file"), 0644)

	apps := loadDesktopApps([]string{user, system, filepath.Join(system, "missing")}, "")
	want := []desktopApp{
		{id: "firefox", name: "Firefox", icon: "firefox"},
		{id: "org.gnome.Nautilus", name: "My Files"},
		{id: "zed", name: "zed editor"},
	}
	if len(apps) != len(want) {
		t.Fatalf("got %v, want %v", apps, want)
	}
	for i := range want {
		if apps[i] != want[i] {
			t.Errorf("app %d: got %v, want %v", i, apps[i], want[i])
		}
	}
}

func TestAppMatches(t *testing.T) {
	app := desktopApp{id: "org.gnome.Nautilus", name: "Files"}
	cases := map[string]bool{
		"":             true,
		"files":        true,
		"FIL":          true,
		"nautilus":     true,
		"gnome files":  true,
		"  files  ":    true,
		"files chrome": false,
		"firefox":      false,
	}
	for query, want := range cases {
		if got := appMatches(app, query); got != want {
			t.Errorf("appMatches(%q) = %v, want %v", query, got, want)
		}
	}
}

func TestUserLanguage(t *testing.T) {
	cases := []struct{ lcAll, lang, want string }{
		{"", "es_CO.UTF-8", "es"},
		{"", "en_US", "en"},
		{"", "pt.UTF-8", "pt"},
		{"", "C", ""},
		{"de_DE.UTF-8", "es_CO.UTF-8", "de"},
		{"", "", ""},
	}
	for _, c := range cases {
		t.Setenv("LC_ALL", c.lcAll)
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", c.lang)
		if got := userLanguage(); got != c.want {
			t.Errorf("LC_ALL=%q LANG=%q: got %q, want %q", c.lcAll, c.lang, got, c.want)
		}
	}
}

func TestPickerHeight(t *testing.T) {
	cases := []struct {
		name          string
		monitorH, gap int
		vertical      bool
		want          int
	}{
		{"large monitor, capped", 1440, 88, false, pickerMaxHeight},
		{"laptop: fills the space above the dock", 720, 88, false, 720 - 88 - 40},
		{"tiny monitor: minimum", 300, 88, false, pickerMinHeight},
		{"vertical dock takes no height", 640, 300, true, 640 - 40},
		{"unknown monitor", 0, 88, false, 480},
	}
	for _, c := range cases {
		if got := pickerHeight(c.monitorH, c.gap, c.vertical); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
