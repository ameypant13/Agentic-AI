package parser

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/patterns"
)

func TestLokiQueryShape(t *testing.T) {
	q := lokiQuery("medmutual", []string{"Failed to create snapshot", "java.lang.OutOfMemoryError"}, true)
	for _, want := range []string{`k8s_cluster_name="medmutual"`, `service_name!="k8s-events"`, `k8s_namespace_name!~`,
		`|~ "(?i)Failed to create snapshot|java\\.lang\\.OutOfMemoryError"`} {
		if !strings.Contains(q, want) {
			t.Errorf("query %s\nmissing %s", q, want)
		}
	}
}

// TestLokiLive runs one Loki cycle against a real Grafana when PARSER_LOKI_URL,
// PARSER_LOKI_TOKEN and PARSER_LOKI_CLUSTER are set.
func TestLokiLive(t *testing.T) {
	url, token, cluster := os.Getenv("PARSER_LOKI_URL"), os.Getenv("PARSER_LOKI_TOKEN"), os.Getenv("PARSER_LOKI_CLUSTER")
	if url == "" || token == "" || cluster == "" {
		t.Skip("no live Loki configured")
	}
	lib := newLib(t)
	hub := grafana.NewHub()
	hub.Add(grafana.Instance{Name: "live", URL: url, Token: token}, "secret")
	w, err := NewWorker(WorkerConfig{Dir: t.TempDir(), Interval: time.Minute, RetentionDays: 30}, lib, lib.st, kube.NewRegistry(kube.Credentials{}))
	if err != nil {
		t.Fatal(err)
	}
	w.WithGrafana(hub)
	cs := &ClusterState{ClusterID: "c", Namespaces: map[string]map[string]*PodState{}}
	start := time.Now()
	recs, err := w.scanClusterLoki(context.Background(), kube.Infra{ID: "c", ClusterName: cluster},
		&grafana.Mapping{Instance: "live", PromCluster: cluster, LokiCluster: cluster}, cs, lib.Set())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("10-minute Loki cycle on %s: %d hits in %v", cluster, len(recs), time.Since(start).Round(time.Millisecond))
	byRule := map[string]int{}
	for _, r := range recs {
		byRule[r.RuleID+" @ "+r.Pod]++
	}
	for k, n := range byRule {
		t.Logf("  %4d  %s", n, k)
	}
}

// TestLokiLivePatterns samples unmatched errors from a real Loki into the
// pattern miner (same env vars as TestLokiLive).
func TestLokiLivePatterns(t *testing.T) {
	url, token, cluster := os.Getenv("PARSER_LOKI_URL"), os.Getenv("PARSER_LOKI_TOKEN"), os.Getenv("PARSER_LOKI_CLUSTER")
	if url == "" || token == "" || cluster == "" {
		t.Skip("no live Loki configured")
	}
	lib := newLib(t)
	hub := grafana.NewHub()
	hub.Add(grafana.Instance{Name: "live", URL: url, Token: token}, "secret")
	w, err := NewWorker(WorkerConfig{Dir: t.TempDir(), Interval: time.Minute, RetentionDays: 30}, lib, lib.st, kube.NewRegistry(kube.Credentials{}))
	if err != nil {
		t.Fatal(err)
	}
	m := patterns.NewMiner()
	w.WithGrafana(hub).WithPatterns(m)
	cs := &ClusterState{ClusterID: "c", Namespaces: map[string]map[string]*PodState{}}
	start := time.Now()
	if _, err := w.scanClusterLoki(context.Background(), kube.Infra{ID: "c", ClusterName: cluster},
		&grafana.Mapping{Instance: "live", PromCluster: cluster, LokiCluster: cluster}, cs, lib.Set()); err != nil {
		t.Fatal(err)
	}
	ts := m.Dirty()
	sort.Slice(ts, func(i, j int) bool { return ts[i].Count > ts[j].Count })
	t.Logf("%d unmatched error templates on %s in %v", len(ts), cluster, time.Since(start).Round(time.Millisecond))
	for i, x := range ts {
		if i == 15 {
			break
		}
		t.Logf("  %4d  %-28s %s", x.Count, x.Service, x.Text)
	}
}
