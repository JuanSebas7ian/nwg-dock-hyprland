// omarchy-spotify is the backend of the Spotify bar widget. Every command
// prints one JSON line (watch prints a log). Playback on this PC is spotifyd
// (Spotify Connect device "Omarchy"); everything else is the Web API with the
// user's own Spotify app token, refreshed here (no spotify-player daemon).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"omarchy/internal/jsonout"
	"omarchy/internal/spotify"
)

const usage = `usage: omarchy-spotify <command>
  setup-state                     connected? own client id? spotifyd running?
  library [--force]               recent, playlists, albums, top, liked (cached 10 min)
  tracks <playlist|album uri>     songs of a list
  search <text>                   tracks, albums, playlists, artists (10 each)
  devices                         Spotify Connect devices
  local-play                      play on this PC with whatever there is
  play-uri <uri>                  play a list or a song on this PC
  play <context|-> <track|-> [ms] play a song inside a list, optionally from a position
  pause
  device <id|name>                move playback to another device
  queue <track uri> | queue-list
  like|unlike|liked <uri>...
  playlist-add <playlist> <track>...  playlist-remove <playlist> <track>...
  playlist-create <name>
  remember <uri>                  last song seen (cold start)
  heal                            fix playback now if spotifyd broke
  watch                           follow spotifyd's log and heal automatically (service)`

type app struct {
	catalog *spotify.Catalog
	player  *spotify.Player
	healer  *spotify.Healer
	tokens  *spotify.TokenStore
	last    spotify.LastURI
	spdConf string
}

func newApp() *app {
	home, _ := os.UserHomeDir()
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(home, ".cache")
	}
	barCache := filepath.Join(cache, "spotify-bar")
	_ = os.MkdirAll(barCache, 0o700)
	tokens := &spotify.TokenStore{
		Path:     filepath.Join(home, ".config", "omarchy-spotify", "token.json"),
		Seed:     filepath.Join(home, ".cache", "spotify-player", "user_client_token.json"),
		ClientID: spotify.ClientIDFromToml(filepath.Join(home, ".config", "spotify-player", "app.toml")),
	}
	api := &spotify.Client{Tokens: tokens}
	sys := spotify.UserSystemd{}
	player := &spotify.Player{API: api, Sys: sys, Device: "Omarchy", Unit: "spotifyd.service"}
	return &app{
		catalog: &spotify.Catalog{API: api, CacheFile: filepath.Join(barCache, "library.json")},
		player:  player,
		healer: &spotify.Healer{Player: player, Sys: sys, CacheDir: filepath.Join(cache, "spotifyd"),
			Window: 10 * time.Minute, Max: 3, Log: os.Stderr},
		tokens:  tokens,
		last:    spotify.LastURI(filepath.Join(barCache, "last-uri")),
		spdConf: filepath.Join(home, ".config", "spotifyd", "spotifyd.conf"),
	}
}

func dash(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

func fail(err error) int {
	jsonout.Err(err.Error())
	return 1
}

func (a *app) run(ctx context.Context, cmd string, args []string) int {
	switch cmd {
	case "setup-state":
		_, spdErr := exec.LookPath("spotifyd")
		active := exec.Command("systemctl", "--user", "is-active", "-q", "spotifyd.service").Run() == nil
		_, confErr := os.Stat(a.spdConf)
		jsonout.Print(map[string]any{"installed": spdErr == nil && confErr == nil, "authenticated": a.tokens.Connected(),
			"ownClientId": a.tokens.ClientID != "", "daemon": active, "device": a.player.Device})
	case "library":
		force := len(args) > 0 && args[0] == "--force"
		jsonout.Print(a.catalog.Library(ctx, force))
	case "tracks":
		if len(args) < 1 {
			return fail(errors.New("tracks needs a playlist or album"))
		}
		tracks, err := a.catalog.Tracks(ctx, args[0])
		if err != nil {
			jsonout.Print(map[string]any{"ok": false, "error": err.Error(), "uri": args[0], "tracks": []any{}})
			return 1
		}
		jsonout.Print(map[string]any{"ok": true, "uri": args[0], "tracks": tracks})
	case "search":
		if len(args) < 1 {
			return fail(errors.New("search needs text"))
		}
		res, err := a.catalog.Search(ctx, args[0])
		if err != nil {
			return fail(err)
		}
		jsonout.Print(map[string]any{"ok": true, "query": args[0], "results": res})
	case "devices":
		devs, err := a.player.Devices(ctx)
		if err != nil {
			jsonout.Print(map[string]any{"devices": []any{}, "error": err.Error()})
			return 1
		}
		jsonout.Print(map[string]any{"devices": devs, "error": ""})
	case "local-play":
		via, err := a.player.Resume(ctx, a.last)
		if errors.Is(err, spotify.ErrNotStarted) {
			if res, herr := a.healer.Heal(ctx, spotify.SigStopped); herr == nil && res == "resumed" {
				err, via = nil, "healed"
			}
		}
		if err != nil {
			return fail(err)
		}
		jsonout.OK(map[string]any{"via": via})
	case "play-uri", "play":
		var req spotify.PlayRequest
		if cmd == "play-uri" {
			if len(args) < 1 {
				return fail(errors.New("play-uri needs a URI"))
			}
			kind, _, ok := spotify.ParseURI(args[0])
			if !ok {
				return fail(fmt.Errorf("not a Spotify URI: %q", args[0]))
			}
			if kind == "track" || kind == "episode" {
				req.Track = args[0]
			} else {
				req.Context = args[0]
			}
		} else {
			if len(args) < 2 {
				return fail(errors.New("play needs <context|-> <track|->"))
			}
			req.Context, req.Track = dash(args[0]), dash(args[1])
			if len(args) > 2 {
				req.PositionMs, _ = strconv.Atoi(args[2])
			}
		}
		err := a.player.Play(ctx, req)
		if errors.Is(err, spotify.ErrNotStarted) {
			// spotifyd took the order but is stuck (the crash the user saw):
			// restart it with a clean cache and try once more.
			_, _ = a.healer.CleanRecentCache()
			_ = a.player.Sys.RestartUnit(a.player.Unit)
			err = a.player.Play(ctx, req)
		}
		if err != nil {
			return fail(err)
		}
		if req.Track != "" {
			a.last.Write(req.Track)
		}
		jsonout.OK(nil)
	case "pause":
		if err := a.player.Pause(ctx); err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "device":
		if len(args) < 1 {
			return fail(errors.New("device needs a name"))
		}
		if err := a.player.Transfer(ctx, args[0], true); err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "queue":
		if len(args) < 1 {
			return fail(errors.New("queue needs a song"))
		}
		if err := a.player.Queue(ctx, args[0]); err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "queue-list":
		q, err := a.player.QueueList(ctx)
		if err != nil {
			return fail(err)
		}
		jsonout.OK(map[string]any{"queue": q})
	case "like", "unlike":
		if err := a.catalog.Like(ctx, args, cmd == "like"); err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "liked":
		m, err := a.catalog.Liked(ctx, args)
		if err != nil {
			return fail(err)
		}
		jsonout.OK(map[string]any{"liked": m})
	case "playlist-add", "playlist-remove":
		if len(args) < 2 {
			return fail(errors.New(cmd + " needs <playlist> <track>..."))
		}
		var err error
		if cmd == "playlist-add" {
			err = a.catalog.PlaylistAdd(ctx, args[0], args[1:])
		} else {
			err = a.catalog.PlaylistRemove(ctx, args[0], args[1:])
		}
		if err != nil {
			return fail(err)
		}
		jsonout.OK(nil)
	case "playlist-create":
		if len(args) < 1 {
			return fail(errors.New("playlist-create needs a name"))
		}
		e, err := a.catalog.PlaylistCreate(ctx, args[0])
		if err != nil {
			return fail(err)
		}
		jsonout.OK(map[string]any{"playlist": e})
	case "remember":
		if len(args) > 0 {
			a.last.Write(spotify.NormalizeTrackRef(args[0]))
		}
		jsonout.OK(nil)
	case "heal":
		res, err := a.healer.Heal(ctx, spotify.SigStopped)
		if err != nil {
			jsonout.Print(map[string]any{"ok": false, "result": res, "error": err.Error()})
			return 1
		}
		jsonout.OK(map[string]any{"result": res})
	default:
		fmt.Fprintln(os.Stderr, usage)
		jsonout.Err("unknown command")
		return 2
	}
	return 0
}

func (a *app) watch(ctx context.Context) int {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "journalctl", "--user", "-u", "spotifyd.service", "-f", "-n", "0", "-o", "cat")
		out, err := cmd.StdoutPipe()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "watching spotifyd")
		_ = a.healer.Watch(ctx, out, 4*time.Second)
		_ = cmd.Wait()
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	a := newApp()
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "watch" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		os.Exit(a.watch(ctx))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	os.Exit(a.run(ctx, cmd, args))
}
