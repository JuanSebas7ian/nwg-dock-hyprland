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
	idleCheckPeriod = 200                    // ms
)

var (
	pointerInDock    bool
	lastDockActivity = time.Now()
	openMenus        []*gtk.Menu
)

// Registers a dock menu, so that the dock stays visible while it's shown
func trackMenu(menu *gtk.Menu) {
	if !*dnd {
		return
	}
	openMenus = append(openMenus, menu)
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
		held := dockHeld() || menuShown()
		if held {
			// count the idle time from when the dock is released
			markDockActivity()
			return true
		}
		if shouldHideDock(time.Since(lastDockActivity), pointerInDock || mouseInsideHotspot, held) {
			log.Debug("Idle, hiding the dock")
			cancelClose()
			pointerInDock = false
			mouseInsideDock = false
			win.Hide()
		}
		return true
	})
}
