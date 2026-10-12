package iphone

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Imported photos reach Google Photos through gphotos-sync: one source per device folder.

func (p Paths) gphotosConfig() string {
	return filepath.Join(filepath.Dir(filepath.Dir(p.Config)), "gphotos-sync", "config.json")
}

func (p Paths) gphotosState() string { return filepath.Join(filepath.Dir(p.State), "gphotos-sync") }

// AddGooglePhotosSources adds the iPhone and iPad folders as gphotos-sync sources (albums "iPhone 2025",
// "iPad 2026"...). Existing sources are kept; the old file is saved as config.json.bak.<date>.
func AddGooglePhotosSources(p Paths) ([]string, error) {
	path := p.gphotosConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	sources, _ := cfg["sources"].([]any)
	have := map[string]bool{}
	for _, s := range sources {
		if m, ok := s.(map[string]any); ok {
			have[str(m, "id")] = true
		}
	}
	c := p.LoadConfig()
	var added []string
	for _, class := range []string{"iPhone", "iPad"} {
		id := strings.ToLower(class)
		if have[id] {
			continue
		}
		dir := Folder(c, class)
		if rel, err := filepath.Rel(p.Home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			dir = "~/" + rel
		}
		sources = append(sources, map[string]any{"id": id, "name": class, "path": dir, "album": class + " {top}", "root_album": class})
		added = append(added, id)
	}
	if len(added) == 0 {
		return nil, nil
	}
	cfg["sources"] = sources
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(path+".bak."+time.Now().Format("20060102-150405"), b, 0o644)
	return added, os.WriteFile(path, append(out, '\n'), 0o644)
}

// GooglePhotosStatus: per device folder, whether it is a gphotos-sync source and how many files are uploaded.
func GooglePhotosStatus(p Paths, cfg Config) M {
	sources := map[string]bool{}
	if b, err := os.ReadFile(p.gphotosConfig()); err == nil {
		var c struct {
			Sources []struct {
				ID string `json:"id"`
			} `json:"sources"`
		}
		if json.Unmarshal(b, &c) == nil {
			for _, s := range c.Sources {
				sources[s.ID] = true
			}
		}
	}
	counts := map[string]int{}
	if f, err := os.Open(filepath.Join(p.gphotosState(), "uploaded.jsonl")); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var e struct {
				Src string `json:"src"`
			}
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				counts[e.Src]++
			}
		}
		f.Close()
	}
	var st M
	readJSON(filepath.Join(p.gphotosState(), "status.json"), &st)
	state := ""
	if st != nil {
		state, _ = st["state"].(string)
	}
	_, installed := os.Stat(p.gphotosConfig())
	return M{"installed": installed == nil, "state": state,
		"iphone": M{"source": sources["iphone"], "uploaded": counts["iphone"]},
		"ipad":   M{"source": sources["ipad"], "uploaded": counts["ipad"]}}
}
