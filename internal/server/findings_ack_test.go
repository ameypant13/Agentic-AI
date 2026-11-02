package server

import (
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func TestAcknowledgeRecordsWho(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	if err := h.st.SaveFinding(&store.Finding{ID: "F-1", Key: "k1", Severity: "critical", Title: "x", State: "open",
		EntityName: "cwan", FirstSeenAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	admin := h.login("admin@prophecy.io")
	var f map[string]any
	if st := h.call(admin, "POST", "/api/findings/F-1/ack", nil, &f); st != 200 {
		t.Fatalf("ack status %d", st)
	}
	if f["state"] != "acknowledged" || f["acknowledgedBy"] != "admin@prophecy.io" || f["acknowledgedByName"] != "admin" || f["acknowledgedAt"] == nil {
		t.Errorf("acknowledged finding = %v", f)
	}
	// Everyone sees who acknowledged it.
	var list []map[string]any
	h.call(h.login("sre@prophecy.io"), "GET", "/api/findings", nil, &list)
	if len(list) != 1 || list[0]["acknowledgedBy"] != "admin@prophecy.io" {
		t.Errorf("findings for another user = %v", list)
	}
}
