package cost

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func TestChangesFindsNodeAndWorkloadDrivers(t *testing.T) {
	today := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	bFrom, bTo, rFrom, rTo := Windows(today, 3, 14)
	if bFrom != "2026-10-03" || bTo != "2026-10-16" || rFrom != "2026-10-17" || rTo != "2026-10-19" {
		t.Fatalf("windows %s %s %s %s", bFrom, bTo, rFrom, rTo)
	}
	var days []store.CostClusterDay
	var wl []store.CostWorkloadDay
	for d := 3; d <= 19; d++ {
		day := fmt.Sprintf("2026-10-%02d", d)
		recent := d >= 17
		cost, nodes, types := 100.0, 4, `{"m6a.xlarge":4}`
		if recent {
			cost, nodes, types = 160, 7, `{"m6a.xlarge":4,"m6a.2xlarge":3}`
		}
		days = append(days, store.CostClusterDay{Day: day, Cluster: "westpac", Tenant: "westpac", NodeCost: cost, IdleCost: 20, Nodes: nodes, NodeTypes: types, Hours: 24},
			store.CostClusterDay{Day: day, Cluster: "steady", NodeCost: 50, Nodes: 2, NodeTypes: `{}`, Hours: 24})
		edweb := 30.0
		if recent {
			edweb = 75
		}
		wl = append(wl, store.CostWorkloadDay{Day: day, Cluster: "westpac", Namespace: "prophecy", Kind: "Deployment", Name: "edweb", CPUCost: edweb, Pods: map[bool]int{false: 1, true: 3}[recent]})
		if recent {
			wl = append(wl, store.CostWorkloadDay{Day: day, Cluster: "westpac", Namespace: "spark", Kind: "Job", Name: "nightly", CPUCost: 12, Pods: 5})
		}
	}
	got := Changes(days, wl, bFrom, bTo, rFrom, rTo)
	if len(got) != 1 || got[0].Cluster != "westpac" {
		t.Fatalf("changes = %+v", got)
	}
	c := got[0]
	if c.DeltaPerDay != 60 || c.Pct != 60 || c.BaselinePerDay != 100 {
		t.Errorf("change = %+v", c)
	}
	all := ""
	for _, d := range c.Drivers {
		all += d.Kind + ": " + d.What + "\n"
	}
	for _, want := range []string{"nodes 4.0 → 7.0", "m6a.2xlarge 0 → 3", "prophecy/Deployment edweb $30/day → $75/day, up to 1 → 3 pods", "spark/Job nightly is new"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	if !strings.HasPrefix(c.Summary, "westpac is up 60%") {
		t.Errorf("summary = %s", c.Summary)
	}
}

func TestChangesIgnoresSmallAndShortHistory(t *testing.T) {
	days := []store.CostClusterDay{
		{Day: "2026-10-18", Cluster: "new", NodeCost: 500, Hours: 24}, // no baseline
		{Day: "2026-10-05", Cluster: "small", NodeCost: 10, Hours: 24}, {Day: "2026-10-06", Cluster: "small", NodeCost: 10, Hours: 24},
		{Day: "2026-10-07", Cluster: "small", NodeCost: 10, Hours: 24}, {Day: "2026-10-18", Cluster: "small", NodeCost: 20, Hours: 24}, // +100% but $10/day
	}
	if got := Changes(days, nil, "2026-10-03", "2026-10-16", "2026-10-17", "2026-10-19"); len(got) != 0 {
		t.Errorf("changes = %+v", got)
	}
}
