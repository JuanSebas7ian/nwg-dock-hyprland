package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// TokenSource hands out access tokens.
type TokenSource interface {
	Access(ctx context.Context) (string, error)
	Invalidate()
}

// APIError is a non-2xx answer of the Web API.
type APIError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	switch {
	case e.Status == http.StatusTooManyRequests:
		return fmt.Sprintf("Spotify rate limit, try again in %ds", int(e.RetryAfter.Seconds()+0.5))
	case e.Status == http.StatusNotFound && e.Message != "":
		return "Spotify: " + e.Message
	case e.Message != "":
		return fmt.Sprintf("Spotify error %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("Spotify error %d", e.Status)
}

// IsStatus reports whether err is an APIError with that HTTP status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// Client talks to the Spotify Web API with retries that keep the panel usable:
// one refresh on 401, a short wait on 429 (if Spotify asks for <= 5 s) and one
// retry on 5xx.
type Client struct {
	Base   string
	HTTP   *http.Client
	Tokens TokenSource
	Sleep  func(time.Duration)
}

func (c *Client) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Do sends one request. body is JSON-encoded when not nil; out is decoded when
// not nil and the answer has a body.
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	base := c.Base
	if base == "" {
		base = "https://api.spotify.com/v1"
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	refreshed, waited, retried5xx := false, false, false
	for {
		tok, err := c.Tokens.Access(ctx)
		if err != nil {
			return err
		}
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		} else if method == http.MethodPut || method == http.MethodPost {
			req.ContentLength = 0
		}
		resp, err := hc.Do(req)
		if err != nil {
			return fmt.Errorf("Spotify unreachable: %w", err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusUnauthorized && !refreshed:
			refreshed = true
			c.Tokens.Invalidate()
			continue
		case resp.StatusCode == http.StatusTooManyRequests:
			ra := retryAfter(resp.Header.Get("Retry-After"))
			if !waited && ra <= 5*time.Second {
				waited = true
				c.sleep(ra)
				continue
			}
			return &APIError{Status: resp.StatusCode, RetryAfter: ra}
		case resp.StatusCode >= 500 && !retried5xx:
			retried5xx = true
			c.sleep(500 * time.Millisecond)
			continue
		case resp.StatusCode >= 300:
			return &APIError{Status: resp.StatusCode, Message: errorMessage(data)}
		}
		if out != nil && len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("Spotify sent an unexpected answer: %w", err)
			}
		}
		return nil
	}
}

func retryAfter(h string) time.Duration {
	if n, err := strconv.Atoi(h); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return time.Second
}

func errorMessage(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Reason  string `json:"reason"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil {
		if e.Error.Reason == "NO_ACTIVE_DEVICE" {
			return "no active device"
		}
		return e.Error.Message
	}
	return ""
}
