package main

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v3"
	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v3"
	log "github.com/sirupsen/logrus"
)

func taskInstances(ID string) []client {
	var found []client
	for _, c := range clients {
		if strings.ToUpper(c.Class) == strings.ToUpper(ID) {
			found = append(found, c)
		}
	}
	return found
}

func pinnedButton(ID string, pinIdx int, position *string) *gtk.Box {
	vertical = *position == "left" || *position == "right"

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	if vertical {
		box.SetOrientation(gtk.OrientationHorizontal)
	}

	button := gtk.NewButton()
	setupPinnedDnd(box, button, ID, pinIdx)

	image, err := createImage(ID, imgSizeScaled)
	if err != nil || image == nil {
		pixbuf, err := gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/icon-missing.svg"),
			imgSizeScaled, imgSizeScaled)
		if err == nil {
			image = gtk.NewImageFromPixbuf(pixbuf)
		} else {
			image = gtk.NewImage()
		}
	}

	button.SetImage(image)
	button.SetImagePosition(gtk.PosTop)
	button.SetAlwaysShowImage(true)
	button.SetTooltipText(getName(ID))

	button.Connect("clicked", func() {
		if dndBlocksClick() {
			return
		}
		launch(ID)
	})

	button.Connect("button-release-event", func(btn *gtk.Button, e *gdk.Event) bool {
		if dndBlocksClick() {
			return true
		}
		btnEvent := e.AsButton()
		if btnEvent.Button() == 1 || btnEvent.Button() == 2 {
			launch(ID)
			return true
		} else if btnEvent.Button() == 3 {
			contextMenu := pinnedMenuContext(ID)
			contextMenu.PopupAtWidget(button, widgetAnchor, menuAnchor, nil)
			return true
		}
		return false
	})

	button.Connect("enter-notify-event", cancelClose)

	var pixbuf *gdkpixbuf.Pixbuf
	if !vertical {
		pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-empty.svg"),
			imgSizeScaled, imgSizeScaled/8)
	} else {
		pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-empty-vertical.svg"),
			imgSizeScaled/8, imgSizeScaled)
	}

	if err == nil {
		img := gtk.NewImageFromPixbuf(pixbuf)
		if *position == "left" || *position == "top" {
			box.PackStart(img, false, false, 0)
			box.PackStart(button, false, false, 0)
		} else {
			box.PackStart(button, false, false, 0)
			box.PackStart(img, false, false, 0)
		}
	}

	return box
}

func pinnedMenuContext(taskID string) gtk.Menu {
	menu := gtk.NewMenu()
	trackMenu(menu)
	menuItem := gtk.NewMenuItemWithLabel("Unpin")
	menuItem.Connect("activate", func() {
		unpinTask(taskID)
	})
	menu.Append(menuItem)
	addAppMenuItem(menu)

	menu.ShowAll()
	return *menu
}

func launcherButton(position *string) *gtk.Box {
	vertical = *position == "left" || *position == "right"

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	if vertical {
		box.SetOrientation(gtk.OrientationHorizontal)
	}

	if !*noLauncher && *launcherCmd != "" {
		button := gtk.NewButton()
		var pixbuf *gdkpixbuf.Pixbuf
		var e error
		if *ico == "" {
			pixbuf, e = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/grid.svg"), imgSizeScaled, imgSizeScaled)
		} else {
			pixbuf, e = createPixbuf(*ico, imgSizeScaled)
		}
		if e == nil {
			image := gtk.NewImageFromPixbuf(pixbuf)
			button.SetImage(image)
			button.SetAlwaysShowImage(true)

			button.Connect("clicked", func() {
				if *dnd && !isCommand(strings.Fields(*launcherCmd)[0]) {
					openPicker()
					return
				}
				elements := strings.Split(*launcherCmd, " ")
				cmd := exec.Command(elements[0], elements[1:]...)

				go func() {
					err := cmd.Run()
					if err != nil {
						log.Warnf("Unable to start program: %s", err.Error())
					}
				}()

				if *autohide {
					win.Hide()
				}
			})
			button.Connect("enter-notify-event", cancelClose)
			if *dnd {
				button.SetTooltipText("Right click: add app")
				button.Connect("button-release-event", func(btn *gtk.Button, e *gdk.Event) bool {
					if e.AsButton().Button() != 3 {
						return false
					}
					menu := gtk.NewMenu()
					trackMenu(menu)
					addAppMenuItem(menu)
					menu.ShowAll()
					menu.PopupAtWidget(button, widgetAnchor, menuAnchor, nil)
					return true
				})
			}

			if !vertical {
				pixbuf, e = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-empty.svg"),
					imgSizeScaled, imgSizeScaled/8)
			} else {
				pixbuf, e = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-empty-vertical.svg"),
					imgSizeScaled/8, imgSizeScaled)
			}

			if e == nil {
				img := gtk.NewImageFromPixbuf(pixbuf)
				if *position == "left" || *position == "top" {
					box.PackStart(img, false, false, 0)
					box.PackStart(button, false, false, 0)
				} else {
					box.PackStart(button, false, false, 0)
					box.PackStart(img, false, false, 0)
				}
			}
		}
		return box
	}
	return nil
}

/*
Window on-leave-notify event hides the dock with glib Timeout 1000 ms.
We might have left the window by accident, so let's clear the timeout if window re-entered.
Furthermore - hovering a button triggers window on-leave-notify event, and the timeout
needs to be cleared as well.
*/
func cancelClose() {
	markDockActivity()
	if src > 0 {
		glib.SourceRemove(src)
		src = 0
	}
}

// Close the window after a while, unless cancelClose is called in the meantime
func scheduleClose() {
	src = glib.TimeoutAdd(uint(1000), func() bool {
		if dockHeld() {
			src = 0
			return false
		}
		mouseInsideDock = false
		win.Hide()
		src = 0
		return false
	})
}

// pinIdx is the client's index in the pinned list, or -1 if it's not pinned
func taskButton(t client, instances []client, pinIdx int, position *string) *gtk.Box {
	vertical = *position == "left" || *position == "right"

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	if vertical {
		box.SetOrientation(gtk.OrientationHorizontal)
	}

	button := gtk.NewButton()
	if pinIdx >= 0 {
		setupPinnedDnd(box, button, t.Class, pinIdx)
	}

	image, _ := createImage(t.Class, imgSizeScaled)
	if image == nil {
		//var pixbuf *gdk.Pixbuf
		//var err error
		pixbuf, err := gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/icon-missing.svg"),
			imgSizeScaled, imgSizeScaled)

		if err == nil {
			image = gtk.NewImageFromPixbuf(pixbuf)
		}
	}

	if image != nil {
		button.SetImage(image)
		button.SetImagePosition(gtk.PosTop)
		button.SetAlwaysShowImage(true)
	}
	button.SetTooltipText(getName(t.Class))

	var img *gtk.Image
	var pixbuf *gdkpixbuf.Pixbuf
	var err error
	if len(instances) > 1 {
		if !vertical {
			pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-multiple.svg"),
				imgSizeScaled, imgSizeScaled/8)
		} else {
			pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-multiple-vertical.svg"),
				imgSizeScaled/8, imgSizeScaled)
		}
	} else if len(instances) == 1 {
		if !vertical {
			pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-single.svg"),
				imgSizeScaled, imgSizeScaled/8)
		} else {
			pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-single-vertical.svg"),
				imgSizeScaled/8, imgSizeScaled)
		}
	} else {
		if !vertical {
			pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-empty.svg"),
				imgSizeScaled, imgSizeScaled/8)
		} else {
			pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/task-empty-vertical.svg"),
				imgSizeScaled/8, imgSizeScaled)
		}
	}
	if err == nil {
		img = gtk.NewImageFromPixbuf(pixbuf)
	}
	if img != nil {
		if *position == "left" || *position == "top" {
			box.PackStart(img, false, false, 0)
			box.PackStart(button, false, false, 0)
		} else {
			box.PackStart(button, false, false, 0)
			box.PackStart(img, false, false, 0)
		}

	}
	button.Connect("enter-notify-event", cancelClose)

	if len(instances) == 1 {
		button.Connect("event", func(btn *gtk.Button, e *gdk.Event) bool {
			btnEvent := e.AsButton()
			if btnEvent.Type() == gdk.ButtonReleaseType || btnEvent.Type() == gdk.TouchEndType {
				if dndBlocksClick() {
					return true
				}
				if btnEvent.Button() == 1 || btnEvent.Type() == gdk.TouchEndType {
					focusWindow(t.Address)
					return true
				} else if btnEvent.Button() == 2 {
					launch(t.Class)
					return true
				} else if btnEvent.Button() == 3 {
					contextMenu := clientMenuContext(t.Class, instances)
					contextMenu.PopupAtWidget(button, widgetAnchor, menuAnchor, nil)
					return true
				}
			}
			return false
		})
	} else {
		button.Connect("button-release-event", func(btn *gtk.Button, e *gdk.Event) bool {
			if dndBlocksClick() {
				return true
			}
			btnEvent := e.AsButton()
			if btnEvent.Button() == 1 {
				menu := clientMenu(t.Class, instances)
				menu.PopupAtWidget(button, widgetAnchor, menuAnchor, nil)
				return true
			} else if btnEvent.Button() == 2 {
				launch(t.Class)
				return true
			} else if btnEvent.Button() == 3 {
				contextMenu := clientMenuContext(t.Class, instances)
				contextMenu.PopupAtWidget(button, widgetAnchor, menuAnchor, nil)
				return true
			}
			return false
		})
	}

	return box
}

func clientMenu(class string, instances []client) gtk.Menu {
	menu := gtk.NewMenu()
	trackMenu(menu)

	iconName, err := getIcon(class)
	if err != nil {
		log.Warn(err)
	}
	for _, instance := range instances {
		menuItem := gtk.NewMenuItem()
		hbox := gtk.NewBox(gtk.OrientationHorizontal, 6)
		image := gtk.NewImageFromIconName(iconName, int(gtk.IconSizeMenu))
		hbox.PackStart(image, false, false, 0)
		title := instance.Title
		if len(title) > 25 {
			title = title[:25]
		}
		var label *gtk.Label
		label = gtk.NewLabel(fmt.Sprintf("%s (%v)", title, instance.Workspace.Name))
		hbox.PackStart(label, false, false, 0)
		menuItem.Add(hbox)
		menu.Append(menuItem)
		instance := instance
		menuItem.Connect("activate", func() {
			focusWindow(instance.Address)
		})

	}
	menu.ShowAll()
	return *menu
}

func clientMenuContext(class string, instances []client) gtk.Menu {
	menu := gtk.NewMenu()
	trackMenu(menu)

	iconName, err := getIcon(class)
	if err != nil {
		log.Warnf("%s %s", err, class)
	}
	for _, instance := range instances {
		menuItem := gtk.NewMenuItem()
		hbox := gtk.NewBox(gtk.OrientationHorizontal, 6)
		image := gtk.NewImageFromIconName(iconName, int(gtk.IconSizeMenu))
		hbox.PackStart(image, false, false, 0)
		title := instance.Title

		if len(title) > 25 {
			title = title[:25]
		}

		label := gtk.NewLabel(fmt.Sprintf("%s (%v)", title, instance.Workspace.Name))
		hbox.PackStart(label, false, false, 0)
		menuItem.Add(hbox)
		menu.Append(menuItem)
		submenu := gtk.NewMenu()
		trackMenu(submenu)

		a := instance.Address

		subitem := gtk.NewMenuItemWithLabel("closewindow")
		submenu.Append(subitem)
		subitem.Connect("activate", func() {
			closeWindow(a)
		})

		subitem = gtk.NewMenuItemWithLabel("togglefloating")
		submenu.Append(subitem)
		subitem.Connect("activate", func() {
			toggleFloatingWindow(a)
			focusWindow(a)
		})

		subitem = gtk.NewMenuItemWithLabel("fullscreen")
		submenu.Append(subitem)
		subitem.Connect("activate", func() {
			toggleFullscreenWindow(a)
			focusWindow(a)
		})

		s := gtk.NewSeparatorMenuItem()
		submenu.Append(&s.MenuItem)

		for i := 1; i < int(*numWS)+1; i++ {
			subItem := gtk.NewMenuItemWithLabel(fmt.Sprintf("-> WS %v", i))
			target := i
			subItem.Connect("activate", func() {
				moveWindowToWorkspace(a, target)
			})
			submenu.Append(subItem)
		}

		menuItem.SetSubmenu(submenu)
	}
	separator := gtk.NewSeparatorMenuItem()
	menu.Append(&separator.MenuItem)

	item := gtk.NewMenuItemWithLabel("New window")
	item.Connect("activate", func() {
		launch(class)
	})
	menu.Append(item)

	closeAllWindows := gtk.NewMenuItem()
	closeAllWindows.SetLabel("Close all windows")
	closeAllWindows.Connect("activate", func() {
		for _, instance := range instances {
			closeWindow(instance.Address)
		}
	})
	menu.Append(closeAllWindows)

	pinItem := gtk.NewMenuItem()
	if !inPinned(class) {
		pinItem.SetLabel("Pin")
		pinItem.Connect("activate", func() {
			log.Infof("pin %s", class)
			pinTask(class)
			if *dnd {
				// move it to the pinned items right away
				rebuildWhenIdle()
			}
		})
	} else {
		pinItem.SetLabel("Unpin")
		pinItem.Connect("activate", func() {
			log.Infof("unpin %s", class)
			unpinTask(class)
		})
	}
	menu.Append(pinItem)
	addAppMenuItem(menu)

	menu.ShowAll()
	return *menu
}

func inPinned(taskID string) bool {
	for _, id := range pinned {
		if strings.TrimSpace(taskID) == strings.TrimSpace(id) {
			return true
		}
	}
	return false
}

func inTasks(pinID string) bool {
	for _, task := range clients {
		if strings.TrimSpace(task.Class) == strings.TrimSpace(pinID) {
			return true
		}
	}
	return false
}

func createImage(appID string, size int) (*gtk.Image, error) {
	name, err := getIcon(appID)
	if err != nil {
		name = appID
	}
	pixbuf, e := createPixbuf(name, size)
	if e != nil {
		return nil, err
	}
	image := gtk.NewImageFromPixbuf(pixbuf)

	return image, nil
}

func createPixbuf(icon string, size int) (*gdkpixbuf.Pixbuf, error) {
	if strings.HasPrefix(icon, "/") {
		pixbuf, err := gdkpixbuf.NewPixbufFromFileAtSize(icon, size, size)
		if err != nil {
			log.Errorf("%s", err)
			return nil, err
		}
		return pixbuf, nil
	}

	iconTheme := gtk.IconThemeGetDefault()
	pixbuf, err := iconTheme.LoadIcon(icon, size, gtk.IconLookupForceSize)
	if err != nil {
		ico, err := getIcon(icon)
		if err != nil {
			return nil, err
		}

		if strings.HasPrefix(ico, "/") {
			pixbuf, err := gdkpixbuf.NewPixbufFromFileAtSize(ico, size, size)
			if err != nil {
				return nil, err
			}
			return pixbuf, nil
		}

		pixbuf, err := iconTheme.LoadIcon(ico, size, gtk.IconLookupForceSize)
		if err != nil {
			return nil, err
		}
		return pixbuf, nil
	}
	return pixbuf, nil
}

func cacheDir() string {
	if os.Getenv("XDG_CACHE_HOME") != "" {
		return os.Getenv("XDG_CACHE_HOME")
	}
	if os.Getenv("HOME") != "" && pathExists(filepath.Join(os.Getenv("HOME"), ".cache")) {
		p := filepath.Join(os.Getenv("HOME"), ".cache")
		return p
	}
	return ""
}

func tempDir() string {
	if os.Getenv("TMPDIR") != "" {
		return os.Getenv("TMPDIR")
	} else if os.Getenv("TEMP") != "" {
		return os.Getenv("TEMP")
	} else if os.Getenv("TMP") != "" {
		return os.Getenv("TMP")
	}
	return "/tmp"
}

func readTextFile(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

func configDir() string {
	if os.Getenv("XDG_CONFIG_HOME") != "" {
		return fmt.Sprintf("%s/nwg-dock-hyprland", os.Getenv("XDG_CONFIG_HOME"))
	}
	return fmt.Sprintf("%s/.config/nwg-dock-hyprland", os.Getenv("HOME"))
}

func createDir(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.ModePerm)
		if err == nil {
			log.Infof("Creating dir: %s", dir)
		}
	}
}

func copyFile(src, dst string) error {
	log.Infof("Copying file: %s", dst)

	var err error
	var srcfd *os.File
	var dstfd *os.File
	var srcinfo os.FileInfo

	if srcfd, err = os.Open(src); err != nil {
		return err
	}
	defer srcfd.Close()

	if dstfd, err = os.Create(dst); err != nil {
		return err
	}
	defer dstfd.Close()

	if _, err = io.Copy(dstfd, srcfd); err != nil {
		return err
	}
	if srcinfo, err = os.Stat(src); err != nil {
		return err
	}
	return os.Chmod(dst, srcinfo.Mode())
}

func getDataHome() (string, error) {
	var dirs []string
	home := os.Getenv("HOME")
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome != "" {
		dirs = append(dirs, xdgDataHome)
	} else if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local/share"))
	}

	var xdgDataDirs []string
	if os.Getenv("XDG_DATA_DIRS") != "" {
		xdgDataDirs = strings.Split(os.Getenv("XDG_DATA_DIRS"), ":")
	} else {
		xdgDataDirs = []string{"/usr/local/share/", "/usr/share/"}
	}
	dirs = append(dirs, xdgDataDirs...)

	for _, d := range dirs {
		if pathExists(filepath.Join(d, "nwg-dock-hyprland")) {
			return d, nil
		}
	}
	return "", errors.New("no data directory found for nwg-dock-hyprland")
}

func getAppDirs() []string {
	var dirs []string
	xdgDataDirs := ""

	home := os.Getenv("HOME")
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if os.Getenv("XDG_DATA_DIRS") != "" {
		xdgDataDirs = os.Getenv("XDG_DATA_DIRS")
	} else {
		xdgDataDirs = "/usr/local/share/:/usr/share/"
	}
	if xdgDataHome != "" {
		dirs = append(dirs, filepath.Join(xdgDataHome, "applications"))
	} else if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local/share/applications"))
	}
	for _, d := range strings.Split(xdgDataDirs, ":") {
		dirs = append(dirs, filepath.Join(d, "applications"))
	}
	flatpakDirs := []string{filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
		"/var/lib/flatpak/exports/share/applications"}

	for _, d := range flatpakDirs {
		if !isIn(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func isIn(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

func getIcon(appName string) (string, error) {
	appName = strings.Split(appName, " ")[0]
	if strings.HasPrefix(strings.ToUpper(appName), "GIMP") {
		return "gimp", nil
	}
	p := ""
	for _, d := range appDirs {
		path := filepath.Join(d, fmt.Sprintf("%s.desktop", appName))
		if pathExists(path) {
			p = path
			break
		} else if pathExists(strings.ToLower(path)) {
			p = strings.ToLower(path)
			break
		}
	}
	/* Some apps' class varies from their .desktop file name, e.g. 'gimp-2.9.9' or 'pamac-manager'.
	   Let's try to find a matching .desktop file name */
	if !strings.HasPrefix(appName, "/") && p == "" { // skip icon paths given instead of names
		p = searchDesktopDirs(appName)
	}

	if p != "" {
		lines, err := loadTextFile(p)
		if err != nil {
			return "", err
		}
		for _, line := range lines {
			if strings.HasPrefix(strings.ToUpper(line), "ICON") {
				return strings.Split(line, "=")[1], nil
			}
		}
	}
	return "", errors.New("couldn't find the icon")
}

func searchDesktopDirs(badAppID string) string {
	b4Separator := strings.Split(badAppID, "-")[0]
	for _, d := range appDirs {
		items, _ := os.ReadDir(d)
		for _, item := range items {
			if strings.Contains(item.Name(), b4Separator) {
				//Let's check items starting from 'org.' first
				if strings.Count(item.Name(), ".") > 1 && strings.HasSuffix(item.Name(),
					fmt.Sprintf("%s.desktop", badAppID)) {
					return filepath.Join(d, item.Name())
				}
			}
		}
	}
	// exceptions like "class": "VirtualBox Manager" & virtualbox.desktop
	b4Separator = strings.Split(badAppID, " ")[0]
	for _, d := range appDirs {
		items, _ := os.ReadDir(d)

		// first look for exact 'class.desktop' file, see #31
		for _, item := range items {
			if strings.ToUpper(item.Name()) == strings.ToUpper(fmt.Sprintf("%s.desktop", badAppID)) {
				return filepath.Join(d, item.Name())
			}
		}

		for _, item := range items {
			if strings.Contains(strings.ToUpper(item.Name()), strings.ToUpper(b4Separator)) {
				return filepath.Join(d, item.Name())
			}
		}

		for _, item := range items {
			if item.IsDir() {
				continue
			}

			p := filepath.Join(d, item.Name())
			lines, err := readTextFile(p)

			if err != nil {
				log.Warn(err)
			} else {
				if strings.Contains(lines, "StartupWMClass="+b4Separator) {
					return filepath.Join(d, item.Name())
				}
			}
		}
	}
	return ""
}

func getExec(appName string) (string, error) {
	cmd := appName
	if strings.HasPrefix(strings.ToUpper(appName), "GIMP") {
		cmd = "gimp"
	}
	path := ""
	for _, d := range appDirs {
		files, _ := os.ReadDir(d)
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".desktop") {
				if f.Name() == fmt.Sprintf("%s.desktop", appName) ||
					f.Name() == fmt.Sprintf("%s.desktop", strings.ToLower(appName)) {
					path = filepath.Join(d, f.Name())
					break
				}
			}
		}
	}

	// as above in getIcon - for tasks w/ improper app_id
	if path == "" {
		path = searchDesktopDirs(appName)
	}

	if path != "" {
		lines, err := loadTextFile(path)
		if err != nil {
			return "", err
		}
		for _, line := range lines {
			if strings.HasPrefix(strings.ToUpper(line), "EXEC") {
				l := line[5:]
				cutAt := strings.Index(l, "%")
				if cutAt != -1 {
					l = l[:cutAt-1]
				}
				cmd = l
				break
			}
		}
		return cmd, nil
	}

	return cmd, nil
}

func getName(appName string) string {
	name := appName
	path := ""

	for _, d := range appDirs {
		files, _ := os.ReadDir(d)
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".desktop") {
				if f.Name() == fmt.Sprintf("%s.desktop", appName) ||
					f.Name() == fmt.Sprintf("%s.desktop", strings.ToLower(appName)) {
					path = filepath.Join(d, f.Name())
					break
				}
			}
		}
	}

	// as above in getIcon - for tasks w/ improper app_id
	if path == "" {
		path = searchDesktopDirs(appName)
	}

	if path != "" {
		lines, err := loadTextFile(path)
		if err != nil {
			return name
		}
		for _, line := range lines {
			if strings.HasPrefix(strings.ToUpper(line), "NAME=") {
				name = line[5:]
				break
			}
		}
	}

	return name
}

func pathExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func loadTextFile(path string) ([]string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(bytes), "\n")
	var output []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			output = append(output, line)
		}

	}
	return output, nil
}

func pinTask(itemID string) {
	for _, item := range pinned {
		if item == itemID {
			println(item, "already pinned")
			return
		}
	}
	pinned = append(pinned, itemID)
	savePinned()
}

func unpinTask(itemID string) {
	pinned = remove(pinned, itemID)
	savePinned()
	buildMainBox()
}

func remove(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func savePinned() {
	f, err := os.OpenFile(pinnedFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		log.Fatal(err)
	}

	defer f.Close()

	for _, line := range pinned {
		if line != "" {
			_, err := f.WriteString(line + "\n")

			if err != nil {
				log.Errorf("Error saving pinned %s", err)
			}
		}
	}
}

/*
Reordering of pinned items by dragging them, enabled with the -dnd flag.
It's built on plain pointer events, not on GTK/Wayland drag and drop: the compositor
never enters a drag session, so no drag can get stuck there and freeze the desktop.
While dragging, the other pinned items make room live; releasing outside the dock cancels.
All the state below is only touched on the GTK main thread.
*/

type dndItem struct {
	box    *gtk.Box
	id     string
	pinIdx int // index in `pinned`
}

var (
	dndItems          []*dndItem // displayed pinned items, in display order; reset by buildMainBox
	dndBase           int        // mainBox position of the first pinned item
	dndPressed        *dndItem   // item under a left button press, which may turn into a drag
	dndPressX         int
	dndPressY         int
	dndDragged        *dndItem
	dndSlots          []int // centers of the pinned item slots when the drag started
	dndDragActive     bool  // a pinned item is being dragged
	dndDragJustEnded  bool  // swallow clicks that may follow a drag
	dndRefreshPending bool  // rebuild the dock once the drag ends
	dndHidePending    bool  // hide the dock once the drag ends
	dndBackupDone     bool  // the pinned file has been backed up in this session
)

func dndDragging() bool {
	return *dnd && dndDragActive
}

func dndBlocksClick() bool {
	return *dnd && (dndDragActive || dndDragJustEnded)
}

func dndDeferRefresh() bool {
	if dndDragging() {
		dndRefreshPending = true
		return true
	}
	return false
}

func dndDeferHide() bool {
	if dockHeld() {
		dndHidePending = true
		return true
	}
	return false
}

/*
Makes the pinned item `ID`, at index `pinIdx` of `pinned` and displayed in `box`, draggable.
Must be called before connecting the button's own handlers, so that the release ending a drag
never reaches them.
*/
func setupPinnedDnd(box *gtk.Box, button *gtk.Button, ID string, pinIdx int) {
	if !*dnd {
		return
	}
	item := &dndItem{box: box, id: ID, pinIdx: pinIdx}
	dndItems = append(dndItems, item)

	button.AddEvents(int(gdk.ButtonMotionMask))
	button.Connect("event", func(btn *gtk.Button, e *gdk.Event) bool {
		switch e.AsType() {
		case gdk.ButtonPressType:
			if e.AsButton().Button() == 1 && dndDragged == nil {
				dndPressed = item
				dndPressX, dndPressY = win.Pointer()
			}
		case gdk.MotionNotifyType:
			if dndDragged == nil && dndPressed == item {
				x, y := win.Pointer()
				if win.DragCheckThreshold(dndPressX, dndPressY, x, y) {
					dndDragStart(item)
				}
			}
			if dndDragged == item {
				dndDragMotion()
				return true
			}
		case gdk.ButtonReleaseType:
			dndPressed = nil
			if dndDragged == item {
				dndDragFinish(true)
				return true
			}
		case gdk.GrabBrokenType:
			dndPressed = nil
			if dndDragged == item {
				dndDragFinish(false)
			}
		}
		return false
	})
}

func dndDragStart(item *dndItem) {
	dndDragged = item
	dndDragActive = true
	cancelClose()

	dndSlots = nil
	for _, it := range dndItems {
		a := it.box.Allocation()
		if vertical {
			dndSlots = append(dndSlots, a.Y()+a.Height()/2)
		} else {
			dndSlots = append(dndSlots, a.X()+a.Width()/2)
		}
	}

	item.box.SetOpacity(0.5)
	if w := win.Window(); w != nil {
		if cursor := gdk.NewCursorFromName(win.Display(), "grabbing"); cursor != nil {
			gdk.BaseWindow(w).SetCursor(cursor)
		}
	}
	log.Debugf("Drag start: '%s' at %d", item.id, item.pinIdx)
}

// Moves the dragged item to the slot nearest to the pointer, shifting the others
func dndDragMotion() {
	x, y := win.Pointer()
	pos := x
	if vertical {
		pos = y
	}
	items, moved := moveItem(dndItems, dndDragged, nearestSlot(dndSlots, pos))
	if !moved {
		return
	}
	dndItems = items
	for i, it := range dndItems {
		mainBox.ReorderChild(it.box, dndBase+i)
	}
}

// Index of the slot whose center is nearest to `pos`, or -1 if there are no slots
func nearestSlot(slots []int, pos int) int {
	nearest := -1
	for i, center := range slots {
		if nearest < 0 || abs(center-pos) < abs(slots[nearest]-pos) {
			nearest = i
		}
	}
	return nearest
}

// Returns `items` with `item` moved to index `target`, and whether anything moved
func moveItem(items []*dndItem, item *dndItem, target int) ([]*dndItem, bool) {
	current := slices.Index(items, item)
	if current < 0 || target < 0 || target >= len(items) || target == current {
		return items, false
	}
	moved := slices.Clone(items)
	moved = slices.Delete(moved, current, current+1)
	moved = slices.Insert(moved, target, item)
	return moved, true
}

// Ends the drag; the new order is saved if `commit` is set and the pointer is still over the dock
func dndDragFinish(commit bool) {
	inside := pointerInsideDock()

	dndDragged.box.SetOpacity(1)
	if w := win.Window(); w != nil {
		gdk.BaseWindow(w).SetCursor(nil)
	}
	log.Debugf("Drag end: '%s', commit: %v, inside: %v", dndDragged.id, commit, inside)
	dndDragged = nil
	dndDragActive = false
	dndDragJustEnded = true
	glib.TimeoutAdd(uint(250), func() bool {
		dndDragJustEnded = false
		return false
	})

	if commit && inside {
		dndSaveDisplayedOrder()
	}

	// Rebuild in any case: it restores the order after a cancel, and runs any deferred refresh
	dndRefreshPending = false
	rebuildWhenIdle()

	if pickerOpen() {
		return
	}
	if dndHidePending {
		dndHidePending = false
		win.Hide()
	} else if *autohide && !inside {
		// The pointer left the dock during the drag, while closing was suppressed
		cancelClose()
		scheduleClose()
	}
}

// Saves `pinned` with the displayed pinned items in their new order
func dndSaveDisplayedOrder() {
	onDisk, err := loadTextFile(pinnedFile)
	if err != nil || !slices.Equal(onDisk, pinned) {
		log.Warn("Pinned file changed outside the dock, reordering aborted")
		return
	}

	order, err := reorderedPinned(pinned, dndItems)
	if err != nil {
		log.Warnf("%s, reordering aborted", err)
		return
	}
	if slices.Equal(order, pinned) {
		return
	}

	err = savePinnedOrder(order)
	if err != nil {
		log.Errorf("Error saving pinned order: %s", err)
		return
	}
	pinned = order
	log.Infof("Pinned order saved: %s", strings.Join(order, ", "))
}

/*
Returns a copy of `pinned` with the displayed `items` written, in their display order, into the
slots they occupied. Pinned items that aren't displayed (ignored ones, duplicates) keep their place.
*/
func reorderedPinned(pinned []string, items []*dndItem) ([]string, error) {
	var slots []int
	for _, it := range items {
		if it.pinIdx < 0 || it.pinIdx >= len(pinned) || pinned[it.pinIdx] != it.id {
			return nil, errors.New("pinned items moved since the dock was built")
		}
		if slices.Contains(slots, it.pinIdx) {
			return nil, errors.New("pinned item displayed twice")
		}
		slots = append(slots, it.pinIdx)
	}
	slices.Sort(slots)

	order := slices.Clone(pinned)
	for i, it := range items {
		order[slots[i]] = it.id
	}
	return order, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

/*
Exits if the GTK main loop stops responding, so that the launcher script restarts the dock
instead of leaving a frozen one on the screen.
*/
func startHangWatchdog() {
	const limit = 10 * time.Second
	var beat atomic.Int64
	beat.Store(time.Now().UnixNano())
	glib.TimeoutAdd(uint(1000), func() bool {
		beat.Store(time.Now().UnixNano())
		return true
	})

	go func() {
		last := time.Now()
		for {
			time.Sleep(2 * time.Second)
			now := time.Now()
			stalled, hung := mainLoopHung(now, last, time.Unix(0, beat.Load()), limit)
			last = now
			if hung {
				log.Errorf("Main loop unresponsive for %s, exiting so that the dock gets restarted", stalled.Round(time.Second))
				os.Exit(2)
			}
		}
	}()
}

/*
Tells how long the main loop has been stalled, judging by its `lastBeat`, and whether it's hung.
A watchdog check (`now`) much later than the previous one (`lastCheck`) means the system was
suspended: both clocks jumped, so the main loop gets a chance to catch up.
*/
func mainLoopHung(now, lastCheck, lastBeat time.Time, limit time.Duration) (time.Duration, bool) {
	stalled := now.Sub(lastBeat)
	if now.Sub(lastCheck) > 5*time.Second {
		return stalled, false
	}
	return stalled, stalled > limit
}

/*
Atomically replaces the pinned file with `order`, backing up the original file once per session.
Unlike savePinned, it never leaves a truncated file behind.
*/
func savePinnedOrder(order []string) error {
	if len(order) == 0 {
		return errors.New("refusing to save an empty pinned list")
	}
	info, err := os.Stat(pinnedFile)
	if err != nil {
		return err
	}

	if !dndBackupDone {
		err = copyFile(pinnedFile, pinnedFile+".dnd.bak")
		if err != nil {
			return fmt.Errorf("backing up %s: %w", pinnedFile, err)
		}
		dndBackupDone = true
	}

	dir := filepath.Dir(pinnedFile)
	tmp, err := os.CreateTemp(dir, ".nwg-dock-pinned-*")
	if err != nil {
		return err
	}
	// a no-op once the file has been renamed
	defer os.Remove(tmp.Name())

	_, err = tmp.WriteString(strings.Join(order, "\n") + "\n")
	if err == nil {
		err = tmp.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	err = os.Rename(tmp.Name(), pinnedFile)
	if err != nil {
		return err
	}

	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func launch(ID string) {
	command, err := getExec(ID)
	if err != nil {
		log.Errorf("%s", err)
	}
	// remove quotation marks if any
	if strings.Contains(command, "\"") {
		command = strings.ReplaceAll(command, "\"", "")
	}

	elements := strings.Split(command, " ")

	// find prepended env variables, if any
	envVarsNum := strings.Count(command, "=")
	var envVars []string

	cmdIdx := -1

	if envVarsNum > 0 {
		for idx, item := range elements {
			if strings.Contains(item, "=") {
				envVars = append(envVars, item)
			} else if !strings.HasPrefix(item, "-") && cmdIdx == -1 {
				cmdIdx = idx
			}
		}
	}
	if cmdIdx == -1 {
		cmdIdx = 0
	}
	var args []string
	for _, arg := range elements[1+cmdIdx:] {
		if !strings.Contains(arg, "=") {
			args = append(args, arg)
		}
	}

	cmd := exec.Command(elements[cmdIdx], elements[1+cmdIdx:]...)

	// set env variables
	if len(envVars) > 0 {
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env, envVars...)
	}

	msg := fmt.Sprintf("env vars: %s; command: '%s'; args: %s\n", envVars, elements[cmdIdx], args)
	log.Info(msg)

	if err := cmd.Start(); err != nil {
		log.Error("Unable to launch command!", err.Error())
	}

	if *autohide {
		win.Hide()
	}
}

// Returns map output name -> gdk.Monitor
func mapOutputs() (map[string]*gdk.Monitor, error) {
	result := make(map[string]*gdk.Monitor)

	err := listMonitors()
	if err != nil {
		log.Fatalf("Error listing monitors: %v", err)
	}

	display := gdk.DisplayGetDefault()
	if err != nil {
		log.Fatalf("Error finding default GDK display: %v", err)
	}

	num := display.NMonitors()
	for i := 0; i < num; i++ {
		mon := display.Monitor(i)
		result[monitors[i].Name] = mon
	}
	return result, nil
}

func listGdkMonitors() ([]gdk.Monitor, error) {
	var monitors []gdk.Monitor
	display := gdk.DisplayGetDefault()

	num := display.NMonitors()
	for i := 0; i < num; i++ {
		monitor := display.Monitor(i)
		monitors = append(monitors, *monitor)
	}
	return monitors, nil
}

// Returns output of a CLI command with optional arguments
func getCommandOutput(command string) string {
	out, err := exec.Command("env", "-S", command).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

func isCommand(command string) bool {
	cmd := strings.Fields(command)[0]
	return getCommandOutput(fmt.Sprintf("command -v %s ", cmd)) != ""
}

func md5Hash(text string) string {
	hash := md5.Sum([]byte(text))
	return hex.EncodeToString(hash[:])
}
