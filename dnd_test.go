package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func items(ids []string, pinIdxs ...int) []*dndItem {
	var result []*dndItem
	for i, id := range ids {
		result = append(result, &dndItem{id: id, pinIdx: pinIdxs[i]})
	}
	return result
}

func ids(items []*dndItem) []string {
	var result []string
	for _, it := range items {
		result = append(result, it.id)
	}
	return result
}

func TestNearestSlot(t *testing.T) {
	slots := []int{24, 72, 120, 168}
	cases := []struct {
		pos, want int
	}{
		{-50, 0},
		{0, 0},
		{47, 0},
		{49, 1},
		{120, 2},
		{150, 3},
		{1000, 3},
	}
	for _, c := range cases {
		if got := nearestSlot(slots, c.pos); got != c.want {
			t.Errorf("nearestSlot(%d) = %d, want %d", c.pos, got, c.want)
		}
	}
	if got := nearestSlot(nil, 10); got != -1 {
		t.Errorf("nearestSlot(nil) = %d, want -1", got)
	}
}

func TestMoveItem(t *testing.T) {
	list := items([]string{"A", "B", "C", "D"}, 0, 1, 2, 3)
	a, b, d := list[0], list[1], list[3]

	cases := []struct {
		name   string
		item   *dndItem
		target int
		want   []string
		moved  bool
	}{
		{"forward", a, 2, []string{"B", "C", "A", "D"}, true},
		{"to the end", a, 3, []string{"B", "C", "D", "A"}, true},
		{"backward", d, 1, []string{"A", "D", "B", "C"}, true},
		{"to the start", d, 0, []string{"D", "A", "B", "C"}, true},
		{"same place", b, 1, []string{"A", "B", "C", "D"}, false},
		{"out of range", b, 4, []string{"A", "B", "C", "D"}, false},
		{"negative", b, -1, []string{"A", "B", "C", "D"}, false},
		{"unknown item", &dndItem{id: "X"}, 0, []string{"A", "B", "C", "D"}, false},
	}
	for _, c := range cases {
		got, moved := moveItem(list, c.item, c.target)
		if moved != c.moved || !slices.Equal(ids(got), c.want) {
			t.Errorf("%s: got %v (moved %v), want %v (moved %v)", c.name, ids(got), moved, c.want, c.moved)
		}
	}
	if !slices.Equal(ids(list), []string{"A", "B", "C", "D"}) {
		t.Errorf("moveItem modified its input: %v", ids(list))
	}
}

// Successive moves, as during a drag across the dock, end up where the pointer is
func TestMoveItemDuringDrag(t *testing.T) {
	list := items([]string{"A", "B", "C", "D"}, 0, 1, 2, 3)
	a := list[0]
	for _, target := range []int{1, 2, 3, 2} {
		list, _ = moveItem(list, a, target)
	}
	if want := []string{"B", "C", "A", "D"}; !slices.Equal(ids(list), want) {
		t.Errorf("got %v, want %v", ids(list), want)
	}
}

func TestReorderedPinned(t *testing.T) {
	pinned := []string{"A", "B", "C", "D"}
	// displayed in a new order
	order, err := reorderedPinned(pinned, items([]string{"C", "A", "B", "D"}, 2, 0, 1, 3))
	if err != nil || !slices.Equal(order, []string{"C", "A", "B", "D"}) {
		t.Errorf("got %v, %v", order, err)
	}
	if !slices.Equal(pinned, []string{"A", "B", "C", "D"}) {
		t.Errorf("reorderedPinned modified its input: %v", pinned)
	}
}

// Pinned items that aren't displayed (ignored ones, duplicates) keep their place
func TestReorderedPinnedHiddenItems(t *testing.T) {
	pinned := []string{"A", "hidden", "B", "C", "A"}
	order, err := reorderedPinned(pinned, items([]string{"C", "A", "B"}, 3, 0, 2))
	if want := []string{"C", "hidden", "A", "B", "A"}; err != nil || !slices.Equal(order, want) {
		t.Errorf("got %v, %v, want %v", order, err, want)
	}
}

func TestReorderedPinnedStale(t *testing.T) {
	pinned := []string{"A", "B", "C"}
	cases := map[string][]*dndItem{
		"id changed":     items([]string{"B", "X"}, 1, 2),
		"index too big":  items([]string{"A", "C"}, 0, 3),
		"negative index": items([]string{"A"}, -1),
		"index twice":    items([]string{"A", "A"}, 0, 0),
	}
	for name, displayed := range cases {
		if order, err := reorderedPinned(pinned, displayed); err == nil {
			t.Errorf("%s: expected an error, got %v", name, order)
		}
	}
}

func setupPinnedFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	oldFile, oldBackup := pinnedFile, dndBackupDone
	t.Cleanup(func() {
		pinnedFile, dndBackupDone = oldFile, oldBackup
	})
	pinnedFile = filepath.Join(dir, "nwg-dock-pinned")
	dndBackupDone = false
	if err := os.WriteFile(pinnedFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSavePinnedOrder(t *testing.T) {
	dir := setupPinnedFile(t, "A\nB\nC\n")

	if err := savePinnedOrder([]string{"C", "A", "B"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, pinnedFile); got != "C\nA\nB\n" {
		t.Errorf("pinned file = %q", got)
	}
	if got := readFile(t, pinnedFile+".dnd.bak"); got != "A\nB\nC\n" {
		t.Errorf("backup = %q, want the original content", got)
	}
	info, err := os.Stat(pinnedFile)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Errorf("permissions not kept: %v, %v", info.Mode(), err)
	}

	// the backup is only made once per session
	if err := savePinnedOrder([]string{"B", "C", "A"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, pinnedFile+".dnd.bak"); got != "A\nB\nC\n" {
		t.Errorf("backup overwritten: %q", got)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".nwg-dock-pinned-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestSavePinnedOrderRefusesEmpty(t *testing.T) {
	setupPinnedFile(t, "A\nB\n")
	if err := savePinnedOrder(nil); err == nil {
		t.Error("expected an error for an empty list")
	}
	if got := readFile(t, pinnedFile); got != "A\nB\n" {
		t.Errorf("pinned file changed: %q", got)
	}
}

func TestSavePinnedOrderMissingFile(t *testing.T) {
	setupPinnedFile(t, "A\n")
	os.Remove(pinnedFile)
	if err := savePinnedOrder([]string{"A"}); err == nil {
		t.Error("expected an error when the pinned file doesn't exist")
	}
}

func TestMainLoopHung(t *testing.T) {
	limit := 10 * time.Second
	now := time.Now()
	cases := []struct {
		name                string
		sinceCheck, beatAgo time.Duration
		want                bool
	}{
		{"healthy", 2 * time.Second, 1 * time.Second, false},
		{"slow but within limit", 2 * time.Second, 9 * time.Second, false},
		{"hung", 2 * time.Second, 11 * time.Second, true},
		{"just resumed from suspend", 10 * time.Minute, 10 * time.Minute, false},
	}
	for _, c := range cases {
		_, hung := mainLoopHung(now, now.Add(-c.sinceCheck), now.Add(-c.beatAgo), limit)
		if hung != c.want {
			t.Errorf("%s: hung = %v, want %v", c.name, hung, c.want)
		}
	}
}
