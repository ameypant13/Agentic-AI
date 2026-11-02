package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/testutil"
)

// fakeSlack records Web API calls; users.info maps U1 to sre@prophecy.io.
type fakeSlack struct {
	*httptest.Server
	mu    sync.Mutex
	calls []map[string]any
}

func newFakeSlack(t *testing.T) *fakeSlack {
	f := &fakeSlack{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		method := strings.TrimPrefix(r.URL.Path, "/")
		body["method"] = method
		f.mu.Lock()
		f.calls = append(f.calls, body)
		f.mu.Unlock()
		switch method {
		case "users.info":
			email := map[string]string{"U1": "sre@prophecy.io", "U2": "stranger@prophecy.io"}[body["user"].(string)]
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"profile": map[string]any{"email": email}}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1700000000.000200"})
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// wait returns the text of the first call matching method whose text contains want.
func (f *fakeSlack) wait(t *testing.T, method, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, c := range f.calls {
			if txt, _ := c["text"].(string); c["method"] == method && strings.Contains(txt, want) {
				f.mu.Unlock()
				return txt
			}
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("no %s containing %q; calls: %v", method, want, f.calls)
	return ""
}

func (h *harness) slackPost(body string, secret string) int {
	h.t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":" + body))
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/slack/events", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func mention(eventID, user, text string) string {
	b, _ := json.Marshal(map[string]any{"type": "event_callback", "event_id": eventID, "event": map[string]any{
		"type": "app_mention", "user": user, "text": "<@UBOT> " + text, "channel": "C1", "ts": "1700000000.000100"}})
	return string(b)
}

func TestSlackCopilot(t *testing.T) {
	h := newHarness(t, []testutil.Block{{Text: "**capgroup** post-deploy failed at the DNS step."}})
	slack := newFakeSlack(t)
	h.server.cfg.SlackBotToken, h.server.cfg.SlackSigningSecret = "xoxb-test", "s3cret"
	h.server.slackBaseURL = slack.URL
	h.login("sre@prophecy.io") // a console session links the Slack user

	if st := h.slackPost(`{"type":"url_verification","challenge":"abc"}`, "wrong"); st != 401 {
		t.Errorf("bad signature: %d", st)
	}
	if st := h.slackPost(`{"type":"url_verification","challenge":"abc"}`, "s3cret"); st != 200 {
		t.Errorf("url_verification: %d", st)
	}

	if st := h.slackPost(mention("Ev1", "U1", "why is capgroup failing?"), "s3cret"); st != 200 {
		t.Fatalf("event: %d", st)
	}
	got := slack.wait(t, "chat.update", "post-deploy failed")
	if !strings.Contains(got, "*capgroup*") || !strings.Contains(got, "/copilot/") {
		t.Errorf("answer = %q", got)
	}
	// A retry of the same event is ignored: Claude was asked once.
	h.slackPost(mention("Ev1", "U1", "why is capgroup failing?"), "s3cret")
	time.Sleep(200 * time.Millisecond)
	if n := h.claude.RequestCount(); n != 1 {
		t.Errorf("claude requests = %d, want 1", n)
	}

	// Someone without a console session is told to sign in; Claude is not called.
	h.slackPost(mention("Ev2", "U2", "hello"), "s3cret")
	slack.wait(t, "chat.postMessage", "Sign in to the console")
}
