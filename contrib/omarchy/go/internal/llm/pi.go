package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PiProvider is the provider omarchy-llm writes into Pi's models.json. Pi's
// built-in "llama.cpp" provider only lists models after an interactive
// catalog refresh, so `pi -p` (sub-agents) could not use them; this provider
// lists every preset and the router loads whichever is picked.
const PiProvider = "llama-local"

// SyncPi writes Pi's models.json provider and, when llama is the default
// backend, Pi's startup provider/model in settings.json (other keys kept). It
// also exports LLAMA_BASE_URL (environment.d) for Pi's /llama manager.
// agentDir is ~/.pi/agent.
func SyncPi(agentDir string, c Config, models []Model) (bool, error) {
	home := filepath.Dir(filepath.Dir(agentDir))
	base := fmt.Sprintf("http://%s:%d", c.Host, c.Port)
	changed, err := WriteIfChanged(filepath.Join(home, ".config", "environment.d", "50-omarchy-llm.conf"),
		"# omarchy-llm: Pi (and other clients) find the local llama.cpp router here.\nLLAMA_BASE_URL="+base+"\n", 0o644)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(agentDir); err != nil {
		return changed, nil // Pi not installed
	}

	modelsPath := filepath.Join(agentDir, "models.json")
	doc := map[string]any{}
	if b, err := os.ReadFile(modelsPath); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return changed, fmt.Errorf("%s is not valid JSON, not touching it: %w", modelsPath, err)
		}
	}
	providers, _ := doc["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	list := []any{}
	for _, m := range models {
		p := c.Merged(m.ID)
		ctxLen := p.Ctx
		if ctxLen == 0 {
			ctxLen = 4096
		}
		entry := map[string]any{"id": m.ID, "name": m.Name + " (llama.cpp)", "contextWindow": ctxLen,
			"maxTokens": min(8192, ctxLen/2), "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
		if m.MMProj != "" {
			entry["input"] = []string{"text", "image"}
		}
		list = append(list, entry)
	}
	providers[PiProvider] = map[string]any{"baseUrl": base + "/v1", "api": "openai-completions", "apiKey": "local", "models": list}
	doc["providers"] = providers
	b, _ := json.MarshalIndent(doc, "", "  ")
	c1, err := WriteIfChanged(modelsPath, string(b)+"\n", 0o600)
	if err != nil {
		return changed, err
	}

	settingsPath := filepath.Join(agentDir, "settings.json")
	set := map[string]any{}
	if b, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(b, &set); err != nil {
			return changed || c1, fmt.Errorf("%s is not valid JSON, not touching it: %w", settingsPath, err)
		}
	}
	known := false
	for _, m := range models {
		known = known || m.ID == c.DefaultModel
	}
	if c.DefaultBackend == "llama" && known {
		set["defaultProvider"] = PiProvider
		set["defaultModel"] = c.DefaultModel
	} else if set["defaultProvider"] == PiProvider || set["defaultProvider"] == "llama.cpp" {
		delete(set, "defaultProvider")
		delete(set, "defaultModel")
	}
	b, _ = json.MarshalIndent(set, "", "  ")
	c2, err := WriteIfChanged(settingsPath, string(b)+"\n", 0o644)
	return changed || c1 || c2, err
}
