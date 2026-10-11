package llm

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Model is a GGUF that llama.cpp can serve.
type Model struct {
	Arch   string `json:"arch,omitempty"`
	ID     string `json:"id"`     // name agents use (no ':': the router upper-cases after it)
	Name   string `json:"name"`   // human name (Ollama tag or file name)
	Source string `json:"source"` // ollama | hf
	Path   string `json:"path"`
	MMProj string `json:"mmproj,omitempty"`
	Size   int64  `json:"size"`
	Quant  string `json:"quant,omitempty"`
}

var idUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

// ModelID turns any name into a router-safe id: lower case, ':' and '/' to '-'.
func ModelID(name string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	id = strings.TrimSuffix(id, ":latest")
	id = idUnsafe.ReplaceAllString(id, "-")
	return strings.Trim(id, "-.")
}

var quantRe = regexp.MustCompile(`(?i)(?:^|[-_.])((?:UD-)?I?Q[1-8](?:_[0-9A-Z]+)*|BF16|F16|F32|MXFP4(?:_MOE)?)(?:[-_.]|$)`)

// QuantOf finds the quantization in a file or tag name ("" when none).
func QuantOf(name string) string {
	ms := quantRe.FindAllStringSubmatch(filepath.Base(name), -1)
	if len(ms) == 0 {
		return ""
	}
	return strings.ToUpper(ms[len(ms)-1][1])
}

type ollamaManifest struct {
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"layers"`
}

// ollamaName rebuilds "model:tag" from manifests/<registry>/<namespace>/<model>/<tag>.
func ollamaName(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 4 {
		return ""
	}
	registry, tag := parts[0], parts[len(parts)-1]
	repo := parts[1 : len(parts)-1]
	switch {
	case registry == "registry.ollama.ai" && repo[0] == "library":
		return strings.Join(repo[1:], "/") + ":" + tag
	case registry == "registry.ollama.ai":
		return strings.Join(repo, "/") + ":" + tag
	default:
		return registry + "/" + strings.Join(repo, "/") + ":" + tag
	}
}

// ollamaID is a short id: the last path element plus the tag.
func ollamaID(name string) string {
	base := name
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return ModelID(base)
}

// DiscoverOllama lists Ollama's downloaded models whose weights are a GGUF
// blob (embedding models are skipped: agents can't chat with them).
func DiscoverOllama(dir string) []Model {
	root := filepath.Join(dir, "manifests")
	var out []Model
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		name := ollamaName(rel)
		if name == "" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var m ollamaManifest
		if json.Unmarshal(b, &m) != nil {
			return nil
		}
		mod := Model{ID: ollamaID(name), Name: name, Source: "ollama"}
		for _, l := range m.Layers {
			blob := filepath.Join(dir, "blobs", strings.Replace(l.Digest, ":", "-", 1))
			switch {
			case strings.HasSuffix(l.MediaType, ".model"):
				mod.Path, mod.Size = blob, l.Size
			case strings.HasSuffix(l.MediaType, ".projector"):
				mod.MMProj = blob
			}
		}
		if mod.Path == "" || strings.Contains(name, "embed") || !isGGUF(mod.Path) {
			return nil
		}
		if i := strings.LastIndex(name, ":"); i >= 0 {
			mod.Quant = QuantOf(name[i+1:])
		}
		out = append(out, mod)
		return nil
	})
	return out
}

func isGGUF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	magic := make([]byte, 4)
	n, _ := f.Read(magic)
	return n == 4 && string(magic) == "GGUF"
}

var partRe = regexp.MustCompile(`-(\d{5})-of-(\d{5})\.gguf$`)

// DiscoverGGUF lists *.gguf under dir (Hugging Face downloads). Multi-part
// models count once (their first part); mmproj files attach to the model in
// the same folder.
func DiscoverGGUF(dir string) []Model {
	byDir := map[string][]string{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".gguf") {
			byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], path)
		}
		return nil
	})
	var out []Model
	for _, d := range sortedKeys(byDir) {
		files := byDir[d]
		sort.Strings(files)
		mmproj := ""
		for _, f := range files {
			if strings.Contains(strings.ToLower(filepath.Base(f)), "mmproj") {
				mmproj = f
			}
		}
		for _, f := range files {
			base := filepath.Base(f)
			if strings.Contains(strings.ToLower(base), "mmproj") {
				continue
			}
			size := fileSize(f)
			if m := partRe.FindStringSubmatch(base); m != nil {
				if m[1] != "00001" {
					continue
				}
				for _, g := range files {
					if g != f && strings.HasPrefix(g, strings.TrimSuffix(f, m[0])) {
						size += fileSize(g)
					}
				}
				base = strings.TrimSuffix(base, m[0]) + ".gguf"
			}
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			out = append(out, Model{ID: ModelID(stem), Name: stem, Source: "hf", Path: f, MMProj: mmproj,
				Size: size, Quant: QuantOf(base)})
		}
	}
	return out
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// Discover lists every servable model with unique ids (Hugging Face files win
// over an Ollama copy of the same name; later duplicates get a suffix).
func Discover(c Config) []Model {
	all := append(DiscoverGGUF(c.ModelsDir), DiscoverOllama(c.OllamaDir)...)
	for i := range all {
		if info, err := ReadGGUFInfo(all[i].Path); err == nil {
			all[i].Arch = info.Arch
			if all[i].Quant == "" {
				all[i].Quant = info.Quant
			}
		}
	}
	seen := map[string]int{}
	out := make([]Model, 0, len(all))
	for _, m := range all {
		if n := seen[m.ID]; n > 0 {
			seen[m.ID]++
			m.ID = fmt.Sprintf("%s-%s", m.ID, m.Source)
		} else {
			seen[m.ID] = 1
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
