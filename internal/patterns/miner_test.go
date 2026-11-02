package patterns

import (
	"strings"
	"testing"
	"time"
)

func TestMinerGroupsVariableLines(t *testing.T) {
	m := NewMiner()
	now := time.Now()
	lines := []string{
		`2026-10-17T10:00:01Z ERROR [pool-3] Connection to 10.0.4.12:5432 refused after 3000ms (attempt 1)`,
		`2026-10-17T10:00:05Z ERROR [pool-7] Connection to 10.0.4.13:5432 refused after 3000ms (attempt 2)`,
		`2026-10-17T10:01:00Z ERROR [pool-1] Connection to 10.0.9.1:5432 refused after 5000ms (attempt 3)`,
		`2026-10-17T10:02:00Z ERROR Failed to load workflow 8f14e45fceea167a5a36dedd4bea2543 for user jane@acme.com`,
	}
	for i, l := range lines {
		m.Observe([]string{"c1", "c2", "c1", "c1"}[i], "metagraph", l, now)
	}
	d := m.Dirty()
	if len(d) != 2 {
		t.Fatalf("templates = %d: %+v", len(d), d)
	}
	var conn Template
	for _, x := range d {
		if strings.Contains(x.Text, "Connection") {
			conn = x
		}
	}
	if conn.Count != 3 || conn.Clusters["c1"] != 2 || conn.Clusters["c2"] != 1 {
		t.Errorf("connection template = %+v", conn)
	}
	if !strings.Contains(conn.Text, "Connection to <*> refused after <*>") {
		t.Errorf("template text = %q", conn.Text)
	}
	for _, x := range d {
		if strings.Contains(x.Text+x.Example, "jane@acme.com") {
			t.Errorf("email kept: %+v", x)
		}
	}
	if len(m.Dirty()) != 0 {
		t.Error("dirty not cleared")
	}
}

func TestIsErrorAndService(t *testing.T) {
	for line, want := range map[string]bool{
		"ERROR something broke":                                               true,
		`{"level":"error","msg":"x"}`:                                         true,
		"INFO request ok in 3ms":                                              false,
		"java.lang.NullPointerException at Foo":                               true,
		"no errors found, all good (errorless)":                               false,
		"2026-10-17 [INFO] [PID: 7] Teardown failed: Error: expect(received)": false,
		`level=info msg="retrying after error"`:                               false,
	} {
		if got := IsError("", line); got != want {
			t.Errorf("IsError(%q) = %v", line, got)
		}
	}
	if !IsError("ERROR", "x") || IsError("INFO", "an error was retried") {
		t.Error("level word ignored")
	}
	for pod, want := range map[string]string{
		"metagraph-0": "metagraph", "edweb-prophecy-7d9c8b5f4-xk2pq": "edweb-prophecy", "athena-prophecy-0": "athena-prophecy",
		"prophecy-nginx-ingress-controller-5c6559cddd-gvlzf": "prophecy-nginx-ingress-controller",
		"sql-sql-ekr5cy": "sql-sql", "sql-sql-0ntwm0": "sql-sql", "python-gem-plugin-53fpy7": "python-gem-plugin",
		"knowledgegraph-prophecy-6f9d8c7b5-zq8kd": "knowledgegraph-prophecy", "loki-canary": "loki-canary",
	} {
		if got := Service(pod); got != want {
			t.Errorf("Service(%s) = %s", pod, got)
		}
	}
}

func TestTrend(t *testing.T) {
	now := time.Now()
	m := NewMiner()
	for i := 0; i < 5; i++ {
		m.Observe("c", "svc", "ERROR timeout talking to backend", now.Add(-time.Duration(i)*2*time.Hour))
	}
	tm := m.Dirty()[0]
	l6, l24, isNew := tm.Trend(now)
	if l6 != 3 || l24 != 5 || !isNew {
		t.Errorf("trend = %d %d %v", l6, l24, isNew)
	}
}

// Real orchestrator lines from alixpartners-emea: one error, three pods, and
// JSON keys in different orders must give one template.
func TestMinerGroupsJSONLinesAndPodNames(t *testing.T) {
	m := NewMiner()
	now := time.Now()
	lines := []string{
		`{"service":"orchestrator","level":"error","caller":"/appsrc/pkg/rm/k8s/resource_manager.go:812","time":"2026-10-19T02:37:39.569753083Z","message":"Pod go-operator-sandbox-tdeemg failed to start: Unschedulable: 0/3 nodes are available: 2 Insufficient cpu."}`,
		`{"service":"orchestrator","level":"error","caller":"/appsrc/pkg/rm/k8s/resource_manager.go:812","time":"2026-10-19T02:37:24.394579311Z","message":"Pod go-operator-sandbox-xmxfvk failed to start: Unschedulable: 0/3 nodes are available: 2 Insufficient cpu."}`,
		`{"service":"orchestrator","error":"Pod go-operator-sandbox-qecwao failed to start: Unschedulable: 0/3 nodes are available: 2 Insufficient cpu.","level":"error","caller":"/appsrc/pkg/rm/k8s/resource_manager.go:812"}`,
	}
	ids := map[string]bool{}
	for _, l := range lines {
		if tp := m.Observe("c1", "orchestrator-prophecy", l, now); tp != nil {
			ids[tp.ID] = true
		}
	}
	if len(ids) != 1 {
		t.Fatalf("want 1 template, got %d", len(ids))
	}
	for _, tp := range m.byID {
		if !strings.Contains(tp.Text, "go-operator-sandbox-<*>") || strings.Contains(tp.Text, "caller") {
			t.Errorf("template = %q", tp.Text)
		}
		if tp.Count != 3 {
			t.Errorf("count = %d", tp.Count)
		}
	}
}

func TestMessageText(t *testing.T) {
	if got := messageText(`{"msg":"boom","err":"x failed"}`); got != "boom x failed" {
		t.Errorf("got %q", got)
	}
	for _, l := range []string{`plain ERROR line`, `{not json`, `{"level":"error","code":5}`} {
		if got := messageText(l); got != l {
			t.Errorf("%q -> %q", l, got)
		}
	}
}

func TestServiceSandbox(t *testing.T) {
	for pod, want := range map[string]string{
		"go-operator-sandbox-fiaxyh": "go-operator-sandbox",
		"go-operator-sandbox-ekr5cy": "go-operator-sandbox",
		"orchestrator-prophecy-0":    "orchestrator-prophecy",
		"read-write":                 "read-write",
		"loki-read-7d9f8b6c4-x2kqp":  "loki-read",
	} {
		if got := Service(pod); got != want {
			t.Errorf("Service(%q) = %q, want %q", pod, got, want)
		}
	}
}
