package cost

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestNodeRatesSplitPriceInOpenCostRatio(t *testing.T) {
	n := Node{Name: "n", Cores: 4, MemGiB: 16, Hourly: 0.2}
	r, known := nodeRates(n)
	if !known {
		t.Fatal("known price reported as unknown")
	}
	// The rates must rebuild the node price exactly.
	if got := r.cpu*4 + r.ram*16; !near(got, 0.2) {
		t.Errorf("rates rebuild %v, want 0.2", got)
	}
	// And keep OpenCost's CPU:RAM ratio.
	if !near(r.cpu/r.ram, DefaultCPUHourly/DefaultRAMHourly) {
		t.Errorf("cpu/ram ratio %v", r.cpu/r.ram)
	}
	if _, known := nodeRates(Node{Cores: 4, MemGiB: 16}); known {
		t.Error("node without a price reported as known")
	}
}

func TestAllocate(t *testing.T) {
	nodes := []Node{
		{Name: "a", InstanceType: "m6a.xlarge", Cores: 4, MemGiB: 16, Hours: 24, Hourly: 0.2},
		{Name: "b", Cores: 2, MemGiB: 8, Hours: 12}, // unknown price: default rates
	}
	pods := []Pod{
		// Requested more than it used: pays for the request.
		{Namespace: "prophecy", Name: "app-1", Node: "a", CPURequest: 24, CPUUsage: 10, MemRequest: 96, MemUsage: 50, OwnerKind: "Deployment", OwnerName: "app"},
		// Used more than requested: pays for the usage.
		{Namespace: "prophecy", Name: "app-2", Node: "a", CPURequest: 12, CPUUsage: 20, MemRequest: 48, MemUsage: 40, OwnerKind: "Deployment", OwnerName: "app"},
		// Node not known: cluster-average rates.
		{Namespace: "monitoring", Name: "orphan", Node: "gone", CPURequest: 1, MemRequest: 1},
		// Nothing requested or used: no line.
		{Namespace: "x", Name: "idle", Node: "a"},
	}
	res := Allocate(nodes, pods)

	nodeA := 0.2 * 24
	nodeB := (2*DefaultCPUHourly + 8*DefaultRAMHourly) * 12
	if !near(res.NodeCost, nodeA+nodeB) {
		t.Errorf("node cost %v, want %v", res.NodeCost, nodeA+nodeB)
	}
	if res.Nodes != 2 || res.EstimatedNodes != 1 || res.NodeTypes["m6a.xlarge"] != 1 || res.NodeTypes["unknown"] != 1 {
		t.Errorf("nodes %d estimated %d types %v", res.Nodes, res.EstimatedNodes, res.NodeTypes)
	}
	if len(res.Workloads) != 2 {
		t.Fatalf("workloads = %+v", res.Workloads)
	}
	app := res.Workloads[0]
	if app.Kind != "Deployment" || app.Name != "app" || app.Pods != 2 {
		t.Fatalf("first workload = %+v", app)
	}
	r, _ := nodeRates(nodes[0])
	wantCPU := (24 + 20) * r.cpu // max(24,10) + max(12,20)
	wantMem := (96 + 48) * r.ram // max(96,50) + max(48,40)
	if !near(app.CPUCost, wantCPU) || !near(app.MemCost, wantMem) {
		t.Errorf("app cost cpu %v mem %v, want %v %v", app.CPUCost, app.MemCost, wantCPU, wantMem)
	}
	orphan := res.Workloads[1]
	if orphan.Kind != "Pod" || orphan.Name != "orphan" {
		t.Errorf("orphan = %+v", orphan)
	}
	if !near(res.AllocatedCost, app.Total()+orphan.Total()) || !near(res.IdleCost, res.NodeCost-res.AllocatedCost) {
		t.Errorf("allocated %v idle %v node %v", res.AllocatedCost, res.IdleCost, res.NodeCost)
	}
	// Node a: 44 core-hours and 144 GiB-hours allocated of 96 and 384 available,
	// so node a alone leaves idle cost; the total must stay non-negative.
	if res.IdleCost <= 0 {
		t.Errorf("idle = %v, want > 0", res.IdleCost)
	}
}

func TestOwnersResolve(t *testing.T) {
	o := Owners{
		Pod: map[[2]string][2]string{
			{"p", "app-7d9-x"}:    {"ReplicaSet", "app-7d9"},
			{"p", "db-0"}:         {"StatefulSet", "db"},
			{"p", "backup-123-z"}: {"Job", "backup-123"},
			{"p", "bare"}:         {"<none>", "<none>"},
		},
		ReplicaSet: map[[2]string][2]string{{"p", "app-7d9"}: {"Deployment", "app"}},
		Job:        map[[2]string][2]string{{"p", "backup-123"}: {"CronJob", "backup"}},
	}
	for pod, want := range map[string][2]string{
		"app-7d9-x": {"Deployment", "app"}, "db-0": {"StatefulSet", "db"},
		"backup-123-z": {"CronJob", "backup"}, "bare": {"", ""}, "unknown": {"", ""},
	} {
		k, n := o.Resolve("p", pod)
		if k != want[0] || n != want[1] {
			t.Errorf("%s -> %s/%s, want %v", pod, k, n, want)
		}
	}
}
