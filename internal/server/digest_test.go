package server

import (
	"strings"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func TestDigest(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour)
	h.st.SaveFinding(&store.Finding{ID: "F-1", Key: "a", Severity: "critical", Title: "metagraph CrashLoopBackOff", EntityName: "capgroup", State: "open", FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now})
	h.st.SaveFinding(&store.Finding{ID: "F-2", Key: "b", Severity: "critical", Title: "Postgres password rejected", EntityName: "cwan", State: "open", FirstSeenAt: old, LastSeenAt: now})
	resolvedAt := now.Add(-2 * time.Hour)
	h.st.SaveFinding(&store.Finding{ID: "F-3", Key: "c", Severity: "warning", Title: "DNS lookup failed", EntityName: "jde", State: "resolved", FirstSeenAt: old, LastSeenAt: now, ResolvedAt: &resolvedAt})
	h.st.SaveInvestigation(&store.Investigation{ID: "INC-1", Title: "capgroup — metagraph crash", Category: "athena-postdeploy", CreatedAt: now})
	h.st.SaveLearning(&store.Learning{ID: "L1", Status: store.LearningDraft, Title: "x", Symptoms: "y", RootCause: "z", LastSeen: now})

	d := h.server.BuildDigest(now)
	text := d.Slack("https://console")
	for _, want := range []string{"1 new critical, 0 new warnings, 1 resolved", "New: capgroup — metagraph CrashLoopBackOff",
		"cwan — Postgres password rejected (open 10 days)", "INC-1", "1 draft learnings wait for review", "<https://console/issues|open>"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if !strings.HasPrefix(d.Headline, "Week of ") {
		t.Errorf("headline = %s", d.Headline)
	}

	sre, admin := h.login("sre@prophecy.io"), h.login("admin@prophecy.io")
	if st := h.call(sre, "GET", "/api/digest", nil, nil); st != 403 {
		t.Errorf("non-admin preview %d", st)
	}
	var sent struct {
		Sent    bool   `json:"sent"`
		Message string `json:"message"`
	}
	if st := h.call(admin, "POST", "/api/digest/send", nil, &sent); st != 200 || sent.Sent || !strings.Contains(sent.Message, "muted") {
		t.Errorf("send without a webhook = %d %+v", st, sent)
	}
}
