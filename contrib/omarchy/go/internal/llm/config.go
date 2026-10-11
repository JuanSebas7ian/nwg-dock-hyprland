// Package llm manages local models for agents: llama.cpp's router server as
// the default runtime (models load when an agent asks for them and unload
// when idle), Ollama as the alternative, and GGUF downloads from Hugging Face.
package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Params are llama-server options for a model (zero = inherit).
type Params struct {
	Ctx          int    `json:"ctx,omitempty"`
	GPULayers    int    `json:"gpuLayers,omitempty"`
	FlashAttn    string `json:"flashAttn,omitempty"` // on|off|auto
	CacheK       string `json:"cacheK,omitempty"`    // f16|q8_0|q4_0
	CacheV       string `json:"cacheV,omitempty"`
	Threads      int    `json:"threads,omitempty"`
	ThreadsBatch int    `json:"threadsBatch,omitempty"`
	Batch        int    `json:"batch,omitempty"`
	UBatch       int    `json:"ubatch,omitempty"`
	Parallel     int    `json:"parallel,omitempty"`
	CacheReuse   int    `json:"cacheReuse,omitempty"` // reuse prompt chunks after edits (agents)
	Jinja        *bool  `json:"jinja,omitempty"`
	Extra        string `json:"extra,omitempty"` // free "key = value; key = value" preset lines
}

// Config is ~/.config/omarchy-llm/config.json.
type Config struct {
	DefaultBackend string            `json:"defaultBackend"` // llama | ollama
	DefaultModel   string            `json:"defaultModel"`
	Host           string            `json:"host"`
	Port           int               `json:"port"`
	ModelsDir      string            `json:"modelsDir"`
	OllamaDir      string            `json:"ollamaDir"`
	OllamaURL      string            `json:"ollamaUrl"`
	IdleSeconds    int               `json:"idleSeconds"` // unload a model after this long without requests
	MaxLoaded      int               `json:"maxLoaded"`
	AutoTune       bool              `json:"autoTune"` // pick ctx and KV cache per model from its GGUF and the GPU
	Profile        int               `json:"profile"`  // version of the defaults (migrations)
	Defaults       Params            `json:"defaults"`
	Models         map[string]Params `json:"models"`
	VRAM           int64             `json:"-"` // set by the caller from nvidia-smi
}

// currentProfile is bumped when the tuned defaults change; older configs migrate.
const currentProfile = 1

func boolPtr(b bool) *bool { return &b }

// DefaultConfig is tuned for this PC: RTX 3060 12 GB, Ryzen 5 9600X (6C/12T);
// see autotune.go for the measurements. Ctx and the KV cache are 0/"" = auto
// per model, n-gpu-layers is left to llama.cpp's --fit.
func DefaultConfig(home string) Config {
	return Config{
		DefaultBackend: "llama",
		Host:           "127.0.0.1",
		Port:           8080,
		ModelsDir:      filepath.Join(home, "ai", "models", "gguf"),
		OllamaDir:      "/var/lib/ollama",
		OllamaURL:      "http://127.0.0.1:11434",
		IdleSeconds:    900,
		MaxLoaded:      1,
		AutoTune:       true,
		Profile:        currentProfile,
		Defaults: Params{FlashAttn: "on", Threads: 6, ThreadsBatch: 6, Batch: 2048, UBatch: 1024,
			Parallel: 1, CacheReuse: 256, Jinja: boolPtr(true)},
		Models: map[string]Params{},
	}
}

// LoadConfig reads path over the defaults; a missing file gives the defaults.
func LoadConfig(path, home string) (Config, error) {
	c := DefaultConfig(home)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.Profile = 0 // a file without "profile" predates the auto-tuner
	if err := json.Unmarshal(b, &c); err != nil {
		return DefaultConfig(home), fmt.Errorf("%s: %w", path, err)
	}
	if c.Models == nil {
		c.Models = map[string]Params{}
	}
	if c.Profile < currentProfile { // defaults written before the auto-tuner: adopt the measured ones
		d := DefaultConfig(home)
		c.Defaults.Ctx, c.Defaults.CacheK, c.Defaults.CacheV = 0, "", ""
		c.Defaults.Threads, c.Defaults.ThreadsBatch, c.Defaults.UBatch = d.Defaults.Threads, d.Defaults.ThreadsBatch, d.Defaults.UBatch
		if c.Defaults.GPULayers == 99 {
			c.Defaults.GPULayers = 0
		}
		c.Defaults.CacheReuse = d.Defaults.CacheReuse
		c.AutoTune, c.Profile = true, currentProfile
	}
	return c, c.Validate()
}

// Save writes the config atomically.
func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate rejects values llama-server would choke on.
func (c Config) Validate() error {
	if c.DefaultBackend != "llama" && c.DefaultBackend != "ollama" {
		return fmt.Errorf("defaultBackend must be llama or ollama, not %q", c.DefaultBackend)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range", c.Port)
	}
	if c.IdleSeconds < -1 {
		return fmt.Errorf("idleSeconds must be -1 (never) or more")
	}
	if c.MaxLoaded < 0 {
		return fmt.Errorf("maxLoaded must be 0 (unlimited) or more")
	}
	if err := c.Defaults.validate("defaults"); err != nil {
		return err
	}
	for name, p := range c.Models {
		if err := p.validate(name); err != nil {
			return err
		}
	}
	return nil
}

var cacheTypes = map[string]bool{"": true, "f16": true, "bf16": true, "q8_0": true, "q5_1": true, "q5_0": true, "q4_1": true, "q4_0": true, "iq4_nl": true}

func (p Params) validate(where string) error {
	switch {
	case p.Ctx < 0 || p.Ctx > 1<<20:
		return fmt.Errorf("%s: ctx %d out of range", where, p.Ctx)
	case p.GPULayers < 0 || p.GPULayers > 999:
		return fmt.Errorf("%s: gpuLayers %d out of range", where, p.GPULayers)
	case p.FlashAttn != "" && p.FlashAttn != "on" && p.FlashAttn != "off" && p.FlashAttn != "auto":
		return fmt.Errorf("%s: flashAttn must be on, off or auto", where)
	case !cacheTypes[p.CacheK] || !cacheTypes[p.CacheV]:
		return fmt.Errorf("%s: unknown KV cache type", where)
	case p.Threads < 0 || p.ThreadsBatch < 0 || p.Batch < 0 || p.UBatch < 0 || p.Parallel < 0 || p.CacheReuse < 0:
		return fmt.Errorf("%s: negative value", where)
	case strings.ContainsAny(p.Extra, "[]\n"):
		return fmt.Errorf("%s: extra must be key = value pairs separated by ;", where)
	}
	return nil
}

// Settable lists the keys `omarchy-llm set` accepts, for the panel's settings.
var Settable = []string{"defaultBackend", "defaultModel", "idleSeconds", "maxLoaded", "port", "autoTune",
	"ctx", "gpuLayers", "flashAttn", "cacheK", "cacheV", "threads", "threadsBatch", "batch", "ubatch", "parallel", "cacheReuse", "jinja", "extra"}

// Set changes one key, globally (model == "") or for one model. On error the
// config is left as it was.
func (c *Config) Set(model, key, value string) error {
	next := *c
	next.Models = map[string]Params{}
	for k, v := range c.Models {
		next.Models[k] = v
	}
	if err := next.set(model, key, value); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*c = next
	return nil
}

func (c *Config) set(model, key, value string) error {
	value = strings.TrimSpace(value)
	atoi := func() (int, error) {
		n, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("%s needs a number", key)
		}
		return n, nil
	}
	if model == "" {
		switch key {
		case "defaultBackend":
			c.DefaultBackend = value
			return c.Validate()
		case "defaultModel":
			c.DefaultModel = value
			return nil
		case "autoTune":
			switch value {
			case "on", "true", "1":
				c.AutoTune = true
			case "off", "false", "0":
				c.AutoTune = false
			default:
				return fmt.Errorf("autoTune must be on or off")
			}
			return nil
		case "idleSeconds", "maxLoaded", "port":
			n, err := atoi()
			if err != nil {
				return err
			}
			switch key {
			case "idleSeconds":
				c.IdleSeconds = n
			case "maxLoaded":
				c.MaxLoaded = n
			default:
				c.Port = n
			}
			return c.Validate()
		}
	}
	p := c.Defaults
	if model != "" {
		p = c.Models[model]
	}
	var err error
	switch key {
	case "ctx", "gpuLayers", "threads", "threadsBatch", "batch", "ubatch", "parallel", "cacheReuse":
		var n int
		if n, err = atoi(); err != nil {
			return err
		}
		*map[string]*int{"ctx": &p.Ctx, "gpuLayers": &p.GPULayers, "threads": &p.Threads, "threadsBatch": &p.ThreadsBatch,
			"batch": &p.Batch, "ubatch": &p.UBatch, "parallel": &p.Parallel, "cacheReuse": &p.CacheReuse}[key] = n
	case "flashAttn":
		p.FlashAttn = value
	case "cacheK":
		p.CacheK = autoWord(value)
	case "cacheV":
		p.CacheV = autoWord(value)
	case "jinja":
		switch value {
		case "", "default":
			p.Jinja = nil
		case "on", "true", "1":
			p.Jinja = boolPtr(true)
		case "off", "false", "0":
			p.Jinja = boolPtr(false)
		default:
			return fmt.Errorf("jinja must be on or off")
		}
	case "extra":
		p.Extra = value
	default:
		return fmt.Errorf("unknown setting %q (one of %s)", key, strings.Join(Settable, ", "))
	}
	if err := p.validate(key); err != nil {
		return err
	}
	if model == "" {
		c.Defaults = p
	} else if p == (Params{}) {
		delete(c.Models, model)
	} else {
		c.Models[model] = p
	}
	return nil
}

func autoWord(v string) string {
	if v == "auto" {
		return ""
	}
	return v
}

// Effective is what llama-server gets for a model: defaults, then the
// auto-tuner's context/KV cache where the defaults say auto, then the user's
// per-model settings.
func (c Config) Effective(m Model) Params {
	p := c.Defaults
	if c.AutoTune && m.Tuned != nil {
		if p.Ctx == 0 {
			p.Ctx = m.Tuned.Ctx
		}
		if p.CacheK == "" {
			p.CacheK = m.Tuned.Cache
		}
		if p.CacheV == "" {
			p.CacheV = m.Tuned.Cache
		}
	}
	return overlay(p, c.Models[m.ID])
}

// Merged is the Params for a model without the auto-tuner (overrides over defaults).
func (c Config) Merged(model string) Params {
	return overlay(c.Defaults, c.Models[model])
}

func overlay(p, o Params) Params {
	if o.Ctx != 0 {
		p.Ctx = o.Ctx
	}
	if o.GPULayers != 0 {
		p.GPULayers = o.GPULayers
	}
	for _, f := range []struct{ dst, src *string }{{&p.FlashAttn, &o.FlashAttn}, {&p.CacheK, &o.CacheK}, {&p.CacheV, &o.CacheV}} {
		if *f.src != "" {
			*f.dst = *f.src
		}
	}
	for _, f := range []struct{ dst, src *int }{{&p.Threads, &o.Threads}, {&p.ThreadsBatch, &o.ThreadsBatch},
		{&p.Batch, &o.Batch}, {&p.UBatch, &o.UBatch}, {&p.Parallel, &o.Parallel}, {&p.CacheReuse, &o.CacheReuse}} {
		if *f.src != 0 {
			*f.dst = *f.src
		}
	}
	if o.Jinja != nil {
		p.Jinja = o.Jinja
	}
	if o.Extra != "" {
		p.Extra = strings.Trim(p.Extra+"; "+o.Extra, "; ")
	}
	return p
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
