// Package reward scores the smoke test JSON from 0 to 100.
//
// PASS = full weight, WARN = half, FAIL = 0, SKIP = ignored. A group made only
// of SKIPs drops out of the total and the score is rescaled to 100; a group
// with no checks at all counts as 0. A blocking group (TRIM, SMART, DRIVERS)
// needs at least one non-SKIP check, otherwise it is a blocker.
package reward

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Weights per check group (sum 100), in report order.
var Order = []string{"TRIM", "SMART", "PACCACHE", "SCRUB", "ORPHAN", "PKG", "BOOT", "SVC", "HYPR", "DRIVERS",
	"GUARD", "MEMG", // the desktop guards (freeze-guard, memguard): not blocking
	"STAB", "SPLASH", "WIDG", "LLM"} // 2026-10-11: watchdog, boot, Go widgets, llama.cpp

// The four groups added on 2026-10-11 had pushed the sum to 114 (scores were
// silently rescaled); TRIM, the periodic jobs and the services gave way.
var Weights = map[string]float64{"TRIM": 16, "SMART": 15, "PACCACHE": 5, "SCRUB": 5, "ORPHAN": 3, "PKG": 6, "BOOT": 3, "SVC": 6,
	"HYPR": 4, "DRIVERS": 15, "GUARD": 4, "MEMG": 4, "STAB": 4, "SPLASH": 2, "WIDG": 4, "LLM": 4}

const Threshold = 90.0

var Blocking = []string{"TRIM", "SMART", "DRIVERS"}

var value = map[string]float64{"PASS": 1, "WARN": 0.5, "FAIL": 0}

// Check is one smoke test line.
type Check struct {
	ID     any    `json:"id"`
	Status any    `json:"status"`
	Text   string `json:"text"`
}

type Group struct {
	Weight   float64  `json:"weight"`
	Points   float64  `json:"points"`
	Statuses []string `json:"statuses"`
	Dropped  bool     `json:"dropped"`
}

type Result struct {
	Score     float64          `json:"score"`
	Threshold float64          `json:"threshold"`
	Blockers  []string         `json:"blockers"`
	Stable    bool             `json:"stable"`
	Groups    map[string]Group `json:"groups"`
}

func GroupOf(id string) string {
	names := append([]string{}, Order...)
	sort.SliceStable(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, g := range names {
		if strings.HasPrefix(id, g) {
			return g
		}
	}
	return ""
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func isBlocking(g string) bool {
	for _, b := range Blocking {
		if b == g {
			return true
		}
	}
	return false
}

func Score(checks []Check) Result {
	per := map[string][]string{}
	for _, c := range checks {
		if g := GroupOf(fmt.Sprint(c.ID)); g != "" {
			per[g] = append(per[g], strings.ToUpper(fmt.Sprint(c.Status)))
		}
	}
	res := Result{Threshold: Threshold, Blockers: []string{}, Groups: map[string]Group{}}
	var total, active float64
	for _, g := range Order {
		w, sts := Weights[g], per[g]
		var scored []string
		for _, s := range sts {
			if s != "SKIP" {
				scored = append(scored, s)
			}
		}
		dropped := len(sts) > 0 && len(scored) == 0
		got, zero := 0.0, false
		for _, s := range scored {
			got += value[s]
			zero = zero || value[s] == 0
		}
		if len(scored) > 0 {
			got = w * got / float64(len(scored))
		}
		if !dropped {
			total += got
			active += w
		}
		if sts == nil {
			sts = []string{}
		}
		res.Groups[g] = Group{w, round2(got), sts, dropped}
		if isBlocking(g) && (len(scored) == 0 || zero) {
			res.Blockers = append(res.Blockers, g)
		}
	}
	if active > 0 {
		res.Score = round2(total * 100 / active)
	}
	res.Stable = res.Score >= Threshold && len(res.Blockers) == 0
	return res
}
