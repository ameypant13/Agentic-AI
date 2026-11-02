package kube

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func helmSecret(t *testing.T, name string, rev int, deployed time.Time) *corev1.Secret {
	rel := `{"name":"prophecy","version":` + itoa(rev) + `,"info":{"status":"deployed","last_deployed":"` + deployed.UTC().Format(time.RFC3339) +
		`","description":"Upgrade complete"},"chart":{"metadata":{"name":"prophecy","version":"4.2.11.` + itoa(rev) + `"}}}`
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(rel))
	_ = zw.Close()
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prophecy", Labels: map[string]string{"owner": "helm"}},
		Data: map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(gz.Bytes()))}}
}

func itoa(n int) string { return strconv.Itoa(n) }

func rs(name string, rev string, created time.Time, image string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prophecy", CreationTimestamp: metav1.NewTime(created),
		Annotations:     map[string]string{"deployment.kubernetes.io/revision": rev},
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "metagraph"}}},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "metagraph", Image: image}}}}}}
}

func TestChanges(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	old, recent := now.Add(-30*24*time.Hour), now.Add(-2*time.Hour)
	k := fake.NewSimpleClientset(
		helmSecret(t, "sh.helm.release.v1.prophecy.v6", 6, old),
		helmSecret(t, "sh.helm.release.v1.prophecy.v7", 7, recent),
		rs("metagraph-a", "3", old, "gcr.io/prophecy/metagraph:4.2.10"),
		rs("metagraph-b", "4", recent.Add(time.Minute), "gcr.io/prophecy/metagraph:4.2.11"),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n-new", CreationTimestamp: metav1.NewTime(recent.Add(2 * time.Minute)),
			Labels: map[string]string{"node.kubernetes.io/instance-type": "m6a.2xlarge", "karpenter.sh/nodepool": "general"}}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n-old", CreationTimestamp: metav1.NewTime(old)}},
	)
	c := NewClusterForTest(Infra{ID: "c1"}, k, metricsfake.NewSimpleClientset())
	got, err := c.Changes(context.Background(), now.Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("changes = %+v", got)
	}
	if got[0].Source != "helm" || !strings.Contains(got[0].What, "revision 7") || !strings.Contains(got[0].What, "4.2.11.7") {
		t.Errorf("helm = %+v", got[0])
	}
	if got[1].Object != "deployment/metagraph" || got[1].What != "gcr.io/prophecy/metagraph:4.2.10 → gcr.io/prophecy/metagraph:4.2.11" {
		t.Errorf("rollout = %+v", got[1])
	}
	if got[2].Source != "node" || !strings.Contains(got[2].What, "m6a.2xlarge, pool general") {
		t.Errorf("node = %+v", got[2])
	}
}

func TestChangesLabelsRestarts(t *testing.T) {
	now := time.Now().UTC()
	a := rs("web-a", "1", now.Add(-3*time.Hour), "web:1")
	b := rs("web-b", "2", now.Add(-2*time.Hour), "web:1")
	b.Spec.Template.Annotations = map[string]string{"kubectl.kubernetes.io/restartedAt": now.Format(time.RFC3339)}
	c := rs("web-c", "3", now.Add(-time.Hour), "web:1")
	c.Spec.Template.Annotations = b.Spec.Template.Annotations
	c.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "X", Value: "1"}}
	cl := NewClusterForTest(Infra{ID: "c1"}, fake.NewSimpleClientset(a, b, c), metricsfake.NewSimpleClientset())
	got, err := cl.Changes(context.Background(), now.Add(-90*time.Minute-time.Hour))
	if err != nil || len(got) != 2 {
		t.Fatalf("changes = %+v %v", got, err)
	}
	if !strings.HasPrefix(got[0].What, "restarted") || !strings.HasPrefix(got[1].What, "pod spec changed") {
		t.Errorf("labels = %q, %q", got[0].What, got[1].What)
	}
}
