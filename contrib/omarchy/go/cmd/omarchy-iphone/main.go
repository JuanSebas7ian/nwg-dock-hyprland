// omarchy-iphone: the iPhone bar widget backend (libimobiledevice, ifuse, UxPlay, KDE Connect).
//
//	omarchy-iphone start import|backup|drive|safe|free [udid]   run a job detached (systemd-run), as the widget does
//	omarchy-iphone status                       devices, photos, backup, tethering, AirPlay, KDE Connect (JSON)
//	omarchy-iphone pair [udid]                  ask the iPhone to trust this PC
//	omarchy-iphone scan|import [udid]           count / copy new photos and videos (import prints progress lines)
//	omarchy-iphone open photos|app <id> [udid]  mount the camera roll or an app's files and open the folder
//	omarchy-iphone apps [udid]                  apps that share files
//	omarchy-iphone eject [udid]                 release the mounts
//	omarchy-iphone backup [udid]                local backup (incremental; progress lines)
//	omarchy-iphone drive [udid]                 upload the local backup to Google Drive (restic, encrypted)
//	omarchy-iphone safe [udid]                  which photos are safe to delete on the device (copied, identical, in Google Photos)
//	omarchy-iphone safe-list [udid]             the photos that can be deleted (from the last check)
//	omarchy-iphone free-start <udid> --all|<key>...   delete safe photos from the device (detached; over PTP)
//	omarchy-iphone reorganize [--dry-run]       move imported photos to the folder of the year they were taken
//	omarchy-iphone app-sizes [udid]             apps on the device by size (app + its data)
//	omarchy-iphone backup-delete <udid>         delete a device backup from this PC (only if Google Drive has it)
//	omarchy-iphone gphotos-add                  add the iPhone/iPad photo folders to gphotos-sync (Google Photos)
//	omarchy-iphone encryption on|off [udid]     encrypted backups (asks for the password: run it in a terminal)
//	omarchy-iphone airplay on|off               screen mirroring receiver (UxPlay)
//	omarchy-iphone kde pair|unpair|ring|ping|clipboard <device id>
//	omarchy-iphone set <key> <value>            photosDir, backupDir, autoImport, autoBackupDays, airplayName
//	omarchy-iphone watch                        follow connections (omarchy-iphone.service)
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"omarchy/internal/iphone"
	"omarchy/internal/jsonout"
)

const usage = `omarchy-iphone status | pair [udid] | scan|import [udid] | open photos|app <id> [udid] | apps [udid] | eject [udid] |
               backup [udid] | drive [udid] | safe [udid] | encryption on|off [udid] | airplay on|off | kde <action> <id> | set <key> <value> | watch`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(usage)
		return
	}
	p := iphone.DefaultPaths()
	run := iphone.ExecRunner(30 * time.Second)
	cmd, rest := args[0], args[1:]
	arg := func(i int) string {
		if i < len(rest) {
			return rest[i]
		}
		return ""
	}
	dev := func(i int) iphone.Device {
		d, err := iphone.TrustedDevice(run, p, arg(i))
		if err != nil {
			jsonout.Err(err.Error())
			os.Exit(1)
		}
		return d
	}
	device := func(i int) string { return dev(i).UDID }
	fail := func(err error) {
		if err != nil {
			jsonout.Err(err.Error())
			os.Exit(1)
		}
	}

	switch cmd {
	case "start":
		// the widget's way to run a long job: a transient user unit, independent of the bar
		job := arg(0)
		if !slices.Contains(iphone.Jobs, job) {
			fail(fmt.Errorf("start import|backup|drive|safe [udid]"))
		}
		fail(iphone.StartJob(job, device(1)))
		jsonout.OK(nil)
	case "status":
		jsonout.Print(iphone.Status(run, p, "/sys/class/net"))
	case "pair":
		u := arg(0)
		if u == "" {
			if ds := iphone.ListUDIDs(run); len(ds) > 0 {
				u = ds[0].UDID
			}
		}
		if u == "" {
			fail(fmt.Errorf("no iPhone connected: plug it in by cable"))
		}
		msg, err := iphone.Pair(run, u)
		fail(err)
		jsonout.OK(map[string]any{"message": msg})
	case "scan":
		r, err := iphone.Scan(run, p, device(0))
		fail(err)
		r["ok"] = true
		jsonout.Print(r)
	case "import":
		d := dev(0)
		res, err := iphone.Import(iphone.ExecRunner(10*time.Minute), p, d.UDID, d.Class, func(done, total int, name string) {
			jsonout.Print(map[string]any{"progress": true, "done": done, "total": total, "file": name})
			frac, line := -1.0, "checking the camera roll…"
			if total > 0 {
				frac, line = float64(done)/float64(total), fmt.Sprintf("%d of %d · %s", done, total, name)
			} else if done == 0 && name == "" {
				line = "nothing new"
			}
			p.SaveProgress("import", d.UDID, frac, line)
		})
		fail(err)
		jsonout.OK(map[string]any{"result": res})
	case "open":
		what := "media"
		udidArg := 1
		switch arg(0) {
		case "photos":
		case "app":
			what, udidArg = "app:"+arg(1), 2
		default:
			fail(fmt.Errorf("open photos | open app <bundle id>"))
		}
		dir, err := iphone.Mount(run, p, device(udidArg), what)
		fail(err)
		if what == "media" {
			dir += "/DCIM"
		}
		_ = exec.Command("xdg-open", dir).Start()
		jsonout.OK(map[string]any{"dir": dir})
	case "apps":
		apps, err := iphone.Apps(run, device(0))
		fail(err)
		jsonout.OK(map[string]any{"apps": apps})
	case "eject":
		released, err := iphone.Eject(run, p, arg(0))
		fail(err)
		jsonout.OK(map[string]any{"released": released})
	case "backup":
		u := device(0)
		err := iphone.Backup(p, u, func(pct int, line string) {
			jsonout.Print(map[string]any{"progress": true, "pct": pct, "line": line})
			frac := -1.0
			if pct >= 0 {
				frac = float64(pct) / 100
			}
			p.SaveProgress("backup", u, frac, line)
		})
		fail(err)
		if p.LoadConfig().DriveBackup {
			p.SaveProgress("drive", u, -1, "starting the upload to Google Drive…")
			if err := iphone.DriveUpload(p, u, func(frac float64, line string) { p.SaveProgress("drive", u, frac, line) }); err != nil {
				iphone.Notify("Respaldo local listo; la subida a Google Drive falló", err.Error())
				fail(err)
			}
		}
		jsonout.OK(nil)
	case "drive":
		u := device(0)
		err := iphone.DriveUpload(p, u, func(frac float64, line string) {
			p.SaveProgress("drive", u, frac, line)
		})
		fail(err)
		jsonout.OK(nil)
	case "safe":
		d := dev(0)
		p.SaveProgress("safe", d.UDID, -1, "comparing the camera roll with this PC and Google Photos…")
		r, err := iphone.Safe(iphone.ExecRunner(10*time.Minute), p, d.UDID, d.Class, func(done, total int, name string) {
			if done%10 == 0 || done == total {
				p.SaveProgress("safe", d.UDID, float64(done)/float64(max(total, 1)), fmt.Sprintf("comparing copies byte by byte · %d of %d · %s", done, total, name))
			}
		})
		fail(err)
		r.Groups = nil
		jsonout.OK(map[string]any{"report": r})
	case "safe-list":
		u := arg(0)
		if u == "" {
			u = device(0)
		}
		jsonout.OK(map[string]any{"groups": iphone.SafeGroupsOf(p, u)})
	case "free-start":
		u := arg(0)
		req := iphone.FreeRequest{All: slices.Contains(rest, "--all")}
		for _, k := range rest[min(1, len(rest)):] {
			if k != "--all" {
				req.Keys = append(req.Keys, k)
			}
		}
		if u == "" || (!req.All && len(req.Keys) == 0) {
			fail(fmt.Errorf("free-start <udid> --all|<key>..."))
		}
		fail(iphone.StartFree(p, u, req))
		jsonout.OK(nil)
	case "free":
		d := dev(0)
		req, err := iphone.LoadFreeRequest(p, d.UDID)
		fail(err)
		p.SaveProgress("free", d.UDID, -1, "checking the photos again before deleting…")
		res, err := iphone.Free(iphone.ExecRunner(2*time.Minute), p, d.UDID, req, func(done, total int, name string) {
			p.SaveProgress("free", d.UDID, float64(done)/float64(max(total, 1)), fmt.Sprintf("deleting · %d of %d · %s", done, total, name))
		})
		if err != nil {
			res.Error = err.Error()
			iphone.SaveFreeResult(p, d.UDID, res)
			iphone.Notify(d.Name+": no se borró nada", err.Error())
			fail(err)
		}
		iphone.Notify(d.Name+": espacio liberado", fmt.Sprintf("%d archivos borrados (%.1f GB). Para recuperar el espacio ya: Fotos › Eliminado › Eliminar todo", res.Deleted, float64(res.Bytes)/1e9))
		jsonout.OK(map[string]any{"result": res})
	case "reorganize":
		d := dev(0)
		dry := slices.Contains(rest, "--dry-run")
		res, err := iphone.Reorganize(iphone.ExecRunner(time.Minute), p, d.UDID, d.Class, dry, func(done, total int) {
			p.SaveProgress("import", d.UDID, float64(done)/float64(total), fmt.Sprintf("reorganizing by capture date · %d of %d", done, total))
		})
		fail(err)
		jsonout.OK(map[string]any{"result": res})
	case "app-sizes":
		r, err := iphone.AppSizes(p, device(0))
		fail(err)
		r["ok"] = true
		jsonout.Print(r)
	case "backup-delete":
		freed, err := iphone.DeleteLocalBackup(p, arg(0))
		fail(err)
		jsonout.OK(map[string]any{"freed": freed})
	case "gphotos-add":
		added, err := iphone.AddGooglePhotosSources(p)
		fail(err)
		jsonout.OK(map[string]any{"added": added})
	case "encryption":
		if arg(0) != "on" && arg(0) != "off" {
			fail(fmt.Errorf("encryption on|off"))
		}
		c := exec.Command("idevicebackup2", "-u", device(1), "-i", "encryption", arg(0))
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		fail(c.Run())
	case "airplay":
		fail(iphone.SetAirPlay(run, p, arg(0) == "on"))
		jsonout.OK(nil)
	case "kde":
		fail(iphone.KDEAction(run, arg(0), arg(1)))
		jsonout.OK(nil)
	case "set":
		fail(p.Set(arg(0), arg(1)))
		jsonout.OK(map[string]any{"config": p.LoadConfig()})
	case "watch":
		stop := make(chan struct{})
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() { <-sig; close(stop) }()
		iphone.Watch(iphone.ExecRunner(10*time.Minute), p, 4*time.Second, stop)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(3)
	}
}
