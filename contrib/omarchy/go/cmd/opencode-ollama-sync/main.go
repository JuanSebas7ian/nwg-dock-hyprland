// opencode-ollama-sync keeps opencode's Ollama provider in step with the
// models Ollama really has (run by opencode-ollama-sync.path/.timer).
//
//	opencode-ollama-sync            update ~/.config/opencode/opencode.json (only if something changed)
//	opencode-ollama-sync --dry-run  print what would change
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"omarchy/internal/llm"
)

func serverContext() int {
	out, _ := exec.Command("systemctl", "show", "ollama.service", "-p", "Environment", "--value").Output()
	for _, w := range strings.Fields(string(out)) {
		if v, ok := strings.CutPrefix(w, "OLLAMA_CONTEXT_LENGTH="); ok {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return 4096
}

func names(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func main() {
	dry := len(os.Args) > 1 && os.Args[1] == "--dry-run"
	base := os.Getenv("OLLAMA_HOST_URL")
	if base == "" {
		base = "http://127.0.0.1:11434"
	}
	path := os.Getenv("OPENCODE_CONFIG")
	if path == "" {
		cfgDir := os.Getenv("XDG_CONFIG_HOME")
		if cfgDir == "" {
			home, _ := os.UserHomeDir()
			cfgDir = filepath.Join(home, ".config")
		}
		path = filepath.Join(cfgDir, "opencode", "opencode.json")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	shown, err := llm.FetchOllamaModels(ctx, base)
	if err != nil {
		fmt.Printf("opencode-ollama-sync: Ollama not reachable (%v); nothing changed\n", err)
		return // a burst of path triggers must never fail the unit
	}
	found := llm.DescribeOllama(shown, serverContext())
	config := map[string]any{"$schema": "https://opencode.ai/config.json"}
	if b, err := os.ReadFile(path); err == nil {
		config = map[string]any{}
		if err := json.Unmarshal(b, &config); err != nil {
			fmt.Fprintf(os.Stderr, "opencode-ollama-sync: %s is not valid JSON (%v); nothing changed\n", path, err)
			os.Exit(1)
		}
	}
	updated := llm.MergeOllama(config, found, base)
	foundNames := make([]string, 0, len(found))
	for k := range found {
		foundNames = append(foundNames, k)
	}
	sort.Strings(foundNames)
	if llm.SameJSON(updated, config) {
		list := strings.Join(foundNames, ", ")
		if list == "" {
			list = "no chat models"
		}
		fmt.Printf("opencode-ollama-sync: up to date (%s)\n", list)
		return
	}
	before := map[string]bool{}
	if p, ok := config["provider"].(map[string]any); ok {
		if o, ok := p["ollama"].(map[string]any); ok {
			if m, ok := o["models"].(map[string]any); ok {
				for _, n := range names(m) {
					before[n] = true
				}
			}
		}
	}
	var added, removed, kept []string
	for _, n := range foundNames {
		if before[n] {
			kept = append(kept, n)
		} else {
			added = append(added, n)
		}
	}
	for n := range before {
		if _, ok := found[n]; !ok {
			removed = append(removed, n)
		}
	}
	sort.Strings(removed)
	summary := fmt.Sprintf("+%v -%v updated %v", added, removed, kept)
	if dry {
		b, _ := json.MarshalIndent(updated["provider"].(map[string]any)["ollama"], "", "  ")
		fmt.Println(string(b))
		fmt.Println(summary)
		return
	}
	if old, err := os.ReadFile(path); err == nil {
		bak := path + ".bak." + time.Now().Format("20060102")
		if _, err := os.Stat(bak); os.IsNotExist(err) {
			_ = os.WriteFile(bak, old, 0o644)
		}
	}
	b, _ := json.MarshalIndent(updated, "", "  ")
	if _, err := llm.WriteIfChanged(path, string(b)+"\n", 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "opencode-ollama-sync:", err)
		os.Exit(1)
	}
	fmt.Println("opencode-ollama-sync:", summary)
}
