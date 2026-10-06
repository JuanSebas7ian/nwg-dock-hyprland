package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withDnd(t *testing.T) {
	old := *dnd
	*dnd = true
	t.Cleanup(func() { *dnd = old })
}

func resetTrace() {
	traceMu.Lock()
	traceCount = 0
	traceMu.Unlock()
}

func TestTraceKeepsNewestInOrder(t *testing.T) {
	withDnd(t)
	resetTrace()
	for i := range traceSize + 5 {
		trace("event %d", i)
	}
	lines := traceLines()
	if len(lines) != traceSize {
		t.Fatalf("got %d lines, want %d", len(lines), traceSize)
	}
	if !strings.HasSuffix(lines[0], "event 5") || !strings.HasSuffix(lines[len(lines)-1], fmt.Sprintf("event %d", traceSize+4)) {
		t.Fatalf("wrong order: first %q last %q", lines[0], lines[len(lines)-1])
	}
}

func TestTraceOffWithoutDnd(t *testing.T) {
	resetTrace()
	old := *dnd
	*dnd = false
	defer func() { *dnd = old }()
	trace("ignored")
	if len(traceLines()) != 0 {
		t.Fatal("trace must be a no-op without -dnd")
	}
}

func TestHangReportContentsAndPruning(t *testing.T) {
	withDnd(t)
	resetTrace()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	trace("hyprland: activewindowv2>>abc")
	dir := hangReportDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range keptReports + 3 {
		name := fmt.Sprintf("stall-20200101-0000%02d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path, err := writeHangReport("hang", 12*time.Second, []byte("early stacks"), time.Now(), []byte("late stacks"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"hang report", "main loop stalled: 12s", "activewindowv2>>abc", "early stacks", "late stacks"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("report lacks %q", want)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != keptReports {
		t.Fatalf("kept %d reports, want %d", len(entries), keptReports)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the newest report was pruned")
	}
}

func TestStallTrackerWritesReportOnRecovery(t *testing.T) {
	withDnd(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var st stallTracker
	now := time.Now()
	st.check(now, 2*time.Second, false)
	if st.snap != nil {
		t.Fatal("snapshot taken for a short stall")
	}
	st.check(now, 5*time.Second, false)
	st.check(now, 7*time.Second, false)
	if st.snap == nil || st.longest != 7*time.Second {
		t.Fatalf("snapshot %v, longest %s", st.snap != nil, st.longest)
	}
	st.check(now, time.Second, false)
	if st.snap != nil {
		t.Fatal("snapshot not cleared after recovery")
	}
	entries, _ := os.ReadDir(hangReportDir())
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "stall-") {
		t.Fatalf("expected one stall report, got %v", entries)
	}
}

func TestStallTrackerIgnoresSuspend(t *testing.T) {
	withDnd(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var st stallTracker
	now := time.Now()
	st.check(now, 6*time.Second, false)
	st.check(now, 600*time.Second, true) // the machine slept
	st.check(now, time.Second, false)
	if _, err := os.Stat(hangReportDir()); err == nil {
		entries, _ := os.ReadDir(hangReportDir())
		if len(entries) != 0 {
			t.Fatalf("a suspend must not produce a report: %v", entries)
		}
	}
}
