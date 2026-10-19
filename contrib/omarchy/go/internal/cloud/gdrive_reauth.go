package cloud

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ReauthEvery is the minimum time between automatic Google sign-in windows, so a refused
// sign-in does not open the browser every few minutes.
const ReauthEvery = 6 * 3600

// IsAuthError tells a withdrawn or expired Google permission (rclone 401 / invalid_grant)
// from a network problem: only the first deserves a sign-in window.
func IsAuthError(out string) bool {
	s := strings.ToLower(out)
	for _, m := range []string{"invalid_grant", "invalid credentials", "error 401", "got 401", "status code 401",
		"couldn't fetch token", "cannot fetch token", "token expired", "unauthorized_client", "failed to get oauth token"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// DueReauth reports whether a sign-in may start now given the last stamp.
func DueReauth(lastTS, now int64) bool { return now-lastTS >= ReauthEvery }

func (d Drive) stamp() string {
	return filepath.Join(stateHome(), "omarchy-cloud", "gdrive-reauth.json")
}

func (d Drive) lastReauth() int64 {
	var v struct{ TS int64 }
	if b, err := os.ReadFile(d.stamp()); err == nil {
		_ = json.Unmarshal(b, &v)
	}
	return v.TS
}

func (d Drive) writeStamp(ts int64) {
	_ = os.MkdirAll(filepath.Dir(d.stamp()), 0o755)
	b, _ := json.Marshal(map[string]int64{"TS": ts})
	_ = os.WriteFile(d.stamp(), b, 0o600)
}

func notify(title, body string) {
	_ = exec.Command("notify-send", "-a", "Google Drive", "-i", "folder-cloud", title, body).Run()
}

// Probe asks Drive for its top-level folders (no mount needed). ok=false with auth=true
// means Google withdrew the permission.
func (d Drive) Probe() (ok, auth bool, detail string) {
	out, errs, code, err := run(45*time.Second, "rclone", "lsd", d.Remote, "--max-depth", "1",
		"--contimeout", "10s", "--timeout", "20s", "--retries", "1", "--low-level-retries", "1")
	if err == nil && code == 0 {
		return true, false, ""
	}
	detail = strings.TrimSpace(errs + " " + out)
	if err != nil {
		detail = err.Error()
	}
	return false, IsAuthError(detail), detail
}

// Heal is what the timer runs: if Drive's permission was withdrawn and the remote is
// configured, open the Google sign-in by itself (at most every ReauthEvery).
func (d Drive) Heal() M {
	ok, auth, detail := d.Probe()
	switch {
	case ok:
		return M{"ok": true, "action": "none"}
	case !auth:
		return M{"ok": false, "action": "none", "reason": "not an auth error (offline?)", "detail": detail}
	}
	now := time.Now().Unix()
	if !DueReauth(d.lastReauth(), now) {
		return M{"ok": false, "action": "throttled", "detail": detail}
	}
	exe, err := os.Executable()
	if err != nil {
		return M{"ok": false, "error": err.Error()}
	}
	d.writeStamp(now)
	notify("Google Drive needs you to sign in again", "Opening the Google sign-in in your browser.")
	if err := exec.Command("systemd-run", "--user", "--collect", "--quiet", "--unit=omarchy-gdrive-reauth", exe, "gdrive", "reauth").Run(); err != nil {
		return M{"ok": false, "action": "reauth", "error": err.Error()}
	}
	return M{"ok": true, "action": "reauth"}
}

// Reauth runs rclone's own sign-in (opens the browser, saves the new token) and then brings
// back what depended on Drive: the mount and the pending backup.
func (d Drive) Reauth() M {
	_, errs, code, err := run(15*time.Minute, "rclone", "config", "reconnect", d.Remote, "--auto-confirm")
	if err != nil || code != 0 {
		msg := strings.TrimSpace(errs)
		if err != nil {
			msg = err.Error()
		}
		notify("Google Drive sign-in failed", "Will try again later. "+msg)
		return M{"ok": false, "error": msg}
	}
	if ok, _, detail := d.Probe(); !ok {
		notify("Google Drive still refuses the login", detail)
		return M{"ok": false, "error": detail}
	}
	_ = exec.Command("systemctl", "--user", "reset-failed", d.Service).Run()
	_ = exec.Command("systemctl", "--user", "restart", d.Service).Run()
	_ = exec.Command("systemctl", "--user", "reset-failed", "omarchy-backup.service").Run()
	_ = exec.Command("systemctl", "--user", "start", "--no-block", "omarchy-backup.service").Run()
	notify("Google Drive connected again", "The mount and the backup are running.")
	return M{"ok": true}
}
