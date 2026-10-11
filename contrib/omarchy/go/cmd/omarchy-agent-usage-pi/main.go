// omarchy-agent-usage-pi prints Pi's usage record for the omarchy.agents bar
// panel (run by omarchy-agent-usage-extra every 2 min).
//
//	omarchy-agent-usage-pi            the record (JSON)
//	omarchy-agent-usage-pi --details  Pi's configuration for the Local AI widget
//	omarchy-agent-usage-pi --measure  measure Pi's prompt size with one tiny request
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"omarchy/internal/agents"
	"omarchy/internal/jsonout"
	"omarchy/internal/llm"
)

func main() {
	home, _ := os.UserHomeDir()
	p := agents.Paths{AgentDir: filepath.Join(home, ".pi", "agent"), CacheDir: filepath.Join(home, ".cache", "omarchy-agents")}
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	switch arg {
	case "--details":
		jsonout.Print(p.Details(true))
	case "--measure":
		n, err := measure(p)
		if err != nil {
			jsonout.Err(err.Error())
			os.Exit(1)
		}
		jsonout.OK(map[string]any{"promptTokens": n})
	case "":
		d := p.Details(true)
		u := agents.ScanSessions(filepath.Join(p.AgentDir, "sessions"), time.Now())
		jsonout.Print(agents.Record(d, u, time.Now()))
	default:
		fmt.Fprintln(os.Stderr, "usage: omarchy-agent-usage-pi [--details|--measure]")
		os.Exit(2)
	}
}

// measure runs one tiny prompt in throwaway sessions, once with everything
// and once without each component, and records how many prompt tokens each
// one costs (system prompt + tool definitions).
func measure(p agents.Paths) (int, error) {
	model := smallestLocal(p) // token counts barely depend on the model
	run := func(extra ...string) (int, error) {
		dir, err := os.MkdirTemp("", "pi-measure-")
		if err != nil {
			return 0, err
		}
		defer os.RemoveAll(dir)
		args := []string{"-p", "--session-dir", dir}
		if model != "" {
			args = append(args, "--model", "llama-local/"+model)
		}
		args = append(append(args, extra...), "Reply with exactly: OK")
		cmd := exec.Command("pi", args...)
		cmd.Stdin = nil
		if out, err := cmd.CombinedOutput(); err != nil {
			return 0, fmt.Errorf("pi failed: %v: %s", err, out)
		}
		if n := agents.FirstPromptTokens(dir); n > 0 {
			return n, nil
		}
		return 0, fmt.Errorf("no usage recorded in the session")
	}
	full, err := run()
	if err != nil {
		return 0, err
	}
	cost := agents.PromptCost{Total: full}
	for _, c := range []struct {
		dst  *int
		flag string
	}{{&cost.Extensions, "-ne"}, {&cost.MCP, "--no-mcp"}, {&cost.ContextFiles, "-nc"}, {&cost.Skills, "--no-skills"}} {
		if n, err := run(c.flag); err == nil && full > n {
			*c.dst = full - n
		}
	}
	if n, err := run("-ne", "--no-mcp", "-nc", "--no-skills"); err == nil {
		cost.Base = n
	}
	_ = os.MkdirAll(p.CacheDir, 0o755)
	b, _ := json.Marshal(cost)
	_ = os.WriteFile(filepath.Join(p.CacheDir, "pi-prompt.json"), b, 0o644)
	return full, os.WriteFile(filepath.Join(p.CacheDir, "pi-prompt-tokens"), []byte(strconv.Itoa(full)), 0o644)
}

func smallestLocal(p agents.Paths) string {
	home := filepath.Dir(filepath.Dir(p.AgentDir))
	cfg, _ := llm.LoadConfig(filepath.Join(home, ".config", "omarchy-llm", "config.json"), home)
	best, size := "", int64(-1)
	for _, m := range llm.Discover(cfg) {
		if m.Size > 1<<30 && (size < 0 || m.Size < size) { // skip toy models
			best, size = m.ID, m.Size
		}
	}
	return best
}
