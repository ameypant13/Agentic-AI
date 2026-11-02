package cost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func days(n int, f func(i int) store.CostContainerDay) []store.CostContainerDay {
	var out []store.CostContainerDay
	for i := 0; i < n; i++ {
		d := f(i)
		d.Day = fmt.Sprintf("2026-10-%02d", i+1)
		d.Cluster, d.Namespace, d.Kind = "preview", "prophecy", "Deployment"
		out = append(out, d)
	}
	return out
}

func TestRecommendOverProvisioned(t *testing.T) {
	// edweb-like: requests 8 cores / 32000Mi, uses p95 0.22 core and peaks at 24.5Gi.
	rows := days(30, func(i int) store.CostContainerDay {
		p95 := 0.20
		if i == 10 {
			p95 = 0.219 // the highest day wins
		}
		return store.CostContainerDay{Name: "edweb", Container: "edweb", CPUP95: p95, CPUMax: 0.45, MemMax: 24540 << 20,
			CPURequest: 8, CPULimit: 8, MemRequest: 32000 << 20, MemLimit: 32000 << 20, PodHours: 24}
	})
	prices := map[[4]string]UnitPrices{{"preview", "prophecy", "Deployment", "edweb"}: {CPU: 0.04, Mem: 0.005}}
	recs := Recommend(rows, prices)
	if len(recs) != 1 {
		t.Fatalf("%d recs", len(recs))
	}
	r := recs[0]
	if CPUString(r.Recommended.CPURequest) != "220m" || r.Recommended.CPULimit != 0 {
		t.Errorf("cpu = %s limit %v", CPUString(r.Recommended.CPURequest), r.Recommended.CPULimit)
	}
	// 24540Mi × 1.15 = 28221Mi, rounded up to 16Mi.
	if got := MemString(r.Recommended.MemRequest); got != "28224Mi" || r.Recommended.MemLimit != r.Recommended.MemRequest {
		t.Errorf("memory = %s / %s", got, MemString(r.Recommended.MemLimit))
	}
	if r.Confidence != "high" || r.Replicas != 1 {
		t.Errorf("confidence %s replicas %v", r.Confidence, r.Replicas)
	}
	// (8 - 0.22) cores × 720 h × $0.04 + (32000 - 28224)/1024 GiB × 720 h × $0.005 ≈ 224 + 13.3
	if r.MonthlySaving < 230 || r.MonthlySaving > 245 {
		t.Errorf("saving = %v", r.MonthlySaving)
	}
	if strings.Contains(strings.Join(r.Notes, " "), "JVM") {
		t.Errorf("heap note at 77%% of the limit: %v", r.Notes)
	}
	// A peak within 15% of the limit, without OOM kills, gets the heap note.
	near := days(10, func(int) store.CostContainerDay {
		return store.CostContainerDay{Name: "metagraph", Container: "metagraph", CPUP95: 0.1, MemMax: 15000 << 20,
			CPURequest: 4, MemRequest: 16000 << 20, MemLimit: 16000 << 20, PodHours: 24}
	})
	if n := strings.Join(Recommend(near, nil)[0].Notes, " "); !strings.Contains(n, "JVM") {
		t.Errorf("no heap note at 94%%: %s", n)
	}
}

func TestRecommendThrottledAndOOM(t *testing.T) {
	rows := days(5, func(i int) store.CostContainerDay {
		return store.CostContainerDay{Name: "athena", Container: "athena", CPUP95: 0.24, MemMax: 1500 << 20,
			CPURequest: 0.5, CPULimit: 0.5, MemRequest: 1536 << 20, MemLimit: 1536 << 20, ThrottledPct: 45, OOMKilled: i == 3, PodHours: 24}
	})
	r := Recommend(rows, nil)[0]
	notes := strings.Join(r.Notes, " ")
	if !strings.Contains(notes, "throttled 45%") || !strings.Contains(notes, "OOM-killed on 1 of 5") || !strings.Contains(notes, "Only 5 day") {
		t.Errorf("notes = %v", r.Notes)
	}
	// OOM: never below limit × 1.25 = 1920Mi.
	if r.Recommended.MemLimit < 1920<<20 {
		t.Errorf("memory after OOM = %s", MemString(r.Recommended.MemLimit))
	}
	if r.Confidence != "low" {
		t.Errorf("confidence = %s", r.Confidence)
	}
}

func TestRecommendFloorsAndMissingMemory(t *testing.T) {
	rows := days(10, func(int) store.CostContainerDay {
		return store.CostContainerDay{Name: "pkg-manager", Container: "pkg-manager", CPURequest: 1, MemRequest: 2000 << 20, MemLimit: 2000 << 20, PodHours: 24}
	})
	r := Recommend(rows, nil)[0]
	if CPUString(r.Recommended.CPURequest) != "10m" {
		t.Errorf("cpu floor = %s", CPUString(r.Recommended.CPURequest))
	}
	if r.Recommended.MemRequest != r.Current.MemRequest || r.Confidence != "medium" {
		t.Errorf("memory without data changed: %+v %s", r.Recommended, r.Confidence)
	}
}

func TestQuantityStrings(t *testing.T) {
	for in, want := range map[float64]string{0.25: "250m", 2: "2", 1.5: "1500m"} {
		if got := CPUString(in); got != want {
			t.Errorf("CPUString(%v) = %s", in, got)
		}
	}
	for in, want := range map[float64]string{1 << 30: "1Gi", 512 << 20: "512Mi", 100<<20 + 1: "101Mi"} {
		if got := MemString(in); got != want {
			t.Errorf("MemString(%v) = %s", in, got)
		}
	}
}
