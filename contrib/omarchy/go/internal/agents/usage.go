package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"time"
)

// Stats accumulates token usage the way the omarchy.agents panel reads it.
type Stats struct {
	now      time.Time
	today    string
	recent   []string
	byDay    map[string]int
	models   map[string]*ModelUsage
	todayBy  map[string]int
	sessions map[string]bool
	todaySes map[string]bool
	active   map[string]bool
	prompts  int
	todayP   int
	todayTok int
}

// NewStats starts an empty accumulator for the week ending `now`.
func NewStats(now time.Time) *Stats {
	s := &Stats{now: now, today: now.Format("2006-01-02"), byDay: map[string]int{}, models: map[string]*ModelUsage{},
		todayBy: map[string]int{}, sessions: map[string]bool{}, todaySes: map[string]bool{}, active: map[string]bool{}}
	for d := 6; d >= 0; d-- {
		s.recent = append(s.recent, now.AddDate(0, 0, -d).Format("2006-01-02"))
	}
	return s
}

// Add records one model answer (zero-token answers are ignored).
func (s *Stats) Add(session string, when time.Time, model string, in, out, cacheRead, cacheWrite int) {
	total := in + out + cacheRead + cacheWrite
	if total <= 0 {
		return
	}
	day := when.Format("2006-01-02")
	s.prompts++
	s.sessions[session] = true
	s.active[day] = true
	m := s.models[model]
	if m == nil {
		m = &ModelUsage{}
		s.models[model] = m
	}
	m.InputTokens += in
	m.OutputTokens += out
	m.CacheReadInputTokens += cacheRead
	m.CacheCreationInputTokens += cacheWrite
	s.byDay[day] += total
	if day == s.today {
		s.todayP++
		s.todaySes[session] = true
		s.todayTok += total
		s.todayBy[model] += total
	}
}

// Fields returns the local-stats part of a record.
func (s *Stats) Fields() map[string]any {
	days := make([]map[string]any, 0, 7)
	for _, d := range s.recent {
		days = append(days, map[string]any{"date": d, "messageCount": s.byDay[d]})
	}
	active := make([]string, 0, len(s.active))
	for d := range s.active {
		active = append(active, d)
	}
	sort.Strings(active)
	models := map[string]any{}
	for k, v := range s.models {
		models[k] = v
	}
	return map[string]any{"todayPrompts": s.todayP, "todaySessions": len(s.todaySes), "todayTotalTokens": s.todayTok,
		"todayTokensByModel": s.todayBy, "recentDays": days, "totalPrompts": s.prompts, "totalSessions": len(s.sessions),
		"activeDays": len(active), "activeDates": active, "modelUsage": models}
}

// UsageRecord builds a full panel record.
func UsageRecord(id, name string, now time.Time, limits []map[string]any, tier, status, help string, s *Stats) map[string]any {
	if limits == nil {
		limits = []map[string]any{}
	}
	r := map[string]any{"schemaVersion": 1, "id": id, "name": name, "updatedAt": now.UTC().Format(time.RFC3339),
		"ready": true, "hasLocalStats": true, "limits": limits, "tierLabel": tier, "usageStatusText": status, "authHelpText": help}
	for k, v := range s.Fields() {
		r[k] = v
	}
	return r
}

// SQLite runs a read-only query with the sqlite3 CLI (a base package on Arch)
// and decodes its JSON rows.
func SQLite(db, query string) ([]map[string]any, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("sqlite3", "-readonly", "-json", "-cmd", ".timeout 2000", db, query)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sqlite3 %s: %v %s", db, err, errb.String())
	}
	if out.Len() == 0 {
		return nil, nil
	}
	var rows []map[string]any
	return rows, json.Unmarshal(out.Bytes(), &rows)
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		if x > 0 {
			return int(x)
		}
	case json.Number:
		n, _ := x.Int64()
		if n > 0 {
			return int(n)
		}
	}
	return 0
}
