// Package agents builds usage records for the omarchy.agents bar panel and
// the configuration details shown in the Local AI widget.
package agents

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MCPServer is one entry of ~/.pi/agent/mcp.json (+ status from `pi mcp list`).
type MCPServer struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Exposure string `json:"exposure"`
	Command  string `json:"command"`
	Status   string `json:"status,omitempty"` // connected | disabled | failed | unknown
	Tools    int    `json:"tools,omitempty"`
}

// Extension is an installed Pi package or a local extension file.
type Extension struct {
	Spec        string `json:"spec"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Local       bool   `json:"local,omitempty"`
}

// PiDetails is everything the widget shows about Pi.
type PiDetails struct {
	Installed       bool        `json:"installed"`
	Version         string      `json:"version,omitempty"`
	DefaultProvider string      `json:"defaultProvider,omitempty"`
	DefaultModel    string      `json:"defaultModel,omitempty"`
	Backend         string      `json:"backend"` // llama.cpp | ollama | cloud
	AgentsMD        string      `json:"agentsMd,omitempty"`
	AgentsMDWords   int         `json:"agentsMdWords"`
	AgentsMDRules   []string    `json:"agentsMdRules"`
	MCP             []MCPServer `json:"mcp"`
	Extensions      []Extension `json:"extensions"`
	Skills          []string    `json:"skills"`
	Subagents       []string    `json:"subagents"`
	Models          int         `json:"models"`
	ContextWindow   int         `json:"contextWindow,omitempty"`
	PromptTokens    int         `json:"promptTokens,omitempty"` // measured system prompt + tools
	PromptCost      PromptCost  `json:"promptCost"`
	Sandbox         string      `json:"sandbox,omitempty"` // "pi-box (docker)" when the image exists
}

// PromptCost is what each component adds to every request's prompt, in tokens
// (measured by omarchy-agent-usage-pi --measure).
type PromptCost struct {
	Total        int `json:"total"`
	Base         int `json:"base"`       // core prompt + built-in tools
	Extensions   int `json:"extensions"` // packages and built-in extensions
	MCP          int `json:"mcp"`
	ContextFiles int `json:"contextFiles"` // AGENTS.md / CLAUDE.md
	Skills       int `json:"skills"`
}

// Paths locates Pi's files (tests point them at a temp dir).
type Paths struct {
	AgentDir string // ~/.pi/agent
	CacheDir string // ~/.cache/omarchy-agents
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

var mcpLineRe = regexp.MustCompile(`^([A-Za-z0-9_-]+): (connected|disabled|failed|error|disconnected)[^,]*(?:, (\d+) tools?)?`)

// ParseMCPList reads `pi mcp list` output.
func ParseMCPList(out string) map[string]MCPServer {
	res := map[string]MCPServer{}
	for _, l := range strings.Split(out, "\n") {
		if m := mcpLineRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			n, _ := strconv.Atoi(m[3])
			st := m[2]
			if st == "error" || st == "disconnected" {
				st = "failed"
			}
			res[m[1]] = MCPServer{Name: m[1], Status: st, Tools: n}
		}
	}
	return res
}

// mcpStatus runs `pi mcp list` at most once an hour (it starts every server).
func (p Paths) mcpStatus(probe bool) map[string]MCPServer {
	cache := filepath.Join(p.CacheDir, "pi-mcp-list.txt")
	if st, err := os.Stat(cache); err == nil && time.Since(st.ModTime()) < time.Hour {
		b, _ := os.ReadFile(cache)
		return ParseMCPList(string(b))
	}
	if !probe {
		b, _ := os.ReadFile(cache)
		return ParseMCPList(string(b))
	}
	cmd := exec.Command("pi", "mcp", "list")
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err == nil {
		_ = os.MkdirAll(p.CacheDir, 0o755)
		_ = os.WriteFile(cache, out, 0o644)
	}
	return ParseMCPList(string(out))
}

// Details inspects Pi's configuration. probe=true may run `pi mcp list`.
func (p Paths) Details(probe bool) PiDetails {
	d := PiDetails{MCP: []MCPServer{}, Extensions: []Extension{}, Skills: []string{}, AgentsMDRules: []string{}, Subagents: []string{}}
	if _, err := os.Stat(p.AgentDir); err != nil {
		return d
	}
	d.Installed = true
	if out, err := exec.Command("pi", "--version").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		d.Version = strings.TrimSpace(lines[len(lines)-1])
	}
	var set struct {
		DefaultProvider string   `json:"defaultProvider"`
		DefaultModel    string   `json:"defaultModel"`
		Packages        []string `json:"packages"`
	}
	_ = readJSON(filepath.Join(p.AgentDir, "settings.json"), &set)
	d.DefaultProvider, d.DefaultModel = set.DefaultProvider, set.DefaultModel
	switch set.DefaultProvider {
	case "llama-local", "llama.cpp":
		d.Backend = "llama.cpp"
	case "ollama":
		d.Backend = "ollama"
	case "":
		d.Backend = "not set"
	default:
		d.Backend = "cloud (" + set.DefaultProvider + ")"
	}

	var models struct {
		Providers map[string]struct {
			Models []struct {
				ID            string `json:"id"`
				ContextWindow int    `json:"contextWindow"`
			} `json:"models"`
		} `json:"providers"`
	}
	_ = readJSON(filepath.Join(p.AgentDir, "models.json"), &models)
	for name, prov := range models.Providers {
		d.Models += len(prov.Models)
		for _, m := range prov.Models {
			if name == set.DefaultProvider && m.ID == set.DefaultModel {
				d.ContextWindow = m.ContextWindow
			}
		}
	}

	for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(p.AgentDir, name)
		if b, err := os.ReadFile(path); err == nil {
			d.AgentsMD = path
			d.AgentsMDWords = len(strings.Fields(string(b)))
			d.AgentsMDRules = mdRules(string(b))
			break
		}
	}

	var mcp struct {
		Servers map[string]struct {
			Command  string   `json:"command"`
			Args     []string `json:"args"`
			URL      string   `json:"url"`
			Enabled  *bool    `json:"enabled"`
			Exposure string   `json:"exposure"`
		} `json:"mcpServers"`
	}
	_ = readJSON(filepath.Join(p.AgentDir, "mcp.json"), &mcp)
	status := p.mcpStatus(probe)
	for _, name := range sortedKeys(mcp.Servers) {
		s := mcp.Servers[name]
		srv := MCPServer{Name: name, Enabled: s.Enabled == nil || *s.Enabled, Exposure: s.Exposure, Status: "unknown"}
		if srv.Exposure == "" {
			srv.Exposure = "codemode"
		}
		srv.Command = strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
		if s.URL != "" {
			srv.Command = s.URL
		}
		if st, ok := status[name]; ok {
			srv.Status, srv.Tools = st.Status, st.Tools
		}
		if !srv.Enabled {
			srv.Status = "disabled"
		}
		d.MCP = append(d.MCP, srv)
	}

	for _, spec := range set.Packages {
		e := Extension{Spec: spec, Name: strings.TrimPrefix(spec, "npm:")}
		if i := strings.LastIndex(e.Name, "@"); i > 0 {
			e.Name = e.Name[:i]
		}
		var pkg struct{ Version, Description string }
		if readJSON(filepath.Join(p.AgentDir, "npm", "node_modules", e.Name, "package.json"), &pkg) == nil {
			e.Version, e.Description = pkg.Version, pkg.Description
		}
		d.Extensions = append(d.Extensions, e)
		if strings.Contains(e.Name, "subagent") {
			d.Subagents = append(d.Subagents, e.Name+" (declarative delegation)")
		}
	}
	if files, err := os.ReadDir(filepath.Join(p.AgentDir, "extensions")); err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".ts") || strings.HasSuffix(f.Name(), ".js") {
				d.Extensions = append(d.Extensions, Extension{Spec: f.Name(), Name: strings.TrimSuffix(f.Name(), filepath.Ext(f.Name())), Local: true})
			}
		}
	}
	if dirs, err := os.ReadDir(filepath.Join(p.AgentDir, "skills")); err == nil {
		for _, f := range dirs {
			if st, err := os.Stat(filepath.Join(p.AgentDir, "skills", f.Name())); err == nil && st.IsDir() {
				d.Skills = append(d.Skills, f.Name()) // skills are often symlinks
			}
		}
	}
	if strings.Contains(strings.ToLower(readText(d.AgentsMD)), "pi -p") {
		d.Subagents = append([]string{"pi -p (fresh context per sub-task, told in AGENTS.md)"}, d.Subagents...)
	}
	if b, err := os.ReadFile(filepath.Join(p.CacheDir, "pi-prompt-tokens")); err == nil {
		d.PromptTokens, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	_ = readJSON(filepath.Join(p.CacheDir, "pi-prompt.json"), &d.PromptCost)
	if exec.Command("docker", "image", "inspect", "pi-box:latest").Run() == nil {
		d.Sandbox = "pi-box (Docker, only the project folder)"
	}
	return d
}

func readText(path string) string {
	if path == "" {
		return ""
	}
	b, _ := os.ReadFile(path)
	return string(b)
}

var ruleRe = regexp.MustCompile(`^\s*[-*]\s+(?:\*\*)?([^:.*]{3,40})`)

// mdRules returns the first words of each top-level bullet of AGENTS.md.
func mdRules(md string) []string {
	out := []string{}
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "  ") {
			continue
		}
		if m := ruleRe.FindStringSubmatch(l); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Usage is the token usage found in Pi's session files.
type Usage struct {
	Prompts  int
	Sessions int
	ByDay    map[string]int // date -> tokens
	ByModel  map[string]*ModelUsage
	Today    struct{ Prompts, Sessions, Tokens int }
	TodayBy  map[string]int
}

// ModelUsage matches the panel's modelUsage entries.
type ModelUsage struct {
	InputTokens              int `json:"inputTokens"`
	OutputTokens             int `json:"outputTokens"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens"`
}

// ScanSessions sums usage from ~/.pi/agent/sessions/**.jsonl.
func ScanSessions(dir string, now time.Time) Usage {
	u := Usage{ByDay: map[string]int{}, ByModel: map[string]*ModelUsage{}, TodayBy: map[string]int{}}
	today := now.Format("2006-01-02")
	_ = filepath.WalkDir(dir, func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		u.Sessions++
		sessToday := false
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			var e struct {
				Type    string `json:"type"`
				Message struct {
					Role     string `json:"role"`
					Model    string `json:"model"`
					Provider string `json:"provider"`
					Time     int64  `json:"timestamp"`
					Usage    struct {
						Input, Output, CacheRead, CacheWrite int
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "message" {
				continue
			}
			day := time.UnixMilli(e.Message.Time).Format("2006-01-02")
			switch e.Message.Role {
			case "user":
				u.Prompts++
				if day == today {
					u.Today.Prompts++
					sessToday = true
				}
			case "assistant":
				us := e.Message.Usage
				tokens := us.Input + us.Output + us.CacheRead + us.CacheWrite
				key := e.Message.Model
				if e.Message.Provider == "llama-local" || e.Message.Provider == "llama.cpp" || e.Message.Provider == "ollama" {
					key += " · local"
				}
				m := u.ByModel[key]
				if m == nil {
					m = &ModelUsage{}
					u.ByModel[key] = m
				}
				m.InputTokens += us.Input
				m.OutputTokens += us.Output
				m.CacheReadInputTokens += us.CacheRead
				m.CacheCreationInputTokens += us.CacheWrite
				u.ByDay[day] += tokens
				if day == today {
					u.Today.Tokens += tokens
					u.TodayBy[key] += tokens
				}
			}
		}
		if sessToday {
			u.Today.Sessions++
		}
		return nil
	})
	return u
}

// Record builds the omarchy.agents usage record for Pi.
func Record(d PiDetails, u Usage, now time.Time) map[string]any {
	days := []map[string]any{}
	active := []string{}
	for i := 6; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		days = append(days, map[string]any{"date": day, "messageCount": u.ByDay[day]})
	}
	for _, day := range sortedKeys(u.ByDay) {
		active = append(active, day)
	}
	// The panel has no free-form details list; its "limits" rows (title +
	// meter) carry Pi's setup. Each meter is the share of the context window
	// that the component costs on every request (from --measure).
	limits := []map[string]any{}
	ctxWin := float64(d.ContextWindow)
	if ctxWin <= 0 {
		ctxWin = 32768
	}
	add := func(title string, tokens int) {
		if tokens > 0 {
			title += " · " + short(tokens) + " tok"
		}
		limits = append(limits, map[string]any{"label": title, "title": title, "percent": float64(tokens) / ctxWin, "resetsAt": ""})
	}
	if d.PromptTokens > 0 {
		add(fmt.Sprintf("Prompt of every request (of %s)", short(int(ctxWin))), d.PromptTokens)
	}
	on, names := 0, []string{}
	for _, s := range d.MCP {
		if s.Enabled {
			on++
			names = append(names, s.Name)
		}
	}
	if len(d.MCP) > 0 {
		add(fmt.Sprintf("MCP %d/%d %s", on, len(d.MCP), strings.Join(names, ", ")), d.PromptCost.MCP)
	}
	if len(d.Extensions) > 0 {
		en := []string{}
		for _, e := range d.Extensions {
			en = append(en, e.Name)
		}
		add("Extensions "+strings.Join(en, ", "), d.PromptCost.Extensions)
	}
	if len(d.Subagents) > 0 {
		add("Sub-agents "+strings.Join(subNames(d.Subagents), ", "), 0)
	}
	if d.AgentsMD != "" {
		add(fmt.Sprintf("AGENTS.md %d words", d.AgentsMDWords), d.PromptCost.ContextFiles)
	}
	if len(d.Skills) > 0 {
		add("Skills "+strings.Join(d.Skills, ", "), d.PromptCost.Skills)
	}
	tier := d.Backend
	if d.DefaultModel != "" {
		tier += " · " + d.DefaultModel
	}
	byModel := map[string]any{}
	for k, v := range u.ByModel {
		byModel[k] = v
	}
	return map[string]any{
		"schemaVersion": 1, "id": "pi", "name": "Pi", "updatedAt": now.UTC().Format(time.RFC3339),
		"ready": d.Installed, "hasLocalStats": true, "hasPromptStats": true,
		"limits": limits, "tierLabel": tier, "usageStatusText": "",
		"authHelpText": "Local models have no quota; rows show Pi's setup.",
		"todayPrompts": u.Today.Prompts, "todaySessions": u.Today.Sessions, "todayTotalTokens": u.Today.Tokens,
		"todayTokensByModel": u.TodayBy, "recentDays": days, "totalPrompts": u.Prompts, "totalSessions": u.Sessions,
		"activeDays": len(active), "activeDates": active, "modelUsage": byModel,
	}
}

func subNames(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, strings.SplitN(s, " (", 2)[0])
	}
	return out
}

func short(n int) string {
	if n >= 1000 {
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
	return strconv.Itoa(n)
}

// FirstPromptTokens returns input+cache tokens of the first assistant reply
// found under dir (a measuring session).
func FirstPromptTokens(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(path string, de fs.DirEntry, err error) error {
		if n > 0 || err != nil || de.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			var e struct {
				Message struct {
					Role  string                                     `json:"role"`
					Usage struct{ Input, CacheRead, CacheWrite int } `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Message.Role == "assistant" {
				n = e.Message.Usage.Input + e.Message.Usage.CacheRead + e.Message.Usage.CacheWrite
				return fs.SkipAll
			}
		}
		return nil
	})
	return n
}
