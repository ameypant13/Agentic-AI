package cost

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

// TestLive runs Collect + Allocate against a real Grafana. It is skipped unless
// COST_LIVE_URL, COST_LIVE_TOKEN and COST_LIVE_CLUSTER are set.
func TestLive(t *testing.T) {
	u, tok, cl := os.Getenv("COST_LIVE_URL"), os.Getenv("COST_LIVE_TOKEN"), os.Getenv("COST_LIVE_CLUSTER")
	if u == "" || tok == "" || cl == "" {
		t.Skip("set COST_LIVE_URL, COST_LIVE_TOKEN and COST_LIVE_CLUSTER")
	}
	c := grafana.New(grafana.Instance{Name: "live", URL: u, Token: tok})
	end := time.Now().UTC().Truncate(24 * time.Hour)
	start := end.Add(-24 * time.Hour)
	t0 := time.Now()
	nodes, pods, err := Collect(context.Background(), c, cl, start, end, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("collected %d nodes, %d pods in %v", len(nodes), len(pods), time.Since(t0))
	for _, n := range nodes {
		t.Logf("node %s type=%s region=%s cores=%.0f mem=%.1fGiB hours=%.2f", n.Name, n.InstanceType, n.Region, n.Cores, n.MemGiB, n.Hours)
	}
	res := Allocate(nodes, pods)
	t.Logf("day %s: node $%.2f allocated $%.2f idle $%.2f (default-priced nodes: %d)", start.Format("2006-01-02"), res.NodeCost, res.AllocatedCost, res.IdleCost, res.EstimatedNodes)
	for i, w := range res.Workloads {
		if i == 12 {
			break
		}
		t.Logf("  $%.2f %s/%s %s pods=%d cpu req/use %.1f/%.1f core-h mem req/use %.0f/%.0f GiB-h", w.Total(), w.Namespace, w.Name, w.Kind, w.Pods, w.CPURequest, w.CPUUsage, w.MemRequest, w.MemUsage)
	}
}

// TestLiveContainers runs CollectContainers for yesterday against a real
// Grafana. Skipped unless COST_LIVE_URL, COST_LIVE_TOKEN and COST_LIVE_CLUSTER are set.
func TestLiveContainers(t *testing.T) {
	u, tok, cl := os.Getenv("COST_LIVE_URL"), os.Getenv("COST_LIVE_TOKEN"), os.Getenv("COST_LIVE_CLUSTER")
	if u == "" || tok == "" || cl == "" {
		t.Skip("set COST_LIVE_URL, COST_LIVE_TOKEN and COST_LIVE_CLUSTER")
	}
	c := grafana.New(grafana.Instance{Name: "live", URL: u, Token: tok})
	end := time.Now().UTC().Truncate(24 * time.Hour)
	t0 := time.Now()
	rows, err := CollectContainers(context.Background(), c, cl, end.Add(-24*time.Hour), end)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d workload containers in %v", len(rows), time.Since(t0))
	for _, r := range rows {
		if r.Namespace != "prophecy" {
			continue
		}
		t.Logf("%s/%s %s: p95=%.3f max=%.3f mem=%.0fMi req=%.2f/%.0fMi lim=%.2f/%.0fMi oom=%v thr=%.1f%% podh=%.1f pods=%d",
			r.Kind, r.Name, r.Container, r.CPUP95, r.CPUMax, r.MemMax/(1<<20), r.CPURequest, r.MemRequest/(1<<20),
			r.CPULimit, r.MemLimit/(1<<20), r.OOMKilled, r.ThrottledPct, r.PodHours, r.Pods)
	}
}
