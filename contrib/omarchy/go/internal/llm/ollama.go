package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Ollama is the Ollama daemon's local API.
type Ollama struct {
	URL  string
	HTTP *http.Client
}

func (o *Ollama) client(timeout time.Duration) *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: timeout}
}

var (
	pullNameRe = regexp.MustCompile(`^(?:[a-z0-9][a-z0-9.-]*(?::\d+)?/)?[A-Za-z0-9][A-Za-z0-9._-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)*(?::[A-Za-z0-9][A-Za-z0-9._-]*)?$`)
	hfURLRe    = regexp.MustCompile(`^(?:https?://)?(?:www\.)?(?:huggingface\.co|hf\.co)/`)
)

// NormalizePullName fixes what people paste and rejects what Ollama answers
// with a bare "400 Bad Request" (the widget's download error on 2026-10-11:
// a search text with spaces, an empty field, or "repo:Q4_K_M:Q4_K_M").
func NormalizePullName(name string) (string, error) {
	n := strings.TrimSpace(name)
	n = strings.TrimSuffix(n, "/")
	if hfURLRe.MatchString(n) {
		n = "hf.co/" + hfURLRe.ReplaceAllString(n, "")
		n = strings.SplitN(n, "/tree/", 2)[0]
		n = strings.SplitN(n, "/blob/", 2)[0]
	}
	switch {
	case n == "":
		return "", fmt.Errorf("write a model name, e.g. qwen3:8b or hf.co/bartowski/Qwen2.5-Coder-7B-Instruct-GGUF:Q4_K_M")
	case strings.ContainsAny(n, " \t"):
		return "", fmt.Errorf("%q has spaces: that's a search, not a model name (press Enter to search)", n)
	case strings.Count(n[strings.LastIndex(n, "/")+1:], ":") > 1:
		return "", fmt.Errorf("%q has two tags; keep one, e.g. %s", n, n[:strings.LastIndex(n, ":")])
	case !pullNameRe.MatchString(n):
		return "", fmt.Errorf("%q is not a valid Ollama model name", n)
	}
	return n, nil
}

// OllamaModel is a downloaded Ollama model.
type OllamaModel struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Family   string `json:"family"`
	Params   string `json:"params"`
	Quant    string `json:"quant"`
	Modified string `json:"modified"`
	Loaded   bool   `json:"loaded"`
	VRAM     int64  `json:"vram,omitempty"`
}

// OllamaStatus is the Ollama tab of the panel.
type OllamaStatus struct {
	Running bool          `json:"running"`
	Version string        `json:"version"`
	Models  []OllamaModel `json:"models"`
	Error   string        `json:"error,omitempty"`
}

func (o *Ollama) getJSON(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, o.URL+path, nil)
	resp, err := o.client(5 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama %s: %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Status lists models and which are loaded.
func (o *Ollama) Status(ctx context.Context) OllamaStatus {
	st := OllamaStatus{Models: []OllamaModel{}}
	var v struct{ Version string }
	if err := o.getJSON(ctx, "/api/version", &v); err != nil {
		st.Error = "Ollama is not running (sudo systemctl start ollama)"
		return st
	}
	st.Running, st.Version = true, v.Version
	var tags struct {
		Models []struct {
			Name     string `json:"name"`
			Size     int64  `json:"size"`
			Modified string `json:"modified_at"`
			Details  struct {
				Family string `json:"family"`
				Params string `json:"parameter_size"`
				Quant  string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	var ps struct {
		Models []struct {
			Name string `json:"name"`
			VRAM int64  `json:"size_vram"`
		} `json:"models"`
	}
	if err := o.getJSON(ctx, "/api/tags", &tags); err != nil {
		st.Error = err.Error()
	}
	_ = o.getJSON(ctx, "/api/ps", &ps)
	loaded := map[string]int64{}
	for _, m := range ps.Models {
		loaded[m.Name] = m.VRAM
	}
	for _, m := range tags.Models {
		q := m.Details.Quant
		if q == "" {
			if i := strings.LastIndex(m.Name, ":"); i >= 0 {
				q = QuantOf(m.Name[i+1:])
			}
		}
		mod := OllamaModel{Name: m.Name, Size: m.Size, Family: m.Details.Family, Params: m.Details.Params, Quant: q}
		if len(m.Modified) >= 10 {
			mod.Modified = m.Modified[:10]
		}
		if vram, ok := loaded[m.Name]; ok {
			mod.Loaded, mod.VRAM = true, vram
		}
		st.Models = append(st.Models, mod)
	}
	return st
}

// PullProgress is one line of a download.
type PullProgress struct {
	Model     string `json:"model"`
	Status    string `json:"status,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Pull downloads a model, calling progress at most every interval.
func (o *Ollama) Pull(ctx context.Context, name string, interval time.Duration, progress func(PullProgress)) error {
	n, err := NormalizePullName(name)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"model": n, "stream": true})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{} // downloads take as long as they take
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("Ollama is not running: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct{ Error string }
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("Ollama: %s", e.Error)
		}
		return fmt.Errorf("Ollama refused %s (%d)", n, resp.StatusCode)
	}
	var last time.Time
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var line struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Error != "" {
			return fmt.Errorf("Ollama: %s", friendlyPullError(line.Error, n))
		}
		if line.Status == "success" {
			progress(PullProgress{Model: n, Done: true})
			return nil
		}
		if time.Since(last) >= interval {
			last = time.Now()
			progress(PullProgress{Model: n, Status: line.Status, Total: line.Total, Completed: line.Completed})
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("download interrupted: %w", err)
	}
	return fmt.Errorf("download of %s ended without finishing", n)
}

func friendlyPullError(msg, name string) string {
	if strings.Contains(msg, "file does not exist") {
		if strings.HasPrefix(name, "hf.co/") {
			return "that quantization does not exist in the repository; pick one from the list of files"
		}
		return "no such model or tag on ollama.com"
	}
	return msg
}

// Delete removes a model.
func (o *Ollama) Delete(ctx context.Context, name string) error {
	b, _ := json.Marshal(map[string]string{"model": name})
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, o.URL+"/api/delete", bytes.NewReader(b))
	resp, err := o.client(30 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama could not delete %s (%d)", name, resp.StatusCode)
	}
	return nil
}

// Unload frees a model's memory now.
func (o *Ollama) Unload(ctx context.Context, name string) error {
	b, _ := json.Marshal(map[string]any{"model": name, "keep_alive": 0})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/generate", bytes.NewReader(b))
	resp, err := o.client(30 * time.Second).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// UnloadAll frees every loaded Ollama model (before llama.cpp needs the VRAM).
func (o *Ollama) UnloadAll(ctx context.Context) int {
	var ps struct {
		Models []struct{ Name string } `json:"models"`
	}
	if o.getJSON(ctx, "/api/ps", &ps) != nil {
		return 0
	}
	for _, m := range ps.Models {
		_ = o.Unload(ctx, m.Name)
	}
	return len(ps.Models)
}

// CatalogEntry is a model to download (ollama.com or Hugging Face).
type CatalogEntry struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
	Sizes        []string `json:"sizes"`
	Pulls        string   `json:"pulls"`
	Source       string   `json:"source"` // ollama | hf
	Updated      string   `json:"updated,omitempty"`
}

var (
	olBlockRe = regexp.MustCompile(`href="/library/([^"]+)"`)
	olDescRe  = regexp.MustCompile(`<p[^>]*>([^<]*)</p>`)
	olTagRe   = regexp.MustCompile(`<span[^>]*class="[^"]*text-(indigo|blue)-600[^"]*"[^>]*>\s*([^<]+?)\s*</span>`)
	olPullsRe = regexp.MustCompile(`<span[^>]*>([\d.,]+[KMB]?)</span>\s*<span[^>]*>(?:&nbsp;|\s)*Pulls`)
)

// ParseOllamaSearch reads ollama.com/search's HTML (best effort).
func ParseOllamaSearch(page string, limit int) []CatalogEntry {
	out := []CatalogEntry{}
	locs := olBlockRe.FindAllStringSubmatchIndex(page, -1)
	seen := map[string]bool{}
	for i, loc := range locs {
		name := page[loc[2]:loc[3]]
		if seen[name] || strings.Contains(name, "/") {
			continue
		}
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if end-loc[0] > 6000 {
			end = loc[0] + 6000
		}
		chunk := page[loc[0]:end]
		e := CatalogEntry{Name: name, Source: "ollama", Capabilities: []string{}, Sizes: []string{}}
		if m := olDescRe.FindStringSubmatch(chunk); m != nil {
			e.Description = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		for _, m := range olTagRe.FindAllStringSubmatch(chunk, -1) {
			if m[1] == "indigo" {
				e.Capabilities = append(e.Capabilities, m[2])
			} else {
				e.Sizes = append(e.Sizes, m[2])
			}
		}
		if m := olPullsRe.FindStringSubmatch(chunk); m != nil {
			e.Pulls = m[1]
		}
		seen[name] = true
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// SearchOllama queries ollama.com's library.
func SearchOllama(ctx context.Context, hc *http.Client, base, q string) ([]CatalogEntry, error) {
	u := base + "/search"
	if q = strings.TrimSpace(q); q != "" {
		u += "?" + url.Values{"q": {q}}.Encode()
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "omarchy-llm")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama.com unreachable: %w", err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return ParseOllamaSearch(string(page), 30), nil
}
