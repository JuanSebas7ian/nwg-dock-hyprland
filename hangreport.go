package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

/*
Hang reports (-dnd only). A ring buffer keeps the last events the dock handled; when the GTK
main loop stalls, the goroutine stacks are captured while it is still stalled, and a report is
written to $XDG_STATE_HOME/nwg-dock/hangs/ when the watchdog gives up (hang-*) or when the loop
recovers from a long stall (stall-*). Cheap enough to stay on: one formatted line per event.
*/

const (
	traceSize      = 200
	stallThreshold = 4 * time.Second
	keptReports    = 10
)

var (
	traceMu    sync.Mutex
	traceBuf   [traceSize]string
	traceCount int
	startedAt  = time.Now()
)

func trace(format string, args ...any) {
	if !*dnd {
		return
	}
	line := time.Now().Format("15:04:05.000 ") + fmt.Sprintf(format, args...)
	traceMu.Lock()
	traceBuf[traceCount%traceSize] = line
	traceCount++
	traceMu.Unlock()
}

// traceLines returns the buffered events, oldest first.
func traceLines() []string {
	traceMu.Lock()
	defer traceMu.Unlock()
	n := min(traceCount, traceSize)
	lines := make([]string, 0, n)
	for i := traceCount - n; i < traceCount; i++ {
		lines = append(lines, traceBuf[i%traceSize])
	}
	return lines
}

func goroutineDump() []byte {
	buf := make([]byte, 1<<20)
	return buf[:runtime.Stack(buf, true)]
}

func hangReportDir() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(state, "nwg-dock", "hangs")
}

/*
Writes a report named `kind`-<time>.txt and keeps only the newest keptReports reports.
`early` is the stack dump taken when the stall started; `late` the one taken now (may be nil).
*/
func writeHangReport(kind string, stalled time.Duration, early []byte, earlyAt time.Time, late []byte) (string, error) {
	dir := hangReportDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	now := time.Now()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	var b strings.Builder
	fmt.Fprintf(&b, "nwg-dock-hyprland %s -dnd: %s report\n", version, kind)
	fmt.Fprintf(&b, "time: %s\npid: %d\nuptime: %s\nmain loop stalled: %s\n", now.Format(time.RFC3339), os.Getpid(),
		now.Sub(startedAt).Round(time.Second), stalled.Round(100*time.Millisecond))
	fmt.Fprintf(&b, "goroutines: %d\nheap: %d MiB, sys: %d MiB, gc runs: %d\nargs: %s\n",
		runtime.NumGoroutine(), ms.HeapAlloc>>20, ms.Sys>>20, ms.NumGC, strings.Join(os.Args[1:], " "))
	b.WriteString("\n== last events (oldest first)\n")
	for _, l := range traceLines() {
		b.WriteString(l + "\n")
	}
	if early != nil {
		fmt.Fprintf(&b, "\n== goroutines when the stall was noticed (%s)\n", earlyAt.Format("15:04:05.000"))
		b.Write(early)
	}
	if late != nil {
		b.WriteString("\n== goroutines now\n")
		b.Write(late)
	}

	path := filepath.Join(dir, fmt.Sprintf("%s-%s.txt", kind, now.Format("20060102-150405")))
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	pruneHangReports(dir, keptReports)
	return path, nil
}

func pruneHangReports(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".txt") {
			names = append(names, e.Name())
		}
	}
	// names carry the time after the kind; sort by that part
	slices.SortFunc(names, func(a, b string) int {
		return strings.Compare(a[strings.Index(a, "-"):], b[strings.Index(b, "-"):])
	})
	for len(names) > keep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

/*
Follows one watchdog check: takes a stack snapshot once a stall passes stallThreshold, writes a
stall report when the loop recovers, and forgets the snapshot across a suspend.
*/
type stallTracker struct {
	snap    []byte
	snapAt  time.Time
	longest time.Duration
}

func (t *stallTracker) check(now time.Time, stalled time.Duration, resumed bool) {
	switch {
	case resumed:
		t.snap = nil
	case stalled > stallThreshold:
		t.longest = max(t.longest, stalled)
		if t.snap == nil {
			t.snap, t.snapAt = goroutineDump(), now
			log.Warnf("Main loop stalled for %s, stacks captured", stalled.Round(time.Second))
		}
	case t.snap != nil:
		path, err := writeHangReport("stall", t.longest, t.snap, t.snapAt, nil)
		if err == nil {
			log.Warnf("Main loop recovered after %s; report: %s", t.longest.Round(time.Second), path)
		}
		t.snap, t.longest = nil, 0
	}
}
