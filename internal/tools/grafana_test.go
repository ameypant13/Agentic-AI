package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

func fakeGrafana(t *testing.T) (*grafana.Hub, *[]string) {
	t.Helper()
	var mu sync.Mutex
	queries := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*queries = append(*queries, r.URL.Query().Get("query"))
		mu.Unlock()
		switch {
		case r.URL.Path == "/api/datasources":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"uid": "p", "type": "prometheus", "isDefault": true}, {"uid": "l", "type": "loki"}})
		case strings.HasSuffix(r.URL.Path, "/label/cluster/values"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []string{"medmutual"}})
		case strings.HasSuffix(r.URL.Path, "/label/k8s_cluster_name/values"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []string{"medmutual"}})
		case strings.HasSuffix(r.URL.Path, "/api/v1/query"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector",
				"result": []any{map[string]any{"metric": map[string]string{"pod": "metagraph-1", "receive": "true"}, "value": []any{1, "0.951234"}}}}})
		case strings.HasSuffix(r.URL.Path, "/loki/api/v1/query_range"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": []any{
				map[string]any{"stream": map[string]string{"k8s_pod_name": "app-1", "k8s_container_name": "app"},
					"values": [][2]string{{"1791540000000000000", "ERROR db password=hunter2 failed"}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	hub := grafana.NewHub()
	hub.Add(grafana.Instance{Name: "azure", URL: srv.URL, Token: "t"}, "secret")
	hub.Refresh(context.Background())
	return hub, queries
}

func TestGrafanaTools(t *testing.T) {
	hub, queries := fakeGrafana(t)
	env := &Env{Grafana: hub}

	out, err := runQueryMetrics(context.Background(), env, json.RawMessage(`{"cluster":"medmutual","promql":"max(container_memory_working_set_bytes{cluster=\"$cluster\"})"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"value":0.9512`) || strings.Contains(out, "receive") {
		t.Errorf("metrics result = %s", out)
	}
	last := (*queries)[len(*queries)-1]
	if !strings.Contains(last, `cluster="medmutual"`) || strings.Contains(last, "$cluster") {
		t.Errorf("placeholder not replaced: %s", last)
	}
	if _, err := runQueryMetrics(context.Background(), env, json.RawMessage(`{"cluster":"medmutual","promql":"up"}`)); err == nil {
		t.Error("a query without $cluster was accepted")
	}
	if _, err := runQueryMetrics(context.Background(), env, json.RawMessage(`{"cluster":"nope","promql":"up{cluster=\"$cluster\"}"}`)); err == nil ||
		!strings.Contains(err.Error(), "not monitored") {
		t.Errorf("unknown cluster: %v", err)
	}

	out, err = runQueryLogs(context.Background(), env, json.RawMessage(`{"cluster":"medmutual","namespace":"prophecy","pod_prefix":"app","filter":"(?i)error"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hunter2") || !strings.Contains(out, `"pod":"app-1"`) {
		t.Errorf("logs result = %s", out)
	}
	last = (*queries)[len(*queries)-1]
	for _, want := range []string{`k8s_cluster_name="medmutual"`, `k8s_namespace_name="prophecy"`, `k8s_pod_name=~"app.*"`, `|~ "(?i)error"`} {
		if !strings.Contains(last, want) {
			t.Errorf("logql %s missing %s", last, want)
		}
	}
	if _, err := runQueryLogs(context.Background(), env, json.RawMessage(`{"cluster":"medmutual","filter":"("}`)); err == nil {
		t.Error("invalid regexp accepted")
	}
}
