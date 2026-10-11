// omarchy-llm runs local models for agents. llama.cpp (llama-server in router
// mode, user service llama-router.service) is the default: the router itself
// uses no GPU, and a model loads when an agent asks for it (or `up`) and
// unloads after idleSeconds. Ollama stays available as the other backend.
// Every command prints JSON lines for the bar widget.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"omarchy/internal/jsonout"
	"omarchy/internal/llm"
)

const usage = `usage: omarchy-llm <command>
  status                         backends, models, GPU, settings (JSON)
  sync                           regenerate the router presets and agent configs
  up [model]                     start the llama.cpp server and load a model (default model if none)
  load|unload <model>            llama.cpp: load or free one model
  restart                        restart the llama.cpp server (applies new settings)
  backend <llama|ollama>         default backend for agents
  set [--model <id>] <key> <value>   change a setting (see config)
  config                         current settings and the keys you can set
  ollama-search [text]           models on ollama.com
  ollama-pull <name>             download with Ollama (progress lines)
  ollama-rm|ollama-unload <name>
  hf-search [text]               GGUF repositories on Hugging Face
  hf-files <owner/repo>          its quantizations with sizes and GPU fit
  hf-pull <owner/repo> <quant> [--mmproj]   download into the models folder (resumes)
  hf-rm <model id>               delete a downloaded GGUF
  run <agent> [args...]          start the backend, then exec the agent (opencode, pi...)`

type app struct {
	home, cfgPath, presetPath, envPath, statePath string
	cfg                                           llm.Config
	router                                        *llm.Router
	ollama                                        *llm.Ollama
	hf                                            *llm.HF
}

func newApp() (*app, error) {
	home, _ := os.UserHomeDir()
	a := &app{
		home:       home,
		cfgPath:    filepath.Join(home, ".config", "omarchy-llm", "config.json"),
		presetPath: filepath.Join(home, ".config", "omarchy-llm", "models.ini"),
		envPath:    filepath.Join(home, ".config", "omarchy-llm", "router.env"),
		statePath:  filepath.Join(home, ".local", "state", "omarchy-llm"),
	}
	cfg, err := llm.LoadConfig(a.cfgPath, home)
	a.cfg = cfg
	a.router = &llm.Router{URL: fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port)}
	a.ollama = &llm.Ollama{URL: cfg.OllamaURL}
	a.hf = &llm.HF{Base: "https://huggingface.co", Token: os.Getenv("HF_TOKEN")}
	return a, err
}

func systemctl(args ...string) error {
	return exec.Command("systemctl", append([]string{"--user"}, args...)...).Run()
}

func (a *app) pendingFile() string { return filepath.Join(a.statePath, "restart-pending") }

// sync writes presets, the router env file and opencode's provider. The
// router is restarted only when its presets changed and nothing is loaded
// (restarting would cut an agent off mid-answer); otherwise it is marked
// pending and the panel offers "Apply".
func (a *app) sync(ctx context.Context, allowRestart bool) map[string]any {
	models := llm.Discover(a.cfg)
	if a.cfg.DefaultModel == "" && len(models) > 0 {
		a.cfg.DefaultModel = pickDefault(models)
		_ = a.cfg.Save(a.cfgPath)
	}
	changedPreset, err1 := llm.WriteIfChanged(a.presetPath, llm.RenderPreset(a.cfg, models), 0o644)
	changedEnv, err2 := llm.WriteIfChanged(a.envPath, llm.RenderEnv(a.cfg, a.presetPath), 0o644)
	changedOC, err3 := llm.SyncOpencode(filepath.Join(a.home, ".config", "opencode", "opencode.json"), a.cfg, models, time.Now())
	changedPi, err4 := llm.SyncPi(filepath.Join(a.home, ".pi", "agent"), a.cfg, models)
	res := map[string]any{"models": len(models), "presetChanged": changedPreset || changedEnv,
		"opencodeChanged": changedOC, "piChanged": changedPi}
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		res["error"] = err.Error()
	}
	_ = os.MkdirAll(a.statePath, 0o755)
	if changedPreset || changedEnv {
		_ = os.WriteFile(a.pendingFile(), []byte(time.Now().Format(time.RFC3339)), 0o644)
	}
	if _, err := os.Stat(a.pendingFile()); err == nil && allowRestart && a.router.Up(ctx) && !a.anyLoaded(ctx) {
		if systemctl("restart", "llama-router.service") == nil {
			_ = os.Remove(a.pendingFile())
			res["restarted"] = true
		}
	}
	_, pending := os.Stat(a.pendingFile())
	res["pendingRestart"] = pending == nil && a.router.Up(ctx)
	return res
}

// pickDefault prefers a coder model that fits the GPU, else the smallest.
func pickDefault(ms []llm.Model) string {
	best := ms[0]
	for _, m := range ms {
		coder := strings.Contains(m.ID, "coder")
		if coder && m.Size < 9<<30 && (!strings.Contains(best.ID, "coder") || m.Size > best.Size) {
			best = m
		}
	}
	return best.ID
}

func (a *app) anyLoaded(ctx context.Context) bool {
	ms, err := a.router.Models(ctx)
	if err != nil {
		return false
	}
	for _, m := range ms {
		if m.Status == "loaded" || m.Status == "loading" {
			return true
		}
	}
	return false
}

func (a *app) ensureRouter(ctx context.Context) error {
	if a.router.Up(ctx) {
		return nil
	}
	if err := systemctl("start", "llama-router.service"); err != nil {
		return fmt.Errorf("could not start llama-router.service (journalctl --user -u llama-router)")
	}
	for i := 0; i < 40; i++ {
		if a.router.Up(ctx) {
			_ = os.Remove(a.pendingFile())
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("llama-router.service started but does not answer on port %d", a.cfg.Port)
}

// up starts the router and loads a model, freeing Ollama's VRAM first.
func (a *app) up(ctx context.Context, model string) (string, error) {
	a.sync(ctx, true)
	if model == "" {
		model = a.cfg.DefaultModel
	}
	if model == "" {
		return "", fmt.Errorf("no models yet: download one from the Hugging Face or Ollama tab")
	}
	if err := a.ensureRouter(ctx); err != nil {
		return "", err
	}
	a.ollama.UnloadAll(ctx)
	return model, a.router.Load(ctx, model, 4*time.Minute)
}

type modelRow struct {
	llm.Model
	Status string `json:"status"`
	Fit    string `json:"fit"`
}

func (a *app) status(ctx context.Context) map[string]any {
	models := llm.Discover(a.cfg)
	gpu, hasGPU := llm.QueryGPU()
	routerUp := a.router.Up(ctx)
	states := map[string]string{}
	if routerUp {
		if ms, err := a.router.Models(ctx); err == nil {
			for _, m := range ms {
				states[m.ID] = m.Status
			}
		}
	}
	rows := make([]modelRow, 0, len(models))
	for _, m := range models {
		st := states[m.ID]
		if st == "" {
			st = "unloaded"
			if routerUp {
				st = "not in server (Apply)"
			}
		}
		rows = append(rows, modelRow{Model: m, Status: st, Fit: llm.Fit(m.Size, gpu.Total)})
	}
	_, pendErr := os.Stat(a.pendingFile())
	res := map[string]any{
		"backend": a.cfg.DefaultBackend,
		"llama": map[string]any{"running": routerUp, "endpoint": fmt.Sprintf("http://%s:%d/v1", a.cfg.Host, a.cfg.Port),
			"models": rows, "pendingRestart": routerUp && pendErr == nil, "defaultModel": a.cfg.DefaultModel},
		"ollama":    a.ollama.Status(ctx),
		"config":    a.cfg,
		"settable":  llm.Settable,
		"modelsDir": a.cfg.ModelsDir,
	}
	if hasGPU {
		res["gpu"] = gpu
	}
	var st syscall.Statfs_t
	dir := a.cfg.ModelsDir
	if _, err := os.Stat(dir); err != nil {
		dir = a.home
	}
	if syscall.Statfs(dir, &st) == nil {
		res["diskFree"] = int64(st.Bavail) * int64(st.Bsize)
	}
	return res
}

func fail(err error) int { jsonout.Err(err.Error()); return 1 }

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func (a *app) run(ctx context.Context, cmd string, args []string) int {
	switch cmd {
	case "status":
		jsonout.Print(a.status(ctx))
	case "sync":
		jsonout.Print(a.sync(ctx, arg(args, 0) != "--no-restart"))
	case "up":
		m, err := a.up(ctx, arg(args, 0))
		if err != nil {
			return fail(err)
		}
		jsonout.OK(map[string]any{"model": m, "endpoint": fmt.Sprintf("http://%s:%d/v1", a.cfg.Host, a.cfg.Port)})
	case "load":
		if arg(args, 0) == "" {
			return fail(errors.New("load needs a model id"))
		}
		if _, err := a.up(ctx, args[0]); err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "unload":
		if err := a.router.Unload(ctx, arg(args, 0)); err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "restart":
		a.sync(ctx, false)
		if err := systemctl("restart", "llama-router.service"); err != nil {
			return fail(errors.New("could not restart llama-router.service"))
		}
		_ = os.Remove(a.pendingFile())
		jsonout.OK(nil)
	case "stop":
		if err := systemctl("stop", "llama-router.service"); err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "backend":
		if err := a.cfg.Set("", "defaultBackend", arg(args, 0)); err != nil {
			return fail(err)
		}
		if err := a.cfg.Save(a.cfgPath); err != nil {
			return fail(err)
		}
		jsonout.Print(a.sync(ctx, true))
	case "set":
		model := ""
		if arg(args, 0) == "--model" {
			model, args = arg(args, 1), args[min(2, len(args)):]
		}
		if len(args) < 2 {
			return fail(errors.New("set needs <key> <value>"))
		}
		if err := a.cfg.Set(model, args[0], strings.Join(args[1:], " ")); err != nil {
			return fail(err)
		}
		if err := a.cfg.Save(a.cfgPath); err != nil {
			return fail(err)
		}
		jsonout.Print(a.sync(ctx, true))
	case "config":
		jsonout.Print(map[string]any{"config": a.cfg, "settable": llm.Settable, "path": a.cfgPath})
	case "ollama-search":
		res, err := llm.SearchOllama(ctx, &http.Client{Timeout: 10 * time.Second}, "https://ollama.com", arg(args, 0))
		if err != nil {
			jsonout.Print(map[string]any{"results": []any{}, "error": err.Error()})
			return 1
		}
		jsonout.Print(map[string]any{"results": res})
	case "ollama-pull":
		err := a.ollama.Pull(ctx, strings.Join(args, " "), 500*time.Millisecond, func(p llm.PullProgress) { jsonout.Print(p) })
		if err != nil {
			jsonout.Print(llm.PullProgress{Model: strings.Join(args, " "), Error: err.Error()})
			return 1
		}
		a.sync(ctx, true)
	case "ollama-rm":
		if err := a.ollama.Delete(ctx, arg(args, 0)); err != nil {
			return fail(err)
		}
		a.sync(ctx, true)
		jsonout.OK(nil)
	case "ollama-unload":
		if err := a.ollama.Unload(ctx, arg(args, 0)); err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "hf-search":
		res, err := a.hf.Search(ctx, arg(args, 0))
		if err != nil {
			jsonout.Print(map[string]any{"results": []any{}, "error": err.Error()})
			return 1
		}
		jsonout.Print(map[string]any{"results": res})
	case "hf-files":
		gpu, _ := llm.QueryGPU()
		files, err := a.hf.Files(ctx, arg(args, 0), gpu.Total)
		if err != nil {
			jsonout.Print(map[string]any{"ok": false, "files": []any{}, "error": err.Error()})
			return 1
		}
		jsonout.OK(map[string]any{"repo": arg(args, 0), "files": files})
	case "hf-pull":
		return a.hfPull(ctx, args)
	case "hf-rm":
		return a.hfRemove(ctx, arg(args, 0))
	default:
		fmt.Fprintln(os.Stderr, usage)
		jsonout.Err("unknown command")
		return 2
	}
	return 0
}

func (a *app) hfPull(ctx context.Context, args []string) int {
	repo, quant := arg(args, 0), strings.ToUpper(arg(args, 1))
	withMMProj := len(args) > 2 && args[2] == "--mmproj"
	gpu, _ := llm.QueryGPU()
	files, err := a.hf.Files(ctx, repo, gpu.Total)
	if err != nil {
		jsonout.Print(llm.DownloadProgress{Model: repo, Status: "error: " + err.Error()})
		return fail(err)
	}
	var pick *llm.HFFile
	for i := range files {
		if strings.ToUpper(files[i].Quant) == quant {
			pick = &files[i]
		}
	}
	if pick == nil {
		names := make([]string, 0, len(files))
		for _, f := range files {
			names = append(names, f.Quant)
		}
		return fail(fmt.Errorf("%s has no %s file; it has: %s", repo, quant, strings.Join(names, ", ")))
	}
	want := append([]string{}, pick.Files...)
	if withMMProj && pick.MMProj != "" {
		want = append(want, pick.MMProj)
	}
	if st := a.diskFree(); st > 0 && st < pick.Size+(2<<30) {
		return fail(fmt.Errorf("not enough disk space: %d GB free, the model needs %d GB", st>>30, (pick.Size>>30)+1))
	}
	_, err = a.hf.Download(ctx, repo, want, a.cfg.ModelsDir, 500*time.Millisecond, func(p llm.DownloadProgress) { jsonout.Print(p) })
	if err != nil {
		return fail(err)
	}
	a.sync(ctx, true)
	return 0
}

func (a *app) diskFree() int64 {
	var st syscall.Statfs_t
	dir := a.cfg.ModelsDir
	_ = os.MkdirAll(dir, 0o755)
	if syscall.Statfs(dir, &st) != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// hfRemove deletes a downloaded GGUF (and its other split parts); Ollama
// models are removed with ollama-rm instead.
func (a *app) hfRemove(ctx context.Context, id string) int {
	for _, m := range llm.DiscoverGGUF(a.cfg.ModelsDir) {
		if m.ID != id {
			continue
		}
		_ = a.router.Unload(ctx, id)
		base := m.Path
		if i := strings.Index(base, "-00001-of-"); i >= 0 {
			base = base[:i]
		}
		matches, _ := filepath.Glob(base + "*")
		for _, f := range matches {
			if strings.HasSuffix(f, ".gguf") || strings.HasSuffix(f, ".part") {
				_ = os.Remove(f)
			}
		}
		if dir := filepath.Dir(m.Path); len(llm.DiscoverGGUF(dir)) == 0 {
			_ = os.RemoveAll(dir) // only mmproj left
		}
		a.sync(ctx, true)
		jsonout.OK(nil)
		return 0
	}
	return fail(fmt.Errorf("no downloaded GGUF called %q (Ollama models: use ollama-rm)", id))
}

// runAgent prepares the backend and execs the agent in its place.
func (a *app) runAgent(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return fail(errors.New("run needs an agent command"))
	}
	if a.cfg.DefaultBackend == "llama" {
		fmt.Fprintf(os.Stderr, "llama.cpp: loading %s…\n", a.cfg.DefaultModel)
		if m, err := a.up(ctx, ""); err != nil {
			fmt.Fprintf(os.Stderr, "llama.cpp could not start (%v); the agent starts anyway\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "llama.cpp: %s ready on http://%s:%d/v1\n", m, a.cfg.Host, a.cfg.Port)
		}
	}
	bin, err := exec.LookPath(args[0])
	if err != nil {
		return fail(fmt.Errorf("%s is not installed", args[0]))
	}
	return fail(syscall.Exec(bin, args, os.Environ()))
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	a, cfgErr := newApp()
	if cfgErr != nil {
		fmt.Fprintln(os.Stderr, "omarchy-llm: using defaults:", cfgErr)
	}
	cmd, args := os.Args[1], os.Args[2:]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cmd == "run" {
		os.Exit(a.runAgent(ctx, args))
	}
	var cancel context.CancelFunc = func() {}
	if !strings.HasSuffix(cmd, "-pull") && cmd != "up" && cmd != "load" {
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	}
	defer cancel()
	os.Exit(a.run(ctx, cmd, args))
}
