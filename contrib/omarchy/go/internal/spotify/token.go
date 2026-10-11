package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Token is the OAuth token of the user's own Spotify app. The JSON layout is
// the one spotify-player writes, so its cache can seed ours.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope,omitempty"`
}

// ErrNotConnected means there is no usable token: run spotify-bar-setup.
var ErrNotConnected = errors.New("Spotify is not connected: click Connect (spotify-bar-setup)")

// TokenStore keeps our own copy of the token and refreshes it with the
// refresh token (PKCE: no client secret). spotify-player used to be the only
// one refreshing it, and when its daemon was down the panel lost its lists.
type TokenStore struct {
	Path     string // our copy, 0600
	Seed     string // spotify-player's cache, read when Path does not exist yet
	ClientID string
	TokenURL string
	HTTP     *http.Client
	Now      func() time.Time

	mu  sync.Mutex
	tok *Token
}

var clientIDRe = regexp.MustCompile(`(?m)^\s*client_id\s*=\s*"([0-9a-f]{32})"`)

// ClientIDFromToml reads client_id from spotify-player's app.toml.
func ClientIDFromToml(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := clientIDRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

func (s *TokenStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func readToken(path string) (*Token, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	if t.AccessToken == "" && t.RefreshToken == "" {
		return nil, errors.New("empty token")
	}
	return &t, nil
}

func (s *TokenStore) load() *Token {
	if s.tok != nil {
		return s.tok
	}
	if t, err := readToken(s.Path); err == nil {
		s.tok = t
		return t
	}
	if s.Seed != "" {
		if t, err := readToken(s.Seed); err == nil {
			s.tok = t
			return t
		}
	}
	return nil
}

func (s *TokenStore) save(t *Token) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Connected reports whether a token (fresh or refreshable) exists.
func (s *TokenStore) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load() != nil
}

// Invalidate forces a refresh on the next Access (after a 401).
func (s *TokenStore) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.load(); t != nil {
		t.ExpiresAt = time.Time{}
	}
}

// Access returns a valid access token, refreshing it when it expires in < 60 s.
func (s *TokenStore) Access(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.load()
	if t == nil {
		return "", ErrNotConnected
	}
	if t.AccessToken != "" && t.ExpiresAt.Sub(s.now()) > 60*time.Second {
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" || s.ClientID == "" {
		return "", ErrNotConnected
	}
	nt, err := s.refresh(ctx, t)
	if errors.Is(err, ErrNotConnected) && s.Seed != "" {
		// spotify-player may have rotated the refresh token under us: its copy wins.
		if seed, serr := readToken(s.Seed); serr == nil && seed.RefreshToken != t.RefreshToken {
			if seed.AccessToken != "" && seed.ExpiresAt.Sub(s.now()) > 60*time.Second {
				nt, err = seed, nil
			} else if seed.RefreshToken != "" {
				nt, err = s.refresh(ctx, seed)
			}
		}
	}
	if err != nil {
		return "", err
	}
	s.tok = nt
	if err := s.save(nt); err != nil {
		return "", fmt.Errorf("saving the Spotify token: %w", err)
	}
	return nt.AccessToken, nil
}

func (s *TokenStore) refresh(ctx context.Context, t *Token) (*Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
		"client_id":     {s.ClientID},
	}
	tokenURL := s.TokenURL
	if tokenURL == "" {
		tokenURL = "https://accounts.spotify.com/api/token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Spotify login server unreachable: %w", err)
	}
	defer resp.Body.Close()
	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	if resp.StatusCode != http.StatusOK || r.AccessToken == "" {
		if r.Error == "invalid_grant" {
			return nil, ErrNotConnected
		}
		return nil, fmt.Errorf("Spotify token refresh failed (%d %s)", resp.StatusCode, r.Error)
	}
	nt := &Token{AccessToken: r.AccessToken, RefreshToken: t.RefreshToken, Scope: t.Scope,
		ExpiresAt: s.now().Add(time.Duration(r.ExpiresIn) * time.Second)}
	if r.RefreshToken != "" { // Spotify rotates it sometimes
		nt.RefreshToken = r.RefreshToken
	}
	if r.Scope != "" {
		nt.Scope = r.Scope
	}
	return nt, nil
}
