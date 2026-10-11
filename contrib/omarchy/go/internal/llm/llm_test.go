package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var ctx = context.Background()

// ---------------------------------------------------------------- names

func TestNormalizePullName(t *testing.T) {
	ok := map[string]string{
		"qwen3:8b":         "qwen3:8b",
		"  gemma4:12b ":    "gemma4:12b",
		"nomic-embed-text": "nomic-embed-text",
		"hf.co/bartowski/Qwen2.5-Coder-7B-Instruct-GGUF:Q4_K_M":      "hf.co/bartowski/Qwen2.5-Coder-7B-Instruct-GGUF:Q4_K_M",
		"https://huggingface.co/bartowski/phi-4-GGUF":                "hf.co/bartowski/phi-4-GGUF",
		"huggingface.co/unsloth/Qwen3-GGUF/tree/main":                "hf.co/unsloth/Qwen3-GGUF",
		"hf.co/unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF:UD-Q4_K_XL": "hf.co/unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF:UD-Q4_K_XL",
	}
	for in, want := range ok {
		got, err := NormalizePullName(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, err, want)
		}
	}
	// the three shapes that gave Ollama's bare 400 in the widget
	for in, hint := range map[string]string{"": "write a model name", "qwen coder": "spaces",
		"hf.co/bartowski/X-GGUF:Q4_K_M:Q4_K_M": "two tags", "bad$name": "not a valid"} {
		if _, err := NormalizePullName(in); err == nil || !strings.Contains(err.Error(), hint) {
			t.Errorf("%q: want error with %q, got %v", in, hint, err)
		}
	}
}

func TestModelIDIsRouterSafe(t *testing.T) {
	for in, want := range map[string]string{
		"qwen2.5-coder:7b":                 "qwen2.5-coder-7b",
		"gemma4:latest":                    "gemma4",
		"Qwen2.5-Coder-7B-Instruct-Q4_K_M": "qwen2.5-coder-7b-instruct-q4_k_m",
		" weird / name ":                   "weird-name",
	} {
		if got := ModelID(in); got != want || strings.Contains(got, ":") {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestQuantOf(t *testing.T) {
	for in, want := range map[string]string{
		"Qwen2.5-Coder-7B-Instruct-Q4_K_M.gguf": "Q4_K_M", "model-IQ4_XS.gguf": "IQ4_XS",
		"x-UD-Q4_K_XL-00001-of-00002.gguf": "UD-Q4_K_XL", "phi-4-f16.gguf": "F16", "gpt-oss-20b-MXFP4.gguf": "MXFP4",
		"7b": "", "README.md": "",
	} {
		if got := QuantOf(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRepo(t *testing.T) {
	for in, want := range map[string]string{"bartowski/phi-4-GGUF": "bartowski/phi-4-GGUF",
		"hf.co/bartowski/phi-4-GGUF:Q4_K_M": "bartowski/phi-4-GGUF", "https://huggingface.co/a/b/tree/main": "a/b"} {
		if got, err := NormalizeRepo(in); err != nil || got != want {
			t.Errorf("%q -> %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "justname", "a/b/c", "../etc"} {
		if _, err := NormalizeRepo(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// ---------------------------------------------------------------- config

func TestConfigDefaultsLoadSaveAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	c, err := LoadConfig(path, "/home/u")
	if err != nil || c.DefaultBackend != "llama" || c.Port != 8080 || c.Defaults.Ctx != 32768 || c.ModelsDir != "/home/u/ai/models/gguf" {
		t.Fatalf("%v %+v", err, c)
	}
	if err := c.Set("", "defaultBackend", "vllm"); err == nil || c.DefaultBackend != "llama" {
		t.Fatal("unknown backend accepted or left behind")
	}
	if err := c.Set("", "port", "99999"); err == nil || c.Port != 8080 {
		t.Fatal("bad port accepted or left behind")
	}
	if err := c.Set("", "ctx", "16384"); err != nil || c.Defaults.Ctx != 16384 {
		t.Fatal(err)
	}
	if err := c.Set("gemma4-12b", "ctx", "8192"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("gemma4-12b", "cacheK", "q9_9"); err == nil {
		t.Fatal("bad cache type accepted")
	}
	if err := c.Set("", "flashAttn", "maybe"); err == nil {
		t.Fatal("bad flashAttn accepted")
	}
	if err := c.Set("", "extra", "foo = 1\n[x]"); err == nil {
		t.Fatal("INI injection accepted")
	}
	if err := c.Set("", "nope", "1"); err == nil {
		t.Fatal("unknown key accepted")
	}
	if err := c.Set("", "jinja", "off"); err != nil || *c.Defaults.Jinja {
		t.Fatal("jinja off not set")
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadConfig(path, "/home/u")
	if err != nil || c2.Merged("gemma4-12b").Ctx != 8192 || c2.Merged("other").Ctx != 16384 {
		t.Fatalf("%v %+v", err, c2)
	}
	// removing the only override drops the model entry
	if err := c2.Set("gemma4-12b", "ctx", "0"); err != nil || len(c2.Models) != 0 {
		t.Fatalf("%v %+v", err, c2.Models)
	}
	_ = os.WriteFile(path, []byte("{broken"), 0o644)
	if c3, err := LoadConfig(path, "/home/u"); err == nil || c3.Port != 8080 {
		t.Fatal("broken config must fall back to defaults with an error")
	}
}

func TestMergedOverridesOnlyWhatIsSet(t *testing.T) {
	c := DefaultConfig("/h")
	c.Models["m"] = Params{Ctx: 4096, CacheK: "f16", Extra: "seed = 1"}
	c.Defaults.Extra = "temp = 0.2"
	p := c.Merged("m")
	if p.Ctx != 4096 || p.CacheK != "f16" || p.CacheV != "q8_0" || p.GPULayers != 99 || p.Extra != "temp = 0.2; seed = 1" {
		t.Fatalf("%+v", p)
	}
}

// ---------------------------------------------------------------- discovery

func writeGGUF(t *testing.T, path, arch string, fileType uint32, extraArray bool) {
	t.Helper()
	var b bytes.Buffer
	le := binary.LittleEndian
	str := func(s string) { _ = binary.Write(&b, le, uint64(len(s))); b.WriteString(s) }
	kvs := 2
	if extraArray {
		kvs = 3
	}
	b.WriteString("GGUF")
	_ = binary.Write(&b, le, uint32(3))
	_ = binary.Write(&b, le, uint64(0))
	_ = binary.Write(&b, le, uint64(kvs))
	if extraArray { // a token array before the keys we want
		str("tokenizer.ggml.tokens")
		_ = binary.Write(&b, le, uint32(gArray))
		_ = binary.Write(&b, le, uint32(gString))
		_ = binary.Write(&b, le, uint64(3))
		str("a")
		str("bb")
		str("ccc")
	}
	str("general.architecture")
	_ = binary.Write(&b, le, uint32(gString))
	str(arch)
	str("general.file_type")
	_ = binary.Write(&b, le, uint32(gU32))
	_ = binary.Write(&b, le, fileType)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadGGUFInfo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.gguf")
	writeGGUF(t, p, "qwen2", 15, true)
	info, err := ReadGGUFInfo(p)
	if err != nil || info.Arch != "qwen2" || info.Quant != "Q4_K_M" {
		t.Fatalf("%+v %v", info, err)
	}
	_ = os.WriteFile(p, []byte("NOPE...."), 0o644)
	if _, err := ReadGGUFInfo(p); err == nil {
		t.Fatal("non-GGUF accepted")
	}
	if _, err := parseGGUF(bufio.NewReader(strings.NewReader("GGUF"))); err == nil {
		t.Fatal("truncated header accepted")
	}
}

func ollamaTree(t *testing.T) string {
	dir := t.TempDir()
	blob := func(name string, gguf bool) string {
		p := filepath.Join(dir, "blobs", "sha256-"+name)
		if gguf {
			writeGGUF(t, p, "gemma4", 15, false)
		} else {
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte("{}"), 0o644)
		}
		return "sha256:" + name
	}
	manifest := func(rel string, layers ...map[string]any) {
		p := filepath.Join(dir, "manifests", rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		b, _ := json.Marshal(map[string]any{"layers": layers})
		_ = os.WriteFile(p, b, 0o644)
	}
	manifest("registry.ollama.ai/library/gemma4/12b",
		map[string]any{"mediaType": "application/vnd.ollama.image.model", "digest": blob("aaa", true), "size": 100},
		map[string]any{"mediaType": "application/vnd.ollama.image.projector", "digest": blob("bbb", true), "size": 10},
		map[string]any{"mediaType": "application/vnd.ollama.image.template", "digest": blob("ccc", false), "size": 1})
	manifest("hf.co/bartowski/Qwen2.5-Coder-7B-Instruct-GGUF/IQ4_XS",
		map[string]any{"mediaType": "application/vnd.ollama.image.model", "digest": blob("ddd", true), "size": 200})
	manifest("registry.ollama.ai/library/nomic-embed-text/latest",
		map[string]any{"mediaType": "application/vnd.ollama.image.model", "digest": blob("eee", true), "size": 5})
	manifest("registry.ollama.ai/library/safetensors-only/latest",
		map[string]any{"mediaType": "application/vnd.ollama.image.model", "digest": blob("fff", false), "size": 5})
	return dir
}

func TestDiscoverOllama(t *testing.T) {
	dir := ollamaTree(t)
	ms := DiscoverOllama(dir)
	byID := map[string]Model{}
	for _, m := range ms {
		byID[m.ID] = m
	}
	if len(ms) != 2 {
		t.Fatalf("want gemma + qwen (no embeddings, no non-GGUF), got %+v", ms)
	}
	g := byID["gemma4-12b"]
	if g.Name != "gemma4:12b" || !strings.HasSuffix(g.MMProj, "sha256-bbb") || !strings.HasSuffix(g.Path, "sha256-aaa") {
		t.Fatalf("%+v", g)
	}
	q := byID["qwen2.5-coder-7b-instruct-gguf-iq4_xs"]
	if q.Name != "hf.co/bartowski/Qwen2.5-Coder-7B-Instruct-GGUF:IQ4_XS" || q.Quant != "IQ4_XS" {
		t.Fatalf("%+v", ms)
	}
}

func TestDiscoverGGUFGroupsSplitsAndMMProj(t *testing.T) {
	dir := t.TempDir()
	writeGGUF(t, filepath.Join(dir, "a__vision", "vision-Q4_K_M.gguf"), "gemma3", 15, false)
	writeGGUF(t, filepath.Join(dir, "a__vision", "mmproj-F16.gguf"), "clip", 1, false)
	writeGGUF(t, filepath.Join(dir, "b__big", "big-Q8_0-00001-of-00002.gguf"), "llama", 7, false)
	writeGGUF(t, filepath.Join(dir, "b__big", "big-Q8_0-00002-of-00002.gguf"), "llama", 7, false)
	_ = os.WriteFile(filepath.Join(dir, "b__big", "big-Q8_0-00002-of-00002.gguf.part"), []byte("x"), 0o644)
	ms := DiscoverGGUF(dir)
	if len(ms) != 2 {
		t.Fatalf("%+v", ms)
	}
	byID := map[string]Model{}
	for _, m := range ms {
		byID[m.ID] = m
	}
	big, vis := byID["big-q8_0"], byID["vision-q4_k_m"]
	if big.ID != "big-q8_0" || big.Quant != "Q8_0" || big.Size != 2*fileSize(big.Path) || !strings.HasSuffix(big.Path, "00001-of-00002.gguf") {
		t.Fatalf("big %+v", big)
	}
	if vis.ID != "vision-q4_k_m" || !strings.HasSuffix(vis.MMProj, "mmproj-F16.gguf") {
		t.Fatalf("vision %+v", vis)
	}
}

func TestDiscoverDeduplicatesIDs(t *testing.T) {
	c := DefaultConfig("/h")
	c.OllamaDir = ollamaTree(t)
	c.ModelsDir = t.TempDir()
	writeGGUF(t, filepath.Join(c.ModelsDir, "x", "gemma4-12b.gguf"), "gemma4", 15, false)
	ms := Discover(c)
	ids := map[string]bool{}
	for _, m := range ms {
		if ids[m.ID] {
			t.Fatalf("duplicate id %s", m.ID)
		}
		ids[m.ID] = true
		if m.Arch == "" {
			t.Fatalf("arch not read for %s", m.ID)
		}
	}
	if !ids["gemma4-12b"] || !ids["gemma4-12b-ollama"] {
		t.Fatalf("%v", ids)
	}
}

// ---------------------------------------------------------------- preset & agents

func TestRenderPresetIsDeterministicAndComplete(t *testing.T) {
	c := DefaultConfig("/h")
	c.Models["b"] = Params{Ctx: 8192, Jinja: boolPtr(false), Extra: "temp = 0.3"}
	ms := []Model{{ID: "a", Path: "/m/a.gguf", MMProj: "/m/p.gguf"}, {ID: "b", Path: "/m/b.gguf"}}
	out := RenderPreset(c, ms)
	if out != RenderPreset(c, ms) {
		t.Fatal("not deterministic")
	}
	for _, want := range []string{"version = 1", "[*]\nsleep-idle-seconds = 900", "[a]\nmodel = /m/a.gguf\nmmproj = /m/p.gguf\nctx-size = 32768",
		"cache-type-k = q8_0", "flash-attn = on", "[b]\nmodel = /m/b.gguf\nctx-size = 8192", "jinja = off", "temp = 0.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("preset lacks %q:\n%s", want, out)
		}
	}
	c.IdleSeconds = -1
	if strings.Contains(RenderPreset(c, ms), "sleep-idle") {
		t.Fatal("idle -1 should not sleep")
	}
}

func TestWriteIfChanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "f")
	if ch, err := WriteIfChanged(p, "x", 0o644); !ch || err != nil {
		t.Fatal(ch, err)
	}
	if ch, _ := WriteIfChanged(p, "x", 0o644); ch {
		t.Fatal("same content reported as change")
	}
	if ch, _ := WriteIfChanged(p, "y", 0o644); !ch {
		t.Fatal("change missed")
	}
}

func TestSyncOpencodeKeepsOtherKeysAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "opencode.json")
	_ = os.WriteFile(p, []byte(`{"autoupdate":false,"provider":{"ollama":{"npm":"x","models":{"a":{}}},"llama":{"models":{"default":{}}}}}`), 0o644)
	c := DefaultConfig("/h")
	c.DefaultModel = "qwen"
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	ch, err := SyncOpencode(p, c, []Model{{ID: "qwen", Name: "qwen2.5-coder:7b", Quant: "Q4_K_M"}}, now)
	if err != nil || !ch {
		t.Fatal(ch, err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(p)
	_ = json.Unmarshal(b, &doc)
	prov := doc["provider"].(map[string]any)
	llama := prov["llama"].(map[string]any)
	models := llama["models"].(map[string]any)
	if doc["autoupdate"] != false || prov["ollama"] == nil || models["default"] != nil || models["qwen"] == nil || doc["model"] != "llama/qwen" {
		t.Fatalf("%s", b)
	}
	if llama["options"].(map[string]any)["baseURL"] != "http://127.0.0.1:8080/v1" {
		t.Fatal("baseURL")
	}
	if _, err := os.Stat(p + ".bak.20261011"); err != nil {
		t.Fatal("no backup")
	}
	if ch, _ := SyncOpencode(p, c, []Model{{ID: "qwen", Name: "qwen2.5-coder:7b", Quant: "Q4_K_M"}}, now); ch {
		t.Fatal("second sync changed something")
	}
	c.DefaultBackend = "ollama"
	_, _ = SyncOpencode(p, c, []Model{{ID: "qwen"}}, now)
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), `"model": "llama/`) {
		t.Fatalf("opencode still defaults to llama with the ollama backend: %s", b)
	}
	_ = os.WriteFile(p, []byte("{nope"), 0o644)
	if _, err := SyncOpencode(p, c, nil, now); err == nil {
		t.Fatal("broken JSON overwritten")
	}
}

func TestSyncPiWritesProviderAndDefaults(t *testing.T) {
	home := t.TempDir()
	agent := filepath.Join(home, ".pi", "agent")
	_ = os.MkdirAll(agent, 0o755)
	_ = os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"theme":"omarchy-system"}`), 0o644)
	_ = os.WriteFile(filepath.Join(agent, "models.json"), []byte(`{"providers":{"ollama":{"baseUrl":"x"}}}`), 0o600)
	c := DefaultConfig(home)
	c.DefaultModel = "qwen"
	ms := []Model{{ID: "qwen", Name: "qwen:7b"}, {ID: "vis", Name: "vis", MMProj: "/p"}}
	if _, err := SyncPi(agent, c, ms); err != nil {
		t.Fatal(err)
	}
	var set, mod map[string]any
	b, _ := os.ReadFile(filepath.Join(agent, "settings.json"))
	_ = json.Unmarshal(b, &set)
	b2, _ := os.ReadFile(filepath.Join(agent, "models.json"))
	_ = json.Unmarshal(b2, &mod)
	if set["theme"] != "omarchy-system" || set["defaultProvider"] != PiProvider || set["defaultModel"] != "qwen" {
		t.Fatalf("settings %s", b)
	}
	provs := mod["providers"].(map[string]any)
	lp := provs[PiProvider].(map[string]any)
	if provs["ollama"] == nil || lp["baseUrl"] != "http://127.0.0.1:8080/v1" || len(lp["models"].([]any)) != 2 {
		t.Fatalf("models %s", b2)
	}
	if !strings.Contains(string(b2), `"image"`) {
		t.Fatal("vision model without image input")
	}
	env, _ := os.ReadFile(filepath.Join(home, ".config", "environment.d", "50-omarchy-llm.conf"))
	if !strings.Contains(string(env), "LLAMA_BASE_URL=http://127.0.0.1:8080") {
		t.Fatalf("env %s", env)
	}
	// switching to Ollama removes our defaults but keeps the provider
	c.DefaultBackend = "ollama"
	_, _ = SyncPi(agent, c, ms)
	b, _ = os.ReadFile(filepath.Join(agent, "settings.json"))
	if strings.Contains(string(b), "defaultProvider") {
		t.Fatalf("defaults kept with ollama backend: %s", b)
	}
	// no Pi installed: only the env file, no error
	if _, err := SyncPi(filepath.Join(t.TempDir(), ".pi", "agent"), c, ms); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------- router client

func TestRouterModelsLoadUnload(t *testing.T) {
	var loads int32
	var status atomic.Value
	status.Store("unloaded")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			fmt.Fprintf(w, `{"data":[{"id":"qwen","status":{"value":%q}},{"id":"bad","status":{"value":"unloaded","failed":true}}]}`, status.Load())
		case "/models/load":
			atomic.AddInt32(&loads, 1)
			status.Store("loaded")
			w.Write([]byte(`{"success":true}`))
		case "/models/unload":
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"model is not loaded"}}`))
		}
	}))
	defer srv.Close()
	r := &Router{URL: srv.URL}
	if !r.Up(ctx) {
		t.Fatal("not up")
	}
	ms, err := r.Models(ctx)
	if err != nil || len(ms) != 2 || ms[1].Status != "failed" {
		t.Fatalf("%v %+v", err, ms)
	}
	if err := r.Load(ctx, "qwen", time.Second); err != nil || loads != 1 {
		t.Fatal(err)
	}
	if err := r.Load(ctx, "bad", time.Second); err == nil || !strings.Contains(err.Error(), "could not load") {
		t.Fatalf("got %v", err)
	}
	if err := r.Unload(ctx, "qwen"); err == nil || !strings.Contains(err.Error(), "not loaded") {
		t.Fatalf("got %v", err)
	}
	down := &Router{URL: "http://127.0.0.1:1"}
	if down.Up(ctx) {
		t.Fatal("closed port up")
	}
}

// ---------------------------------------------------------------- ollama client

func TestOllamaPullSurfacesRealErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.Model {
		case "ok:1b":
			w.Write([]byte("{\"status\":\"pulling manifest\"}\n{\"status\":\"pulling x\",\"total\":10,\"completed\":5}\n{\"status\":\"success\"}\n"))
		case "hf.co/a/b-GGUF:Q9":
			w.Write([]byte("{\"status\":\"pulling manifest\"}\n{\"error\":\"pull model manifest: file does not exist\"}\n"))
		case "cut:1b":
			w.Write([]byte("{\"status\":\"pulling x\",\"total\":10,\"completed\":5}\n"))
		default:
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid model name"}`))
		}
	}))
	defer srv.Close()
	o := &Ollama{URL: srv.URL}
	var lines []PullProgress
	if err := o.Pull(ctx, "ok:1b", 0, func(p PullProgress) { lines = append(lines, p) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || !lines[2].Done || lines[1].Completed != 5 {
		t.Fatalf("%+v", lines)
	}
	if err := o.Pull(ctx, "hf.co/a/b-GGUF:Q9", 0, func(PullProgress) {}); err == nil || !strings.Contains(err.Error(), "quantization does not exist") {
		t.Fatalf("got %v", err)
	}
	if err := o.Pull(ctx, "cut:1b", 0, func(PullProgress) {}); err == nil || !strings.Contains(err.Error(), "without finishing") {
		t.Fatalf("got %v", err)
	}
	if err := o.Pull(ctx, "server:rejects", 0, func(PullProgress) {}); err == nil || !strings.Contains(err.Error(), "invalid model name") {
		t.Fatalf("got %v", err)
	}
	if err := o.Pull(ctx, "with space", 0, func(PullProgress) {}); err == nil || !strings.Contains(err.Error(), "spaces") {
		t.Fatalf("got %v", err)
	}
}

func TestOllamaStatusDownAndUp(t *testing.T) {
	if st := (&Ollama{URL: "http://127.0.0.1:1"}).Status(ctx); st.Running || st.Error == "" || st.Models == nil {
		t.Fatalf("%+v", st)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			w.Write([]byte(`{"version":"0.33.3"}`))
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"hf.co/a/b:IQ4_XS","size":9,"modified_at":"2026-10-09T10:00:00Z","details":{}},{"name":"qwen:7b","details":{"quantization_level":"Q4_K_M"}}]}`))
		case "/api/ps":
			w.Write([]byte(`{"models":[{"name":"qwen:7b","size_vram":42}]}`))
		}
	}))
	defer srv.Close()
	st := (&Ollama{URL: srv.URL}).Status(ctx)
	if !st.Running || len(st.Models) != 2 || st.Models[0].Quant != "IQ4_XS" || st.Models[0].Modified != "2026-10-09" || !st.Models[1].Loaded || st.Models[1].VRAM != 42 {
		t.Fatalf("%+v", st)
	}
}

func TestParseOllamaSearch(t *testing.T) {
	page := `<a href="/library/qwen3" class="group"><p class="x">Qwen 3 &amp; friends</p>
<span class="text-indigo-600">tools</span><span class="text-blue-600">8b</span><span class="text-blue-600">32b</span>
<span>1.2M</span><span class="y">&nbsp;Pulls</span></a>
<a href="/library/qwen3" class="group">dup</a>
<a href="/library/gemma4" class="group"><p>Gemma</p></a>`
	res := ParseOllamaSearch(page, 10)
	if len(res) != 2 || res[0].Name != "qwen3" || res[0].Description != "Qwen 3 & friends" || res[0].Pulls != "1.2M" ||
		len(res[0].Sizes) != 2 || res[0].Capabilities[0] != "tools" || res[1].Name != "gemma4" {
		t.Fatalf("%+v", res)
	}
}

// ---------------------------------------------------------------- hugging face

func hfServer(t *testing.T, payload []byte) (*httptest.Server, *int32) {
	var ranged int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/models" && r.URL.Query().Get("filter") == "gguf":
			w.Write([]byte(`[{"id":"bartowski/Qwen-GGUF","downloads":43210,"likes":12,"tags":["gguf","code","conversational"],"pipeline_tag":"text-generation","lastModified":"2026-09-01T00:00:00Z"}]`))
		case r.URL.Path == "/api/models/bartowski/Qwen-GGUF/tree/main":
			w.Write([]byte(`[{"type":"file","path":"README.md","size":10},
{"type":"file","path":"Qwen-Q4_K_M.gguf","size":100,"lfs":{"size":4000000000}},
{"type":"file","path":"Qwen-Q8_0-00001-of-00002.gguf","lfs":{"size":6000000000}},
{"type":"file","path":"Qwen-Q8_0-00002-of-00002.gguf","lfs":{"size":6000000000}},
{"type":"file","path":"mmproj-Qwen-f16.gguf","lfs":{"size":600000000}},
{"type":"directory","path":"sub"}]`))
		case r.URL.Path == "/api/models/gated/x/tree/main":
			w.WriteHeader(401)
		case strings.HasPrefix(r.URL.Path, "/bartowski/Qwen-GGUF/resolve/main/"):
			start := 0
			if rg := r.Header.Get("Range"); rg != "" {
				atomic.AddInt32(&ranged, 1)
				fmt.Sscanf(rg, "bytes=%d-", &start)
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)-start))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			}
			w.Write(payload[start:])
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &ranged
}

func TestHFSearchAndFiles(t *testing.T) {
	srv, _ := hfServer(t, nil)
	h := &HF{Base: srv.URL}
	res, err := h.Search(ctx, "hf.co/qwen")
	if err != nil || len(res) != 1 || res[0].Pulls != "43k" || res[0].Source != "hf" || res[0].Updated != "2026-09-01" {
		t.Fatalf("%v %+v", err, res)
	}
	files, err := h.Files(ctx, "hf.co/bartowski/Qwen-GGUF:Q4_K_M", 12<<30)
	if err != nil || len(files) != 2 {
		t.Fatalf("%v %+v", err, files)
	}
	if files[0].Quant != "Q4_K_M" || files[0].Size != 4000000000 || files[0].Fit != "gpu" || files[0].MMProj != "mmproj-Qwen-f16.gguf" {
		t.Fatalf("%+v", files[0])
	}
	if files[1].Quant != "Q8_0" || len(files[1].Files) != 2 || files[1].Size != 12000000000 || files[1].Fit != "partial" {
		t.Fatalf("%+v", files[1])
	}
	if _, err := h.Files(ctx, "gated/x", 0); err == nil || !strings.Contains(err.Error(), "gated") {
		t.Fatalf("got %v", err)
	}
}

func TestFit(t *testing.T) {
	gb := int64(1 << 30)
	if Fit(4*gb, 12*gb) != "gpu" || Fit(11*gb, 12*gb) != "partial" || Fit(30*gb, 12*gb) != "cpu" || Fit(gb, 0) != "cpu" {
		t.Fatal("fit thresholds")
	}
}

func TestHFDownloadResumesAndRejectsTraversal(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789"), 1000)
	srv, ranged := hfServer(t, payload)
	h := &HF{Base: srv.URL}
	dir := t.TempDir()
	repoDir := RepoDir(dir, "bartowski/Qwen-GGUF")
	_ = os.MkdirAll(repoDir, 0o755)
	_ = os.WriteFile(filepath.Join(repoDir, "Qwen-Q4_K_M.gguf.part"), payload[:3000], 0o644) // interrupted before
	var last DownloadProgress
	p, err := h.Download(ctx, "bartowski/Qwen-GGUF", []string{"Qwen-Q4_K_M.gguf"}, dir, 0, func(d DownloadProgress) { last = d })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, payload) || *ranged != 1 || !last.Done {
		t.Fatalf("resume failed: %d bytes, ranged=%d, last=%+v", len(got), *ranged, last)
	}
	if _, err := os.Stat(p + ".part"); err == nil {
		t.Fatal(".part left behind")
	}
	for _, bad := range [][]string{{"../evil.gguf"}, {"/etc/passwd"}, {"notes.txt"}, {}} {
		if _, err := h.Download(ctx, "bartowski/Qwen-GGUF", bad, dir, 0, func(DownloadProgress) {}); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestParseNvidiaSmi(t *testing.T) {
	g, ok := ParseNvidiaSmi("NVIDIA GeForce RTX 3060, 12288, 2309, 9979\n")
	if !ok || g.Total != 12288<<20 || g.Free != 9979<<20 || g.Name != "NVIDIA GeForce RTX 3060" {
		t.Fatalf("%+v", g)
	}
	if _, ok := ParseNvidiaSmi("No devices were found"); ok {
		t.Fatal("garbage parsed")
	}
}
