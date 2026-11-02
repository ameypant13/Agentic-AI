package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/config"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/metricrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/parser"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/testutil"
)

type harness struct {
	t      *testing.T
	server *Server
	srv    *httptest.Server
	ch     *testutil.FakeControlHub
	claude *testutil.FakeClaude
	st     *store.Store
}

func newHarness(t *testing.T, script ...[]testutil.Block) *harness {
	t.Helper()
	ch := testutil.NewFakeControlHub()
	t.Cleanup(ch.Close)
	claude := testutil.NewFakeClaude(script...)
	t.Cleanup(claude.Close)

	st, err := store.Open("file:" + filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := secret.NewBox([]byte("0123456789abcdef0123456789abcdef"))
	cfg := &config.Config{ControlHubURL: ch.URL, SharedAnthropicKey: "sk-ant-shared", StaticDir: t.TempDir()}
	client := controlhub.New(ch.URL)
	ag := agent.New(st, client, nil, nil, "claude-opus-5-5", "high").
		WithClientOptions(option.WithBaseURL(claude.URL), option.WithMaxRetries(0))
	lib, err := parser.NewLibrary(st)
	if err != nil {
		t.Fatal(err)
	}
	reg := kube.NewRegistry(kube.Credentials{})
	worker, err := parser.NewWorker(parser.WorkerConfig{Dir: t.TempDir()}, lib, st, reg)
	if err != nil {
		t.Fatal(err)
	}
	hub := grafana.NewHub()
	metrics, err := metricrules.NewWorker(hub, t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, Deps{Store: st, CH: client, Box: box, Agent: ag, Kube: reg, Rules: lib, LogWork: worker, Grafana: hub, Metrics: metrics})
	s.verifyKey = func(context.Context, string) error { return nil }
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &harness{t: t, server: s, srv: srv, ch: ch, claude: claude, st: st}
}

// login returns a client holding the session cookie.
func (h *harness) login(email string) *http.Client {
	h.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	var me map[string]any
	if status := h.call(c, "POST", "/api/auth/login", map[string]string{"email": email, "password": "pw"}, &me); status != 200 {
		h.t.Fatalf("login %s: status %d", email, status)
	}
	return c
}

func (h *harness) call(c *http.Client, method, path string, body, out any) int {
	h.t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

type sse struct {
	event string
	data  map[string]any
}

func (h *harness) send(c *http.Client, chatID, text string) []sse {
	h.t.Helper()
	raw, _ := json.Marshal(map[string]string{"text": text})
	resp, err := c.Post(h.srv.URL+"/api/chats/"+chatID+"/messages", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		h.t.Fatalf("send: status %d", resp.StatusCode)
	}
	var events []sse
	var ev sse
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			ev = sse{event: strings.TrimPrefix(line, "event: ")}
		case strings.HasPrefix(line, "data: "):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.data)
			events = append(events, ev)
		}
	}
	return events
}

func investigationScript() [][]testutil.Block {
	return [][]testutil.Block{
		{{Tool: "find_control_plane", ID: "toolu_1", Input: map[string]any{"query": "capgroup"}}},
		{{Tool: "get_job", ID: "toolu_2", Input: map[string]any{"job_id": "job-1"}}},
		{
			{Tool: "record_investigation", ID: "toolu_3", Input: map[string]any{
				"title": "capgroup — DNS step timed out", "entity_id": "cp-cap", "entity_name": "capgroup",
				"category": "networking-dns", "confidence": "high",
				"summary_for_requester": "An issue on our side stopped the DNS step.",
				"root_cause":            "Ingress LoadBalancer never got an address; subnets missing the elb role tag.",
				"recommended_fix":       "Tag public subnets at VPC creation."}},
			{Tool: "propose_action", ID: "toolu_4", Input: map[string]any{
				"kind": "retry_cp_postdeploy", "control_plane_id": "cp-cap", "target_name": "capgroup",
				"reason": "Post-deploy failed on a transient error."}},
		},
		{{Text: "An issue on our side stopped the DNS step. Reference INC-x."}},
	}
}

func TestCopilotInvestigationAsNonAdmin(t *testing.T) {
	h := newHarness(t, investigationScript()...)
	sre := h.login("sre@prophecy.io")

	var chat map[string]any
	h.call(sre, "POST", "/api/chats", map[string]string{}, &chat)
	chatID := chat["id"].(string)
	events := h.send(sre, chatID, "provisioning failing for capgroup")

	var steps, text []string
	var actionID, invID string
	var done bool
	for _, e := range events {
		switch e.event {
		case "step":
			if _, leaked := e.data["tool"]; leaked {
				t.Errorf("non-admin step exposes the tool name: %v", e.data)
			}
			steps = append(steps, e.data["label"].(string))
		case "step_done":
			if _, leaked := e.data["summary"]; leaked {
				t.Errorf("non-admin step_done exposes the tool output: %v", e.data)
			}
		case "text":
			text = append(text, e.data["delta"].(string))
		case "action":
			actionID = e.data["id"].(string)
		case "investigation":
			invID = e.data["id"].(string)
		case "done":
			done = true
			if e.data["keySource"] != "shared" {
				t.Errorf("keySource = %v, want shared", e.data["keySource"])
			}
		case "error":
			t.Fatalf("turn failed: %v", e.data)
		}
	}
	if !done || actionID == "" || invID == "" || len(steps) != 4 {
		t.Fatalf("done=%v action=%q investigation=%q steps=%v", done, actionID, invID, steps)
	}
	if !strings.Contains(strings.Join(text, ""), "DNS step") {
		t.Errorf("reply text missing: %q", strings.Join(text, ""))
	}

	// The model saw the control plane, but never the post-deploy password.
	second, _ := json.Marshal(h.claude.Requests[1]["messages"])
	for _, want := range []string{`\"id\":\"cp-cap\"`, "CREATION_FAILED", "The DNS step timed out"} {
		if !strings.Contains(string(second), want) {
			t.Errorf("tool result to the model is missing %s: %s", want, second)
		}
	}
	if strings.Contains(string(second), "hunter2") {
		t.Errorf("post-deploy password reached the model: %s", second)
	}
	// Refusal fallback is opted into.
	if h.claude.Requests[0]["fallbacks"] != "default" ||
		!strings.Contains(h.claude.Headers[0].Get("anthropic-beta"), "server-side-fallback-2026-07-01") {
		t.Errorf("fallbacks not requested: %v / %q", h.claude.Requests[0]["fallbacks"], h.claude.Headers[0].Get("anthropic-beta"))
	}

	// The requester sees the summary, not the root cause.
	var inv map[string]any
	h.call(sre, "GET", "/api/investigations/"+invID, nil, &inv)
	if _, leaked := inv["rootCause"]; leaked {
		t.Errorf("non-admin can read the root cause: %v", inv)
	}
	if inv["summaryForRequester"] == "" {
		t.Errorf("summary missing: %v", inv)
	}

	// An admin sees the root cause and the evidence trail.
	admin := h.login("admin@prophecy.io")
	h.call(admin, "GET", "/api/investigations/"+invID, nil, &inv)
	if !strings.Contains(inv["rootCause"].(string), "elb role tag") {
		t.Errorf("admin root cause: %v", inv["rootCause"])
	}
	if ev, _ := inv["evidence"].([]any); len(ev) != 2 {
		t.Errorf("evidence = %v, want find_control_plane and get_job", inv["evidence"])
	}

	// Another user can't read or act on this chat.
	if status := h.call(admin, "GET", "/api/chats/"+chatID, nil, nil); status != 404 {
		t.Errorf("admin reading someone else's chat: status %d", status)
	}
	if status := h.call(admin, "POST", "/api/actions/"+actionID+"/execute", nil, nil); status != 404 {
		t.Errorf("admin running someone else's action: status %d", status)
	}

	// Nothing ran in control-hub until the requester clicked.
	if posts := h.ch.PostLog(); len(posts) != 0 {
		t.Fatalf("control-hub was changed before the click: %v", posts)
	}
	var act map[string]any
	if status := h.call(sre, "POST", "/api/actions/"+actionID+"/execute", nil, &act); status != 200 || act["status"] != "succeeded" {
		t.Fatalf("execute: status %d, %v", status, act)
	}
	if posts := h.ch.PostLog(); len(posts) != 1 || !strings.Contains(posts[0], "/api/control-planes/cp-cap/postdeploy?realm=sap") {
		t.Errorf("control-hub calls = %v", posts)
	}
	if status := h.call(sre, "POST", "/api/actions/"+actionID+"/execute", nil, nil); status != 409 {
		t.Errorf("second click: status %d, want 409", status)
	}

	// The reloaded chat keeps steps (labels only) and the action.
	var reloaded struct {
		Items []map[string]any `json:"items"`
	}
	h.call(sre, "GET", "/api/chats/"+chatID, nil, &reloaded)
	if len(reloaded.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(reloaded.Items))
	}
	reply := reloaded.Items[1]
	if steps := reply["steps"].([]any); len(steps) != 4 {
		t.Errorf("reloaded steps = %v", steps)
	}
	if reply["keySource"] != "shared" {
		t.Errorf("reloaded keySource = %v", reply["keySource"])
	}
}

func TestPersonalKeyAndAuth(t *testing.T) {
	h := newHarness(t)
	anon := &http.Client{}
	if status := h.call(anon, "GET", "/api/me", nil, nil); status != 401 {
		t.Errorf("anonymous /api/me: %d", status)
	}
	if status := h.call(anon, "POST", "/api/auth/login", map[string]string{"email": "x@y", "password": "bad"}, nil); status != 401 {
		t.Errorf("bad login: %d", status)
	}

	c := h.login("sre@prophecy.io")
	var key map[string]any
	if status := h.call(c, "PUT", "/api/me/claude-key", map[string]string{"apiKey": "not-a-key"}, &key); status != 400 {
		t.Errorf("malformed key accepted: %d", status)
	}
	h.call(c, "PUT", "/api/me/claude-key", map[string]string{"apiKey": "sk-ant-api03-personal-abcd"}, &key)
	if key["source"] != "personal" || key["last4"] != "abcd" {
		t.Errorf("after save: %v", key)
	}
	k, err := h.st.GetUserKey("sre@prophecy.io")
	if err != nil || strings.Contains(k.KeySealed, "personal") {
		t.Errorf("key not stored encrypted: %+v %v", k, err)
	}
	h.call(c, "DELETE", "/api/me/claude-key", nil, &key)
	if key["source"] != "shared" {
		t.Errorf("after delete: %v", key)
	}
	if status := h.call(c, "GET", "/api/audit", nil, nil); status != 403 {
		t.Errorf("non-admin audit: %d", status)
	}
}

func TestReadOnlyRefusesActions(t *testing.T) {
	h := newHarness(t, investigationScript()...)
	// Propose an action normally, then flip the server to read-only and try to run it.
	sre := h.login("sre@prophecy.io")
	var chat map[string]any
	h.call(sre, "POST", "/api/chats", map[string]string{}, &chat)
	var actionID string
	for _, e := range h.send(sre, chat["id"].(string), "provisioning failing for capgroup") {
		if e.event == "action" {
			actionID = e.data["id"].(string)
		}
	}
	if actionID == "" {
		t.Fatal("no action proposed")
	}
	h.server.cfg.ReadOnly = true
	if status := h.call(sre, "POST", "/api/actions/"+actionID+"/execute", nil, nil); status != 403 {
		t.Errorf("execute in read-only: status %d, want 403", status)
	}
	if posts := h.ch.PostLog(); len(posts) != 0 {
		t.Errorf("control-hub changed in read-only mode: %v", posts)
	}
	var me map[string]any
	h.call(sre, "GET", "/api/me", nil, &me)
	if me["readOnly"] != true {
		t.Errorf("me = %v", me)
	}
}

func TestDocsNeedSignIn(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Docs</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.server.cfg.DocsDir = dir
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(h.srv.URL + "/docs/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login?next=%2Fdocs%2F" {
		t.Errorf("anonymous: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	c := h.login("sre@prophecy.io")
	resp, err = c.Get(h.srv.URL + "/docs/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Docs") {
		t.Errorf("signed in: %d %s", resp.StatusCode, body)
	}
}
