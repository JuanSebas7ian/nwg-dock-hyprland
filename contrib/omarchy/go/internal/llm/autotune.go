package llm

import (
	"fmt"
	"os"
)

// Measured on this PC (RTX 3060 12 GB, Ryzen 5 9600X, llama.cpp b10809), 2026-10-11:
//   - flash attention: 2.5x faster prompt processing at 16k context (1286 vs 509 t/s);
//   - KV cache f16 generates 26 % faster than q8_0 at 16k depth (48 vs 38 t/s):
//     q8_0 only when f16 would not fit the context;
//   - micro-batch 1024: +3 % prompt speed over 512, more does not help;
//   - 6 threads (physical cores) beat 12 for the CPU part: 4.6 vs 2.5 t/s on a 27B
//     that spills to RAM, prompt 322 vs 274 t/s;
//   - llama.cpp's --fit places layers and MoE experts by itself as well as the best
//     manual --n-cpu-moe (36.8 vs 37.2 t/s on gemma4-26b): never force n-gpu-layers.

// Reserved VRAM outside the model: desktop (~0.6 GB), dictation model (1.55 GB) and
// llama.cpp's compute buffers.
const tuneReserve = 3 << 30

// KVProfile is how much KV cache a model needs: Full bytes per token (f16) for
// full-attention layers, plus a fixed amount for sliding-window layers.
type KVProfile struct {
	PerToken int64 `json:"perToken"`
	SWAFixed int64 `json:"swaFixed"`
	Native   int   `json:"native"`  // training context
	Experts  int   `json:"experts"` // MoE experts (0 = dense)
}

// Profile reads the KV cache shape from a GGUF header.
func Profile(g GGUFInfo) KVProfile {
	layers := int(g.Int("block_count", 0))
	p := KVProfile{Native: int(g.Int("context_length", 32768)), Experts: int(g.Int("expert_count", 0))}
	if layers == 0 {
		p.PerToken = 128 << 10 // unknown architecture: assume a 7-8B model
		return p
	}
	heads := g.Int("attention.head_count", 32)
	embd := g.Int("embedding_length", 4096)
	kLen := g.Int("attention.key_length", embd/max(heads, 1))
	vLen := g.Int("attention.value_length", kLen)
	kSwa := g.Int("attention.key_length_swa", kLen)
	vSwa := g.Int("attention.value_length_swa", vLen)
	window := g.Int("attention.sliding_window", 0)
	pattern := g.Arrays["attention.sliding_window_pattern"]
	kvArr := g.Arrays["attention.head_count_kv"]
	kvScalar := g.Int("attention.head_count_kv", heads)
	interval := g.Int("full_attention_interval", 0) // hybrid models (Qwen3.5/3.6): other layers are recurrent
	if lora := g.Int("attention.kv_lora_rank", 0); lora > 0 { // MLA (DeepSeek): compressed latent cache
		rope := g.Int("rope.dimension_count", 64)
		p.PerToken = int64(layers) * (lora + rope) * 2
		return p
	}
	for l := 0; l < layers; l++ {
		kvh := kvScalar
		if l < len(kvArr) {
			kvh = kvArr[l]
		}
		if kvh == 0 {
			continue
		}
		if interval > 0 && (l+1)%int(interval) != 0 {
			continue
		}
		if window > 0 && l < len(pattern) && pattern[l] == 1 {
			p.SWAFixed += kvh * (kSwa + vSwa) * 2 * window
			continue
		}
		p.PerToken += kvh * (kLen + vLen) * 2
	}
	return p
}

// Tuned is the auto-tuner's choice for one model.
type Tuned struct {
	Ctx     int    `json:"ctx"`
	Cache   string `json:"cache"` // f16 | q8_0
	Fit     string `json:"fit"`   // gpu | partial
	KVBytes int64  `json:"kvBytes"`
	Why     string `json:"why"`
}

func kvBytes(p KVProfile, ctx int, cache string) int64 {
	b := p.PerToken*int64(ctx) + p.SWAFixed
	if cache == "q8_0" {
		b = b * 17 / 32 // 8.5 bits per value
	}
	return b
}

// Tune picks the context and KV cache type for a model on a GPU with vram bytes.
func Tune(weights int64, p KVProfile, vram int64) Tuned {
	budget := vram - tuneReserve
	maxCtx := min(p.Native, 65536)
	if maxCtx <= 0 {
		maxCtx = 32768
	}
	cands := []int{}
	for _, c := range []int{65536, 49152, 32768, 24576, 16384, 8192} {
		if c <= maxCtx {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 || cands[0] != maxCtx {
		cands = append([]int{maxCtx}, cands...)
	}
	try := func(cache string, minCtx int) (Tuned, bool) {
		for _, c := range cands {
			if c < minCtx && c != maxCtx {
				continue
			}
			if kv := kvBytes(p, c, cache); weights+kv <= budget {
				return Tuned{Ctx: c, Cache: cache, Fit: "gpu", KVBytes: kv}, true
			}
		}
		return Tuned{}, false
	}
	if vram > 0 {
		if t, ok := try("f16", min(32768, maxCtx)); ok {
			t.Why = fmt.Sprintf("fits the GPU with %dk context and the fastest KV cache (f16)", t.Ctx/1024)
			return t
		}
		if t, ok := try("q8_0", min(16384, maxCtx)); ok {
			t.Why = fmt.Sprintf("fits the GPU with %dk context thanks to the q8_0 KV cache", t.Ctx/1024)
			return t
		}
	}
	// Does not fit: llama.cpp --fit moves layers (dense) or experts (MoE) to RAM.
	ctx := min(16384, maxCtx) // a dense model needs every MB of VRAM for layers
	why := "bigger than the GPU: llama.cpp puts what fits on the GPU, the rest in RAM (slower)"
	if p.Experts > 0 {
		ctx = min(32768, maxCtx) // MoE: experts go to RAM, attention stays fast on the GPU
		why = "MoE bigger than the GPU: experts go to RAM, attention stays on the GPU"
	}
	return Tuned{Ctx: ctx, Cache: "q8_0", Fit: "partial", KVBytes: kvBytes(p, ctx, "q8_0"), Why: why}
}

// ModelProfile reads a model's GGUF and adds its projector to the weights.
func ModelProfile(m Model) (int64, KVProfile) {
	g, err := ReadGGUFInfo(m.Path)
	weights := m.Size
	if m.MMProj != "" {
		if st, err := os.Stat(m.MMProj); err == nil {
			weights += st.Size()
		}
	}
	if err != nil {
		return weights, KVProfile{PerToken: 128 << 10, Native: 32768}
	}
	return weights, Profile(g)
}
