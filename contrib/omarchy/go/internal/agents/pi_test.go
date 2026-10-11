package agents

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseMCPList(t *testing.T) {
	out := `mise ~/.config/mise/config.toml tools: pi@1.1.0
fetch: connected, 1 tool (deferred, global)
  uvx mcp-server-fetch
filesystem: connected, 14 tools (deferred, global)
git: error: spawn uvx ENOENT
brave-search: disabled (deferred, global)`
	m := ParseMCPList(out)
	if m["fetch"].Tools != 1 || m["filesystem"].Tools != 14 || m["git"].Status != "failed" || m["brave-search"].Status != "disabled" || len(m) != 4 {
		t.Fatalf("%+v", m)
	}
}

func TestMdRules(t *testing.T) {
	got := mdRules("# T\n\n- Tasks: keep TODO.md\n  - nested: no\n- **Plans**: PLAN.md\n* Safety. never\ntext\n")
	if strings.Join(got, "|") != "Tasks|Plans|Safety" {
		t.Fatalf("%q", got)
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetailsReadsEverything(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	cache := filepath.Join(dir, "cache")
	write(t, filepath.Join(agent, "settings.json"), `{"defaultProvider":"llama-local","defaultModel":"qwen","packages":["npm:pi-subagents@0.77.0"]}`)
	write(t, filepath.Join(agent, "models.json"), `{"providers":{"llama-local":{"models":[{"id":"qwen","contextWindow":32768},{"id":"x"}]}}}`)
	write(t, filepath.Join(agent, "AGENTS.md"), "- Tasks: TODO.md\n- Sub-agents: run pi -p for subtasks\n")
	write(t, filepath.Join(agent, "mcp.json"), `{"mcpServers":{"fetch":{"command":"uvx","args":["mcp-server-fetch"],"exposure":"deferred"},"off":{"command":"x","enabled":false}}}`)
	write(t, filepath.Join(agent, "npm", "node_modules", "pi-subagents", "package.json"), `{"version":"0.77.0","description":"delegation"}`)
	write(t, filepath.Join(agent, "extensions", "local.ts"), "x")
	_ = os.MkdirAll(filepath.Join(dir, "realskill"), 0o755)
	_ = os.MkdirAll(filepath.Join(agent, "skills"), 0o755)
	_ = os.Symlink(filepath.Join(dir, "realskill"), filepath.Join(agent, "skills", "omarchy"))
	write(t, filepath.Join(cache, "pi-mcp-list.txt"), "fetch: connected, 1 tool (deferred, global)\n")
	write(t, filepath.Join(cache, "pi-prompt-tokens"), "3856")
	write(t, filepath.Join(cache, "pi-prompt.json"), `{"total":3856,"mcp":274,"extensions":1538}`)
	d := Paths{AgentDir: agent, CacheDir: cache}.Details(false)
	if !d.Installed || d.Backend != "llama.cpp" || d.ContextWindow != 32768 || d.Models != 2 || d.PromptTokens != 3856 || d.PromptCost.MCP != 274 {
		t.Fatalf("%+v", d)
	}
	if len(d.MCP) != 2 || d.MCP[0].Name != "fetch" || d.MCP[0].Tools != 1 || d.MCP[1].Status != "disabled" || d.MCP[1].Exposure != "codemode" {
		t.Fatalf("mcp %+v", d.MCP)
	}
	if len(d.Extensions) != 2 || d.Extensions[0].Version != "0.77.0" || !d.Extensions[1].Local {
		t.Fatalf("ext %+v", d.Extensions)
	}
	if len(d.Skills) != 1 || d.Skills[0] != "omarchy" {
		t.Fatalf("skills (symlink) %+v", d.Skills)
	}
	if len(d.Subagents) != 2 || !strings.HasPrefix(d.Subagents[0], "pi -p") {
		t.Fatalf("subagents %+v", d.Subagents)
	}
	if missing := (Paths{AgentDir: filepath.Join(dir, "none")}).Details(false); missing.Installed || missing.MCP == nil {
		t.Fatalf("%+v", missing)
	}
}

func TestScanSessionsAndRecord(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.Local)
	ms := now.UnixMilli()
	yday := now.AddDate(0, 0, -1).UnixMilli()
	write(t, filepath.Join(dir, "--proj--", "a.jsonl"), strings.Join([]string{
		`{"type":"session","version":3}`,
		`{"type":"message","message":{"role":"user","timestamp":` + itoa(ms) + `}}`,
		`{"type":"message","message":{"role":"assistant","provider":"llama-local","model":"qwen","timestamp":` + itoa(ms) + `,"usage":{"input":100,"output":20,"cacheRead":50,"cacheWrite":0}}}`,
		`{"type":"message","message":{"role":"assistant","provider":"anthropic","model":"claude","timestamp":` + itoa(yday) + `,"usage":{"input":10,"output":5}}}`,
		`not json`,
	}, "\n"))
	u := ScanSessions(dir, now)
	if u.Sessions != 1 || u.Prompts != 1 || u.Today.Tokens != 170 || u.Today.Sessions != 1 || u.ByModel["qwen · local"].CacheReadInputTokens != 50 || u.ByModel["claude"].OutputTokens != 5 {
		t.Fatalf("%+v %+v", u, u.ByModel)
	}
	d := PiDetails{Installed: true, Backend: "llama.cpp", DefaultModel: "qwen", ContextWindow: 32768, PromptTokens: 3856,
		PromptCost: PromptCost{MCP: 274, Extensions: 1538}, MCP: []MCPServer{{Name: "fetch", Enabled: true}, {Name: "off"}},
		Extensions: []Extension{{Name: "pi-subagents"}}, Subagents: []string{"pi -p (x)"}, AgentsMD: "/a", AgentsMDWords: 127}
	r := Record(d, u, now)
	if r["id"] != "pi" || r["tierLabel"] != "llama.cpp · qwen" || r["totalSessions"] != 1 {
		t.Fatalf("%+v", r)
	}
	lim := r["limits"].([]map[string]any)
	if len(lim) != 5 || !strings.Contains(lim[0]["title"].(string), "3.9k tok") || !strings.HasPrefix(lim[1]["title"].(string), "MCP 1/2 fetch") {
		t.Fatalf("%+v", lim)
	}
	for _, l := range lim {
		if p := l["percent"].(float64); p < 0 || p > 0.5 {
			t.Fatalf("info rows must not look like alarms: %v", l)
		}
	}
	days := r["recentDays"].([]map[string]any)
	if len(days) != 7 || days[6]["messageCount"] != 170 || days[5]["messageCount"] != 15 {
		t.Fatalf("%+v", days)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
