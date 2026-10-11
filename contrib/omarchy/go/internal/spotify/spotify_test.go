package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var ctx = context.Background()

// ---------------------------------------------------------------- token

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func tokenServer(t *testing.T, status int, body map[string]any, hits *int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != "cid" || r.Form.Get("refresh_token") == "" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTokenFreshIsReusedWithoutRefresh(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	writeJSON(t, filepath.Join(dir, "seed.json"), Token{AccessToken: "a1", RefreshToken: "r1", ExpiresAt: now.Add(time.Hour)})
	var hits int32
	srv := tokenServer(t, 200, nil, &hits)
	s := &TokenStore{Path: filepath.Join(dir, "tok.json"), Seed: filepath.Join(dir, "seed.json"), ClientID: "cid",
		TokenURL: srv.URL, Now: func() time.Time { return now }}
	got, err := s.Access(ctx)
	if err != nil || got != "a1" || hits != 0 {
		t.Fatalf("got %q %v, %d refreshes", got, err, hits)
	}
}

func TestTokenRefreshKeepsOldRefreshTokenAndSaves0600(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	writeJSON(t, filepath.Join(dir, "seed.json"), Token{AccessToken: "old", RefreshToken: "r1", ExpiresAt: now.Add(-time.Minute)})
	var hits int32
	srv := tokenServer(t, 200, map[string]any{"access_token": "new", "expires_in": 3600}, &hits)
	s := &TokenStore{Path: filepath.Join(dir, "sub", "tok.json"), Seed: filepath.Join(dir, "seed.json"), ClientID: "cid",
		TokenURL: srv.URL, Now: func() time.Time { return now }}
	got, err := s.Access(ctx)
	if err != nil || got != "new" {
		t.Fatalf("got %q %v", got, err)
	}
	saved, err := readToken(s.Path)
	if err != nil || saved.RefreshToken != "r1" || !saved.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("saved %+v %v", saved, err)
	}
	if st, _ := os.Stat(s.Path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	// a second process reads our copy, not the stale seed
	s2 := &TokenStore{Path: s.Path, Seed: s.Seed, ClientID: "cid", TokenURL: srv.URL, Now: s.Now}
	if got, _ := s2.Access(ctx); got != "new" || hits != 1 {
		t.Fatalf("second store got %q after %d refreshes", got, hits)
	}
}

func TestTokenRotatedRefreshTokenIsStored(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "tok.json"), Token{RefreshToken: "r1"})
	var hits int32
	srv := tokenServer(t, 200, map[string]any{"access_token": "new", "refresh_token": "r2", "expires_in": 3600}, &hits)
	s := &TokenStore{Path: filepath.Join(dir, "tok.json"), ClientID: "cid", TokenURL: srv.URL}
	if _, err := s.Access(ctx); err != nil {
		t.Fatal(err)
	}
	if saved, _ := readToken(s.Path); saved.RefreshToken != "r2" {
		t.Fatalf("refresh token not rotated: %+v", saved)
	}
}

func TestTokenInvalidGrantMeansNotConnected(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "tok.json"), Token{RefreshToken: "revoked"})
	var hits int32
	srv := tokenServer(t, 400, map[string]any{"error": "invalid_grant"}, &hits)
	s := &TokenStore{Path: filepath.Join(dir, "tok.json"), ClientID: "cid", TokenURL: srv.URL}
	if _, err := s.Access(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
}

func TestTokenFallsBackToSeedWhenOursWasRevoked(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	writeJSON(t, filepath.Join(dir, "tok.json"), Token{RefreshToken: "revoked"})
	writeJSON(t, filepath.Join(dir, "seed.json"), Token{AccessToken: "fromseed", RefreshToken: "rotated", ExpiresAt: now.Add(time.Hour)})
	var hits int32
	srv := tokenServer(t, 400, map[string]any{"error": "invalid_grant"}, &hits)
	s := &TokenStore{Path: filepath.Join(dir, "tok.json"), Seed: filepath.Join(dir, "seed.json"), ClientID: "cid",
		TokenURL: srv.URL, Now: func() time.Time { return now }}
	got, err := s.Access(ctx)
	if err != nil || got != "fromseed" {
		t.Fatalf("got %q %v", got, err)
	}
	if saved, _ := readToken(s.Path); saved.RefreshToken != "rotated" {
		t.Fatalf("seed not adopted: %+v", saved)
	}
}

func TestTokenMissingFilesMeansNotConnected(t *testing.T) {
	s := &TokenStore{Path: filepath.Join(t.TempDir(), "none.json"), ClientID: "cid"}
	if s.Connected() {
		t.Fatal("connected without a token")
	}
	if _, err := s.Access(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("got %v", err)
	}
}

func TestClientIDFromToml(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.toml")
	_ = os.WriteFile(p, []byte("client_port = 8642\nclient_id = \"039407835e2345aeaa35bcf19958567e\"\n"), 0o600)
	if got := ClientIDFromToml(p); got != "039407835e2345aeaa35bcf19958567e" {
		t.Fatalf("got %q", got)
	}
	_ = os.WriteFile(p, []byte("# client_id = \"x\"\n"), 0o600)
	if got := ClientIDFromToml(p); got != "" {
		t.Fatalf("got %q from a comment", got)
	}
}

// ---------------------------------------------------------------- api

func TestAPIRefreshesOnceOn401(t *testing.T) {
	f := newFake(t)
	var n int32
	f.on("GET /me", func(*http.Request, string) (int, any) {
		if atomic.AddInt32(&n, 1) == 1 {
			return 401, `{"error":{"status":401,"message":"expired"}}`
		}
		return 200, map[string]any{"id": "u1"}
	})
	c := f.client()
	var r struct{ ID string }
	if err := c.Do(ctx, "GET", "/me", nil, nil, &r); err != nil || r.ID != "u1" {
		t.Fatalf("%v %+v", err, r)
	}
	if c.Tokens.(*staticTokens).invalidated != 1 {
		t.Fatal("token not invalidated")
	}
	// a second 401 in a row is an error, not a loop
	f.json("GET /me", 401, map[string]any{"error": map[string]any{"message": "nope"}})
	if err := c.Do(ctx, "GET", "/me", nil, nil, &r); !IsStatus(err, 401) {
		t.Fatalf("want 401, got %v", err)
	}
}

func TestAPI429ShortWaitRetriesLongWaitFails(t *testing.T) {
	f := newFake(t)
	var n int32
	f.on("GET /me", func(r *http.Request, _ string) (int, any) {
		if atomic.AddInt32(&n, 1) == 1 {
			return 429, ""
		}
		return 200, map[string]any{"id": "u1"}
	})
	var slept time.Duration
	c := f.client()
	c.Sleep = func(d time.Duration) { slept += d }
	if err := c.Do(ctx, "GET", "/me", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if slept != time.Second { // no Retry-After header: 1 s
		t.Fatalf("slept %v", slept)
	}
	var calls int32
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
	})
	slept = 0
	err := c.Do(ctx, "GET", "/me", nil, nil, nil)
	if !IsStatus(err, 429) || !strings.Contains(err.Error(), "30s") || calls != 1 || slept != 0 {
		t.Fatalf("got %v after %d calls, slept %v", err, calls, slept)
	}
}

func TestAPI5xxRetriedOnce(t *testing.T) {
	f := newFake(t)
	var n int32
	f.on("GET /me", func(*http.Request, string) (int, any) {
		if atomic.AddInt32(&n, 1) == 1 {
			return 502, ""
		}
		return 200, map[string]any{"id": "u1"}
	})
	if err := f.client().Do(ctx, "GET", "/me", nil, nil, nil); err != nil || n != 2 {
		t.Fatalf("%v after %d calls", err, n)
	}
}

func TestAPIErrorMessageAndNoActiveDevice(t *testing.T) {
	f := newFake(t)
	f.json("PUT /me/player/play", 404, map[string]any{"error": map[string]any{"status": 404, "message": "Player command failed: No active device found", "reason": "NO_ACTIVE_DEVICE"}})
	err := f.client().Do(ctx, "PUT", "/me/player/play", nil, nil, nil)
	if !IsStatus(err, 404) || !strings.Contains(err.Error(), "no active device") {
		t.Fatalf("got %v", err)
	}
}

// ---------------------------------------------------------------- catalog

func TestParseURIAndNormalize(t *testing.T) {
	for uri, want := range map[string]string{trackA: "track", plMine: "playlist", albumX: "album",
		"spotify:user:123:collection": "collection", "spotify:track:short": "", "https://x": "", "": ""} {
		k, _, ok := ParseURI(uri)
		if (want == "") == ok || k != want {
			t.Errorf("%q -> %q %v, want %q", uri, k, ok, want)
		}
	}
	for in, want := range map[string]string{
		"/org/mpris/MediaPlayer2/spotifyd/track/03YdcJcROMngj6U4VPr1X4": trackA,
		"https://open.spotify.com/track/03YdcJcROMngj6U4VPr1X4?si=1":    trackA,
		"spotify:episode:6HnGObopaJVGQK4HWWZCEq":                        "spotify:episode:6HnGObopaJVGQK4HWWZCEq",
		"/org/mpris/MediaPlayer2/TrackList/NoTrack":                     "",
	} {
		if got := NormalizeTrackRef(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestPickImageClosestWidth(t *testing.T) {
	imgs := []image{{"big", 640}, {"mid", 300}, {"small", 64}}
	if got := pickImage(imgs, 64); got != "small" {
		t.Fatal(got)
	}
	if got := pickImage(nil, 64); got != "" {
		t.Fatal(got)
	}
	if got := pickImage([]image{{"nowidth", 0}}, 64); got != "nowidth" {
		t.Fatal(got)
	}
}

func libraryFake(t *testing.T) *fakeSpotify {
	f := newFake(t)
	f.json("GET /me", 200, map[string]any{"id": "me"})
	f.json("GET /me/playlists", 200, map[string]any{"items": []any{
		map[string]any{"uri": plMine, "name": "English", "owner": map[string]any{"id": "me", "display_name": "Juan"}, "items": map[string]any{"total": 1}},
		map[string]any{"uri": plOther, "name": "Top Christian", "owner": map[string]any{"id": "x", "display_name": "Carolina"}, "tracks": map[string]any{"total": 69}},
		nil,
	}})
	f.json("GET /me/albums", 200, map[string]any{"items": []any{map[string]any{"album": map[string]any{"uri": albumX, "name": "Z", "artists": []any{map[string]any{"name": "Cast"}}}}}})
	f.json("GET /me/top/tracks", 200, map[string]any{"items": []any{map[string]any{"uri": trackA, "name": "Echo", "artists": []any{map[string]any{"name": "A"}, map[string]any{"name": "B"}}}}})
	f.json("GET /me/tracks", 200, map[string]any{"items": []any{map[string]any{"track": map[string]any{"uri": trackB, "name": "Liked"}}}})
	f.json("GET /me/player/recently-played", 200, map[string]any{"items": []any{
		map[string]any{"track": map[string]any{"uri": trackA, "name": "Echo"}, "context": map[string]any{"uri": plOther, "type": "playlist"}},
		map[string]any{"track": map[string]any{"uri": trackB, "name": "Liked"}, "context": map[string]any{"uri": plOther, "type": "playlist"}},
		map[string]any{"track": map[string]any{"uri": trackA, "name": "Echo again"}},
	}})
	return f
}

func TestLibraryBuildsEveryListAndCaches(t *testing.T) {
	f := libraryFake(t)
	cache := filepath.Join(t.TempDir(), "lib.json")
	c := &Catalog{API: f.client(), CacheFile: cache}
	lib := c.Library(ctx, false)
	if lib.Error != "" || len(lib.Playlists) != 2 || len(lib.Albums) != 1 || len(lib.Top) != 1 || len(lib.Liked) != 1 {
		t.Fatalf("%+v", lib)
	}
	if !lib.Playlists[0].Mine || lib.Playlists[1].Mine {
		t.Fatal("ownership wrong")
	}
	if lib.Playlists[0].Sub != "1 songs · Juan" || lib.Playlists[1].Sub != "69 songs · Carolina" {
		t.Fatalf("subs %q %q", lib.Playlists[0].Sub, lib.Playlists[1].Sub)
	}
	if lib.Top[0].Sub != "A, B" || lib.Liked[0].Context != "spotify:user:me:collection" {
		t.Fatalf("%+v %+v", lib.Top[0], lib.Liked[0])
	}
	// recent: the playlist once, then both songs
	if len(lib.Recent) != 3 || lib.Recent[0].URI != plOther || !strings.HasPrefix(lib.Recent[0].Sub, "Played recently") {
		t.Fatalf("recent %+v", lib.Recent)
	}
	before := f.count("GET /me/playlists")
	c2 := &Catalog{API: f.client(), CacheFile: cache}
	if c2.Library(ctx, false); f.count("GET /me/playlists") != before {
		t.Fatal("cache not used")
	}
	if c2.Library(ctx, true); f.count("GET /me/playlists") != before+1 {
		t.Fatal("--force did not reload")
	}
}

func TestLibraryOneFailingListDoesNotBlankThePanel(t *testing.T) {
	f := libraryFake(t)
	f.on("GET /me/albums", func(r *http.Request, _ string) (int, any) { return 429, "" })
	f.srv.Config.Handler = wrapRetryAfter(f.srv.Config.Handler, "/me/albums", "60")
	lib := (&Catalog{API: f.client()}).Library(ctx, true)
	if lib.Error != "" || len(lib.Albums) != 0 || len(lib.Playlists) != 2 {
		t.Fatalf("%+v", lib)
	}
}

func wrapRetryAfter(h http.Handler, path, secs string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			w.Header().Set("Retry-After", secs)
		}
		h.ServeHTTP(w, r)
	})
}

func TestLibraryAllFailingSetsErrorAndSkipsCache(t *testing.T) {
	f := newFake(t)
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	})
	cache := filepath.Join(t.TempDir(), "lib.json")
	lib := (&Catalog{API: f.client(), CacheFile: cache}).Library(ctx, true)
	if !strings.Contains(lib.Error, "rate limit") {
		t.Fatalf("error %q", lib.Error)
	}
	if _, err := os.Stat(cache); err == nil {
		t.Fatal("an error was cached")
	}
	if lib.Playlists == nil || lib.Recent == nil {
		t.Fatal("lists must be [] not null for QML")
	}
}

func TestTracksPlaylistNewAndOldFieldsAndForbidden(t *testing.T) {
	f := newFake(t)
	f.json("GET /playlists/3W46mmJBxfxjWhv65K4Vjk/items", 200, map[string]any{"items": []any{
		map[string]any{"item": map[string]any{"uri": "spotify:episode:6HnGObopaJVGQK4HWWZCEq", "type": "episode", "name": "Ep"}},
		map[string]any{"track": map[string]any{"uri": trackA, "type": "track", "name": "Old field"}},
		map[string]any{"item": nil},
	}})
	f.json("GET /playlists/0yMWqoJ9NmHFTMrsEN0Yb5/items", 403, map[string]any{"error": map[string]any{"message": "Forbidden"}})
	c := &Catalog{API: f.client()}
	tr, err := c.Tracks(ctx, plMine)
	if err != nil || len(tr) != 2 || tr[0].Context != plMine || tr[1].URI != trackA {
		t.Fatalf("%v %+v", err, tr)
	}
	if _, err := c.Tracks(ctx, plOther); err == nil || !strings.Contains(err.Error(), "your own playlists") {
		t.Fatalf("got %v", err)
	}
	if _, err := c.Tracks(ctx, "garbage"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestTracksAlbumUsesAlbumCover(t *testing.T) {
	f := newFake(t)
	f.json("GET /albums/5OZ8rT5NZcYNxkfJ1KiaWh", 200, map[string]any{"images": []any{map[string]any{"url": "cover", "width": 64}},
		"tracks": map[string]any{"items": []any{map[string]any{"uri": trackA, "name": "One"}}}})
	tr, err := (&Catalog{API: f.client()}).Tracks(ctx, albumX)
	if err != nil || len(tr) != 1 || tr[0].Image != "cover" || tr[0].Context != albumX {
		t.Fatalf("%v %+v", err, tr)
	}
}

func TestSearchSkipsNullPlaylistsAndAsksForTen(t *testing.T) {
	f := newFake(t)
	f.on("GET /search", func(r *http.Request, _ string) (int, any) {
		if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("q") != "echo" {
			return 400, ""
		}
		return 200, map[string]any{
			"tracks":    map[string]any{"items": []any{map[string]any{"uri": trackA, "name": "Echo"}}},
			"playlists": map[string]any{"items": []any{nil, map[string]any{"name": "no uri"}, map[string]any{"uri": plOther, "name": "P"}}},
			"artists":   map[string]any{"items": []any{map[string]any{"uri": "spotify:artist:1Ra2hEbwVgAeeP3pc8vlfH", "name": "Tauren", "genres": []string{"ccm"}}}},
		}
	})
	res, err := (&Catalog{API: f.client()}).Search(ctx, "  echo ")
	if err != nil || len(res.Tracks) != 1 || len(res.Playlists) != 1 || len(res.Artists) != 1 || res.Albums == nil {
		t.Fatalf("%v %+v", err, res)
	}
	if res, _ := (&Catalog{API: f.client()}).Search(ctx, " "); len(res.Tracks) != 0 || f.count("GET /search") != 1 {
		t.Fatal("empty query hit the API")
	}
}

func TestLikeUsesLibraryEndpointWithURIs(t *testing.T) {
	f := newFake(t)
	f.json("PUT /me/library", 200, nil)
	f.json("DELETE /me/library", 200, nil)
	f.json("GET /me/library/contains", 200, []bool{true, false})
	cache := filepath.Join(t.TempDir(), "lib.json")
	_ = os.WriteFile(cache, []byte("{}"), 0o600)
	c := &Catalog{API: f.client(), CacheFile: cache}
	if err := c.Like(ctx, []string{trackA}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); err == nil {
		t.Fatal("cache not invalidated after a change")
	}
	if err := c.Like(ctx, []string{trackA, albumX}, false); err != nil {
		t.Fatal(err)
	}
	if f.count("DELETE /me/library?uris="+url.QueryEscape(trackA+","+albumX)) != 1 {
		t.Fatalf("calls %v", f.calls)
	}
	m, err := c.Liked(ctx, []string{trackA, trackB})
	if err != nil || !m[trackA] || m[trackB] {
		t.Fatalf("%v %v", err, m)
	}
	if err := c.Like(ctx, []string{"nope"}, true); err == nil {
		t.Fatal("bad URI accepted")
	}
	if err := c.Like(ctx, nil, true); err == nil {
		t.Fatal("empty list accepted")
	}
}

func TestPlaylistAddRemoveCreate(t *testing.T) {
	f := newFake(t)
	f.json("POST /playlists/3W46mmJBxfxjWhv65K4Vjk/items", 201, map[string]any{"snapshot_id": "s"})
	f.json("DELETE /playlists/3W46mmJBxfxjWhv65K4Vjk/items", 200, map[string]any{"snapshot_id": "s"})
	f.json("POST /me/playlists", 201, map[string]any{"uri": "spotify:playlist:NEWNEWNEWNEWNEWNEWNEW1", "name": "Gym", "owner": map[string]any{"id": "me"}})
	c := &Catalog{API: f.client()}
	if err := c.PlaylistAdd(ctx, plMine, []string{trackA, trackB}); err != nil {
		t.Fatal(err)
	}
	if b := f.lastBody("POST /playlists/3W46mmJBxfxjWhv65K4Vjk/items"); b != `{"uris":["`+trackA+`","`+trackB+`"]}` {
		t.Fatalf("body %s", b)
	}
	if err := c.PlaylistRemove(ctx, plMine, []string{trackA}); err != nil {
		t.Fatal(err)
	}
	if b := f.lastBody("DELETE /playlists/3W46mmJBxfxjWhv65K4Vjk/items"); b != `{"items":[{"uri":"`+trackA+`"}]}` {
		t.Fatalf("body %s", b)
	}
	if err := c.PlaylistAdd(ctx, albumX, []string{trackA}); err == nil {
		t.Fatal("added to an album")
	}
	if err := c.PlaylistAdd(ctx, plMine, []string{albumX}); err == nil {
		t.Fatal("added an album to a playlist")
	}
	e, err := c.PlaylistCreate(ctx, " Gym ")
	if err != nil || !e.Mine || e.Name != "Gym" || !strings.Contains(f.lastBody("POST /me/playlists"), `"public":false`) {
		t.Fatalf("%v %+v", err, e)
	}
	if _, err := c.PlaylistCreate(ctx, "  "); err == nil {
		t.Fatal("nameless playlist accepted")
	}
}

// ---------------------------------------------------------------- player

func TestPlayBodyVariants(t *testing.T) {
	cases := []struct {
		req  PlayRequest
		want string
	}{
		{PlayRequest{Context: plOther}, `{"context_uri":"` + plOther + `"}`},
		{PlayRequest{Context: plOther, Track: trackA, PositionMs: 5000}, `{"context_uri":"` + plOther + `","offset":{"uri":"` + trackA + `"},"position_ms":5000}`},
		{PlayRequest{Track: trackA}, `{"uris":["` + trackA + `"]}`},
		{PlayRequest{Context: "spotify:artist:1Ra2hEbwVgAeeP3pc8vlfH", Track: trackA}, `{"context_uri":"spotify:artist:1Ra2hEbwVgAeeP3pc8vlfH"}`},
		{PlayRequest{}, `{}`},
	}
	for _, c := range cases {
		b, err := playBody(c.req)
		got, _ := json.Marshal(b)
		if err != nil || string(got) != c.want {
			t.Errorf("%+v -> %s %v, want %s", c.req, got, err, c.want)
		}
	}
	if _, err := playBody(PlayRequest{Context: trackA}); err == nil {
		t.Error("a song accepted as a context")
	}
	if _, err := playBody(PlayRequest{Track: albumX}); err == nil {
		t.Error("an album accepted as a song")
	}
}

func TestPlayStartsSpotifydWhenThePCIsMissing(t *testing.T) {
	f := newFake(t)
	var up atomic.Bool
	f.on("GET /me/player/devices", func(*http.Request, string) (int, any) {
		if up.Load() {
			return 200, devicesBody("Omarchy")
		}
		return 200, devicesBody()
	})
	f.json("PUT /me/player/play", 204, nil)
	f.json("GET /me/player", 200, playerBody(true, "Omarchy", trackA, plOther, 10))
	sys := &fakeSys{onStart: func() { up.Store(true) }}
	if err := f.player(sys).Play(ctx, PlayRequest{Context: plOther, Track: trackA}); err != nil {
		t.Fatal(err)
	}
	if len(sys.started) != 1 || sys.started[0] != "spotifyd.service" {
		t.Fatalf("started %v", sys.started)
	}
	if f.count("PUT /me/player/play?device_id="+devID) != 1 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestPlayActivatesDeviceOnNoActiveDevice(t *testing.T) {
	f := newFake(t)
	f.json("GET /me/player/devices", 200, devicesBody("Omarchy"))
	var n int32
	f.on("PUT /me/player/play", func(*http.Request, string) (int, any) {
		if atomic.AddInt32(&n, 1) == 1 {
			return 404, map[string]any{"error": map[string]any{"reason": "NO_ACTIVE_DEVICE"}}
		}
		return 204, nil
	})
	f.json("PUT /me/player", 204, nil)
	f.json("GET /me/player", 200, playerBody(true, "Omarchy", trackA, "", 10))
	if err := f.player(&fakeSys{}).Play(ctx, PlayRequest{Track: trackA}); err != nil {
		t.Fatal(err)
	}
	if b := f.lastBody("PUT /me/player"); b != `{"device_ids":["`+devID+`"],"play":false}` {
		t.Fatalf("transfer body %s", b)
	}
}

func TestPlayThatNeverStartsIsReported(t *testing.T) {
	f := newFake(t)
	f.json("GET /me/player/devices", 200, devicesBody("Omarchy"))
	f.json("PUT /me/player/play", 204, nil)
	f.json("GET /me/player", 200, playerBody(false, "Omarchy", trackA, "", 0))
	p := f.player(&fakeSys{})
	start := time.Now()
	err := p.waitPlaying(ctx, devID, trackA, 30*time.Millisecond)
	if !errors.Is(err, ErrNotStarted) || time.Since(start) > 2*time.Second {
		t.Fatalf("got %v", err)
	}
}

func TestPlayingAnotherSongIsNotSuccess(t *testing.T) {
	f := newFake(t)
	f.json("GET /me/player", 200, playerBody(true, "Omarchy", trackB, "", 0))
	if err := f.player(&fakeSys{}).waitPlaying(ctx, devID, trackA, 20*time.Millisecond); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("got %v", err)
	}
	if err := f.player(&fakeSys{}).waitPlaying(ctx, devID, "", 20*time.Millisecond); err != nil {
		t.Fatalf("any song should do when none was asked: %v", err)
	}
}

func TestLocalDeviceGivesUpWithAClearMessage(t *testing.T) {
	f := newFake(t)
	f.json("GET /me/player/devices", 200, devicesBody("Phone"))
	_, err := f.player(&fakeSys{}).LocalDevice(ctx, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "spotifyd.service") {
		t.Fatalf("got %v", err)
	}
}

func TestResumeMovesPlaybackFromThePhone(t *testing.T) {
	f := newFake(t)
	f.json("GET /me/player/devices", 200, devicesBody("Omarchy", "Phone"))
	var moved atomic.Bool
	f.on("PUT /me/player", func(*http.Request, string) (int, any) { moved.Store(true); return 204, nil })
	f.on("GET /me/player", func(*http.Request, string) (int, any) {
		if moved.Load() {
			return 200, playerBody(true, "Omarchy", trackA, "", 100)
		}
		return 200, playerBody(true, "Phone", trackA, "", 100)
	})
	via, err := f.player(&fakeSys{}).Resume(ctx, LastURI(filepath.Join(t.TempDir(), "x")))
	if err != nil || via != "transfer" {
		t.Fatalf("%q %v", via, err)
	}
}

func TestResumeColdStartUsesLastSong(t *testing.T) {
	f := newFake(t)
	f.json("GET /me/player", 204, nil)
	f.json("GET /me/player/devices", 200, devicesBody("Omarchy"))
	f.json("PUT /me/player/play", 204, nil)
	last := LastURI(filepath.Join(t.TempDir(), "last"))
	if _, err := f.player(&fakeSys{}).Resume(ctx, last); err == nil {
		t.Fatal("nothing to resume should be an error")
	}
	last.Write("not a uri")
	if last.Read() != "" {
		t.Fatal("garbage remembered")
	}
	last.Write(trackA)
	f.json("GET /me/player", 200, playerBody(true, "Omarchy", trackA, "", 1))
	// first State call sees "playing on Omarchy": resume reports it; force the cold path:
	f.on("GET /me/player", func() func(*http.Request, string) (int, any) {
		var n int32
		return func(*http.Request, string) (int, any) {
			if atomic.AddInt32(&n, 1) == 1 {
				return 204, nil
			}
			return 200, playerBody(true, "Omarchy", trackA, "", 1)
		}
	}())
	via, err := f.player(&fakeSys{}).Resume(ctx, last)
	if err != nil || via != "last" || !strings.Contains(f.lastBody("PUT /me/player/play"), trackA) {
		t.Fatalf("%q %v", via, err)
	}
}

func TestQueueOnlyTakesSongs(t *testing.T) {
	f := newFake(t)
	f.json("POST /me/player/queue", 204, nil)
	p := f.player(&fakeSys{})
	if err := p.Queue(ctx, trackA); err != nil || f.count("POST /me/player/queue?uri="+url.QueryEscape(trackA)) != 1 {
		t.Fatalf("%v %v", err, f.calls)
	}
	if err := p.Queue(ctx, albumX); err == nil {
		t.Fatal("album queued")
	}
}

// ---------------------------------------------------------------- heal

func TestClassify(t *testing.T) {
	for line, want := range map[string]Signal{
		"[ERROR] Audio key response timeout":                                                 SigKeyTimeout,
		"[WARN] Unable to load key, continuing without decryption: Operation aborted":        SigKeyTimeout,
		"[ERROR] Unable to read audio file: Symphonia Decoder Error: end of stream":          SigDecode,
		"[WARN] unexpected shutdown":                                                         SigStopped,
		"[WARN] invalid mpeg audio header":                                                   SigNone,
		"[INFO] Loading <Echo> with Spotify URI <spotify:track:03YdcJcROMngj6U4VPr1X4>":      SigNone,
		"[WARN] couldn't load context info because: context is not available. type: Default": SigNone,
	} {
		if got := Classify(line); got != want {
			t.Errorf("%q -> %v, want %v", line, got, want)
		}
	}
}

func TestCleanRecentCacheOnlyTouchesRecentAudioShards(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	mk := func(rel string, age time.Duration) string {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte("x"), 0o600)
		_ = os.Chtimes(p, now.Add(-age), now.Add(-age))
		return p
	}
	fresh := mk("17/abc", time.Minute)
	old := mk("a8/def", time.Hour)
	creds := mk("oauth/credentials.json", time.Second)
	vol := mk("volume", time.Second)
	n, err := CleanRecentCache(dir, 10*time.Minute, now)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	for p, want := range map[string]bool{fresh: false, old: true, creds: true, vol: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists=%v want %v", p, err == nil, want)
		}
	}
	if n, err := CleanRecentCache(filepath.Join(dir, "missing"), time.Minute, now); n != 0 || err != nil {
		t.Fatal("missing dir should be fine")
	}
}

func healFake(t *testing.T, states ...map[string]any) (*fakeSpotify, *fakeSys, *Healer) {
	f := newFake(t)
	f.json("GET /me/player/devices", 200, devicesBody("Omarchy"))
	f.json("PUT /me/player/play", 204, nil)
	var i int32
	f.on("GET /me/player", func(*http.Request, string) (int, any) {
		k := int(atomic.AddInt32(&i, 1)) - 1
		if k >= len(states) {
			k = len(states) - 1
		}
		return 200, states[k]
	})
	sys := &fakeSys{}
	p := f.player(sys)
	h := &Healer{Player: p, Sys: sys, Window: 10 * time.Minute, Max: 3}
	return f, sys, h
}

func TestHealLeavesHealthyPlaybackAlone(t *testing.T) {
	_, sys, h := healFake(t, playerBody(true, "Omarchy", trackA, "", 1000), playerBody(true, "Omarchy", trackA, "", 3000))
	res, err := h.Heal(ctx, SigKeyTimeout)
	if err != nil || res != "healthy" || len(sys.restarted) != 0 {
		t.Fatalf("%q %v %v", res, err, sys.restarted)
	}
}

func TestHealIgnoresOtherDevices(t *testing.T) {
	_, sys, h := healFake(t, playerBody(false, "Phone", trackA, "", 1000))
	if res, _ := h.Heal(ctx, SigKeyTimeout); res != "not ours" || len(sys.restarted) != 0 {
		t.Fatalf("%q", res)
	}
}

func TestHealRestartsAndResumesAtThePosition(t *testing.T) {
	stuck := playerBody(true, "Omarchy", trackA, plOther, 42000)
	f, sys, h := healFake(t, stuck, stuck, playerBody(true, "Omarchy", trackA, plOther, 42100))
	res, err := h.Heal(ctx, SigKeyTimeout)
	if err != nil || res != "resumed" {
		t.Fatalf("%q %v", res, err)
	}
	if len(sys.restarted) != 1 || sys.restarted[0] != "spotifyd.service" {
		t.Fatalf("restarted %v", sys.restarted)
	}
	b := f.lastBody("PUT /me/player/play")
	if !strings.Contains(b, `"position_ms":42000`) || !strings.Contains(b, plOther) || !strings.Contains(b, trackA) {
		t.Fatalf("resume body %s", b)
	}
}

func TestHealRateLimitNotifiesAndStops(t *testing.T) {
	paused := playerBody(false, "Omarchy", trackA, "", 5000)
	_, sys, h := healFake(t, paused)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	h.Now = func() time.Time { return now }
	h.history = []time.Time{now.Add(-time.Minute), now.Add(-2 * time.Minute), now.Add(-3 * time.Minute)}
	res, err := h.Heal(ctx, SigKeyTimeout)
	if err == nil || res != "rate limited" || len(sys.restarted) != 0 || len(sys.notes) != 1 {
		t.Fatalf("%q %v restarted=%v notes=%v", res, err, sys.restarted, sys.notes)
	}
	h.history = []time.Time{now.Add(-11 * time.Minute)} // old heals expire
	if !h.Allowed() {
		t.Fatal("old heals still counted")
	}
}

func TestWatchMakesOneHealPerBurst(t *testing.T) {
	stuck := playerBody(false, "Omarchy", trackA, "", 1000)
	_, sys, h := healFake(t, stuck, stuck, playerBody(true, "Omarchy", trackA, "", 1100))
	log := strings.Repeat("[ERROR] Audio key response timeout\n[WARN] invalid mpeg audio header\n", 20)
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	pr, pw := ioPipe()
	go func() { _, _ = pw.Write([]byte(log)); time.Sleep(200 * time.Millisecond); pw.Close() }()
	_ = h.Watch(c, pr, 20*time.Millisecond)
	if len(sys.restarted) != 1 {
		t.Fatalf("restarted %d times", len(sys.restarted))
	}
}
