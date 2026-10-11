package spotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSpotify is a tiny Web API: routes answer "METHOD /path" with a status
// and a JSON body, and every request is recorded.
type fakeSpotify struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]func(r *http.Request, body string) (int, any)
	calls  []string
	bodies map[string][]string
	srv    *httptest.Server
}

func newFake(t *testing.T) *fakeSpotify {
	f := &fakeSpotify{t: t, routes: map[string]func(*http.Request, string) (int, any){}, bodies: map[string][]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.calls = append(f.calls, key+"?"+r.URL.RawQuery)
		f.bodies[key] = append(f.bodies[key], string(b))
		h := f.routes[key]
		f.mu.Unlock()
		if h == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"status":404,"message":"no route ` + key + `"}}`))
			return
		}
		status, out := h(r, string(b))
		if s, ok := out.(string); ok {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(s))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if out != nil {
			_ = json.NewEncoder(w).Encode(out)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSpotify) on(key string, h func(r *http.Request, body string) (int, any)) {
	f.mu.Lock()
	f.routes[key] = h
	f.mu.Unlock()
}

func (f *fakeSpotify) json(key string, status int, out any) {
	f.on(key, func(*http.Request, string) (int, any) { return status, out })
}

func (f *fakeSpotify) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeSpotify) lastBody(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bodies[key]
	if len(b) == 0 {
		return ""
	}
	return b[len(b)-1]
}

// staticTokens always hands out "tok"; Invalidate counts.
type staticTokens struct{ invalidated int }

func (s *staticTokens) Access(context.Context) (string, error) { return "tok", nil }
func (s *staticTokens) Invalidate()                            { s.invalidated++ }

func (f *fakeSpotify) client() *Client {
	return &Client{Base: f.srv.URL, Tokens: &staticTokens{}, Sleep: func(time.Duration) {}}
}

// fakeSys records unit starts/restarts and notifications.
type fakeSys struct {
	mu        sync.Mutex
	started   []string
	restarted []string
	notes     []string
	onStart   func()
	onRestart func()
}

func (s *fakeSys) StartUnit(n string) error {
	s.mu.Lock()
	s.started = append(s.started, n)
	cb := s.onStart
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}
func (s *fakeSys) RestartUnit(n string) error {
	s.mu.Lock()
	s.restarted = append(s.restarted, n)
	cb := s.onRestart
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}
func (s *fakeSys) Notify(t, b string) {
	s.mu.Lock()
	s.notes = append(s.notes, t+": "+b)
	s.mu.Unlock()
}

func (f *fakeSpotify) player(sys System) *Player {
	return &Player{API: f.client(), Sys: sys, Device: "Omarchy", Unit: "spotifyd.service",
		Sleep: func(time.Duration) {}, WaitPoll: time.Millisecond}
}

const (
	devID   = "4da5f53f5af8853f72235596560744f82a8bca96"
	trackA  = "spotify:track:03YdcJcROMngj6U4VPr1X4"
	trackB  = "spotify:track:6ZhORhQoBD0qndKYvpBTv2"
	plMine  = "spotify:playlist:3W46mmJBxfxjWhv65K4Vjk"
	plOther = "spotify:playlist:0yMWqoJ9NmHFTMrsEN0Yb5"
	albumX  = "spotify:album:5OZ8rT5NZcYNxkfJ1KiaWh"
)

func devicesBody(names ...string) map[string]any {
	var ds []map[string]any
	for i, n := range names {
		id := devID
		if i > 0 {
			id = "other" + n
		}
		ds = append(ds, map[string]any{"id": id, "name": n, "type": "Computer", "is_active": false})
	}
	return map[string]any{"devices": ds}
}

func playerBody(playing bool, device, track, ctx string, progress int) map[string]any {
	id := devID
	if device != "Omarchy" {
		id = "other" + device
	}
	b := map[string]any{"is_playing": playing, "progress_ms": progress,
		"device": map[string]any{"id": id, "name": device}, "item": map[string]any{"uri": track, "name": "Echo"}}
	if ctx != "" {
		b["context"] = map[string]any{"uri": ctx}
	}
	return b
}

func ioPipe() (*io.PipeReader, *io.PipeWriter) { return io.Pipe() }
