package iphone

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The device backup goes to Google Drive inside the restic repository of omarchy-backup (encrypted with its
// password, deduplicated: after the first upload only what changed travels). Snapshots are tagged
// "iphone" + the UDID; the last 3 of each device are kept (omarchy-backup prunes weekly).

// ResticEnv reads omarchy-backup's settings: RESTIC_REPOSITORY from its config, the password file next to it.
func ResticEnv(p Paths) ([]string, error) {
	conf := filepath.Join(filepath.Dir(filepath.Dir(p.Config)), "omarchy-backup")
	b, err := os.ReadFile(filepath.Join(conf, "config"))
	if err != nil {
		return nil, errors.New("omarchy-backup is not set up (no ~/.config/omarchy-backup/config)")
	}
	repo := ""
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "RESTIC_REPOSITORY="); ok {
			repo = strings.Trim(v, `"'`)
		}
	}
	pw := filepath.Join(conf, "restic-password")
	if repo == "" {
		return nil, errors.New("no RESTIC_REPOSITORY in ~/.config/omarchy-backup/config")
	}
	if st, err := os.Stat(pw); err != nil || st.Size() == 0 {
		return nil, errors.New("missing ~/.config/omarchy-backup/restic-password")
	}
	return append(os.Environ(), "RESTIC_REPOSITORY="+repo, "RESTIC_PASSWORD_FILE="+pw), nil
}

// ResticProgress reads `restic backup --json` and reports the fraction done and the summary.
func ResticProgress(r io.Reader, progress func(frac float64, line string)) (summary map[string]any, lastErr string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			if t := strings.TrimSpace(sc.Text()); t != "" {
				lastErr = t
			}
			continue
		}
		switch m["message_type"] {
		case "status":
			f, _ := m["percent_done"].(float64)
			if progress != nil {
				progress(f, fmt.Sprintf("%.0f%% · %s of %s", f*100, human(m["bytes_done"]), human(m["total_bytes"])))
			}
		case "summary":
			summary = m
		case "error":
			if e, ok := m["error"].(map[string]any); ok {
				lastErr = fmt.Sprint(e["message"])
			}
		}
	}
	return summary, lastErr
}

func human(v any) string {
	f, _ := v.(float64)
	u := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for f >= 1000 && i < len(u)-1 {
		f /= 1000
		i++
	}
	return fmt.Sprintf("%.1f %s", f, u[i])
}

// DriveUpload sends the local backup of one device to Google Drive.
func DriveUpload(p Paths, udid string, progress func(frac float64, line string)) error {
	cfg := p.LoadConfig()
	src := filepath.Join(cfg.BackupDir, udid)
	if _, err := os.Stat(filepath.Join(src, "Status.plist")); err != nil {
		return errors.New("there is no finished local backup of this device yet: back it up first")
	}
	env, err := ResticEnv(p)
	if err != nil {
		return err
	}
	release, err := p.lock("drive")
	if err != nil {
		return err
	}
	defer release()
	host, _ := os.Hostname()
	cmd := exec.Command("restic", "backup", src, "--tag", "iphone", "--tag", udid, "--host", host, "--json", "--compression", "auto")
	cmd.Env = env
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return err
	}
	summary, lastErr := ResticProgress(out, progress)
	err = cmd.Wait()
	rec := M{"time": time.Now().Format(time.RFC3339), "udid": udid, "seconds": int(time.Since(start).Seconds()), "ok": err == nil && summary != nil}
	if summary != nil {
		rec["snapshot"], rec["added"], rec["total"] = summary["snapshot_id"], summary["data_added"], summary["total_bytes_processed"]
	}
	if err != nil || summary == nil {
		rec["error"] = lastErr
		_ = writeJSON(filepath.Join(p.State, "last-drive.json"), rec)
		return fmt.Errorf("upload to Google Drive failed: %s", lastErr)
	}
	// keep the 3 newest uploads of this device (pruning is omarchy-backup's weekly job)
	forget := exec.Command("restic", "forget", "--tag", "iphone,"+udid, "--keep-last", "3")
	forget.Env = env
	if b, err := forget.CombinedOutput(); err != nil {
		rec["warning"] = "old uploads not trimmed: " + strings.TrimSpace(string(b))
	}
	_ = writeJSON(filepath.Join(p.State, "last-drive.json"), rec)
	return nil
}

// DriveStatus: the last upload and whether one runs now.
func DriveStatus(p Paths, cfg Config) M {
	var last M
	readJSON(filepath.Join(p.State, "last-drive.json"), &last)
	return M{"enabled": cfg.DriveBackup, "last": last, "running": p.locked("drive")}
}
