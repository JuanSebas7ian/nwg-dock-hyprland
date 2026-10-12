package cloud

import "path/filepath"

// Drive is the rclone-gdrive mount at ~/GoogleDrive.
type Drive struct {
	Service, Remote, Mount, Sock, Cache string
}

func DefaultDrive() Drive {
	return Drive{"rclone-gdrive.service", "rclone:", filepath.Join(home(), "GoogleDrive"),
		filepath.Join(runtimeDir(), "rclone-gdrive.sock"), filepath.Join(cacheHome(), "omarchy-gdrive-widget")}
}

func exists(p string) bool { _, err := statPath(p); return err == nil }

func (d Drive) Status() M {
	res := M{"service": ServiceState(d.Service), "mounted": IsMount(d.Mount), "mountpoint": d.Mount,
		"rc": false, "cache": M{}, "transfers": []M{}, "queue": []M{}, "speed": 0, "errors": 0,
		"quota": cachedAbout(d.Remote, filepath.Join(d.Cache, "quota.json"), true), "recent": []M{}}
	if exists(d.Sock) {
		if _, err := rcStats(d.Sock, res, true); err != nil {
			res["rcError"] = err.Error()
		} else {
			if q, err := queue(d.Sock, 20); err == nil {
				res["queue"] = queueRows(q)
			}
			cache := res["cache"].(M)
			p, _ := cache["path"].(string)
			res["recent"] = Recent(p, 12)
		}
	}
	return res
}

func (d Drive) SetMounted(on bool) M {
	if on {
		return Systemctl("start", d.Service)
	}
	return Systemctl("stop", d.Service)
}

func (d Drive) Refresh() M {
	if _, err := RC(d.Sock, "vfs/refresh", M{"recursive": "false"}); err != nil {
		return M{"ok": false, "error": err.Error()}
	}
	return M{"ok": true}
}
