package cost

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// fakeGrafana answers the cost queries for one cluster ("c1") with one node and
// one Deployment pod that requests 1 core / 4 GiB and uses 0.5 core / 2 GiB.
func fakeGrafana(t *testing.T) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var times []string
	vec := func(labels map[string]string, v float64) map[string]any {
		return map[string]any{"metric": labels, "value": []any{0, jsonNum(v)}}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/datasources":
			w.Write([]byte(`[{"uid":"prom","type":"prometheus","isDefault":true}]`))
			return
		case strings.HasSuffix(r.URL.Path, "/label/cluster/values"):
			w.Write([]byte(`{"status":"success","data":["c1"]}`))
			return
		case !strings.HasSuffix(r.URL.Path, "/api/v1/query"):
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query().Get("query")
		mu.Lock()
		times = append(times, r.URL.Query().Get("time"))
		mu.Unlock()
		pod := map[string]string{"namespace": "prophecy", "pod": "app-rs1-x"}
		// Sample counts over a 24h window at 5m resolution.
		const samples = 288.0
		var res []map[string]any
		switch {
		case strings.Contains(q, "kube_node_info"):
			res = append(res, vec(map[string]string{"node": "n1", "internal_ip": "10.0.0.1", "provider_id": "aws:///us-east-1a/i-1"}, 1))
		case strings.Contains(q, "count_over_time(kube_node_status_capacity"):
			res = append(res, vec(map[string]string{"node": "n1"}, samples))
		case strings.Contains(q, `kube_node_status_capacity`) && strings.Contains(q, `resource="cpu"`):
			res = append(res, vec(map[string]string{"node": "n1"}, 4))
		case strings.Contains(q, `kube_node_status_capacity`) && strings.Contains(q, `resource="memory"`):
			res = append(res, vec(map[string]string{"node": "n1"}, 16*gib))
		case strings.Contains(q, "node_dmi_info"):
			res = append(res, vec(map[string]string{"instance": "10.0.0.1:9100", "product_name": "m6a.xlarge"}, 1))
		case strings.Contains(q, "kube_pod_container_resource_requests") && strings.Contains(q, `resource="cpu"`):
			res = append(res, vec(pod, 1*samples))
		case strings.Contains(q, "kube_pod_container_resource_requests") && strings.Contains(q, `resource="memory"`):
			res = append(res, vec(pod, 4*gib*samples))
		case strings.Contains(q, "container_cpu_usage_seconds_total"):
			res = append(res, vec(pod, 0.5*24*3600))
		case strings.Contains(q, "container_memory_working_set_bytes"):
			res = append(res, vec(pod, 2*gib*samples))
		case strings.Contains(q, "kube_pod_info"):
			res = append(res, vec(map[string]string{"namespace": "prophecy", "pod": "app-rs1-x", "node": "n1"}, 1))
		case strings.Contains(q, "kube_pod_owner"):
			res = append(res, vec(map[string]string{"namespace": "prophecy", "pod": "app-rs1-x", "owner_kind": "ReplicaSet", "owner_name": "app-rs1"}, 1))
		case strings.Contains(q, "kube_replicaset_owner"):
			res = append(res, vec(map[string]string{"namespace": "prophecy", "replicaset": "app-rs1", "owner_kind": "Deployment", "owner_name": "app"}, 1))
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": res}})
	}))
	t.Cleanup(srv.Close)
	return srv, &times
}

func jsonNum(v float64) string { b, _ := json.Marshal(v); return string(b) }

func TestWorker(t *testing.T) {
	srv, times := fakeGrafana(t)
	hub := grafana.NewHub()
	hub.Add(grafana.Instance{Name: "aws", URL: srv.URL, Token: "t"}, "secret")
	hub.Refresh(context.Background())

	st, err := store.Open("file:" + filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	prices := NewPrices(t.TempDir(), "", "")
	calls := 0
	prices.fetch["aws"] = func(_ context.Context, region, typ string) (float64, error) {
		calls++
		if region != "us-east-1" || typ != "m6a.xlarge" {
			t.Errorf("price lookup %s %s", region, typ)
		}
		return 0.2, nil
	}
	now := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)
	w := NewWorker(Config{BackfillDays: 3}, hub, st, prices)
	w.now = func() time.Time { return now }
	w.Tenants = func(context.Context) map[string]string { return map[string]string{"aws/c1": "acme"} }

	w.RunToday(context.Background())
	days, _ := st.CostDays("2026-10-12", "2026-10-12", "c1")
	if len(days) != 1 || days[0].Tenant != "acme" || math.Abs(days[0].Hours-6) > 1e-9 {
		t.Fatalf("today = %+v", days)
	}
	if (*times)[0] != "1791784800" { // evaluated at "now", not at day end
		t.Errorf("today evaluated at %s", (*times)[0])
	}

	// Backfill: one past day per step, newest first, then nothing left.
	for i, want := range []string{"2026-10-11", "2026-10-10", "2026-10-09"} {
		if !w.BackfillStep(context.Background()) {
			t.Fatalf("step %d did nothing", i)
		}
		d, _ := st.CostDays(want, want, "c1")
		if len(d) != 1 || d[0].Hours != 24 {
			t.Fatalf("after step %d, %s = %+v", i, want, d)
		}
	}
	if w.BackfillStep(context.Background()) {
		t.Error("backfill kept working after every day was complete")
	}

	// One node at $0.2/h for 24h; the pod is charged for its request (1 core,
	// 4 GiB) since it used less.
	d, _ := st.CostDays("2026-10-11", "2026-10-11", "c1")
	if math.Abs(d[0].NodeCost-0.2*24) > 1e-6 || d[0].EstimatedNodes != 0 || d[0].NodeTypes != `{"m6a.xlarge":1}` {
		t.Errorf("day = %+v", d[0])
	}
	wl, _ := st.CostWorkloads("2026-10-11", "2026-10-11", "c1", "")
	if len(wl) != 1 || wl[0].Kind != "Deployment" || wl[0].Name != "app" {
		t.Fatalf("workloads = %+v", wl)
	}
	r, _ := nodeRates(Node{Cores: 4, MemGiB: 16, Hourly: 0.2})
	if want := 24*r.cpu + 4*24*r.ram; math.Abs(wl[0].CPUCost+wl[0].MemCost-want) > 1e-6 {
		t.Errorf("app cost %v, want %v", wl[0].CPUCost+wl[0].MemCost, want)
	}
	if math.Abs(d[0].IdleCost-(d[0].NodeCost-d[0].AllocatedCost)) > 1e-9 {
		t.Errorf("idle %v", d[0].IdleCost)
	}
	if calls != 1 {
		t.Errorf("price fetched %d times, want 1 (cached)", calls)
	}
}

func TestPricing(t *testing.T) {
	item := `{"terms":{"OnDemand":{"X.Y":{"priceDimensions":{"X.Y.Z":{"unit":"Hrs","pricePerUnit":{"USD":"0.1728000000"}}}}}}}`
	if p, err := parseAWSPrice(item); err != nil || p != 0.1728 {
		t.Errorf("parseAWSPrice = %v %v", p, err)
	}
	items := []azureItem{
		{RetailPrice: 0.5, UnitOfMeasure: "1 Hour", SkuName: "D8s v5", ProductName: "Virtual Machines Dsv5 Series Windows"},
		{RetailPrice: 0.05, UnitOfMeasure: "1 Hour", SkuName: "D8s v5 Spot", ProductName: "Virtual Machines Dsv5 Series"},
		{RetailPrice: 0.384, UnitOfMeasure: "1 Hour", SkuName: "D8s v5", ProductName: "Virtual Machines Dsv5 Series"},
	}
	if p, err := pickAzurePrice(items); err != nil || p != 0.384 {
		t.Errorf("pickAzurePrice = %v %v", p, err)
	}
	if awsRegion("aws:///us-west-1b/i-0abc") != "us-west-1" || azureRegion("azure:///subscriptions/s/resourceGroups/MC_rg_aks1_eastus/providers/x") != "eastus" {
		t.Error("region parsing")
	}
	if looksLikeInstanceType("Virtual Machine") || !looksLikeInstanceType("c5a.large") {
		t.Error("instance type detection")
	}
}
