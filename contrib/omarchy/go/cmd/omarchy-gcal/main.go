// omarchy-gcal: Google Calendar for the Omarchy clock panel.
//
//	omarchy-gcal setup                 once, in a terminal: your OAuth client + sign in in the browser
//	omarchy-gcal status                JSON: connected?, account, calendars, last sync
//	omarchy-gcal sync                  download calendars and events (2 months back, 12 ahead)
//	omarchy-gcal events [--max-age S]  JSON from the cache; syncs first when older than S seconds
//	omarchy-gcal add '<json>'          {calendarId, summary, date, start, end, allDay, location, description, reminder}
//	omarchy-gcal delete <calendarId> <eventId> [--series]
//	omarchy-gcal tick                  for the timer: sync every 5 min, desktop reminders every minute
//	omarchy-gcal logout                forget the sign-in (revokes the token at Google)
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"omarchy/internal/gcal"
	"omarchy/internal/google"
	"omarchy/internal/jsonout"
)

const instructions = `
Google Calendar: your own OAuth client (once, ~5 minutes, free)
  1. https://console.cloud.google.com/projectcreate → create a project (e.g. "omarchy").
  2. https://console.cloud.google.com/apis/library/calendar-json.googleapis.com → Enable.
     (For Google Photos later: also enable "Photos Library API" in the same project.)
  3. https://console.cloud.google.com/auth/branding → app name + your email → Audience: External.
     Then Audience → "Publish app" (in "Testing" Google drops the permission after 7 days).
  4. https://console.cloud.google.com/auth/clients → Create client → type "Desktop app"
     → copy the client ID and the client secret.
`

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func service() *gcal.Service {
	home, _ := os.UserHomeDir()
	cfg := env("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return &gcal.Service{
		Auth: &google.Auth{ClientPath: filepath.Join(cfg, "omarchy-google", "client.json"),
			TokenPath: filepath.Join(cfg, "omarchy-gcal", "token.json"), SetupCmd: "omarchy-gcal setup"},
		Cache:    filepath.Join(env("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "omarchy-gcal", "events.json"),
		Notified: filepath.Join(env("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "omarchy-gcal", "notified.json"),
	}
}

// setup keeps the terminal open on any outcome: it closes as soon as the program ends.
func setup(s *gcal.Service) int {
	err := func() error {
		var c google.Client
		in := bufio.NewReader(os.Stdin)
		if google.ReadJSON(s.Auth.ClientPath, &c) && c.ID != "" {
			fmt.Printf("\nUse the saved OAuth client (%s…)? [Y/n] ", c.ID[:min(24, len(c.ID))])
			a, _ := in.ReadString('\n')
			if a = strings.ToLower(strings.TrimSpace(a)); a == "n" || a == "no" {
				c.ID = ""
			}
		}
		if c.ID == "" {
			if err := google.AskClient(s.Auth.ClientPath, instructions); err != nil {
				return err
			}
		}
		if err := s.Auth.SignIn(gcal.Scopes, "Listo: Google Calendar conectado. Puedes cerrar esta pestaña."); err != nil {
			return err
		}
		d, err := s.Sync()
		if err != nil {
			return err
		}
		fmt.Printf("\nConnected as %s: %d calendar(s), %d event(s).\n", d.Account, len(d.Calendars), len(d.Events))
		return nil
	}()
	rc := 0
	if err != nil {
		fmt.Println("\nERROR:", err)
		rc = 1
	}
	if fi, _ := os.Stdin.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Print("\nPress Enter to close…")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	return rc
}

// reauth signs in again with the saved OAuth client: it opens the Google window and
// needs no questions, so it can run from the timer or from a button.
func reauth(s *gcal.Service) int {
	err := s.Auth.SignIn(gcal.Scopes, "Listo: Google Calendar conectado. Puedes cerrar esta pestaña.")
	if err == nil {
		_, err = s.Sync()
	}
	if err != nil {
		fmt.Println("ERROR:", err)
		_ = exec.Command("notify-send", "-u", "critical", "-a", "Google Calendar", "Google Calendar sign-in failed", err.Error()).Run()
		return 1
	}
	_ = exec.Command("notify-send", "-a", "Google Calendar", "-i", "x-office-calendar", "Google Calendar connected").Run()
	return 0
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Println("usage: omarchy-gcal setup|reauth|status|sync|events [--max-age S]|add JSON|delete CAL ID [--series]|tick|logout")
		return
	}
	s := service()
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "setup" {
		os.Exit(setup(s))
	}
	if cmd == "reauth" {
		os.Exit(reauth(s))
	}
	var res map[string]any
	var err error
	switch cmd {
	case "status":
		var d gcal.Data
		google.ReadJSON(s.Cache, &d)
		_, cerr := os.Stat(s.Auth.ClientPath)
		cals := d.Calendars
		if cals == nil {
			cals = []map[string]any{}
		}
		res = map[string]any{"ok": true, "configured": s.Configured(), "client": cerr == nil, "account": d.Account, "calendars": cals, "lastSync": d.TS}
	case "sync":
		var d gcal.Data
		if d, err = s.Sync(); err == nil {
			res = map[string]any{"ok": true, "events": len(d.Events), "calendars": len(d.Calendars)}
		}
	case "events":
		maxAge := int64(-1)
		for i, a := range args {
			if a == "--max-age" && i+1 < len(args) {
				maxAge, _ = strconv.ParseInt(args[i+1], 10, 64)
			}
		}
		res = s.Events(maxAge)
	case "add":
		if len(args) < 1 {
			err = errors.New("add needs the event as JSON")
		} else {
			res, err = s.Add(args[0])
		}
	case "delete":
		if len(args) < 2 {
			err = errors.New("delete needs <calendarId> <eventId>")
		} else if err = s.Delete(args[0], args[1], len(args) > 2 && args[2] == "--series"); err == nil {
			res = map[string]any{"ok": true}
		}
	case "tick":
		s.Tick()
		res = map[string]any{"ok": true}
	case "logout":
		s.Auth.Revoke()
		_ = os.Remove(s.Cache)
		res = map[string]any{"ok": true}
	default:
		fmt.Fprintln(os.Stderr, "unknown command", cmd)
		os.Exit(3)
	}
	if err != nil {
		var ns *google.NeedsSetup
		if errors.As(err, &ns) {
			res = map[string]any{"ok": false, "needsSetup": true, "error": err.Error()}
		} else {
			res = map[string]any{"ok": false, "error": err.Error()}
		}
	}
	jsonout.Print(res)
	if ok, _ := res["ok"].(bool); !ok {
		os.Exit(1)
	}
}
