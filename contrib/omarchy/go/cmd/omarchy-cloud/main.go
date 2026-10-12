// omarchy-cloud: backend of the juansebas7ian.cloud bar widget. Prints JSON.
//
//	omarchy-cloud status                  Drive, iCloud, Google Photos and Dropbox in one object
//	omarchy-cloud gdrive status|mount|unmount|refresh
//	omarchy-cloud icloud status|ls [DIR]|keep PATH|mount|unmount|photos-mount|photos-unmount|refresh
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"omarchy/internal/cloud"
)

func out(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func main() {
	a := append(os.Args[1:], "", "", "")
	switch a[0] {
	case "", "status":
		out(cloud.Status())
	case "gdrive":
		d := cloud.DefaultDrive()
		switch a[1] {
		case "", "status":
			out(d.Status())
		case "mount", "unmount":
			out(d.SetMounted(a[1] == "mount"))
		case "refresh":
			out(d.Refresh())
		default:
			out(cloud.M{"error": "usage: omarchy-cloud gdrive status|mount|unmount|refresh"})
			os.Exit(2)
		}
	case "icloud":
		c := cloud.DefaultICloud()
		var err error
		switch a[1] {
		case "", "status":
			out(c.Status())
		case "ls":
			out(c.Ls(a[2]))
		case "keep":
			if err = c.Keep(a[2]); err == nil {
				out(cloud.M{"ok": true})
			}
		case "mount", "unmount":
			out(cloud.Systemctl(map[bool]string{true: "start", false: "stop"}[a[1] == "mount"], c.Service))
		case "photos-mount", "photos-unmount":
			out(cloud.Systemctl(map[bool]string{true: "start", false: "stop"}[a[1] == "photos-mount"], c.PhotosService))
		case "refresh":
			if err = c.Refresh(); err == nil {
				out(cloud.M{"ok": true})
			}
		default:
			out(cloud.M{"error": "usage: omarchy-cloud icloud status|ls [DIR]|keep PATH|mount|unmount|photos-mount|photos-unmount|refresh"})
			os.Exit(2)
		}
		if err != nil {
			out(cloud.M{"ok": false, "error": err.Error()})
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: omarchy-cloud status | gdrive ... | icloud ...")
		os.Exit(2)
	}
}
