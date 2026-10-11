package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// HF is the Hugging Face Hub API.
type HF struct {
	Base  string // https://huggingface.co
	HTTP  *http.Client
	Token string // optional, for gated repos (HF_TOKEN)
}

func (h *HF) get(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.Base+path, nil)
	req.Header.Set("User-Agent", "omarchy-llm")
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	hc := h.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("huggingface.co unreachable: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("that repository is gated: accept its license on huggingface.co and set HF_TOKEN")
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("repository not found on Hugging Face")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("Hugging Face answered %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// NormalizeRepo accepts "owner/repo", "hf.co/owner/repo[:quant]" or a URL.
func NormalizeRepo(s string) (string, error) {
	r := strings.TrimSpace(s)
	r = hfURLRe.ReplaceAllString(r, "")
	r = strings.TrimPrefix(r, "hf.co/")
	r = strings.SplitN(r, "/tree/", 2)[0]
	r = strings.SplitN(r, "/blob/", 2)[0]
	r = strings.SplitN(r, ":", 2)[0]
	r = strings.TrimSuffix(r, "/")
	if !repoRe.MatchString(r) {
		return "", fmt.Errorf("%q is not a Hugging Face repository (owner/name)", s)
	}
	return r, nil
}

func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.Itoa(n/1000) + "k"
	case n > 0:
		return strconv.Itoa(n)
	}
	return ""
}

// Search finds GGUF repositories, most downloaded first.
func (h *HF) Search(ctx context.Context, q string) ([]CatalogEntry, error) {
	params := url.Values{"filter": {"gguf"}, "sort": {"downloads"}, "direction": {"-1"}, "limit": {"25"}}
	if q = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), "hf.co/")); q != "" {
		params.Set("search", q)
	} else {
		params.Set("pipeline_tag", "text-generation")
	}
	var res []struct {
		ID        string   `json:"id"`
		Downloads int      `json:"downloads"`
		Likes     int      `json:"likes"`
		Tags      []string `json:"tags"`
		Pipeline  string   `json:"pipeline_tag"`
		Modified  string   `json:"lastModified"`
	}
	if err := h.get(ctx, "/api/models?"+params.Encode(), &res); err != nil {
		return nil, err
	}
	out := []CatalogEntry{}
	for _, m := range res {
		caps := []string{}
		for _, t := range m.Tags {
			switch {
			case strings.Contains(t, "code"):
				caps = appendOnce(caps, "code")
			case t == "conversational":
				caps = appendOnce(caps, "chat")
			case strings.Contains(t, "vision") || t == "image-text-to-text":
				caps = appendOnce(caps, "vision")
			case strings.Contains(t, "reasoning") || strings.Contains(t, "thinking"):
				caps = appendOnce(caps, "thinking")
			case t == "tool-use" || strings.Contains(t, "function-calling"):
				caps = appendOnce(caps, "tools")
			}
		}
		desc := m.Pipeline
		if m.Likes > 0 {
			desc = strings.Trim(desc+" · "+humanCount(m.Likes)+" likes", " ·")
		}
		e := CatalogEntry{Name: m.ID, Description: desc, Capabilities: caps, Sizes: []string{}, Pulls: humanCount(m.Downloads), Source: "hf"}
		if len(m.Modified) >= 10 {
			e.Updated = m.Modified[:10]
		}
		out = append(out, e)
	}
	return out, nil
}

func appendOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// HFFile is one downloadable quantization of a repository.
type HFFile struct {
	Quant  string   `json:"quant"`
	Files  []string `json:"files"` // several for split models
	Size   int64    `json:"size"`
	Fit    string   `json:"fit"` // gpu | partial | cpu
	MMProj string   `json:"mmproj,omitempty"`
}

// Fit says where a model of `size` bytes runs with `vram` bytes of GPU memory.
// Besides the weights it needs the KV cache (~25 % of the weights at 32k
// context, q8_0) and ~2 GB for the desktop and the dictation model.
func Fit(size, vram int64) string {
	const reserved = 2 << 30
	switch {
	case vram <= 0:
		return "cpu"
	case size+size/4+reserved <= vram:
		return "gpu"
	case size <= vram*2:
		return "partial"
	}
	return "cpu"
}

// Files lists a repository's GGUF quantizations with real sizes, smallest
// first; split files (-00001-of-00003) are grouped.
func (h *HF) Files(ctx context.Context, repo string, vram int64) ([]HFFile, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return nil, err
	}
	var tree []struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
		LFS  *struct {
			Size int64 `json:"size"`
		} `json:"lfs"`
	}
	if err := h.get(ctx, "/api/models/"+repo+"/tree/main?recursive=true", &tree); err != nil {
		return nil, err
	}
	groups := map[string]*HFFile{}
	mmproj := ""
	for _, f := range tree {
		if f.Type != "file" || !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			continue
		}
		size := f.Size
		if f.LFS != nil && f.LFS.Size > 0 {
			size = f.LFS.Size
		}
		base := filepath.Base(f.Path)
		if strings.Contains(strings.ToLower(base), "mmproj") {
			if mmproj == "" || strings.Contains(strings.ToLower(base), "f16") {
				mmproj = f.Path
			}
			continue
		}
		key := f.Path
		if m := partRe.FindStringSubmatch(base); m != nil {
			key = strings.TrimSuffix(f.Path, m[0])
		}
		g := groups[key]
		if g == nil {
			q := QuantOf(base)
			if q == "" {
				q = strings.TrimSuffix(base, ".gguf")
			}
			g = &HFFile{Quant: q}
			groups[key] = g
		}
		g.Files = append(g.Files, f.Path)
		g.Size += size
	}
	out := make([]HFFile, 0, len(groups))
	for _, g := range groups {
		sort.Strings(g.Files)
		g.Fit = Fit(g.Size, vram)
		g.MMProj = mmproj
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size < out[j].Size })
	return out, nil
}

// DownloadProgress is one line of a Hugging Face download.
type DownloadProgress struct {
	Model     string `json:"model"`
	File      string `json:"file,omitempty"`
	Status    string `json:"status,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Path      string `json:"path,omitempty"`
}

// RepoDir is where a repository's files are stored under the models folder.
func RepoDir(modelsDir, repo string) string {
	return filepath.Join(modelsDir, strings.ReplaceAll(repo, "/", "__"))
}

// Download fetches files of repo into modelsDir, resuming .part files, and
// reports progress at most every interval. Returns the first file's path.
func (h *HF) Download(ctx context.Context, repo string, files []string, modelsDir string, interval time.Duration, progress func(DownloadProgress)) (string, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("pick a file to download")
	}
	dir := RepoDir(modelsDir, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	hc := h.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	first := ""
	for _, f := range files {
		if strings.Contains(f, "..") || strings.HasPrefix(f, "/") || !strings.HasSuffix(strings.ToLower(f), ".gguf") {
			return "", fmt.Errorf("refusing to download %q", f)
		}
		dst := filepath.Join(dir, filepath.Base(f))
		if first == "" {
			first = dst
		}
		if _, err := os.Stat(dst); err == nil {
			continue // already complete
		}
		if err := h.downloadOne(ctx, hc, repo, f, dst, interval, progress); err != nil {
			return "", err
		}
	}
	progress(DownloadProgress{Model: repo, Done: true, Path: first})
	return first, nil
}

func (h *HF) downloadOne(ctx context.Context, hc *http.Client, repo, file, dst string, interval time.Duration, progress func(DownloadProgress)) error {
	part := dst + ".part"
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}
	u := h.Base + "/" + repo + "/resolve/main/" + (&url.URL{Path: file}).EscapedPath()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "omarchy-llm")
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		have = 0
		flags |= os.O_TRUNC
	case http.StatusRequestedRangeNotSatisfiable:
		return os.Rename(part, dst) // the .part was already complete
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("that repository is gated: accept its license on huggingface.co and set HF_TOKEN")
	default:
		return fmt.Errorf("Hugging Face answered %d for %s", resp.StatusCode, file)
	}
	total := have + resp.ContentLength
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	last := time.Time{}
	done := have
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return fmt.Errorf("writing %s: %w", part, werr)
			}
			done += int64(n)
			if time.Since(last) >= interval {
				last = time.Now()
				progress(DownloadProgress{Model: repo, File: filepath.Base(file), Status: "downloading", Total: total, Completed: done})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return fmt.Errorf("download interrupted at %d MB (run it again to resume): %w", done>>20, rerr)
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	if resp.ContentLength > 0 && done != total {
		return fmt.Errorf("download incomplete (%d of %d bytes); run it again to resume", done, total)
	}
	return os.Rename(part, dst)
}
