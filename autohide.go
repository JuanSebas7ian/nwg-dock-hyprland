package main

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v3"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v3"
	log "github.com/sirupsen/logrus"
)

/*
Idle auto-hide, used with -d and -dnd: hides the dock shortly after the pointer leaves it, or
after a while without any pointer activity over it. It's a periodic check rather than a reaction
to a single leave event, because on Wayland that event may never come (after a popup menu, the
app picker or a drag), which left the dock on screen.
*/

const (
	dockHideDelay   = 500 * time.Millisecond // after the pointer left the dock
	dockIdleTimeout = 5 * time.Second        // without pointer activity, even over the dock
	menuLeaveDelay  = 800 * time.Millisecond // after the pointer left an open menu and the dock
	menuIdleTimeout = 8 * time.Second        // without pointer activity over an open menu
	menuMinVisible  = 2 * time.Second        // an open menu is never closed sooner
	idleCheckPeriod = 200                    // ms
)

var (
	pointerInDock    bool
	pointerInMenu    bool
	menuShownAt      time.Time
	lastDockActivity = time.Now()
	openMenus        []*gtk.Menu
)

/*
Registers a dock menu or submenu. While it's shown and in use the dock stays visible; once the
pointer stays away from it and from the dock, it's closed and the dock hides.
*/
func trackMenu(menu *gtk.Menu) {
	if !*dnd {
		return
	}
	openMenus = append(openMenus, menu)
	menu.AddEvents(int(gdk.PointerMotionMask))
	menu.Connect("enter-notify-event", func() bool {
		pointerInMenu = true
		markDockActivity()
		return false
	})
	menu.Connect("leave-notify-event", func(_ *gtk.Menu, e *gdk.Event) bool {
		if e.AsCrossing().Detail() != gdk.NotifyInferior {
			pointerInMenu = false
			markDockActivity()
		}
		return false
	})
	menu.Connect("motion-notify-event", func() bool {
		markDockActivity()
		return false
	})
	menu.Connect("show", func() {
		menuShownAt = time.Now()
		markDockActivity()
	})
	menu.Connect("hide", func() {
		pointerInMenu = false
		markDockActivity()
	})
}

func popdownMenus() {
	for _, m := range openMenus {
		if m.IsVisible() {
			m.Popdown()
		}
	}
	pointerInMenu = false
}

// Whether any dock menu is shown; forgets the ones that aren't
func menuShown() bool {
	shown := false
	var kept []*gtk.Menu
	for _, m := range openMenus {
		if m.IsVisible() {
			shown = true
			kept = append(kept, m)
		}
	}
	openMenus = kept
	return shown
}

func markDockActivity() {
	lastDockActivity = time.Now()
}

/*
Whether the dock should hide now: it's been `idle` since the last pointer activity, and the
pointer is over the dock or its hotspot (`pointerOver`). A `held` dock (drag, picker, menu)
never hides.
*/
func shouldHideDock(idle time.Duration, pointerOver, held bool) bool {
	if held {
		return false
	}
	if pointerOver {
		return idle >= dockIdleTimeout
	}
	return idle >= dockHideDelay
}

/*
Whether an open menu should be closed (and the dock hidden): it's been `idle` since the last
pointer activity and `shown` since it opened, and the pointer is over the menu or the dock
(`pointerOver`). GTK may report the pointer as gone when a menu opens, so a menu always gets
menuMinVisible to be noticed.
*/
func shouldCloseMenu(idle, shown time.Duration, pointerOver bool) bool {
	if shown < menuMinVisible {
		return false
	}
	if pointerOver {
		return idle >= menuIdleTimeout
	}
	return idle >= menuLeaveDelay
}

func setupIdleHide() {
	win.AddEvents(int(gdk.PointerMotionMask))
	win.Connect("show", markDockActivity)
	win.Connect("enter-notify-event", func() bool {
		pointerInDock = true
		markDockActivity()
		return false
	})
	win.Connect("leave-notify-event", func(_ *gtk.Window, e *gdk.Event) bool {
		// crossing into a button's window isn't leaving the dock
		if e.AsCrossing().Detail() != gdk.NotifyInferior {
			pointerInDock = false
			markDockActivity()
		}
		return false
	})
	win.Connect("motion-notify-event", func() bool {
		markDockActivity()
		return false
	})

	glib.TimeoutAdd(uint(idleCheckPeriod), func() bool {
		if !win.IsVisible() {
			return true
		}
		if dndDragging() || pickerOpen() {
			// count the idle time from when the dock is released
			markDockActivity()
			return true
		}
		idle := time.Since(lastDockActivity)
		if menuShown() {
			if shouldCloseMenu(idle, time.Since(menuShownAt), pointerInDock || pointerInMenu) {
				log.Debug("Menu unused, closing it and hiding the dock")
				popdownMenus()
				hideIdleDock()
			}
			return true
		}
		if shouldHideDock(idle, pointerInDock || mouseInsideHotspot, false) {
			log.Debug("Idle, hiding the dock")
			hideIdleDock()
		}
		return true
	})
}

func hideIdleDock() {
	cancelClose()
	pointerInDock = false
	mouseInsideDock = false
	win.Hide()
}
