package server

import (
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func TestReadinessEndpoint(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.st.SaveFinding(&store.Finding{ID: "F-9", Key: "k9", Severity: "critical", Title: "metagraph CrashLoopBackOff", State: "open",
		EntityName: "capgroup", FirstSeenAt: now, LastSeenAt: now})
	c := h.login("sre@prophecy.io")
	var rep struct {
		Verdict string `json:"verdict"`
		Summary string `json:"summary"`
		Checks  []struct {
			ID, Status, Detail string
		} `json:"checks"`
	}
	if st := h.call(c, "GET", "/api/readiness?cp=capgroup", nil, &rep); st != 200 {
		t.Fatalf("status %d", st)
	}
	got := map[string]string{}
	for _, ch := range rep.Checks {
		got[ch.ID] = ch.Status + ": " + ch.Detail
	}
	if rep.Verdict != "no-go" || got["control-plane"][:4] != "fail" || got["findings"][:4] != "fail" {
		t.Errorf("verdict %s %s\n%v", rep.Verdict, rep.Summary, got)
	}
	if st := h.call(c, "GET", "/api/readiness?cp=nobody-at-all", nil, nil); st != 404 {
		t.Errorf("unknown control plane: %d", st)
	}
	if st := h.call(c, "GET", "/api/readiness", nil, nil); st != 400 {
		t.Errorf("missing cp: %d", st)
	}
}
