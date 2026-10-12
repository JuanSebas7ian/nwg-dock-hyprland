package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
)

// ShownModel is a model from /api/tags with its /api/show answer.
type ShownModel struct {
	Name string
	Show struct {
		Capabilities []string       `json:"capabilities"`
		ModelInfo    map[string]any `json:"model_info"`
	}
}

// DescribeOllama turns Ollama models into opencode entries: chat models only,
// context = min(model, server), output = a quarter (max 8192).
func DescribeOllama(models []ShownModel, serverCtx int) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, m := range models {
		caps := map[string]bool{}
		for _, c := range m.Show.Capabilities {
			caps[c] = true
		}
		if !caps["completion"] {
			continue // embeddings and the like cannot chat
		}
		native := serverCtx
		keys := make([]string, 0, len(m.Show.ModelInfo))
		for k := range m.Show.ModelInfo {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if f, ok := m.Show.ModelInfo[k].(float64); ok && strings.HasSuffix(k, ".context_length") && f == float64(int(f)) {
				native = int(f)
				break
			}
		}
		ctx := min(native, serverCtx)
		e := map[string]any{"name": m.Name + " (local)", "tool_call": caps["tools"], "reasoning": caps["thinking"],
			"limit": map[string]any{"context": ctx, "output": min(8192, ctx/4)}}
		if caps["vision"] {
			e["attachment"] = true
			e["modalities"] = map[string]any{"input": []any{"text", "image"}, "output": []any{"text"}}
		}
		out[m.Name] = e
	}
	return out
}

func ollamaProvider(base string) map[string]any {
	return map[string]any{"npm": "@ai-sdk/openai-compatible", "name": "Ollama (local)", "options": map[string]any{"baseURL": base + "/v1"}}
}

// deepCopy through JSON (configs are JSON anyway).
func deepCopy(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// MergeOllama returns a new config with provider.ollama.models = found, keeping
// the user's names, extra keys and the rest of the file.
func MergeOllama(config map[string]any, found map[string]map[string]any, base string) map[string]any {
	cfg := deepCopy(config)
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
		cfg["provider"] = providers
	}
	prov, _ := providers["ollama"].(map[string]any)
	if prov == nil {
		prov = ollamaProvider(base)
		providers["ollama"] = prov
	}
	for k, v := range ollamaProvider(base) {
		if _, ok := prov[k]; !ok {
			prov[k] = v
		}
	}
	old, _ := prov["models"].(map[string]any)
	models := map[string]any{}
	for name, e := range found {
		prev, _ := old[name].(map[string]any)
		merged := map[string]any{}
		for k, v := range prev {
			merged[k] = v
		}
		for k, v := range e {
			if k != "name" {
				merged[k] = v
			}
		}
		if n, ok := prev["name"].(string); ok && n != "" {
			merged["name"] = n
		} else {
			merged["name"] = e["name"]
		}
		models[name] = merged
	}
	prov["models"] = models
	return deepCopy(cfg) // normalise numbers so equality with the file works
}

// SameJSON compares two decoded JSON documents.
func SameJSON(a, b map[string]any) bool {
	return reflect.DeepEqual(deepCopy(a), deepCopy(b))
}

// FetchOllamaModels asks /api/tags then /api/show for each model.
func FetchOllamaModels(ctx context.Context, base string) ([]ShownModel, error) {
	hc := &http.Client{Timeout: 10 * time.Second}
	o := &Ollama{URL: base, HTTP: hc}
	var tags struct {
		Models []struct{ Name string } `json:"models"`
	}
	if err := o.getJSON(ctx, "/api/tags", &tags); err != nil {
		return nil, err
	}
	var out []ShownModel
	for _, m := range tags.Models {
		body := strings.NewReader(fmt.Sprintf(`{"model":%q}`, m.Name))
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/show", body)
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		s := ShownModel{Name: m.Name}
		_ = json.NewDecoder(resp.Body).Decode(&s.Show)
		resp.Body.Close()
		out = append(out, s)
	}
	return out, nil
}
