package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

func fakeExploreGrafana(t *testing.T) (*Env, *[]string) {
	t.Helper()
	matches := &[]string{}
	j := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m := r.URL.Query().Get("match[]"); m != "" {
			*matches = append(*matches, m)
		}
		p := r.URL.Path
		switch {
		case p == "/api/datasources":
			j(w, []map[string]any{{"uid": "p", "type": "prometheus", "isDefault": true}})
		case strings.HasSuffix(p, "/label/cluster/values"):
			j(w, map[string]any{"status": "success", "data": []string{"preview"}})
		case strings.HasSuffix(p, "/label/__name__/values"):
			j(w, map[string]any{"status": "success", "data": []string{"up", "jvm_memory_used_bytes", "jvm_gc_seconds_sum", "kube_pod_info"}})
		case strings.HasSuffix(p, "/label/namespace/values"):
			j(w, map[string]any{"status": "success", "data": []string{"prophecy", "monitoring"}})
		case strings.HasSuffix(p, "/api/v1/labels"):
			j(w, map[string]any{"status": "success", "data": []string{"__name__", "cluster", "namespace", "pod", "replica", "instance"}})
		case strings.HasSuffix(p, "/api/v1/query"):
			alert := func(replica, state, name, sev string) any {
				return map[string]any{"metric": map[string]string{"__name__": "ALERTS", "alertname": name, "alertstate": state, "severity": sev,
					"cluster": "preview", "namespace": "prophecy", "pod": "metagraph-0", "prometheus_replica": replica}, "value": []any{1, "1"}}
			}
			j(w, map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{
				alert("r0", "firing", "CPUThrottlingHigh", "info"), alert("r1", "firing", "CPUThrottlingHigh", "info"),
				alert("r0", "firing", "KubePodCrashLooping", "warning"), alert("r0", "pending", "KubeMemoryOvercommit", "warning")}}})
		case p == "/api/prometheus/grafana/api/v1/rules":
			j(w, map[string]any{"status": "success", "data": map[string]any{"groups": []any{map[string]any{"name": "g", "file": "Golden-Signals",
				"rules": []any{map[string]any{"type": "alerting", "name": "Pod OOMKilled", "state": "firing", "labels": map[string]string{"severity": "critical"},
					"annotations": map[string]string{"summary": "OOM token=abcdefghijklmnopqrstuvwxyz123456"},
					"alerts": []any{
						map[string]any{"labels": map[string]string{"cluster": "preview", "pod": "app-1"}, "state": "Alerting", "activeAt": "2026-10-14T10:00:00Z"},
						map[string]any{"labels": map[string]string{"cluster": "other", "pod": "x"}, "state": "Alerting"},
					}}}}}}})
		case p == "/api/search":
			j(w, []map[string]any{{"uid": "golden-signals-001", "title": "Golden Signals — Kubernetes", "folderTitle": ""}})
		case p == "/api/dashboards/uid/golden-signals-001":
			j(w, map[string]any{"dashboard": map[string]any{"uid": "golden-signals-001", "title": "Golden Signals",
				"templating": map[string]any{"list": []any{
					map[string]any{"name": "cluster", "type": "query", "query": map[string]string{"query": "label_values(up, cluster)"}},
					map[string]any{"name": "ns", "type": "custom", "query": "a,b"}}},
				"panels": []any{
					map[string]any{"title": "Error rate", "type": "timeseries", "fieldConfig": map[string]any{"defaults": map[string]string{"unit": "percent"}},
						"targets": []any{map[string]string{"expr": `sum(rate(http_errors{cluster="$cluster"}[5m]))`}}},
					map[string]any{"title": "Memory", "type": "row", "panels": []any{
						map[string]any{"title": "Heap", "type": "stat", "targets": []any{map[string]string{"expr": "jvm_heap"}}}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	hub := grafana.NewHub()
	hub.Add(grafana.Instance{Name: "aws", URL: srv.URL, Token: "t"}, "secret")
	hub.Refresh(context.Background())
	return &Env{Grafana: hub}, matches
}

func TestGrafanaListMetrics(t *testing.T) {
	env, matches := fakeExploreGrafana(t)
	out, err := runTool(t, env, "grafana_list_metrics", map[string]any{"cluster": "preview", "search": "JVM"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"matching":2`) || !strings.Contains(out, "jvm_gc_seconds_sum") || strings.Contains(out, `"up"`) {
		t.Errorf("metrics = %s", out)
	}
	if got := (*matches)[len(*matches)-1]; got != `{cluster="preview"}` {
		t.Errorf("match[] = %s", got)
	}
}

func TestGrafanaLabelValues(t *testing.T) {
	env, matches := fakeExploreGrafana(t)
	out, err := runTool(t, env, "grafana_label_values", map[string]any{"cluster": "preview", "metric": "kube_pod_info", "label": "namespace"})
	if err != nil || !strings.Contains(out, "prophecy") {
		t.Fatalf("values = %s %v", out, err)
	}
	if got := (*matches)[len(*matches)-1]; got != `kube_pod_info{cluster="preview"}` {
		t.Errorf("match[] = %s", got)
	}
	out, err = runTool(t, env, "grafana_label_values", map[string]any{"cluster": "preview", "metric": "kube_pod_info"})
	if err != nil || !strings.Contains(out, `"namespace"`) || strings.Contains(out, "replica") || strings.Contains(out, "__name__") {
		t.Errorf("label names = %s %v", out, err)
	}
	for _, bad := range []map[string]any{
		{"cluster": "preview"},
		{"cluster": "preview", "metric": `up"} or vector(1)`},
		{"cluster": "preview", "label": "name space"},
	} {
		if _, err := runTool(t, env, "grafana_label_values", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestGrafanaAlerts(t *testing.T) {
	env, _ := fakeExploreGrafana(t)
	out, err := runTool(t, env, "grafana_alerts", map[string]any{"cluster": "preview"})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Total  int        `json:"total"`
		Alerts []alertRow `json:"alerts"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	// CPUThrottlingHigh once (two replicas), crash looping, overcommit (pending), OOMKilled on preview only.
	if res.Total != 4 {
		t.Fatalf("total %d: %s", res.Total, out)
	}
	if a := res.Alerts[0]; a.Name != "Pod OOMKilled" || a.State != "firing" || a.Source != "grafana:Golden-Signals" {
		t.Errorf("critical firing alert not first: %+v", a)
	}
	if res.Alerts[len(res.Alerts)-1].State != "pending" {
		t.Errorf("pending alert not last: %s", out)
	}
	if strings.Contains(out, "abcdefghijklmnop") || strings.Contains(out, `"pod":"x"`) {
		t.Errorf("leaked token or other cluster: %s", out)
	}
	out, _ = runTool(t, env, "grafana_alerts", map[string]any{"cluster": "preview", "state": "pending"})
	if !strings.Contains(out, `"total":1`) {
		t.Errorf("pending only = %s", out)
	}
}

func TestGrafanaDashboards(t *testing.T) {
	env, _ := fakeExploreGrafana(t)
	out, err := runTool(t, env, "grafana_dashboards", map[string]any{"cluster": "preview", "search": "golden"})
	if err != nil || !strings.Contains(out, "golden-signals-001") {
		t.Fatalf("search = %s %v", out, err)
	}
	out, err = runTool(t, env, "grafana_dashboards", map[string]any{"cluster": "preview", "uid": "golden-signals-001"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`http_errors{cluster=\"$cluster\"}`, `"unit":"percent"`, `"row":"Memory"`, `label_values(up, cluster)`, `"panelCount":2`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	out, _ = runTool(t, env, "grafana_dashboards", map[string]any{"cluster": "preview", "uid": "golden-signals-001", "panel": "heap"})
	if !strings.Contains(out, `"panelCount":1`) {
		t.Errorf("panel filter = %s", out)
	}
	if _, err := runTool(t, env, "grafana_dashboards", map[string]any{"cluster": "preview", "uid": "../admin"}); err == nil {
		t.Error("bad uid accepted")
	}
}
