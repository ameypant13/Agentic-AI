// Package testutil provides fake control-hub and Claude API servers for tests.
package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Tokens the fake control-hub issues.
const (
	AdminToken = "tok-admin"
	UserToken  = "tok-user"
)

// FakeControlHub mimics the control-hub endpoints the console uses.
type FakeControlHub struct {
	*httptest.Server
	mu    sync.Mutex
	Posts []string // "METHOD path?query body" of every POST after login

	// ControlPlanes per realm, as control-hub would return them.
	ControlPlanes map[string][]map[string]any
	// ExecutionPlanes as control-hub returns them (gorm "ID":0 next to "id").
	ExecutionPlanes []map[string]any
	// Infrastructures for GET /api/infrastructures (v2 clusters).
	Infrastructures []map[string]any
}

func NewFakeControlHub() *FakeControlHub {
	f := &FakeControlHub{ControlPlanes: map[string][]map[string]any{
		"sap": {{
			"ID": 0, // gorm.Model's numeric ID, as real control-hub sends it
			"id": "cp-cap",
			"config": map[string]any{"customerName": "capgroup", "controlPlaneUrl": "capgroup.app.prophecy.ai",
				"request": map[string]any{"provider": "gcp", "region": "us-east1", "tenantName": "cap"}},
			"state":         map[string]any{"status": "CREATION_FAILED", "postDeployStatus": "failed"},
			"failureReason": map[string]any{"message": "The DNS step timed out", "action": "Retry", "reference": "job-1"},
			"users":         []any{map[string]any{"username": "admin", "password": "hunter2"}},
		}},
		"dedicated-saas": {
			{"ID": 0, "id": "cp-ok", "customerName": "acme", "status": "OPERATIONAL", "provider": "aws", "region": "us-east-1"},
			{"ID": 0, "id": "cp-stuck", "customerName": "slowco", "status": "CREATING", "provider": "azure", "region": "eastus"},
		},
		"public-saas": {},
	}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *FakeControlHub) PostLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Posts...)
}

func (f *FakeControlHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/auth/login" {
		var in struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch {
		case in.Email == "admin@prophecy.io" && in.Password == "pw":
			writeJSON(w, map[string]any{"token": AdminToken, "email": in.Email, "username": "admin", "isAdmin": true})
		case in.Email == "sre@prophecy.io" && in.Password == "pw":
			writeJSON(w, map[string]any{"token": UserToken, "email": in.Email, "username": "sre", "isAdmin": false})
		default:
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		}
		return
	}
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+AdminToken && auth != "Bearer "+UserToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.Posts = append(f.Posts, strings.TrimSpace(fmt.Sprintf("POST %s?%s %s", r.URL.Path, r.URL.RawQuery, body)))
		f.mu.Unlock()
		writeJSON(w, map[string]any{"jobID": "pd-1", "message": "post-deploy triggered"})
		return
	}
	switch {
	case r.URL.Path == "/api/control-planes":
		realm := r.URL.Query().Get("realm")
		if realm == "" {
			realm = "sap"
		}
		writeJSON(w, f.ControlPlanes[realm])
	case r.URL.Path == "/api/infrastructures":
		infras := f.Infrastructures
		if infras == nil {
			infras = []map[string]any{}
		}
		writeJSON(w, map[string]any{"infrastructures": infras, "count": len(infras)})
	case r.URL.Path == "/api/execution-planes":
		eps := f.ExecutionPlanes
		if eps == nil {
			eps = []map[string]any{}
		}
		writeJSON(w, eps)
	case strings.HasPrefix(r.URL.Path, "/api/control-planes/"), strings.HasPrefix(r.URL.Path, "/api/execution-planes/"):
		// One plane by ID, for action dry runs: any ID is a failed post-deploy.
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		writeJSON(w, map[string]any{"id": id, "status": "OPERATIONAL", "postDeployStatus": "FAILED", "prophecyVersion": "4.2.11.5"})
	case r.URL.Path == "/api/jobs/job-1":
		writeJSON(w, map[string]any{"requestId": "job-1", "status": "FAILED", "message": "Creating DNS record",
			"errorDetails": "timeout: failed to get ingress address after 5 minutes"})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// FakeClaude replays scripted streaming responses, one per request, and keeps
// every request body.
type FakeClaude struct {
	*httptest.Server
	mu       sync.Mutex
	Script   [][]Block
	Requests []map[string]any
	Headers  []http.Header
}

// Block is one content block of a scripted reply: text, or a tool call.
type Block struct {
	Text  string
	Tool  string
	ID    string
	Input map[string]any
}

func NewFakeClaude(script ...[]Block) *FakeClaude {
	f := &FakeClaude{Script: script}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *FakeClaude) serve(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	n := len(f.Requests)
	f.Requests = append(f.Requests, body)
	f.Headers = append(f.Headers, r.Header.Clone())
	f.mu.Unlock()
	if n >= len(f.Script) {
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"script exhausted"}}`, 400)
		return
	}
	blocks := f.Script[n]

	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event string, data any) {
		raw, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
	}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": fmt.Sprintf("msg_%d", n), "type": "message", "role": "assistant", "model": "claude-opus-5-5",
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 0}}})
	stop := "end_turn"
	for i, b := range blocks {
		if b.Tool != "" {
			stop = "tool_use"
			send("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "tool_use", "id": b.ID, "name": b.Tool, "input": map[string]any{}}})
			in, _ := json.Marshal(b.Input)
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": i,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": string(in)}})
		} else {
			send("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "text", "text": ""}})
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": i,
				"delta": map[string]any{"type": "text_delta", "text": b.Text}})
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	send("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 20}})
	send("message_stop", map[string]any{"type": "message_stop"})
}

// RequestCount is the number of Claude API calls made so far.
func (f *FakeClaude) RequestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Requests)
}
