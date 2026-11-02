package parser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/logrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func newLib(t *testing.T) *Library {
	t.Helper()
	st, err := store.Open("file:" + filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	lib, err := NewLibrary(st)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func find(lib *Library, id string) *logrules.Rule {
	for _, r := range lib.Rules() {
		if r.ID == id {
			return &r
		}
	}
	return nil
}

func TestLibraryAdminChanges(t *testing.T) {
	lib := newLib(t)
	builtins := lib.Rules()
	if len(builtins) == 0 {
		t.Fatal("no built-in rules")
	}
	b := builtins[0]

	// Disable a built-in: it stays listed, but no longer matches.
	if err := lib.SetEnabled(b.ID, false, "amey"); err != nil {
		t.Fatal(err)
	}
	if r := find(lib, b.ID); r == nil || r.Enabled || !r.Builtin || r.Edited || r.Pattern != b.Pattern {
		t.Fatalf("after disable: %+v", r)
	}
	for _, r := range lib.Set().Rules() {
		if r.ID == b.ID {
			t.Errorf("disabled rule %s is still compiled", b.ID)
		}
	}

	// Add a custom rule and test a line against it.
	custom := logrules.Rule{ID: "edweb-schema-newer", Title: "edweb build older than API", Services: []string{"edweb"},
		Pattern: `API schema \S+ newer than this build`, Severity: "warning", Examples: []string{"WARN API schema 4.2.11 newer than this build (4.2.8)"}}
	if err := lib.Save(custom, "amey"); err != nil {
		t.Fatal(err)
	}
	if got := lib.Test("edweb-prophecy-5f7c8d9b6-h8lzt", "WARN API schema 4.2.11 newer than this build (4.2.8)"); len(got) != 1 || got[0].ID != custom.ID {
		t.Errorf("test line = %+v", got)
	}
	if got := lib.Test("athena-1", "WARN API schema 4.2.11 newer than this build (4.2.8)"); len(got) != 0 {
		t.Errorf("rule scoped to edweb matched athena: %+v", got)
	}

	// An invalid rule is refused and does not change the library.
	if err := lib.Save(logrules.Rule{ID: "broken", Title: "x", Pattern: "(", Severity: "error"}, "amey"); err == nil {
		t.Error("invalid rule was saved")
	}

	// Edit a built-in, then reset it back to its default.
	edited := b
	edited.Title = "Edited title"
	if err := lib.Save(edited, "amey"); err != nil {
		t.Fatal(err)
	}
	if r := find(lib, b.ID); r.Title != "Edited title" || !r.Builtin || !r.Enabled || !r.Edited {
		t.Errorf("after edit: %+v", r)
	}
	if err := lib.Reset(b.ID); err != nil {
		t.Fatal(err)
	}
	if r := find(lib, b.ID); r.Title != b.Title || !r.Enabled || r.Edited {
		t.Errorf("after reset: %+v", r)
	}
	if err := lib.Reset(custom.ID); err != nil || find(lib, custom.ID) != nil {
		t.Errorf("custom rule not removed: %v", err)
	}
}

// The fake clientset answers every log request with "fake logs".
func TestWorkerCycle(t *testing.T) {
	lib := newLib(t)
	if err := lib.Save(logrules.Rule{ID: "fake-logs", Title: "Fake logs seen", Services: []string{"metagraph"},
		Pattern: `fake logs`, Severity: "error", Examples: []string{"fake logs"}}, "test"); err != nil {
		t.Fatal(err)
	}
	k := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "metagraph-prophecy-74dbc6c4f7-aaaaa", Namespace: "prophecy"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "metagraph"}, {Name: "sidecar"}}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "athena-prophecy-0", Namespace: "prophecy"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "athena"}}}},
	)
	reg := kube.NewRegistry(kube.Credentials{})
	reg.InjectForTest("c1", k, metricsfake.NewSimpleClientset())
	dir := t.TempDir()
	w, err := NewWorker(WorkerConfig{Dir: dir, Interval: time.Minute, RetentionDays: 30}, lib, lib.st, reg)
	if err != nil {
		t.Fatal(err)
	}
	if s := w.Summary("c1", "prophecy"); !s.Pending {
		t.Errorf("summary before any cycle should be pending: %+v", s)
	}
	w.Track(kube.Infra{ID: "c1", ClusterName: "dev-gke"})
	w.Cycle(context.Background())

	check := func(w *Worker, label string) {
		t.Helper()
		byPod := map[string]PodScan{}
		for _, p := range w.Summary("c1", "prophecy").Pods {
			byPod[p.Pod] = p
		}
		// Two containers of metagraph each match once; athena is out of the rule's scope.
		if m := byPod["metagraph-prophecy-74dbc6c4f7-aaaaa"]; m.Errors != 2 || m.Top != "Fake logs seen" || !m.Hits[0].Active {
			t.Errorf("%s: metagraph = %+v", label, m)
		}
		if a := byPod["athena-prophecy-0"]; a.Errors != 0 || len(a.Hits) != 0 {
			t.Errorf("%s: athena = %+v", label, a)
		}
	}
	check(w, "after cycle")

	// Hits are appended to a per-cluster, per-day file.
	files, _ := filepath.Glob(filepath.Join(dir, "hits", "c1", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("hit files = %v", files)
	}
	if raw, _ := os.ReadFile(files[0]); strings.Count(string(raw), "\n") != 2 {
		t.Errorf("hit file = %s", raw)
	}
	totals, _ := lib.st.LogHitTotals(time.Now().Add(-time.Hour))
	if totals["fake-logs"] != 2 {
		t.Errorf("24 h counters = %v", totals)
	}

	// A restarted console reloads the summary and the cluster list from disk.
	w2, err := NewWorker(WorkerConfig{Dir: dir, Interval: time.Minute, RetentionDays: 30}, lib, lib.st, reg)
	if err != nil {
		t.Fatal(err)
	}
	check(w2, "after reload")
	if len(w2.Clusters()) != 1 {
		t.Errorf("clusters after reload = %v", w2.Clusters())
	}
}

func TestNewLinesKeepsOnlyLinesAfterLast(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	lines := []kube.TimedLine{
		{TS: t0.Add(-time.Second), Text: "old"},
		{TS: t0, Text: "seen already"},
		{TS: t0.Add(time.Second), Text: "new ERROR"},
		{Text: "  at continuation of new"},
		{TS: t0.Add(2 * time.Second), Text: "newer"},
	}
	got := newLines(lines, t0)
	if len(got) != 3 || got[0].Text != "new ERROR" || got[1].Text != "  at continuation of new" {
		t.Errorf("newLines = %+v", got)
	}
	if all := newLines(lines, time.Time{}); len(all) != len(lines) {
		t.Errorf("first read should keep everything: %d", len(all))
	}
}

func TestNotReady(t *testing.T) {
	if !notReady(kube.Pod{Ready: "0/1"}) || notReady(kube.Pod{Ready: "1/1"}) {
		t.Error("notReady wrong")
	}
}

func TestMinesNamespace(t *testing.T) {
	w := &Worker{cfg: WorkerConfig{Namespaces: []string{"prophecy", "custom"}}}
	for ns, want := range map[string]bool{"prophecy": true, "prophecy-execution": true, "custom": true, "": true,
		"loki": false, "monitoring": false, "sla-tests": false, "kube-system": false} {
		if got := w.minesNamespace(ns); got != want {
			t.Errorf("minesNamespace(%q) = %v, want %v", ns, got, want)
		}
	}
}
