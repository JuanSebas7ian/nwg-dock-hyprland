package spotify

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Signal is what a spotifyd log line means for playback.
type Signal int

const (
	SigNone Signal = iota
	// SigKeyTimeout: Spotify did not send the song's decryption key; spotifyd
	// then plays (and caches) undecrypted garbage and stops. Seen 16 times on
	// 2026-10-09 ("Audio key response timeout").
	SigKeyTimeout
	// SigDecode: a cached file is corrupt ("Symphonia Decoder Error").
	SigDecode
	// SigStopped: the session fell over ("unexpected shutdown").
	SigStopped
)

func (s Signal) String() string {
	return [...]string{"none", "audio key timeout", "corrupt audio file", "session stopped"}[s]
}

// Classify maps one spotifyd log line to a Signal.
func Classify(line string) Signal {
	switch {
	case strings.Contains(line, "Audio key response timeout"),
		strings.Contains(line, "Unable to load key, continuing without decryption"):
		return SigKeyTimeout
	case strings.Contains(line, "Unable to read audio file"),
		strings.Contains(line, "Symphonia Decoder Error"):
		return SigDecode
	case strings.Contains(line, "unexpected shutdown"):
		return SigStopped
	}
	return SigNone
}

var cacheShardRe = regexp.MustCompile(`^[0-9a-f]{2}$`)

// CleanRecentCache deletes audio files spotifyd wrote in the last `within`
// (they are the undecrypted or truncated ones). Only files inside its
// two-hex-digit shard folders are touched; credentials stay.
func CleanRecentCache(dir string, within time.Duration, now time.Time) (int, error) {
	shards, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, sh := range shards {
		if !sh.IsDir() || !cacheShardRe.MatchString(sh.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, sh.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			info, err := f.Info()
			if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) > within {
				continue
			}
			if os.Remove(filepath.Join(dir, sh.Name(), f.Name())) == nil {
				n++
			}
		}
	}
	return n, nil
}

// Healer brings playback back after spotifyd breaks.
type Healer struct {
	Player   *Player
	Sys      System
	CacheDir string
	Window   time.Duration // rate-limit window (10 min)
	Max      int           // heals per window (3)
	Now      func() time.Time
	Log      io.Writer

	history []time.Time
}

func (h *Healer) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Healer) logf(format string, a ...any) {
	if h.Log != nil {
		fmt.Fprintf(h.Log, format+"\n", a...)
	}
}

// Allowed reports whether another heal fits in the rate limit.
func (h *Healer) Allowed() bool {
	cut := h.now().Add(-h.Window)
	kept := h.history[:0]
	for _, t := range h.history {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	h.history = kept
	return len(h.history) < h.Max
}

// Healthy reports whether this PC is playing and the song is advancing.
func (h *Healer) Healthy(ctx context.Context) (State, bool) {
	s1, ok, err := h.Player.State(ctx)
	if err != nil || !ok || !s1.Playing || !strings.EqualFold(s1.DeviceName, h.Player.Device) {
		return s1, false
	}
	h.Player.sleep(2 * time.Second)
	s2, ok, err := h.Player.State(ctx)
	if err != nil || !ok || !s2.Playing {
		return s1, false
	}
	return s2, s2.TrackURI != s1.TrackURI || s2.ProgressMs > s1.ProgressMs
}

// Heal restarts spotifyd with a clean cache and resumes the song where it was.
// It does nothing when playback is fine, or when this PC was not playing.
func (h *Healer) Heal(ctx context.Context, why Signal) (string, error) {
	state, ok := h.Healthy(ctx)
	if ok {
		return "healthy", nil
	}
	if state.TrackURI == "" || !strings.EqualFold(state.DeviceName, h.Player.Device) {
		return "not ours", nil // another device, or nothing loaded: not our business
	}
	if !h.Allowed() {
		h.Sys.Notify("Spotify is not answering", "Spotify's servers keep failing to send songs. Try again in a few minutes.")
		return "rate limited", fmt.Errorf("too many restarts, giving up for now")
	}
	h.history = append(h.history, h.now())
	n, _ := h.CleanRecentCache()
	h.logf("heal: %s on %q at %d ms; removed %d cached files; restarting %s", why, state.TrackName, state.ProgressMs, n, h.Player.Unit)
	if err := h.Sys.RestartUnit(h.Player.Unit); err != nil {
		return "restart failed", err
	}
	req := PlayRequest{Context: state.ContextURI, Track: state.TrackURI, PositionMs: state.ProgressMs}
	if k, _, ok := ParseURI(req.Context); !ok || k == "track" {
		req.Context = ""
	}
	if err := h.Player.Play(ctx, req); err != nil {
		h.Sys.Notify("Spotify stopped", "Could not resume "+state.TrackName+": "+err.Error())
		return "resume failed", err
	}
	return "resumed", nil
}

// CleanRecentCache removes the last 10 minutes of cached audio.
func (h *Healer) CleanRecentCache() (int, error) {
	if h.CacheDir == "" {
		return 0, nil
	}
	return CleanRecentCache(h.CacheDir, 10*time.Minute, h.now())
}

// Watch reads spotifyd's log and heals after a failure signal, waiting
// `settle` so a burst of errors makes one heal, and letting spotifyd retry on
// its own first.
func (h *Healer) Watch(ctx context.Context, r io.Reader, settle time.Duration) error {
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var pending Signal
	var timer <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return io.EOF
			}
			if sig := Classify(line); sig != SigNone && pending == SigNone {
				pending = sig
				timer = time.After(settle)
			}
		case <-timer:
			res, err := h.Heal(ctx, pending)
			h.logf("heal result: %s %v", res, err)
			pending, timer = SigNone, nil
		}
	}
}
