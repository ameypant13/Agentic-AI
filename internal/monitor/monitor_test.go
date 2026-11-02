package monitor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/config"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/testutil"
)

func TestMonitorLifecycle(t *testing.T) {
	ch := testutil.NewFakeControlHub()
	defer ch.Close()
	ch.ExecutionPlanes = []map[string]any{{"ID": 0, "id": "ep-1",
		"config": map[string]any{"customerName": "epco", "request": map[string]any{"provider": "gcp"}},
		"state":  map[string]any{"status": "CREATION_FAILED"}}}

	var mu sync.Mutex
	var posted []string
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		posted = append(posted, string(b))
		mu.Unlock()
	}))
	defer slack.Close()

	st, err := store.Open("file:" + filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ServiceToken: testutil.AdminToken, StuckAfter: 30 * time.Minute, CertWarnDays: 5,
		Realms: []string{"sap", "dedicated-saas", "public-saas"}, SlackWebhookURL: slack.URL}
	m := New(cfg, st, controlhub.New(ch.URL))
	ctx := context.Background()

	// First run: records what is already broken, without notifying.
	if err := m.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	open := keys(t, st)
	for _, k := range []string{"cp_failed:cp-cap", "postdeploy_failed:cp-cap", "ep_failed:ep-1"} {
		if !open[k] {
			t.Errorf("missing finding %s; have %v", k, open)
		}
	}
	if open["cp_stuck:cp-stuck"] {
		t.Errorf("cp-stuck flagged before STUCK_AFTER elapsed")
	}
	if len(posted) != 0 {
		t.Errorf("baseline run notified Slack: %v", posted)
	}
	f, _ := st.GetFindingByKey("cp_failed:cp-cap")
	if f.Severity != "critical" || !strings.Contains(f.Detail, "DNS step timed out") || !strings.Contains(f.Detail, "job-1") {
		t.Errorf("cp_failed finding = %+v", f)
	}
	fleet, _ := LoadFleet(st)
	if fleet == nil || fleet.Total != 3 || fleet.ByStatus["CREATION_FAILED"] != 1 {
		t.Errorf("fleet = %+v", fleet)
	}

	// Pretend cp-stuck has been CREATING for an hour: the next run flags it and notifies.
	if err := st.PutKV(kvFirstSeen, map[string]time.Time{"cp-stuck:CREATING": time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := m.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !keys(t, st)["cp_stuck:cp-stuck"] {
		t.Errorf("cp-stuck not flagged after an hour")
	}
	if len(posted) != 1 || !strings.Contains(posted[0], "slowco") {
		t.Errorf("slack posts = %v, want one for slowco", posted)
	}

	// capgroup is deleted: its findings resolve on the next run.
	ch.ControlPlanes["sap"] = []map[string]any{}
	if err := m.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if open := keys(t, st); open["cp_failed:cp-cap"] || open["postdeploy_failed:cp-cap"] {
		t.Errorf("capgroup findings still open: %v", open)
	}
	resolved, _ := st.ListFindings("resolved")
	if len(resolved) != 2 {
		t.Errorf("resolved = %d, want 2", len(resolved))
	}
}

func keys(t *testing.T, st *store.Store) map[string]bool {
	t.Helper()
	fs, err := st.ListFindings("open")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, f := range fs {
		out[f.Key] = true
	}
	return out
}
