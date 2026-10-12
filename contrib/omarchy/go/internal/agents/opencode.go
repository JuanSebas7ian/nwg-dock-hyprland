package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var localHints = []string{"localhost", "127.0.0.1", "0.0.0.0", "[::1]"}

// LocalProviders are opencode provider ids whose models run on this machine.
func LocalProviders(configPath string) map[string]bool {
	ids := map[string]bool{"ollama": true, "lmstudio": true, "llama.cpp": true, "llamacpp": true, "llama": true}
	var cfg struct {
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
		} `json:"provider"`
	}
	if b, err := os.ReadFile(configPath); err == nil && json.Unmarshal(b, &cfg) == nil {
		for id, p := range cfg.Provider {
			for _, h := range localHints {
				if strings.Contains(p.Options.BaseURL, h) {
					ids[id] = true
				}
			}
		}
	}
	return ids
}

// AddOpencodeMessage parses one row of opencode's message table into s.
func AddOpencodeMessage(s *Stats, session, raw string, local map[string]bool) {
	var e struct {
		Role     string `json:"role"`
		Provider string `json:"providerID"`
		Model    string `json:"modelID"`
		Time     struct {
			Created float64 `json:"created"`
		} `json:"time"`
		Tokens struct {
			Input     any `json:"input"`
			Output    any `json:"output"`
			Reasoning any `json:"reasoning"`
			Cache     struct {
				Read  any `json:"read"`
				Write any `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
	}
	if json.Unmarshal([]byte(raw), &e) != nil || e.Role != "assistant" || e.Time.Created == 0 {
		return
	}
	model := e.Model
	if model == "" {
		model = "unknown"
	}
	switch {
	case local[e.Provider]:
		model += " · local"
	case e.Provider != "":
		model += " · " + e.Provider
	}
	when := time.UnixMilli(int64(e.Time.Created))
	s.Add(session, when, model, num(e.Tokens.Input), num(e.Tokens.Output)+num(e.Tokens.Reasoning), num(e.Tokens.Cache.Read), num(e.Tokens.Cache.Write))
}

// OpencodeStats reads every assistant message of opencode's database.
func OpencodeStats(db, config string, now time.Time) *Stats {
	s := NewStats(now)
	rows, err := SQLite(db, `SELECT session_id AS s, data AS d FROM message WHERE data LIKE '%"role"%:%"assistant"%' AND CASE WHEN json_valid(data) THEN json_extract(data, '$.role') END = 'assistant'`)
	if err != nil {
		return s
	}
	local := LocalProviders(config)
	for _, r := range rows {
		sess, _ := r["s"].(string)
		raw, _ := r["d"].(string)
		AddOpencodeMessage(s, sess, raw, local)
	}
	return s
}

// GPULimits: one meter for the NVIDIA card's VRAM.
func GPULimits() []map[string]any {
	out, err := exec.Command("nvidia-smi", "--query-gpu=name,memory.used,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	var limits []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, ",")
		if len(f) != 3 {
			continue
		}
		used, e1 := strconv.ParseFloat(strings.TrimSpace(f[1]), 64)
		total, e2 := strconv.ParseFloat(strings.TrimSpace(f[2]), 64)
		if e1 != nil || e2 != nil || total <= 0 {
			continue
		}
		label := fmt.Sprintf("VRAM %s · %.1f/%.0f GB", strings.TrimPrefix(strings.TrimSpace(f[0]), "NVIDIA GeForce "), used/1024, total/1024)
		limits = append(limits, map[string]any{"label": label, "title": label, "percent": float64(int(used/total*10000)) / 10000, "resetsAt": ""})
	}
	return limits
}

func getJSON(url string, out any) error {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// RuntimeState: meters for the models Ollama and llama.cpp have loaded, and the tier line.
func RuntimeState(ollama, llama string, vramTotal float64) ([]map[string]any, string) {
	var limits []map[string]any
	var parts []string
	var ps struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			VRAM    int64  `json:"size_vram"`
			Expires string `json:"expires_at"`
		} `json:"models"`
	}
	if err := getJSON(ollama+"/api/ps", &ps); err == nil {
		var names []string
		for _, m := range ps.Models {
			names = append(names, m.Name)
			if m.Size <= 0 || vramTotal <= 0 {
				continue
			}
			cpu := 1 - float64(m.VRAM)/float64(m.Size)
			title := fmt.Sprintf("%s · %.1f GB VRAM", m.Name, float64(m.VRAM)/(1<<30))
			if cpu > 0.01 {
				title += fmt.Sprintf(" · %.0f%% on CPU", cpu*100)
			}
			limits = append(limits, map[string]any{"label": title, "title": title, "percent": min(1.0, float64(m.VRAM)/vramTotal), "resetsAt": m.Expires})
		}
		if len(names) > 0 {
			parts = append(parts, "Ollama: "+strings.Join(names, ", "))
		}
	}
	var lm struct {
		Data []struct {
			ID     string `json:"id"`
			Status struct {
				Value string `json:"value"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := getJSON(llama+"/models", &lm); err == nil {
		var loaded []string
		for _, m := range lm.Data {
			if m.Status.Value == "loaded" {
				loaded = append(loaded, m.ID)
				mem := llamaVRAM()[m.ID]
				title := fmt.Sprintf("%s · %.1f GB VRAM · llama.cpp", m.ID, float64(mem)/(1<<30))
				pct := 0.0
				if vramTotal > 0 {
					pct = min(1.0, float64(mem)/vramTotal)
				}
				limits = append(limits, map[string]any{"label": title, "title": title, "percent": pct, "resetsAt": ""})
			}
		}
		if len(loaded) > 0 {
			parts = append(parts, "llama.cpp: "+strings.Join(loaded, ", "))
		} else {
			parts = append(parts, "llama.cpp idle")
		}
	}
	if len(parts) == 0 {
		return limits, "local runtimes not running"
	}
	return limits, strings.Join(parts, " · ")
}

// llamaVRAM maps router model ids to the VRAM of their llama-server child
// (found by its --alias in the GPU process list).
func llamaVRAM() map[string]int64 {
	out := map[string]int64{}
	b, err := exec.Command("nvidia-smi", "--query-compute-apps=pid,used_memory", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Split(line, ",")
		if len(f) != 2 {
			continue
		}
		pid := strings.TrimSpace(f[0])
		mib, _ := strconv.ParseInt(strings.TrimSpace(f[1]), 10, 64)
		args, _ := os.ReadFile("/proc/" + pid + "/cmdline")
		parts := strings.Split(string(args), "\x00")
		for i, a := range parts {
			if a == "--alias" && i+1 < len(parts) {
				out[parts[i+1]] += mib << 20
			}
		}
	}
	return out
}
