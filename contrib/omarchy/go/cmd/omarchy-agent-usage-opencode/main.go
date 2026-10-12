// omarchy-agent-usage-opencode prints opencode's usage record for the
// omarchy.agents bar panel: tokens per model and day from opencode's
// database (local models tagged "· local"), and meters for the GPU and the
// models Ollama or llama.cpp have loaded.
package main

import (
	"os"
	"path/filepath"
	"time"

	"omarchy/internal/agents"
	"omarchy/internal/jsonout"
	"omarchy/internal/llm"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	home, _ := os.UserHomeDir()
	data := env("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	cfg := env("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	now := time.Now()
	stats := agents.OpencodeStats(filepath.Join(data, "opencode", "opencode.db"), filepath.Join(cfg, "opencode", "opencode.json"), now)
	ollama := env("OLLAMA_HOST", "http://127.0.0.1:11434")
	if len(ollama) < 4 || ollama[:4] != "http" {
		ollama = "http://" + ollama
	}
	var total float64
	if g, ok := llm.QueryGPU(); ok {
		total = float64(g.Total)
	}
	limits, tier := agents.RuntimeState(ollama, "http://127.0.0.1:8080", total)
	limits = append(agents.GPULimits(), limits...)
	jsonout.Print(agents.UsageRecord("opencode", "opencode", now, limits, tier, "", "Local models have no quota; meters show GPU memory.", stats))
}
