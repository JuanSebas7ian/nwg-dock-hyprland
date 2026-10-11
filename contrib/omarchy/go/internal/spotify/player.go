package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// System is what the player needs from the machine (fake in tests).
type System interface {
	StartUnit(name string) error
	RestartUnit(name string) error
	Notify(title, body string)
}

// UserSystemd runs systemctl --user and notify-send.
type UserSystemd struct{}

func (UserSystemd) StartUnit(name string) error {
	return exec.Command("systemctl", "--user", "start", name).Run()
}
func (UserSystemd) RestartUnit(name string) error {
	return exec.Command("systemctl", "--user", "restart", name).Run()
}
func (UserSystemd) Notify(title, body string) {
	_ = exec.Command("notify-send", "-a", "Spotify", "-i", "spotify", title, body).Run()
}

// Device is a Spotify Connect device.
type Device struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Active bool   `json:"active"`
	Volume *int   `json:"volume,omitempty"`
}

// State is the part of GET /me/player the panel and the healer use.
type State struct {
	Playing    bool   `json:"playing"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	TrackURI   string `json:"trackUri"`
	TrackName  string `json:"trackName"`
	ContextURI string `json:"contextUri"`
	ProgressMs int    `json:"progressMs"`
	Shuffle    bool   `json:"shuffle"`
	Repeat     string `json:"repeat"`
}

// ErrNotStarted means Spotify accepted the command but nothing plays.
var ErrNotStarted = errors.New("Spotify accepted the song but it did not start")

// PlayRequest says what to play. Context alone plays a playlist/album; Track
// alone plays one song; both start the context at that song.
type PlayRequest struct {
	Context    string
	Track      string
	PositionMs int
}

// Player plays on this PC (spotifyd, Connect device Device) through the Web API.
type Player struct {
	API      *Client
	Sys      System
	Device   string // Connect name of this PC, "Omarchy"
	Unit     string // spotifyd.service
	Sleep    func(time.Duration)
	WaitPoll time.Duration // device/playing poll interval (tests: tiny)
}

func (p *Player) sleep(d time.Duration) {
	if p.Sleep != nil {
		p.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (p *Player) poll() time.Duration {
	if p.WaitPoll > 0 {
		return p.WaitPoll
	}
	return 500 * time.Millisecond
}

// Devices lists Spotify Connect devices.
func (p *Player) Devices(ctx context.Context) ([]Device, error) {
	var r struct {
		Devices []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Type   string `json:"type"`
			Active bool   `json:"is_active"`
			Volume *int   `json:"volume_percent"`
		} `json:"devices"`
	}
	if err := p.API.Do(ctx, http.MethodGet, "/me/player/devices", nil, nil, &r); err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(r.Devices))
	for _, d := range r.Devices {
		out = append(out, Device{ID: d.ID, Name: d.Name, Type: d.Type, Active: d.Active, Volume: d.Volume})
	}
	return out, nil
}

// LocalDevice finds this PC's device, starting spotifyd when it is missing and
// waiting up to wait for it to register with Spotify.
func (p *Player) LocalDevice(ctx context.Context, wait time.Duration) (Device, error) {
	started := false
	deadline := time.Now().Add(wait)
	for {
		devs, err := p.Devices(ctx)
		if err != nil {
			return Device{}, err
		}
		for _, d := range devs {
			if strings.EqualFold(d.Name, p.Device) {
				return d, nil
			}
		}
		if !started {
			started = true
			_ = p.Sys.StartUnit(p.Unit)
		}
		if time.Now().After(deadline) {
			return Device{}, fmt.Errorf("this PC (%s) is not showing up in Spotify: check `systemctl --user status %s`", p.Device, p.Unit)
		}
		p.sleep(p.poll() * 2)
	}
}

// State reads the current playback; ok is false when nothing is loaded.
func (p *Player) State(ctx context.Context) (State, bool, error) {
	var r struct {
		IsPlaying  bool   `json:"is_playing"`
		ProgressMs int    `json:"progress_ms"`
		Shuffle    bool   `json:"shuffle_state"`
		Repeat     string `json:"repeat_state"`
		Device     struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"device"`
		Context *struct {
			URI string `json:"uri"`
		} `json:"context"`
		Item *struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"item"`
	}
	if err := p.API.Do(ctx, http.MethodGet, "/me/player", nil, nil, &r); err != nil {
		return State{}, false, err
	}
	if r.Device.ID == "" && r.Item == nil {
		return State{}, false, nil // 204: nothing playing anywhere
	}
	s := State{Playing: r.IsPlaying, DeviceID: r.Device.ID, DeviceName: r.Device.Name, ProgressMs: r.ProgressMs,
		Shuffle: r.Shuffle, Repeat: r.Repeat}
	if r.Context != nil {
		s.ContextURI = r.Context.URI
	}
	if r.Item != nil {
		s.TrackURI, s.TrackName = r.Item.URI, r.Item.Name
	}
	return s, true, nil
}

func playBody(req PlayRequest) (map[string]any, error) {
	body := map[string]any{}
	if req.Context != "" {
		kind, _, ok := ParseURI(req.Context)
		if !ok || kind == "track" || kind == "episode" {
			return nil, fmt.Errorf("can't play %q as a list", req.Context)
		}
		body["context_uri"] = req.Context
		if req.Track != "" && kind != "artist" {
			body["offset"] = map[string]string{"uri": req.Track}
		}
	} else if req.Track != "" {
		kind, _, ok := ParseURI(req.Track)
		if !ok || (kind != "track" && kind != "episode") {
			return nil, fmt.Errorf("not a song: %q", req.Track)
		}
		body["uris"] = []string{req.Track}
	}
	if req.PositionMs > 0 {
		body["position_ms"] = req.PositionMs
	}
	return body, nil
}

// Play starts req on this PC and waits until Spotify reports it playing there.
// An empty request resumes whatever is loaded.
func (p *Player) Play(ctx context.Context, req PlayRequest) error {
	body, err := playBody(req)
	if err != nil {
		return err
	}
	dev, err := p.LocalDevice(ctx, 15*time.Second)
	if err != nil {
		return err
	}
	q := url.Values{"device_id": {dev.ID}}
	var payload any
	if len(body) > 0 {
		payload = body
	}
	err = p.API.Do(ctx, http.MethodPut, "/me/player/play", q, payload, nil)
	if IsStatus(err, http.StatusNotFound) {
		// The device exists but is not active yet: make it active, then play.
		if terr := p.Transfer(ctx, dev.ID, false); terr != nil {
			return terr
		}
		p.sleep(p.poll())
		err = p.API.Do(ctx, http.MethodPut, "/me/player/play", q, payload, nil)
	}
	if err != nil {
		return err
	}
	return p.waitPlaying(ctx, dev.ID, req.Track, 8*time.Second)
}

func (p *Player) waitPlaying(ctx context.Context, deviceID, track string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		s, ok, err := p.State(ctx)
		if err == nil && ok && s.Playing && s.DeviceID == deviceID && (track == "" || s.TrackURI == track) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrNotStarted
		}
		p.sleep(p.poll())
	}
}

// Transfer moves playback to a device (id or name).
func (p *Player) Transfer(ctx context.Context, device string, play bool) error {
	id := device
	if devs, err := p.Devices(ctx); err == nil {
		for _, d := range devs {
			if d.ID == device || strings.EqualFold(d.Name, device) {
				id = d.ID
			}
		}
	}
	return p.API.Do(ctx, http.MethodPut, "/me/player", nil, map[string]any{"device_ids": []string{id}, "play": play}, nil)
}

// Pause pauses wherever Spotify is playing.
func (p *Player) Pause(ctx context.Context) error {
	return p.API.Do(ctx, http.MethodPut, "/me/player/pause", nil, nil, nil)
}

// Queue appends a song to the play queue.
func (p *Player) Queue(ctx context.Context, uri string) error {
	if k, _, ok := ParseURI(uri); !ok || (k != "track" && k != "episode") {
		return fmt.Errorf("only songs can be queued")
	}
	return p.API.Do(ctx, http.MethodPost, "/me/player/queue", url.Values{"uri": {uri}}, nil, nil)
}

// QueueList returns what plays next.
func (p *Player) QueueList(ctx context.Context) ([]Entry, error) {
	var r struct {
		Queue []track `json:"queue"`
	}
	if err := p.API.Do(ctx, http.MethodGet, "/me/player/queue", nil, nil, &r); err != nil {
		return nil, err
	}
	out := []Entry{}
	for i, t := range r.Queue {
		if i >= 20 {
			break
		}
		out = append(out, t.entry(""))
	}
	return out, nil
}

// LastURI remembers the last song seen, for a cold start with nothing loaded.
type LastURI string

func (l LastURI) Read() string {
	b, err := os.ReadFile(string(l))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (l LastURI) Write(uri string) {
	if k, _, ok := ParseURI(uri); ok && (k == "track" || k == "episode") {
		_ = os.WriteFile(string(l), []byte(uri+"\n"), 0o600)
	}
}

// Resume plays on this PC with whatever there is: resume the account's
// session (moving it here), else the last remembered song.
func (p *Player) Resume(ctx context.Context, last LastURI) (string, error) {
	s, ok, err := p.State(ctx)
	if err != nil {
		return "", err
	}
	if ok && s.TrackURI != "" {
		if s.Playing && strings.EqualFold(s.DeviceName, p.Device) {
			return "playing", nil
		}
		dev, err := p.LocalDevice(ctx, 15*time.Second)
		if err != nil {
			return "", err
		}
		if s.DeviceID != dev.ID {
			if err := p.Transfer(ctx, dev.ID, true); err != nil {
				return "", err
			}
			return "transfer", p.waitPlaying(ctx, dev.ID, "", 8*time.Second)
		}
		return "resume", p.Play(ctx, PlayRequest{})
	}
	if uri := last.Read(); uri != "" {
		return "last", p.Play(ctx, PlayRequest{Track: uri})
	}
	return "", fmt.Errorf("nothing to resume yet: pick a playlist or a song below")
}
