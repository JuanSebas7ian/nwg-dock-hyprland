package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Entry is one row of the panel: a playlist, album, artist or track.
type Entry struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Sub      string `json:"sub"`
	Image    string `json:"image"`
	Kind     string `json:"kind"`
	Duration int    `json:"duration,omitempty"` // ms, tracks only
	Context  string `json:"context,omitempty"`  // playlist/album the track was listed in
	Mine     bool   `json:"mine,omitempty"`     // a playlist the user owns: songs can be added
}

// Library is what the panel lists below the player.
type Library struct {
	Recent    []Entry `json:"recent"`
	Playlists []Entry `json:"playlists"`
	Albums    []Entry `json:"albums"`
	Top       []Entry `json:"top"`
	Liked     []Entry `json:"liked"`
	UserID    string  `json:"userId"`
	Error     string  `json:"error"`
	UpdatedAt int64   `json:"updatedAt"`
}

var uriRe = regexp.MustCompile(`^spotify:(track|album|playlist|artist|episode|show):([A-Za-z0-9]{22})$`)

// ParseURI splits spotify:<kind>:<id>; ok is false for anything else.
func ParseURI(uri string) (kind, id string, ok bool) {
	if m := uriRe.FindStringSubmatch(strings.TrimSpace(uri)); m != nil {
		return m[1], m[2], true
	}
	if strings.HasPrefix(uri, "spotify:user:") && strings.HasSuffix(uri, ":collection") {
		return "collection", "", true
	}
	return "", "", false
}

type image struct {
	URL   string `json:"url"`
	Width int    `json:"width"`
}

// pickImage returns the image closest to want px wide.
func pickImage(imgs []image, want int) string {
	best, bestDiff := "", 1<<30
	for _, im := range imgs {
		w := im.Width
		if w == 0 {
			w = want
		}
		d := w - want
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			best, bestDiff = im.URL, d
		}
	}
	return best
}

type artist struct {
	Name string `json:"name"`
	URI  string `json:"uri"`
}

func artistNames(as []artist) string {
	names := make([]string, 0, len(as))
	for _, a := range as {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return strings.Join(names, ", ")
}

type track struct {
	URI      string   `json:"uri"`
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Duration int      `json:"duration_ms"`
	Artists  []artist `json:"artists"`
	Album    struct {
		Name   string  `json:"name"`
		URI    string  `json:"uri"`
		Images []image `json:"images"`
	} `json:"album"`
}

func (t track) entry(ctxURI string) Entry {
	uri := t.URI
	if uri == "" && t.ID != "" {
		uri = "spotify:track:" + t.ID
	}
	return Entry{URI: uri, Name: t.Name, Sub: artistNames(t.Artists), Image: pickImage(t.Album.Images, 64),
		Kind: "track", Duration: t.Duration, Context: ctxURI}
}

type playlist struct {
	URI    string  `json:"uri"`
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Images []image `json:"images"`
	Owner  struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"owner"`
	// Spotify renamed "tracks" to "items" in February 2026; accept both.
	Items *struct {
		Total *int `json:"total"`
	} `json:"items"`
	Tracks *struct {
		Total *int `json:"total"`
	} `json:"tracks"`
}

func (p playlist) entry(me string) Entry {
	var total *int
	if p.Items != nil && p.Items.Total != nil {
		total = p.Items.Total
	} else if p.Tracks != nil && p.Tracks.Total != nil {
		total = p.Tracks.Total
	}
	var parts []string
	if total != nil {
		parts = append(parts, fmt.Sprintf("%d songs", *total))
	}
	if p.Owner.DisplayName != "" {
		parts = append(parts, p.Owner.DisplayName)
	}
	return Entry{URI: p.URI, Name: p.Name, Sub: strings.Join(parts, " · "), Image: pickImage(p.Images, 64),
		Kind: "playlist", Mine: me != "" && p.Owner.ID == me}
}

type album struct {
	URI     string   `json:"uri"`
	Name    string   `json:"name"`
	Images  []image  `json:"images"`
	Artists []artist `json:"artists"`
}

func (a album) entry() Entry {
	return Entry{URI: a.URI, Name: a.Name, Sub: artistNames(a.Artists), Image: pickImage(a.Images, 64), Kind: "album"}
}

// Catalog reads and edits the user's library.
type Catalog struct {
	API       *Client
	CacheFile string // library cache (10 min)
	Now       func() time.Time
	me        string
}

func (c *Catalog) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Me returns the user's Spotify id (cached for the process).
func (c *Catalog) Me(ctx context.Context) (string, error) {
	if c.me != "" {
		return c.me, nil
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := c.API.Do(ctx, http.MethodGet, "/me", nil, nil, &r); err != nil {
		return "", err
	}
	c.me = r.ID
	return r.ID, nil
}

// Library loads every list; a list that fails is left empty, and Error is set
// only when all of them failed (so a 429 on one list does not blank the panel).
func (c *Catalog) Library(ctx context.Context, force bool) Library {
	if !force && c.CacheFile != "" {
		if st, err := os.Stat(c.CacheFile); err == nil && c.now().Sub(st.ModTime()) < 10*time.Minute {
			var lib Library
			if b, err := os.ReadFile(c.CacheFile); err == nil && json.Unmarshal(b, &lib) == nil {
				return lib
			}
		}
	}
	lib := Library{Recent: []Entry{}, Playlists: []Entry{}, Albums: []Entry{}, Top: []Entry{}, Liked: []Entry{}}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	me, err := c.Me(ctx)
	keep(err)
	lib.UserID = me

	var pl struct {
		Items []playlist `json:"items"`
	}
	if err := c.API.Do(ctx, http.MethodGet, "/me/playlists", url.Values{"limit": {"50"}}, nil, &pl); err == nil {
		for _, p := range pl.Items {
			if p.URI != "" {
				lib.Playlists = append(lib.Playlists, p.entry(me))
			}
		}
	} else {
		keep(err)
	}
	var al struct {
		Items []struct {
			Album album `json:"album"`
		} `json:"items"`
	}
	if err := c.API.Do(ctx, http.MethodGet, "/me/albums", url.Values{"limit": {"50"}}, nil, &al); err == nil {
		for _, a := range al.Items {
			if a.Album.URI != "" {
				lib.Albums = append(lib.Albums, a.Album.entry())
			}
		}
	} else {
		keep(err)
	}
	var top struct {
		Items []track `json:"items"`
	}
	if err := c.API.Do(ctx, http.MethodGet, "/me/top/tracks", url.Values{"limit": {"30"}, "time_range": {"short_term"}}, nil, &top); err == nil {
		for _, t := range top.Items {
			lib.Top = append(lib.Top, t.entry(""))
		}
	} else {
		keep(err)
	}
	var liked struct {
		Items []struct {
			Track track `json:"track"`
		} `json:"items"`
	}
	if err := c.API.Do(ctx, http.MethodGet, "/me/tracks", url.Values{"limit": {"50"}}, nil, &liked); err == nil {
		ctxURI := ""
		if me != "" {
			ctxURI = "spotify:user:" + me + ":collection"
		}
		for _, it := range liked.Items {
			lib.Liked = append(lib.Liked, it.Track.entry(ctxURI))
		}
	} else {
		keep(err)
	}
	var rec struct {
		Items []struct {
			Track   track `json:"track"`
			Context *struct {
				URI  string `json:"uri"`
				Type string `json:"type"`
			} `json:"context"`
		} `json:"items"`
	}
	if err := c.API.Do(ctx, http.MethodGet, "/me/player/recently-played", url.Values{"limit": {"40"}}, nil, &rec); err == nil {
		lib.Recent = recentEntries(rec.Items, lib.Playlists, lib.Albums)
	} else {
		keep(err)
	}

	if firstErr != nil && len(lib.Playlists)+len(lib.Albums)+len(lib.Top)+len(lib.Recent)+len(lib.Liked) == 0 {
		lib.Error = firstErr.Error()
	}
	lib.UpdatedAt = c.now().Unix()
	if lib.Error == "" && c.CacheFile != "" {
		_ = os.MkdirAll(filepath.Dir(c.CacheFile), 0o700)
		if b, err := json.Marshal(lib); err == nil {
			_ = os.WriteFile(c.CacheFile, b, 0o600)
		}
	}
	return lib
}

// recentEntries puts the playlists/albums played recently first (once each),
// then the last 25 songs.
func recentEntries(items []struct {
	Track   track `json:"track"`
	Context *struct {
		URI  string `json:"uri"`
		Type string `json:"type"`
	} `json:"context"`
}, playlists, albums []Entry) []Entry {
	known := map[string]Entry{}
	for _, e := range append(append([]Entry{}, playlists...), albums...) {
		known[e.URI] = e
	}
	out := []Entry{}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Context == nil || seen[it.Context.URI] {
			continue
		}
		if e, ok := known[it.Context.URI]; ok {
			seen[it.Context.URI] = true
			e.Sub = "Played recently · " + e.Sub
			out = append(out, e)
		}
	}
	songs := 0
	for _, it := range items {
		if songs >= 25 {
			break
		}
		ctxURI := ""
		if it.Context != nil {
			ctxURI = it.Context.URI
		}
		e := it.Track.entry(ctxURI)
		if e.URI == "" || seen[e.URI] {
			continue // the same song played several times shows once
		}
		seen[e.URI] = true
		songs++
		out = append(out, e)
	}
	return out
}

// Tracks lists the songs of a playlist or album, ready to play in context.
func (c *Catalog) Tracks(ctx context.Context, uri string) ([]Entry, error) {
	kind, id, ok := ParseURI(uri)
	if !ok {
		return nil, fmt.Errorf("not a Spotify URI: %q", uri)
	}
	out := []Entry{}
	switch kind {
	case "playlist":
		var r struct {
			Items []struct {
				Item  *track `json:"item"`
				Track *track `json:"track"` // before February 2026
			} `json:"items"`
		}
		err := c.API.Do(ctx, http.MethodGet, "/playlists/"+id+"/items", url.Values{"limit": {"100"}}, nil, &r)
		if IsStatus(err, http.StatusForbidden) {
			return nil, fmt.Errorf("Spotify only lists the songs of your own playlists; press play to hear this one")
		}
		if err != nil {
			return nil, err
		}
		for _, it := range r.Items {
			t := it.Item
			if t == nil {
				t = it.Track
			}
			if t != nil && (t.URI != "" || t.ID != "") {
				out = append(out, t.entry(uri))
			}
		}
	case "album":
		var a struct {
			Images []image `json:"images"`
			Tracks struct {
				Items []track `json:"items"`
			} `json:"tracks"`
		}
		if err := c.API.Do(ctx, http.MethodGet, "/albums/"+id, nil, nil, &a); err != nil {
			return nil, err
		}
		img := pickImage(a.Images, 64)
		for _, t := range a.Tracks.Items {
			e := t.entry(uri)
			if e.Image == "" {
				e.Image = img
			}
			out = append(out, e)
		}
	case "collection":
		lib := c.Library(ctx, false)
		return lib.Liked, nil
	default:
		return nil, fmt.Errorf("can't list the songs of a %s", kind)
	}
	return out, nil
}

// SearchResult groups search hits by kind.
type SearchResult struct {
	Tracks    []Entry `json:"tracks"`
	Albums    []Entry `json:"albums"`
	Playlists []Entry `json:"playlists"`
	Artists   []Entry `json:"artists"`
}

// Search asks Spotify for up to 10 of each kind (the 2026 maximum).
func (c *Catalog) Search(ctx context.Context, q string) (SearchResult, error) {
	res := SearchResult{Tracks: []Entry{}, Albums: []Entry{}, Playlists: []Entry{}, Artists: []Entry{}}
	q = strings.TrimSpace(q)
	if q == "" {
		return res, nil
	}
	var r struct {
		Tracks struct {
			Items []track `json:"items"`
		} `json:"tracks"`
		Albums struct {
			Items []album `json:"items"`
		} `json:"albums"`
		Playlists struct {
			Items []*playlist `json:"items"`
		} `json:"playlists"`
		Artists struct {
			Items []struct {
				URI    string   `json:"uri"`
				Name   string   `json:"name"`
				Genres []string `json:"genres"`
				Images []image  `json:"images"`
			} `json:"items"`
		} `json:"artists"`
	}
	params := url.Values{"q": {q}, "type": {"track,album,playlist,artist"}, "limit": {"10"}}
	if err := c.API.Do(ctx, http.MethodGet, "/search", params, nil, &r); err != nil {
		return res, err
	}
	for _, t := range r.Tracks.Items {
		res.Tracks = append(res.Tracks, t.entry(""))
	}
	for _, a := range r.Albums.Items {
		res.Albums = append(res.Albums, a.entry())
	}
	for _, p := range r.Playlists.Items {
		if p != nil && p.URI != "" { // Spotify returns null entries here
			res.Playlists = append(res.Playlists, p.entry(c.me))
		}
	}
	for _, a := range r.Artists.Items {
		res.Artists = append(res.Artists, Entry{URI: a.URI, Name: a.Name, Sub: strings.Join(a.Genres, ", "),
			Image: pickImage(a.Images, 64), Kind: "artist"})
	}
	return res, nil
}

func validURIs(uris []string, kinds ...string) ([]string, error) {
	var out []string
	for _, u := range uris {
		k, _, ok := ParseURI(u)
		if !ok {
			return nil, fmt.Errorf("not a Spotify URI: %q", u)
		}
		if len(kinds) > 0 && !contains(kinds, k) {
			return nil, fmt.Errorf("%s can't be used here", k)
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nothing selected")
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (c *Catalog) invalidate() {
	if c.CacheFile != "" {
		_ = os.Remove(c.CacheFile)
	}
}

// Like saves tracks/albums/playlists to the library (PUT /me/library).
func (c *Catalog) Like(ctx context.Context, uris []string, like bool) error {
	uris, err := validURIs(uris)
	if err != nil {
		return err
	}
	method := http.MethodPut
	if !like {
		method = http.MethodDelete
	}
	err = c.API.Do(ctx, method, "/me/library", url.Values{"uris": {strings.Join(uris, ",")}}, nil, nil)
	if err == nil {
		c.invalidate()
	}
	return err
}

// Liked reports which of the URIs are saved.
func (c *Catalog) Liked(ctx context.Context, uris []string) (map[string]bool, error) {
	uris, err := validURIs(uris)
	if err != nil {
		return nil, err
	}
	var r []bool
	if err := c.API.Do(ctx, http.MethodGet, "/me/library/contains", url.Values{"uris": {strings.Join(uris, ",")}}, nil, &r); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for i, u := range uris {
		out[u] = i < len(r) && r[i]
	}
	return out, nil
}

// PlaylistAdd appends tracks to one of the user's playlists.
func (c *Catalog) PlaylistAdd(ctx context.Context, playlistURI string, uris []string) error {
	_, id, ok := ParseURI(playlistURI)
	if !ok || !strings.HasPrefix(playlistURI, "spotify:playlist:") {
		return fmt.Errorf("not a playlist: %q", playlistURI)
	}
	uris, err := validURIs(uris, "track", "episode")
	if err != nil {
		return err
	}
	err = c.API.Do(ctx, http.MethodPost, "/playlists/"+id+"/items", nil, map[string]any{"uris": uris}, nil)
	if err == nil {
		c.invalidate()
	}
	return err
}

// PlaylistRemove takes tracks out of one of the user's playlists.
func (c *Catalog) PlaylistRemove(ctx context.Context, playlistURI string, uris []string) error {
	_, id, ok := ParseURI(playlistURI)
	if !ok || !strings.HasPrefix(playlistURI, "spotify:playlist:") {
		return fmt.Errorf("not a playlist: %q", playlistURI)
	}
	uris, err := validURIs(uris, "track", "episode")
	if err != nil {
		return err
	}
	items := make([]map[string]string, 0, len(uris))
	for _, u := range uris {
		items = append(items, map[string]string{"uri": u})
	}
	err = c.API.Do(ctx, http.MethodDelete, "/playlists/"+id+"/items", nil, map[string]any{"items": items}, nil)
	if err == nil {
		c.invalidate()
	}
	return err
}

// PlaylistCreate makes a new private playlist (POST /me/playlists, 2026).
func (c *Catalog) PlaylistCreate(ctx context.Context, name string) (Entry, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Entry{}, fmt.Errorf("the playlist needs a name")
	}
	var p playlist
	if err := c.API.Do(ctx, http.MethodPost, "/me/playlists", nil, map[string]any{"name": name, "public": false}, &p); err != nil {
		return Entry{}, err
	}
	c.invalidate()
	e := p.entry(p.Owner.ID)
	e.Mine = true
	return e, nil
}

var trackRefRe = regexp.MustCompile(`(track|episode)[:/]([A-Za-z0-9]{22})`)

// NormalizeTrackRef turns an MPRIS track id or an open.spotify.com URL into
// spotify:track:<id>; anything else gives "".
func NormalizeTrackRef(ref string) string {
	if m := trackRefRe.FindStringSubmatch(ref); m != nil {
		return "spotify:" + m[1] + ":" + m[2]
	}
	return ""
}
