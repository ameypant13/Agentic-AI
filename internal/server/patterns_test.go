package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/testutil"
)

func TestLogPatternsAndDraft(t *testing.T) {
	line := "2026-10-17 10:00:01 ERROR c.p.m.Db - Connection to 10.0.4.12:5432 refused"
	h := newHarness(t, []testutil.Block{{Tool: "propose_log_rule", ID: "t1", Input: map[string]any{
		"applicable": true, "id": "pg-refused", "title": "Postgres connection refused", "services": []string{"metagraph"},
		"pattern": `Connection to [^ ]+:5432 refused`, "severity": "error", "meaning": "m", "fix": "f", "examples": []string{line}}}})
	now := time.Now()
	h.st.SaveLogPatternCounts([]store.LogPattern{
		{ID: "P-1", Service: "metagraph-prophecy", Template: "<*> ERROR c.p.m.Db - Connection to <*> refused", Example: line, Count: 40,
			Clusters: `{"preview":40}`, Hourly: `{}`, FirstSeen: now.Add(-2 * time.Hour), LastSeen: now, Status: "new"},
		{ID: "P-2", Service: "sla-tests", Template: "Error Context: <*>", Example: "Error Context: x", Count: 3, Clusters: `{}`, Hourly: `{}`,
			FirstSeen: now.Add(-72 * time.Hour), LastSeen: now, Status: "new"},
	})
	sre, admin := h.login("sre@prophecy.io"), h.login("admin@prophecy.io")
	if st := h.call(sre, "GET", "/api/log-patterns", nil, nil); st != 403 {
		t.Errorf("non-admin list %d", st)
	}
	var list []patternView
	h.call(admin, "GET", "/api/log-patterns", nil, &list)
	if len(list) != 2 || list[0].ID != "P-1" || !list[0].IsNew || list[0].Clusters["preview"] != 40 {
		t.Fatalf("list = %+v", list)
	}
	if st := h.call(admin, "PUT", "/api/log-patterns/P-2", map[string]string{"status": "ignored"}, nil); st != 204 {
		t.Errorf("ignore %d", st)
	}
	h.call(admin, "GET", "/api/log-patterns?status=new", nil, &list)
	if len(list) != 1 {
		t.Errorf("after ignore = %+v", list)
	}
	// Counter updates from the miner keep the admin's status.
	h.st.SaveLogPatternCounts([]store.LogPattern{{ID: "P-2", Service: "sla-tests", Template: "Error Context: <*>", Count: 9, Status: "new", LastSeen: now}})
	if p, _ := h.st.GetLogPattern("P-2"); p.Status != "ignored" || p.Count != 9 {
		t.Errorf("status lost on update: %+v", p)
	}
	var draft struct {
		Applicable bool `json:"applicable"`
		Rule       struct {
			ID, Pattern string
			Services    []string
		} `json:"rule"`
	}
	if st := h.call(admin, "POST", "/api/log-patterns/P-1/draft-rule", nil, &draft); st != 200 {
		t.Fatalf("draft status %d", st)
	}
	if !draft.Applicable || draft.Rule.ID != "pg-refused" {
		t.Errorf("draft = %+v", draft)
	}
	// The prompt named the service without the -prophecy suffix.
	if b, _ := jsonMarshal(h.claude.Requests[0]["messages"]); !contains(b, `service \"metagraph\"`) {
		t.Errorf("prompt = %s", b)
	}
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
