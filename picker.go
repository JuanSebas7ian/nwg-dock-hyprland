package main

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4-layer-shell/pkg/gtklayershell"
	"github.com/diamondburned/gotk4/pkg/gdk/v3"
	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v3"
	"github.com/diamondburned/gotk4/pkg/pango"
	log "github.com/sirupsen/logrus"
)

/*
App picker, enabled with the -dnd flag: a searchable list of the installed applications,
opened from the "Add app…" context menu item, where a click pins or unpins an app.
The picker only takes the keyboard on demand, so it can never lock the keyboard of the session.
*/

type desktopApp struct {
	id   string // desktop file name without ".desktop", as stored in the pinned file
	name string
	icon string
}

var (
	pickerWin       *gtk.Window
	pickerCloseSrc  glib.SourceHandle // pending close after the pointer left the picker
	pickerWidth     = 420
	pickerMaxHeight = 640
	pickerMinHeight = 200
)

func pickerOpen() bool {
	return pickerWin != nil
}

// The dock must stay visible while an item is being dragged, or the picker or a menu is open
func dockHeld() bool {
	return dndDragging() || (*dnd && (pickerOpen() || menuShown()))
}

/*
Parses the [Desktop Entry] group of a desktop file. `lang` is the language of the user
(e.g. "es"), used to prefer a localized name. Returns visible=false for entries that
shouldn't be listed.
*/
func parseDesktopEntry(content, lang string) (name, icon string, visible bool) {
	inEntry := false
	localized := ""
	isApp, hidden := false, false

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "Name":
			name = value
		case "Name[" + lang + "]":
			localized = value
		case "Icon":
			icon = value
		case "Type":
			isApp = value == "Application"
		case "NoDisplay", "Hidden":
			if value == "true" {
				hidden = true
			}
		}
	}
	if localized != "" {
		name = localized
	}
	return name, icon, isApp && !hidden && name != ""
}

// Lists the visible applications found in `dirs`; the first desktop file with a given id wins
func loadDesktopApps(dirs []string, lang string) []desktopApp {
	var apps []desktopApp
	seen := map[string]bool{}
	for _, dir := range dirs {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.desktop"))
		for _, path := range paths {
			id := strings.TrimSuffix(filepath.Base(path), ".desktop")
			if seen[id] {
				continue
			}
			seen[id] = true
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if name, icon, visible := parseDesktopEntry(string(content), lang); visible {
				apps = append(apps, desktopApp{id: id, name: name, icon: icon})
			}
		}
	}
	slices.SortFunc(apps, func(a, b desktopApp) int {
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})
	return apps
}

// Whether `app` matches the search `query`: every word must appear in its name or id
func appMatches(app desktopApp, query string) bool {
	haystack := strings.ToLower(app.name + " " + app.id)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

func userLanguage() string {
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if lang := os.Getenv(v); lang != "" && lang != "C" && lang != "POSIX" {
			return strings.SplitN(strings.SplitN(lang, "_", 2)[0], ".", 2)[0]
		}
	}
	return ""
}

// Pins or unpins `id`, then rebuilds the dock
func togglePin(id string) {
	if inPinned(id) {
		log.Infof("unpin %s", id)
		unpinTask(id)
		return
	}
	log.Infof("pin %s", id)
	pinTask(id)
	rebuildWhenIdle()
}

// Rebuilds the dock once GTK is idle, so that no signal handler destroys its own widget
func rebuildWhenIdle() {
	glib.IdleAdd(func() bool {
		buildMainBox()
		oldClients = clients
		return false
	})
}

func addAppMenuItem(menu *gtk.Menu) {
	if !*dnd {
		return
	}
	item := gtk.NewMenuItemWithLabel("Add app…")
	item.Connect("activate", func() {
		// let the context menu close first
		glib.IdleAdd(func() bool {
			openPicker()
			return false
		})
	})
	menu.Append(item)
}

func openPicker() {
	trace("picker open")
	if pickerOpen() {
		pickerWin.Present()
		return
	}
	cancelClose()

	w := gtk.NewWindow(gtk.WindowToplevel)
	pickerWin = w
	gtklayershell.InitForWindow(w)
	gtklayershell.SetNamespace(w, "nwg-dock-picker")
	gtklayershell.SetLayer(w, gtklayershell.LayerShellLayerOverlay)
	gtklayershell.SetKeyboardMode(w, gtklayershell.LayerShellKeyboardModeOnDemand)
	edge := map[string]gtklayershell.Edge{
		"top":    gtklayershell.LayerShellEdgeTop,
		"left":   gtklayershell.LayerShellEdgeLeft,
		"right":  gtklayershell.LayerShellEdgeRight,
		"bottom": gtklayershell.LayerShellEdgeBottom,
	}[*position]
	gtklayershell.SetAnchor(w, edge, true)
	dockSize := win.AllocatedHeight()
	if vertical {
		dockSize = win.AllocatedWidth()
	}
	gap := dockSize + *marginBottom + 8
	gtklayershell.SetMargin(w, edge, gap)
	height := pickerHeight(monitorHeight(), gap, vertical)
	w.SetObjectProperty("name", "picker")

	vbox := gtk.NewBox(gtk.OrientationVertical, 6)
	vbox.SetObjectProperty("name", "picker-box")
	w.Add(vbox)

	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	search := gtk.NewSearchEntry()
	search.SetPlaceholderText("Search apps to pin or unpin…")
	header.PackStart(search, true, true, 0)
	closeButton := gtk.NewButtonWithLabel("✕")
	closeButton.SetTooltipText("Close (Esc)")
	closeButton.Connect("clicked", closePicker)
	header.PackStart(closeButton, false, false, 0)
	vbox.PackStart(header, false, false, 0)

	apps := loadDesktopApps(appDirs, userLanguage())
	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.SetActivateOnSingleClick(true)
	marks := make([]*gtk.Label, len(apps))
	for i, app := range apps {
		list.Insert(pickerRow(app, &marks[i]), -1)
	}

	query := ""
	list.SetFilterFunc(func(row *gtk.ListBoxRow) bool {
		i := row.Index()
		return i >= 0 && i < len(apps) && appMatches(apps[i], query)
	})
	toggle := func(i int) {
		togglePin(apps[i].id)
		updateMark(marks[i], apps[i].id)
	}
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if i := row.Index(); i >= 0 && i < len(apps) {
			toggle(i)
		}
	})
	search.ConnectSearchChanged(func() {
		query = search.Text()
		list.InvalidateFilter()
	})
	// Enter toggles the first match
	search.ConnectActivate(func() {
		for i, app := range apps {
			if appMatches(app, query) {
				toggle(i)
				return
			}
		}
	})

	scrolled := gtk.NewScrolledWindow(nil, nil)
	scrolled.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	// layer-shell windows ignore the default size: the size request sets it
	scrolled.SetSizeRequest(pickerWidth, height-50)
	scrolled.Add(list)
	vbox.PackStart(scrolled, true, true, 0)

	w.ConnectKeyPressEvent(func(event *gdk.EventKey) bool {
		if event.Keyval() == gdk.KEY_Escape {
			closePicker()
			return true
		}
		return false
	})
	/*
		Don't close on focus out: with follow_mouse (Omarchy's default), the keyboard focus moves to
		any window the pointer crosses on its way to the picker. Close when the pointer stays away
		from the picker instead, as the dock does.
	*/
	w.Connect("leave-notify-event", func(_ *gtk.Window, e *gdk.Event) bool {
		// crossing into a child widget's window isn't leaving the picker
		if e.AsCrossing().Detail() != gdk.NotifyInferior {
			schedulePickerClose()
		}
		return false
	})
	w.Connect("enter-notify-event", func() bool {
		cancelPickerClose()
		return false
	})

	w.ShowAll()
	search.GrabFocus()
	// if the pointer never reaches the picker, it's not wanted: close it after a while
	cancelPickerClose()
	pickerCloseSrc = glib.TimeoutAdd(uint(4000), func() bool {
		pickerCloseSrc = 0
		closePicker()
		return false
	})
	log.Debugf("Picker opened with %d apps", len(apps))
}

func pickerRow(app desktopApp, mark **gtk.Label) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	hbox := gtk.NewBox(gtk.OrientationHorizontal, 8)
	hbox.SetMarginStart(6)
	hbox.SetMarginEnd(6)
	hbox.SetMarginTop(3)
	hbox.SetMarginBottom(3)

	pixbuf, err := createPixbuf(app.icon, 24)
	if err != nil || app.icon == "" {
		pixbuf, err = gdkpixbuf.NewPixbufFromFileAtSize(filepath.Join(dataHome, "nwg-dock-hyprland/images/icon-missing.svg"), 24, 24)
	}
	if err == nil {
		hbox.PackStart(gtk.NewImageFromPixbuf(pixbuf), false, false, 0)
	}

	label := gtk.NewLabel(app.name)
	label.SetXAlign(0)
	label.SetEllipsize(pango.EllipsizeEnd)
	label.SetTooltipText(app.id)
	hbox.PackStart(label, true, true, 0)

	*mark = gtk.NewLabel("")
	updateMark(*mark, app.id)
	hbox.PackEnd(*mark, false, false, 0)

	row.Add(hbox)
	return row
}

func updateMark(mark *gtk.Label, id string) {
	if inPinned(id) {
		mark.SetText("✓ pinned")
	} else {
		mark.SetText("")
	}
}

// Height of the monitor showing the dock, or 0 if unknown
func monitorHeight() int {
	gw := win.Window()
	if gw == nil {
		return 0
	}
	monitor := win.Display().MonitorAtWindow(gw)
	if monitor == nil {
		return 0
	}
	return monitor.Geometry().Height()
}

/*
Height of the picker: as tall as it fits on a monitor `monitorH` pixels high, between
pickerMinHeight and pickerMaxHeight. `gap` is the space the dock takes at the edge the picker
grows from; a vertical dock sits beside the picker, so it takes none.
*/
func pickerHeight(monitorH, gap int, vertical bool) int {
	if monitorH <= 0 {
		return 480
	}
	if vertical {
		gap = 0
	}
	h := monitorH - gap - 40
	return max(pickerMinHeight, min(pickerMaxHeight, h))
}

func schedulePickerClose() {
	cancelPickerClose()
	pickerCloseSrc = glib.TimeoutAdd(uint(1500), func() bool {
		pickerCloseSrc = 0
		closePicker()
		return false
	})
}

func cancelPickerClose() {
	if pickerCloseSrc > 0 {
		glib.SourceRemove(pickerCloseSrc)
		pickerCloseSrc = 0
	}
}

func closePicker() {
	trace("picker close")
	if !pickerOpen() {
		return
	}
	cancelPickerClose()
	pickerWin.Destroy()
	pickerWin = nil
	log.Debug("Picker closed")

	if dndHidePending && !dndDragging() {
		dndHidePending = false
		win.Hide()
	} else if *autohide && !dndDragging() && !pointerInsideDock() {
		cancelClose()
		scheduleClose()
	}
}

func pointerInsideDock() bool {
	x, y := win.Pointer()
	return x >= 0 && y >= 0 && x < win.AllocatedWidth() && y < win.AllocatedHeight()
}
