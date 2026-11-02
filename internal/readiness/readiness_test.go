package readiness

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/certs"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func helmSecret(status string, deployed time.Time) *corev1.Secret {
	rel := `{"name":"prophecy","version":7,"info":{"status":"` + status + `","last_deployed":"` + deployed.UTC().Format(time.RFC3339) +
		`"},"chart":{"metadata":{"name":"prophecy","version":"4.2.11.2"}}}`
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(rel))
	_ = zw.Close()
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.prophecy.v7", Namespace: "prophecy", Labels: map[string]string{"owner": "helm"}},
		Data: map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(gz.Bytes()))}}
}

func pod(name, image string, phase corev1.PodPhase, ready bool) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prophecy"},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: image}}},
		Status: corev1.PodStatus{Phase: phase, ContainerStatuses: []corev1.ContainerStatus{{Name: "c", Ready: ready}}}}
}

func node(name string, ready bool) *corev1.Node {
	st := corev1.ConditionTrue
	if !ready {
		st = corev1.ConditionFalse
	}
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: st}}}}
}

func byID(r Report) map[string]Check {
	out := map[string]Check{}
	for _, c := range r.Checks {
		out[c.ID] = c
	}
	return out
}

func TestHealthyIsGo(t *testing.T) {
	k := fake.NewSimpleClientset(node("n1", true), pod("metagraph-0", "gcr.io/prophecy/metagraph:4.2", corev1.PodRunning, true),
		pod("postgres-0", "postgres:13", corev1.PodRunning, true), helmSecret("deployed", time.Now().Add(-72*time.Hour)))
	cl := kube.NewClusterForTest(kube.Infra{ID: "c1"}, k, metricsfake.NewSimpleClientset())
	days := 60
	r := Run(context.Background(), ControlPlane{ID: "cp", Customer: "acme", URL: "acme.app.prophecy.ai", Status: "OPERATIONAL", PostDeployStatus: "completed"}, Sources{
		Findings: func([]string) []store.Finding { return nil },
		Certs:    func() []certs.Item { return []certs.Item{{Host: "acme.app.prophecy.ai", DaysLeft: &days}} },
		Cluster:  func(context.Context) (*kube.Cluster, error) { return cl, nil },
		Metrics: func(context.Context, string) ([]grafana.Sample, error) {
			return []grafana.Sample{{Labels: map[string]string{"namespace": "prophecy", "persistentvolumeclaim": "data"}, Value: 0.4}}, nil
		},
		Athena: func(context.Context, string) (map[string]any, error) {
			return map[string]any{"isProphecyDown": false}, nil
		},
		Rightsizing: func() []cost.Recommendation { return []cost.Recommendation{{Name: "metagraph", Container: "c"}} },
	})
	if r.Verdict != "go" {
		t.Errorf("verdict %s: %+v", r.Verdict, r.Checks)
	}
	if len(r.Checks) != 12 {
		t.Errorf("%d checks", len(r.Checks))
	}
}

func TestProblemsAreNoGo(t *testing.T) {
	k := fake.NewSimpleClientset(node("n1", false), pod("metagraph-0", "x", corev1.PodPending, false),
		pod("postgres-0", "docker.io/library/postgres:16.4", corev1.PodRunning, true), helmSecret("pending-upgrade", time.Now().Add(-time.Hour)))
	cl := kube.NewClusterForTest(kube.Infra{ID: "c1"}, k, metricsfake.NewSimpleClientset())
	days := 5
	r := Run(context.Background(), ControlPlane{ID: "cp", Customer: "acme", URL: "acme.app.prophecy.ai", Status: "UPDATE_FAILED", FailureReason: "helm timed out"}, Sources{
		Findings: func([]string) []store.Finding {
			return []store.Finding{{Severity: "critical", Title: "metagraph CrashLoopBackOff", State: "open"}}
		},
		Certs:   func() []certs.Item { return []certs.Item{{Host: "acme.app.prophecy.ai", DaysLeft: &days}} },
		Cluster: func(context.Context) (*kube.Cluster, error) { return cl, nil },
		Metrics: func(context.Context, string) ([]grafana.Sample, error) {
			return []grafana.Sample{{Labels: map[string]string{"namespace": "prophecy", "persistentvolumeclaim": "data-postgres-0"}, Value: 0.93}}, nil
		},
		Athena: func(context.Context, string) (map[string]any, error) {
			return map[string]any{"isProphecyDown": true}, nil
		},
		Rightsizing: func() []cost.Recommendation {
			return []cost.Recommendation{{Name: "metagraph", Container: "c", OOMDays: 2}}
		},
	})
	c := byID(r)
	for id, want := range map[string]Status{"control-plane": Fail, "findings": Fail, "certificates": Fail, "nodes": Fail, "pods": Warn,
		"helm": Fail, "postgres": Pass, "disk": Fail, "athena": Fail, "oom": Warn, "changes": Warn} {
		if c[id].Status != want {
			t.Errorf("%s = %s (%s), want %s", id, c[id].Status, c[id].Detail, want)
		}
	}
	if r.Verdict != "no-go" || !strings.Contains(r.Summary, "Fix before upgrading") {
		t.Errorf("verdict %s: %s", r.Verdict, r.Summary)
	}
	if !strings.Contains(c["postgres"].Detail, "Postgres 16") {
		t.Errorf("postgres = %+v", c["postgres"])
	}
}

func TestMissingSourcesAreUnknown(t *testing.T) {
	r := Run(context.Background(), ControlPlane{Status: "OPERATIONAL"}, Sources{
		Cluster: func(context.Context) (*kube.Cluster, error) { return nil, errors.New("Unauthorized") },
	})
	c := byID(r)
	for _, id := range []string{"findings", "certificates", "nodes", "pods", "helm", "postgres", "disk", "athena", "oom", "changes"} {
		if c[id].Status != Unknown {
			t.Errorf("%s = %s", id, c[id].Status)
		}
	}
	if r.Verdict != "unknown" || !strings.Contains(r.Summary, "could not run") {
		t.Errorf("verdict %s: %s", r.Verdict, r.Summary)
	}
}
