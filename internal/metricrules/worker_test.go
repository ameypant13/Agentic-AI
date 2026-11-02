package metricrules

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
)

// fakeGrafana answers datasources and instant queries; firing decides which
// rule IDs return a series, and failing makes every query error.
type fakeGrafana struct {
	mu      sync.Mutex
	firing  map[string]bool
	failing bool
	rules   []Rule
}

func (f *fakeGrafana) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/datasources":
		_ = json.NewEncoder(w).Encode([]map[string]any{{"uid": "prom", "type": "prometheus", "isDefault": true}})
	case strings.HasSuffix(r.URL.Path, "/api/v1/query"):
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failing {
			http.Error(w, "boom", 500)
			return
		}
		expr := r.URL.Query().Get("query")
		result := []any{}
		for _, rule := range f.rules {
			if rule.Expr == expr && f.firing[rule.ID] {
				result = append(result, map[string]any{
					"metric": map[string]string{"cluster": "jde", "namespace": "prophecy", "pod": "metagraph-1", "container": "metagraph", "receive": "true"},
					"value":  []any{1.0, "0.95"}})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": result}})
	default:
		http.NotFound(w, r)
	}
}

func TestWorkerOpensAndResolves(t *testing.T) {
	rules, _ := Defaults()
	f := &fakeGrafana{firing: map[string]bool{"container-memory-near-limit": true}, rules: rules}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	hub := grafana.NewHub()
	hub.Add(grafana.Instance{Name: "azure", URL: srv.URL, Token: "t"}, "secret")
	dir := t.TempDir()
	w, err := NewWorker(hub, dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c := hub.Client("azure")

	w.Evaluate(context.Background(), c)
	issues := w.Issues("azure", "jde")
	if len(issues) != 1 || !issues[0].Active || issues[0].ValueText != "95%" || issues[0].EntityName != "metagraph-1 · metagraph" {
		t.Fatalf("after first evaluation: %+v", issues)
	}
	if _, ok := issues[0].Labels["receive"]; ok {
		t.Error("scrape labels should be trimmed")
	}
	first := issues[0].FirstSeen

	// Still firing: same issue, same first-seen.
	w.Evaluate(context.Background(), c)
	if is := w.Issues("azure", "jde"); len(is) != 1 || !is[0].FirstSeen.Equal(first) {
		t.Errorf("second evaluation: %+v", is)
	}

	// Grafana errors: nothing resolves.
	f.mu.Lock()
	f.failing = true
	f.mu.Unlock()
	w.Evaluate(context.Background(), c)
	if is := w.Issues("azure", "jde"); len(is) != 1 || !is[0].Active {
		t.Errorf("a failed query resolved the issue: %+v", is)
	}
	if len(w.RuleErrors()) == 0 {
		t.Error("rule errors not recorded")
	}

	// Condition gone: resolved, and the state survives a restart.
	f.mu.Lock()
	f.failing, f.firing = false, map[string]bool{}
	f.mu.Unlock()
	w.Evaluate(context.Background(), c)
	if is := w.Issues("azure", "jde"); len(is) != 1 || is[0].Active || is[0].ResolvedAt == nil {
		t.Errorf("after resolve: %+v", is)
	}
	w.save()
	w2, _ := NewWorker(hub, dir, time.Minute)
	if is := w2.Issues("azure", "jde"); len(is) != 1 || is[0].Active {
		t.Errorf("after reload: %+v", is)
	}
}
