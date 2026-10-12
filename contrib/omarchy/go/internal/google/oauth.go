// Package google holds the user's own OAuth client (shared by Calendar and
// Photos), the tokens, the loopback sign-in with PKCE and an API helper.
package google

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	AuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL  = "https://oauth2.googleapis.com/token"
	RevokeURL = "https://oauth2.googleapis.com/revoke"
)

// NeedsSetup means the user must (re)run the setup command.
type NeedsSetup struct{ Msg string }

func (e *NeedsSetup) Error() string { return e.Msg }

// Client is ~/.config/omarchy-google/client.json.
type Client struct {
	ID     string `json:"client_id"`
	Secret string `json:"client_secret"`
}

// Token is the per-app token file.
type Token struct {
	Refresh string  `json:"refresh_token"`
	Access  string  `json:"access_token,omitempty"`
	Expires float64 `json:"expires,omitempty"`
	Since   int64   `json:"since,omitempty"`
}

// ReadJSON decodes a file (false if missing or broken).
func ReadJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// WriteJSON writes atomically; private files are 0600 in a 0700 folder.
func WriteJSON(path string, v any, private bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if private {
		_ = os.Chmod(dir, 0o700)
		mode = 0o600
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := strings.TrimSuffix(path, filepath.Ext(path)) + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	_ = os.Chmod(tmp, mode)
	return os.Rename(tmp, path)
}

// CleanSecret undoes a double paste into a hidden prompt: "GOCSPX-abcGOCSPX-abc" -> "GOCSPX-abc".
func CleanSecret(raw string) string {
	s := strings.Join(strings.Fields(raw), "")
	parts := strings.Split(s, "GOCSPX-")
	if strings.HasPrefix(s, "GOCSPX-") && len(parts) > 2 {
		same := true
		for _, p := range parts[2:] {
			same = same && p == parts[1]
		}
		if same {
			return "GOCSPX-" + parts[1]
		}
	}
	return s
}

// Auth refreshes and stores the access token of one app.
type Auth struct {
	ClientPath string
	TokenPath  string
	SetupCmd   string // "omarchy-gcal setup", shown in errors
	HTTP       *http.Client
	TokenURL   string
}

func (a *Auth) httpc() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (a *Auth) tokenURL() string {
	if a.TokenURL != "" {
		return a.TokenURL
	}
	return TokenURL
}

// PostForm sends a form and decodes the JSON answer.
func PostForm(hc *http.Client, u string, fields url.Values) (map[string]any, int, error) {
	resp, err := hc.PostForm(u, fields)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{"raw": string(b)}
	}
	return out, resp.StatusCode, nil
}

// Access returns a valid access token.
func (a *Auth) Access() (string, error) {
	var c Client
	var t Token
	if !ReadJSON(a.ClientPath, &c) || c.ID == "" {
		return "", &NeedsSetup{"no OAuth client yet: run " + a.SetupCmd}
	}
	if !ReadJSON(a.TokenPath, &t) || t.Refresh == "" {
		return "", &NeedsSetup{"not signed in: run " + a.SetupCmd}
	}
	if t.Access != "" && t.Expires > float64(time.Now().Unix()+60) {
		return t.Access, nil
	}
	r, code, err := PostForm(a.httpc(), a.tokenURL(), url.Values{"client_id": {c.ID}, "client_secret": {c.Secret},
		"refresh_token": {t.Refresh}, "grant_type": {"refresh_token"}})
	if err != nil {
		return "", err
	}
	if code != 200 {
		if r["error"] == "invalid_grant" {
			return "", &NeedsSetup{"Google withdrew the permission (expired or revoked): run " + a.SetupCmd + " again"}
		}
		return "", fmt.Errorf("Google token refresh failed (%d %v)", code, r["error"])
	}
	t.Access, _ = r["access_token"].(string)
	exp, _ := r["expires_in"].(float64)
	if exp == 0 {
		exp = 3600
	}
	t.Expires = float64(time.Now().Unix()) + exp
	return t.Access, WriteJSON(a.TokenPath, t, true)
}

// API calls a Google JSON API with the access token.
func (a *Auth) API(method, u string, body any, out any) error {
	tok, err := a.Access()
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpc().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		msg := string(b)
		if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return fmt.Errorf("Google: %d %s", resp.StatusCode, msg)
	}
	if out != nil && len(bytes.TrimSpace(b)) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Revoke forgets the sign-in at Google (best effort) and deletes the token.
func (a *Auth) Revoke() {
	var t Token
	if ReadJSON(a.TokenPath, &t) && t.Refresh != "" {
		_, _, _ = PostForm(a.httpc(), RevokeURL, url.Values{"token": {t.Refresh}})
	}
	_ = os.Remove(a.TokenPath)
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// PKCE returns a verifier and its S256 challenge.
func PKCE() (verifier, challenge string) {
	verifier = randomString(64)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// SignIn runs the loopback flow in the browser and stores the refresh token.
func (a *Auth) SignIn(scopes, okText string) error {
	var c Client
	if !ReadJSON(a.ClientPath, &c) || c.ID == "" {
		return errors.New("no OAuth client")
	}
	verifier, challenge := PKCE()
	state := randomString(16)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	redirect := "http://" + ln.Addr().String()
	got := make(chan url.Values, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "wrong state", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if q.Get("code") != "" {
			fmt.Fprintf(w, "<h2>%s</h2>", okText)
		} else {
			fmt.Fprintf(w, "<h2>No se recibió el permiso: %s</h2>", q.Get("error"))
		}
		select {
		case got <- q:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	u := AuthURL + "?" + url.Values{"client_id": {c.ID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {scopes},
		"access_type": {"offline"}, "prompt": {"consent"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}.Encode()
	fmt.Println("\nOpening the browser to sign in with Google. If it does not open, visit:\n" + u + "\n")
	fmt.Println(`If Google says "app isn't verified": Advanced → Go to <your app> (it is your own app).`)
	_ = exec.Command("xdg-open", u).Start()
	var q url.Values
	select {
	case q = <-got:
	case <-time.After(5 * time.Minute):
		return errors.New("no permission received (timed out)")
	}
	if q.Get("code") == "" {
		return fmt.Errorf("no permission received (%s)", q.Get("error"))
	}
	r, code, err := PostForm(a.httpc(), a.tokenURL(), url.Values{"client_id": {c.ID}, "client_secret": {c.Secret}, "code": {q.Get("code")},
		"code_verifier": {verifier}, "redirect_uri": {redirect}, "grant_type": {"authorization_code"}})
	if err != nil {
		return err
	}
	ref, _ := r["refresh_token"].(string)
	if code != 200 || ref == "" {
		return fmt.Errorf("Google gave no refresh token (%d %v); run setup again", code, r["error"])
	}
	acc, _ := r["access_token"].(string)
	exp, _ := r["expires_in"].(float64)
	if exp == 0 {
		exp = 3600
	}
	return WriteJSON(a.TokenPath, Token{Refresh: ref, Access: acc, Expires: float64(time.Now().Unix()) + exp, Since: time.Now().Unix()}, true)
}

// AskClient asks for the OAuth client in the terminal (the secret hidden) and stores it.
func AskClient(path string, instructions string) error {
	fmt.Println(instructions)
	in := bufio.NewReader(os.Stdin)
	fmt.Print("Client ID (ends in .apps.googleusercontent.com): ")
	id, _ := in.ReadString('\n')
	id = strings.TrimSpace(id)
	if !strings.HasSuffix(id, ".apps.googleusercontent.com") {
		return errors.New("that does not look like a Google client ID")
	}
	fmt.Print("Client secret (hidden, paste once): ")
	_ = exec.Command("stty", "-F", "/dev/tty", "-echo").Run()
	sec, _ := in.ReadString('\n')
	_ = exec.Command("stty", "-F", "/dev/tty", "echo").Run()
	fmt.Println()
	sec = CleanSecret(sec)
	if sec == "" {
		return errors.New("the secret cannot be empty")
	}
	fmt.Printf("Secret received: %s…%s (%d characters)\n", sec[:min(7, len(sec))], sec[max(0, len(sec)-3):], len(sec))
	return WriteJSON(path, Client{ID: id, Secret: sec}, true)
}
