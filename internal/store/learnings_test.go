package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSearchLearnings(t *testing.T) {
	st, err := Open("file:" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, l := range []*Learning{
		{ID: "L1", Status: LearningApproved, Category: "athena-postdeploy", Title: "Metadata DB never created on Postgres 16",
			Symptoms:  "control plane stuck on prophecy status; metagraph CrashLoopBackOff; permission denied for schema public",
			RootCause: "PG15+ revoked CREATE on public schema", LastSeen: now},
		{ID: "L2", Status: LearningApproved, Category: "networking-dns", Title: "DNS step timeout",
			Symptoms: "failed to get ingress address after 5 minutes", LastSeen: now, Entities: `["capgroup"]`},
		{ID: "L3", Status: LearningDraft, Title: "permission denied for schema public (draft)", Symptoms: "permission denied", LastSeen: now},
	} {
		if err := st.SaveLearning(l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.SearchLearnings("metagraph crashloop: ERROR permission denied for schema public", "", "", []string{LearningApproved}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "L1" {
		t.Fatalf("search = %+v", got)
	}
	got, _ = st.SearchLearnings("", "", "capgroup", []string{LearningApproved}, 5)
	if len(got) != 1 || got[0].ID != "L2" {
		t.Errorf("entity search = %+v", got)
	}
	if got, _ := st.SearchLearnings("", "", "", []string{LearningApproved}, 5); len(got) != 0 {
		t.Errorf("empty query matched %d", len(got))
	}
	l, _ := st.GetLearning("L2")
	l.AddSighting("INC-1", "acme", now)
	l.AddSighting("INC-2", "ACME", now)
	if l.TimesSeen != 2 || len(l.EntityList()) != 2 || len(l.InvestigationList()) != 2 {
		t.Errorf("sightings: seen=%d entities=%v invs=%v", l.TimesSeen, l.EntityList(), l.InvestigationList())
	}
}

func TestCostContainerAggs(t *testing.T) {
	st, err := Open("file:" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range []struct {
		day      string
		p95, req float64
		oom      bool
	}{{"2026-10-01", 0.2, 8, false}, {"2026-10-02", 0.5, 8, true}, {"2026-10-03", 0.3, 4, false}} {
		ct := []CostContainerDay{{Day: d.day, Cluster: "c", Namespace: "ns", Kind: "Deployment", Name: "w", Container: "x",
			CPUP95: d.p95, MemMax: float64(i+1) * 1e9, CPURequest: d.req, OOMKilled: d.oom, PodHours: 24}}
		if err := st.ReplaceCostDay(&CostClusterDay{Day: d.day, Cluster: "c"}, nil, ct); err != nil {
			t.Fatal(err)
		}
	}
	aggs, err := st.CostContainerAggs("2026-10-01", "2026-10-31", "c", "")
	if err != nil || len(aggs) != 1 {
		t.Fatalf("aggs = %+v %v", aggs, err)
	}
	a := aggs[0]
	// Peaks over the range; requests from the latest day.
	if a.Days != 3 || a.CPUP95 != 0.5 || a.MemMax != 3e9 || a.OOMDays != 1 || a.PodHours != 72 || a.CPURequest != 4 {
		t.Errorf("agg = %+v", a)
	}
}

// A paraphrase finds nothing by itself; its rewordings do, and the original
// query's own match still ranks first.
func TestSearchLearningsAny(t *testing.T) {
	st, err := Open("file:" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, l := range []*Learning{
		{ID: "L1", Status: LearningApproved, Title: "Run fails when go-sandbox is Unschedulable",
			Symptoms: "failed to start: Unschedulable; Insufficient memory", LastSeen: now},
		{ID: "L2", Status: LearningApproved, Title: "Pipeline stuck waiting", Symptoms: "pipeline hangs in queued", LastSeen: now},
	} {
		if err := st.SaveLearning(l); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := st.SearchLearnings("jobs die because cluster is full", "", "", []string{LearningApproved}, 5); len(got) != 0 {
		t.Fatalf("paraphrase matched directly: %+v", got)
	}
	got, err := st.SearchLearningsAny([]string{"jobs die because cluster is full", "Unschedulable Insufficient memory", "pipeline queued"},
		"", "", []string{LearningApproved}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "L1" {
		t.Fatalf("fused = %+v", got)
	}
	got, _ = st.SearchLearningsAny([]string{"pipeline hangs", "Unschedulable"}, "", "", []string{LearningApproved}, 5)
	if len(got) != 2 || got[0].ID != "L2" {
		t.Fatalf("first query should weigh more: %+v", got)
	}
}
