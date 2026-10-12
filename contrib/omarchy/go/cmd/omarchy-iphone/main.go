// omarchy-iphone: the iPhone bar widget backend (libimobiledevice, ifuse, UxPlay, KDE Connect).
//
//	omarchy-iphone status                       devices, photos, backup, tethering, AirPlay, KDE Connect (JSON)
//	omarchy-iphone pair [udid]                  ask the iPhone to trust this PC
//	omarchy-iphone scan|import [udid]           count / copy new photos and videos (import prints progress lines)
//	omarchy-iphone open photos|app <id> [udid]  mount the camera roll or an app's files and open the folder
//	omarchy-iphone apps [udid]                  apps that share files
//	omarchy-iphone eject [udid]                 release the mounts
//	omarchy-iphone backup [udid]                local backup (incremental; progress lines)
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
	"syscall"
	"time"

	"omarchy/internal/iphone"
	"omarchy/internal/jsonout"
)

const usage = `omarchy-iphone status | pair [udid] | scan|import [udid] | open photos|app <id> [udid] | apps [udid] | eject [udid] |
               backup [udid] | encryption on|off [udid] | airplay on|off | kde <action> <id> | set <key> <value> | watch`

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
	device := func(i int) string {
		u, err := iphone.FirstTrusted(run, arg(i))
		if err != nil {
			jsonout.Err(err.Error())
			os.Exit(1)
		}
		return u
	}
	fail := func(err error) {
		if err != nil {
			jsonout.Err(err.Error())
			os.Exit(1)
		}
	}

	switch cmd {
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
		res, err := iphone.Import(iphone.ExecRunner(10*time.Minute), p, device(0), func(done, total int, name string) {
			jsonout.Print(map[string]any{"progress": true, "done": done, "total": total, "file": name})
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
		jsonout.OK(map[string]any{"released": iphone.Unmount(run, p, arg(0))})
	case "backup":
		err := iphone.Backup(p, device(0), func(pct int, line string) {
			jsonout.Print(map[string]any{"progress": true, "pct": pct, "line": line})
		})
		fail(err)
		jsonout.OK(nil)
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
