// Package controlhub is a thin HTTP client for control-hub's REST API. Every
// call carries the caller's own control-hub token, so control-hub enforces its
// normal permissions; this package adds none of its own.
package controlhub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
	// readOnly holds tokens that may only make GET requests (the console's
	// own service tokens). Writes with them fail before reaching control-hub.
	readOnly map[string]bool
}

// ErrReadOnlyToken is returned for a non-GET request made with a read-only token.
var ErrReadOnlyToken = errors.New("this control-hub token is read-only")

// WithReadOnlyTokens marks tokens that may only be used for GET requests.
func (c *Client) WithReadOnlyTokens(tokens ...string) *Client {
	if c.readOnly == nil {
		c.readOnly = map[string]bool{}
	}
	for _, t := range tokens {
		if t != "" {
			c.readOnly[t] = true
		}
	}
	return c
}

func New(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 60 * time.Second}}
}

// APIError is a non-2xx response from control-hub.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("control-hub returned %d: %s", e.Status, strings.TrimSpace(e.Body))
}

// LoginResponse is the subset of control-hub's login response the console needs.
type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Email    string `json:"email"`
	IsAdmin  bool   `json:"isAdmin"`
}

func (c *Client) Login(ctx context.Context, email, password string, validityDays int) (*LoginResponse, error) {
	body := map[string]any{"email": email, "password": password, "validity_days": validityDays}
	raw, err := c.do(ctx, "", http.MethodPost, "/api/auth/login", nil, body)
	if err != nil {
		return nil, err
	}
	var out LoginResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode login response: %w", err)
	}
	return &out, nil
}

// Get calls a GET endpoint and returns the raw JSON body.
func (c *Client) Get(ctx context.Context, token, path string, query url.Values) ([]byte, error) {
	return c.do(ctx, token, http.MethodGet, path, query, nil)
}

// Post calls a POST endpoint with a JSON body (nil for none).
func (c *Client) Post(ctx context.Context, token, path string, body any) ([]byte, error) {
	return c.do(ctx, token, http.MethodPost, path, nil, body)
}

func (c *Client) do(ctx context.Context, token, method, path string, query url.Values, body any) ([]byte, error) {
	if method != http.MethodGet && c.readOnly[token] {
		return nil, fmt.Errorf("%s %s: %w", method, path, ErrReadOnlyToken)
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		// control-hub's ExtractToken requires the "Bearer " prefix.
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Body: truncate(string(raw), 2000)}
	}
	return raw, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
