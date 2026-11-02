package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
)

func TestThanosSummary(t *testing.T) {
	answers := []struct{ match, value string }{ // first match wins
		{"unless on", "2"},
		{"kube_node_status_condition", "3"},
		{"count(max by (node) (kube_node_info", "4"},
		{`kube_node_status_allocatable{cluster="c1",resource="cpu"}`, "16"},
		{`kube_node_status_allocatable{cluster="c1",resource="memory"}`, "68719476736"}, // 64 GiB
		{`resource_requests{cluster="c1",resource="cpu"}`, "4"},
		{`resource_requests{cluster="c1",resource="memory"}`, "17179869184"}, // 16 GiB
		{"count((max by (namespace, pod) (kube_pod_status_phase", "40"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources" {
			w.Write([]byte(`[{"uid":"prom","type":"prometheus"}]`))
			return
		}
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, `cluster="c1"`) {
			t.Errorf("query without cluster selector: %s", q)
		}
		res := []any{}
		for _, a := range answers {
			if strings.Contains(q, a.match) {
				res = append(res, map[string]any{"metric": map[string]string{}, "value": []any{0, a.value}})
				break
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": res}})
	}))
	defer srv.Close()
	c := grafana.New(grafana.Instance{Name: "aws", URL: srv.URL, Token: "t"})
	s, err := thanosSummary(context.Background(), c, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Nodes != 4 || s.NodesReady != 3 || s.Pods != 40 || s.PodsNotReady != 2 || s.Source != "thanos" {
		t.Errorf("counts = %+v", s)
	}
	if s.CPURequestedPct != 25 || s.MemRequestedPct != 25 || s.CPUDetail != "4.0 / 16 cores" || s.MemDetail != "16 / 64 GiB" {
		t.Errorf("requests = %+v", s)
	}
}

func TestCardsNeverBlock(t *testing.T) {
	h := newHarness(t)
	s := h.server
	oldWait, oldTTL := cardWait, cardErrorTTL
	cardWait, cardErrorTTL = 100*time.Millisecond, time.Hour
	defer func() { cardWait, cardErrorTTL = oldWait, oldTTL }()

	var mu sync.Mutex
	calls := map[string]int{}
	release := make(chan struct{})
	s.cardCompute = func(_ context.Context, in kube.Infra) (*kube.Summary, string) {
		mu.Lock()
		calls[in.ID]++
		n := calls[in.ID]
		mu.Unlock()
		switch in.ID {
		case "slow":
			<-release
		case "broken":
			return nil, "credentials refused"
		}
		return &kube.Summary{Nodes: n}, ""
	}
	slow, broken, fast := kube.Infra{ID: "slow"}, kube.Infra{ID: "broken"}, kube.Infra{ID: "fast"}

	// A first-time slow cluster: returned as pending after cardWait, not after it finishes.
	start := time.Now()
	if sum, _, pending := s.card(slow); !pending || sum != nil || time.Since(start) > time.Second {
		t.Fatalf("slow first call: pending=%v sum=%v after %v", pending, sum, time.Since(start))
	}
	if sum, _, pending := s.card(fast); pending || sum == nil || sum.Nodes != 1 {
		t.Fatalf("fast first call: pending=%v sum=%+v", pending, sum)
	}
	close(release)
	waitFor(t, func() bool { _, _, p := s.card(slow); return !p })

	// A stale card is returned at once and refreshed in the background.
	s.clusters.mu.Lock()
	s.clusters.cards["fast"].at = time.Now().Add(-time.Hour)
	s.clusters.mu.Unlock()
	if sum, _, _ := s.card(fast); sum == nil || sum.Nodes != 1 {
		t.Fatalf("stale card = %+v, want the old one immediately", sum)
	}
	waitFor(t, func() bool { sum, _, _ := s.card(fast); return sum != nil && sum.Nodes == 2 })

	// A failing cluster is not retried until cardErrorTTL.
	if _, msg, _ := s.card(broken); msg != "credentials refused" {
		t.Fatalf("broken msg = %q", msg)
	}
	s.clusters.mu.Lock()
	s.clusters.cards["broken"].at = time.Now().Add(-2 * summaryTTL)
	s.clusters.mu.Unlock()
	s.card(broken)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls["broken"] != 1 {
		t.Errorf("broken cluster recomputed %d times within the error backoff", calls["broken"])
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
