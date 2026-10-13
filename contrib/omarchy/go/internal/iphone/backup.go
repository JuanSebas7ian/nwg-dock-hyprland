package iphone

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"omarchy/internal/game"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var pctRe = regexp.MustCompile(`(\d{1,3})%`)

// BackupProgress reads idevicebackup2 output and reports each new percentage.
func BackupProgress(r io.Reader, progress func(pct int, line string)) {
	sc := bufio.NewScanner(r)
	sc.Split(scanLinesOrCR)
	last := -1
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := pctRe.FindStringSubmatch(line); m != nil {
			if n, _ := strconv.Atoi(m[1]); n != last && n <= 100 {
				last = n
				progress(n, line)
			}
		} else if line != "" {
			progress(-1, line)
		}
	}
}

// idevicebackup2 redraws its progress bar with \r.
func scanLinesOrCR(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// PrepareBackupDir creates the backup folder; on btrfs as its own subvolume, so the hourly snapshots of
// /home (snapper) do not keep every old version of a 50-200 GB backup.
func PrepareBackupDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if exec.Command("btrfs", "subvolume", "create", dir).Run() == nil {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

// Backup makes (or updates: idevicebackup2 is incremental) the local backup of one device.
func Backup(p Paths, udid string, progress func(pct int, line string)) error {
	cfg := p.LoadConfig()
	release, err := p.lock(jobKey("backup", udid))
	if err != nil {
		return err
	}
	defer release()
	if err := PrepareBackupDir(cfg.BackupDir); err != nil {
		return err
	}
	cmd := exec.Command("idevicebackup2", "-u", udid, "backup", cfg.BackupDir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return err
	}
	var lastLine string
	BackupProgress(out, func(pct int, line string) {
		lastLine = line
		if progress != nil {
			progress(pct, line)
		}
	})
	err = cmd.Wait()
	rec := M{"time": time.Now().Format(time.RFC3339), "udid": udid, "seconds": int(time.Since(start).Seconds()), "ok": err == nil}
	if err != nil {
		rec["error"] = lastLine
	}
	if size, e := dirSize(filepath.Join(cfg.BackupDir, udid)); e == nil {
		rec["bytes"] = size
	}
	_ = writeJSON(p.devFile(udid, "last-backup.json"), rec)
	if err != nil {
		return fmt.Errorf("backup failed: %s", lastLine)
	}
	// what fills the device, for the Free up space tab (read from this backup)
	_, _ = Storage(ExecRunner(5*time.Minute), p, udid)
	return nil
}

func dirSize(dir string) (int64, error) {
	var n int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n, err
}

// BackupStatus: one device's last backup and whether one runs now.
func BackupStatus(p Paths, cfg Config, udid string) M {
	var last M
	readJSON(p.devFile(udid, "last-backup.json"), &last)
	return M{"dir": cfg.BackupDir, "autoDays": cfg.AutoBackupDays, "last": last, "running": p.locked(jobKey("backup", udid))}
}

// BackupDue says whether the automatic backup should run now.
func BackupDue(p Paths, cfg Config, udid string, now time.Time) bool {
	if cfg.AutoBackupDays <= 0 || p.locked(jobKey("backup", udid)) {
		return false
	}
	var last M
	if !readJSON(p.devFile(udid, "last-backup.json"), &last) {
		return true
	}
	t, err := time.Parse(time.RFC3339, fmt.Sprint(last["time"]))
	if err != nil || last["ok"] != true {
		return true
	}
	return now.Sub(t) >= time.Duration(cfg.AutoBackupDays)*24*time.Hour
}

// Notify shows a desktop notification (best effort); tests replace it.
var Notify = func(title, body string) {
	_ = exec.Command("notify-send", "-a", "iPhone", "-i", "phone", title, body).Run()
}

// Watch follows connections: on a trusted device it imports new photos (autoImport) and runs the automatic
// backup when due; on disconnect it releases the device's mounts. It never returns unless stop is closed.
func Watch(run Runner, p Paths, every time.Duration, stop <-chan struct{}) {
	known := map[string]bool{}    // handled (trusted) devices
	warned := map[string]bool{}   // untrusted devices already told to tap Trust
	kdeAsked := map[string]bool{} // KDE Connect pair requests already announced
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		now := map[string]bool{}
		for _, d := range ListUDIDs(run) {
			now[d.UDID] = true
			if known[d.UDID] || d.Conn != "usb" {
				continue
			}
			if gameOn() {
				continue // playing: imports and backups wait until game mode ends (then this device is handled)
			}
			d = Describe(run, d)
			if d.Unreachable {
				if !warned[d.UDID] {
					warned[d.UDID] = true
					Notify(deviceLabel(d)+" conectado pero no responde", "Desbloquéalo; si sigue igual, desconecta y vuelve a conectar el cable")
				}
				continue // retried every tick: it is used as soon as it answers
			}
			if !d.Trusted {
				// keep asking every tick: the user taps Trust a moment after plugging in
				if !warned[d.UDID] {
					warned[d.UDID] = true
					Notify(deviceLabel(d)+" conectado", d.Error)
				}
				continue
			}
			known[d.UDID] = true
			OnConnect(run, p, d)
		}
		for u := range known {
			if !now[u] {
				delete(known, u)
				Unmount(run, p, u)
			}
		}
		for u := range warned {
			if !now[u] {
				delete(warned, u)
			}
		}
		// a phone or tablet asking to pair over KDE Connect: say so (the request expires in ~30 s)
		kdeNow := map[string]bool{}
		if devs, err := KDEDevices(run); err == nil {
			for _, d := range devs {
				if !d.RequestedByPeer {
					continue
				}
				kdeNow[d.ID] = true
				if !kdeAsked[d.ID] {
					kdeAsked[d.ID] = true
					Notify(d.Name+" quiere emparejarse", "Acéptalo en el widget del iPhone › Connect (o en una terminal: omarchy-iphone kde accept "+d.ID+")")
				}
			}
		}
		for id := range kdeAsked {
			if !kdeNow[id] {
				delete(kdeAsked, id)
			}
		}
		select {
		case <-stop:
			return
		case <-tick.C:
		}
	}
}

// OnConnect is what happens when an iPhone is plugged in.
func OnConnect(run Runner, p Paths, d Device) {
	if !d.Trusted {
		Notify("iPhone conectado", d.Error)
		return
	}
	cfg := p.LoadConfig()
	name := deviceLabel(d)
	// photos imported by the first version sit in the year of their file date: move them to the capture year once
	if l := p.LoadLedger(d.UDID); len(l) > 0 && needsReorganize(l) && !p.locked(jobKey("import", d.UDID)) {
		if res, err := Reorganize(run, p, d.UDID, d.Class, false, nil); err == nil && res.Moved > 0 {
			Notify(name+": fotos reordenadas", fmt.Sprintf("%d fotos movidas a la carpeta del año en que se tomaron", res.Moved))
		}
	}
	if cfg.AutoImport && !p.locked(jobKey("import", d.UDID)) {
		if res, err := Import(run, p, d.UDID, d.Class, func(done, total int, name string) {
			p.SaveProgress("import", d.UDID, fraction(done, total), fmt.Sprintf("%d of %d · %s", done, total, name))
		}); err != nil {
			Notify(name+": no se pudieron importar las fotos", err.Error())
		} else if res.Imported > 0 || res.Failed > 0 {
			msg := fmt.Sprintf("%d fotos y videos nuevos en %s", res.Imported, res.Dir)
			if res.Failed > 0 {
				msg += fmt.Sprintf(" (%d fallaron: se reintentan al volver a conectar)", res.Failed)
			}
			Notify(name+": fotos importadas", msg)
		}
		Unmount(run, p, d.UDID)
	} else if s, err := Scan(run, p, d.UDID); err == nil && s["pending"].(int) > 0 {
		Notify(name+" conectado", fmt.Sprintf("%d fotos y videos nuevos para importar", s["pending"]))
		Unmount(run, p, d.UDID)
	}
	if BackupDue(p, cfg, d.UDID, time.Now()) {
		// a detached job (backup, then the Google Drive upload): restarting this service must not kill it
		if err := StartJob("backup", d.UDID); err != nil {
			Notify(name+": no se pudo iniciar el respaldo", err.Error())
		} else {
			Notify(name+": respaldo automático", "Empezó el respaldo; luego se sube a Google Drive. No lo desconectes")
		}
	}
}

// StartJob runs `omarchy-iphone <job> <udid>` as a transient user unit, independent of the bar and of this
// service; the unit name carries the device so two devices run side by side. Tests replace it.
var StartJob = func(job, udid string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	unit := "omarchy-iphone-" + job + "-" + udid[max(0, len(udid)-8):] + "-" + time.Now().Format("150405")
	if b, err := exec.Command("systemd-run", "--user", "--collect", "--quiet", "--unit="+unit, exe, job, udid).CombinedOutput(); err != nil {
		return fmt.Errorf("could not start %s: %s", job, strings.TrimSpace(string(b)))
	}
	return nil
}

// needsReorganize: ledger entries without a recorded place come from the first version.
func needsReorganize(l Ledger) bool {
	for _, e := range l {
		if e.Local == "" {
			return true
		}
	}
	return false
}

// gameOn: game mode is on (omarchy-game); tests replace it.
var gameOn = game.IsOn

func fraction(done, total int) float64 {
	if total <= 0 {
		return -1
	}
	return float64(done) / float64(total)
}

func deviceLabel(d Device) string {
	if d.Name != "" {
		return d.Name
	}
	if d.Class != "" {
		return d.Class
	}
	return "iPhone"
}

var errNoDevice = errors.New("no trusted iPhone or iPad connected")

// ErrUnreachable: the device is plugged in but does not answer (locked, asleep or a stuck USB link).
var ErrUnreachable = errors.New("the device is connected but not answering over USB: unlock it, or unplug and plug the cable again")

// TrustedDevice picks the device to act on: the named one, or the first trusted one. While a backup holds the
// phone (slow to answer) the last known details are used.
func TrustedDevice(run Runner, p Paths, udid string) (Device, error) {
	for _, d := range ListUDIDs(run) {
		if udid != "" && d.UDID != udid {
			continue
		}
		var c Device
		if p.locked(jobKey("backup", d.UDID)) && readJSON(filepath.Join(p.State, d.UDID, "device.json"), &c) && c.Trusted {
			return c, nil
		}
		dd := Describe(run, d)
		if dd.Trusted {
			return dd, nil
		}
		if udid != "" && dd.Unreachable {
			return Device{}, ErrUnreachable
		}
	}
	return Device{}, errNoDevice
}
