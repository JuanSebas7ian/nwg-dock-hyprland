package main

import (
	"testing"
	"time"
)

func TestShouldHideDock(t *testing.T) {
	cases := []struct {
		name        string
		idle        time.Duration
		pointerOver bool
		held        bool
		want        bool
	}{
		{"pointer just left", 100 * time.Millisecond, false, false, false},
		{"pointer left a moment ago", dockHideDelay, false, false, true},
		{"pointer over the dock, active", 2 * time.Second, true, false, false},
		{"pointer resting over the dock", dockIdleTimeout, true, false, true},
		{"held by a drag, menu or picker", time.Hour, false, true, false},
		{"held with the pointer over it", time.Hour, true, true, false},
	}
	for _, c := range cases {
		if got := shouldHideDock(c.idle, c.pointerOver, c.held); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
