// omarchy-agent-usage-antigravity prints Antigravity's usage record for the
// omarchy.agents bar panel: tokens from every conversation database and the
// per-model quotas from the local language server (cached while it is closed).
package main

import (
	"os"
	"path/filepath"
	"time"

	"omarchy/internal/agents"
	"omarchy/internal/jsonout"
)

func main() {
	home, _ := os.UserHomeDir()
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	now := time.Now()
	stats := agents.AntigravityStats([]string{filepath.Join(home, ".gemini", "antigravity"), filepath.Join(home, ".gemini", "antigravity-ide")}, now)
	q, live, ok := agents.FetchQuota(filepath.Join(state, "omarchy", "agents", "cache", "antigravity-quota.json"))
	limits, tier, status := []map[string]any{}, "", ""
	if ok {
		for _, l := range q.Limits {
			r, _ := l["resetsAt"].(string)
			if live || agents.NotReset(r, now.UTC()) { // a cached window that already reset says nothing about now
				limits = append(limits, l)
			}
		}
		tier = q.TierLabel
		if !live && len(limits) > 0 && len(q.FetchedAt) >= 16 {
			status = "Quotas from " + q.FetchedAt[:10] + " " + q.FetchedAt[11:16] + " UTC (Antigravity closed)"
		}
	}
	jsonout.Print(agents.UsageRecord("antigravity", "Antigravity", now, limits, tier, status, "Open Antigravity to refresh the model quotas.", stats))
}
