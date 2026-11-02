package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func TestCosts(t *testing.T) {
	h := newHarness(t)
	h.server.costs = cost.NewWorker(cost.Config{}, grafana.NewHub(), h.st, cost.NewPrices(filepath.Join(t.TempDir(), "p"), "", ""))
	today := time.Now().UTC().Format("2006-01-02")
	old := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")
	for _, d := range []struct {
		day, cluster string
		node, idle   float64
		wl           []store.CostWorkloadDay
	}{
		{today, "preview", 85, 17, []store.CostWorkloadDay{{Namespace: "prophecy", Kind: "Deployment", Name: "edweb", CPUCost: 8, MemCost: 3.6,
			CPURequest: 192, CPUUsage: 5, MemRequest: 750, MemUsage: 575}}},
		{today, "jpmcdemo", 10, 2, nil},
		{old, "preview", 999, 0, nil}, // outside the default 30 days
	} {
		for i := range d.wl {
			d.wl[i].Day, d.wl[i].Cluster = d.day, d.cluster
		}
		var ct []store.CostContainerDay
		if d.cluster == "preview" && d.day == today {
			ct = []store.CostContainerDay{{Day: d.day, Cluster: d.cluster, Namespace: "prophecy", Kind: "Deployment", Name: "edweb",
				Container: "edweb", CPUP95: 0.22, CPUMax: 0.45, MemMax: 24540 << 20, CPURequest: 8, CPULimit: 8,
				MemRequest: 32000 << 20, MemLimit: 32000 << 20, PodHours: 24, Pods: 1}}
		}
		if err := h.st.ReplaceCostDay(&store.CostClusterDay{Day: d.day, Cluster: d.cluster, Instance: "aws", NodeCost: d.node, IdleCost: d.idle, AllocatedCost: d.node - d.idle, Hours: 24}, d.wl, ct); err != nil {
			t.Fatal(err)
		}
	}
	c := h.login("sre@prophecy.io")
	var sum struct {
		Total    float64 `json:"total"`
		Clusters []struct {
			Cluster  string  `json:"cluster"`
			NodeCost float64 `json:"nodeCost"`
		} `json:"clusters"`
	}
	if st := h.call(c, "GET", "/api/costs", nil, &sum); st != 200 {
		t.Fatalf("status %d", st)
	}
	if sum.Total != 95 || len(sum.Clusters) != 2 || sum.Clusters[0].Cluster != "preview" {
		t.Errorf("summary = %+v", sum)
	}
	var wl struct {
		Workloads []store.CostWorkloadDay `json:"workloads"`
	}
	h.call(c, "GET", "/api/costs/workloads?cluster=preview", nil, &wl)
	if len(wl.Workloads) != 1 || wl.Workloads[0].Name != "edweb" || wl.Workloads[0].CPUCost != 8 {
		t.Errorf("workloads = %+v", wl.Workloads)
	}

	var recs struct {
		Recommendations []cost.Recommendation `json:"recommendations"`
	}
	if st := h.call(c, "GET", "/api/costs/recommendations?cluster=preview", nil, &recs); st != 200 || len(recs.Recommendations) != 1 {
		t.Fatalf("recommendations status %d: %+v", st, recs)
	}
	if r := recs.Recommendations[0]; cost.CPUString(r.Recommended.CPURequest) != "220m" || r.MonthlySaving <= 0 || r.Confidence != "low" {
		t.Errorf("recommendation = %+v", r)
	}

	var byCluster struct {
		Clusters map[string]rightsizingTotal `json:"clusters"`
	}
	h.call(c, "GET", "/api/costs/recommendations?summary=1", nil, &byCluster)
	if t := byCluster.Clusters["preview"]; t.Count != 1 || t.MonthlySaving <= 0 || t.NeedsMore != 0 {
		h.t.Errorf("summary = %+v", byCluster)
	}

	f := CostsFunc(h.st)
	out, err := f(context.Background(), "preview", "", "rightsizing", 30)
	if err != nil || !strings.Contains(out, `"Recommended":"cpu 220m, memory 28224Mi"`) || !strings.Contains(out, "(limit 8)") {
		t.Errorf("query_costs rightsizing = %s %v", out, err)
	}
	out, err = f(context.Background(), "", "", "workload", 7)
	if err != nil || !strings.Contains(out, `"Name":"edweb"`) || !strings.Contains(out, "list prices") {
		t.Errorf("query_costs workload = %s %v", out, err)
	}
	out, _ = f(context.Background(), "preview", "", "", 7)
	var parsed map[string]any
	if json.Unmarshal([]byte(out), &parsed) != nil || parsed["clusters"] == nil || strings.Contains(out, "999") {
		t.Errorf("query_costs cluster = %s", out)
	}
}
