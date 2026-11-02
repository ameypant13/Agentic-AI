package kube

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func res(cpu, mem string) corev1.ResourceList {
	return corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(mem)}
}

func node(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"cloud.google.com/gke-nodepool": "pool-a"}},
		Status: corev1.NodeStatus{
			Allocatable: res("4", "16Gi"),
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func pod(name string, mut func(*corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prophecy",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: name + "-7d9f8c6b5"}}},
		Spec: corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{
			Name: "app", Image: "app:1",
			Resources: corev1.ResourceRequirements{Requests: res("1", "4Gi"), Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("6Gi")}},
			Env:       []corev1.EnvVar{{Name: "DB_PASSWORD", Value: "hunter2"}, {Name: "LOG_LEVEL", Value: "info"}},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning,
			Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}},
	}
	if mut != nil {
		mut(p)
	}
	return p
}

func testCluster(objs ...interface{ GetName() string }) *Cluster {
	k := fake.NewSimpleClientset()
	for _, o := range objs {
		switch v := o.(type) {
		case *corev1.Node:
			_, _ = k.CoreV1().Nodes().Create(context.Background(), v, metav1.CreateOptions{})
		case *corev1.Pod:
			_, _ = k.CoreV1().Pods(v.Namespace).Create(context.Background(), v, metav1.CreateOptions{})
		}
	}
	return NewClusterForTest(Infra{ID: "c1"}, k, metricsfake.NewSimpleClientset())
}

func TestPodStatusAndIssues(t *testing.T) {
	oom := pod("metagraph", func(p *corev1.Pod) {
		p.Status.ContainerStatuses[0].RestartCount = 7
		p.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(time.Now().Add(-time.Hour))}}
	})
	pull := pod("edweb", func(p *corev1.Pod) {
		p.Status.Phase = corev1.PodPending
		p.Status.Conditions[0].Status = corev1.ConditionFalse
		p.Status.ContainerStatuses[0] = corev1.ContainerStatus{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}
	})
	unsched := pod("copilot", func(p *corev1.Pod) {
		p.Spec.NodeName = ""
		p.Status = corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Message: "0/6 nodes are available: 3 Insufficient memory."}}}
	})
	cl := testCluster(node("n1"), oom, pull, unsched, pod("athena", nil))

	pods, err := cl.Pods(context.Background(), "prophecy")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Pod{}
	for _, p := range pods {
		got[p.Name] = p
	}
	cases := []struct{ name, status, issue, sev string }{
		{"metagraph", "Running", "OOMKilled 1h ago", "critical"},
		{"edweb", "ImagePullBackOff", "ImagePullBackOff", "critical"},
		{"copilot", "Pending", "Unschedulable: Insufficient memory", "warning"},
		{"athena", "Running", "", ""},
	}
	for _, c := range cases {
		p := got[c.name]
		if p.Status != c.status || p.Issue != c.issue || p.IssueSeverity != c.sev {
			t.Errorf("%s: got status=%q issue=%q sev=%q, want %q %q %q", c.name, p.Status, p.Issue, p.IssueSeverity, c.status, c.issue, c.sev)
		}
	}
	if m := got["metagraph"]; m.Owner != "Deployment/metagraph" || m.Restarts != 7 || m.MemRequest != "4Gi" || m.MemLimit != "6Gi" {
		t.Errorf("metagraph view = %+v", m)
	}
}

func TestSummary(t *testing.T) {
	cl := testCluster(node("n1"), node("n2"), pod("a", nil), pod("b", func(p *corev1.Pod) {
		p.Status.Conditions[0].Status = corev1.ConditionFalse
	}), pod("done", func(p *corev1.Pod) { p.Status.Phase = corev1.PodSucceeded }))
	s, err := cl.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 2 running pods × (1 CPU, 4Gi) on 2 nodes × (4 CPU, 16Gi); the finished pod does not count.
	if s.Nodes != 2 || s.NodesReady != 2 || s.Pods != 2 || s.PodsNotReady != 1 || s.CPURequestedPct != 25 || s.MemRequestedPct != 25 {
		t.Errorf("summary = %+v", s)
	}
	if s.CPUDetail != "2.0 / 8 cores" || s.MemDetail != "8 / 32 GiB" {
		t.Errorf("details = %q, %q", s.CPUDetail, s.MemDetail)
	}
}

func TestManifestMasksSecrets(t *testing.T) {
	p := pod("x", func(p *corev1.Pod) {
		p.Annotations = map[string]string{"kubectl.kubernetes.io/last-applied-configuration": `{"env":"hunter2"}`}
		p.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}
	})
	cl := testCluster(node("n1"), p)
	d, err := cl.PodDetail(context.Background(), "prophecy", "x")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.Manifest, "hunter2") || strings.Contains(d.Manifest, "managedFields") {
		t.Errorf("manifest leaks secrets or managedFields:\n%s", d.Manifest)
	}
	if !strings.Contains(d.Manifest, "value: info") || !strings.Contains(d.Manifest, "kind: Pod") {
		t.Errorf("manifest lost normal content:\n%s", d.Manifest)
	}
	if len(d.Containers) != 1 || d.Containers[0].MemLimit != "6Gi" {
		t.Errorf("containers = %+v", d.Containers)
	}
}

func TestFormatting(t *testing.T) {
	for in, want := range map[int64]string{120: "120m", 1400: "1.4", 2000: "2"} {
		if got := fmtCPU(in); got != want {
			t.Errorf("fmtCPU(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[int64]string{410 << 20: "410Mi", 6 << 30: "6Gi", 5939 << 20: "5.8Gi"} {
		if got := fmtMem(in); got != want {
			t.Errorf("fmtMem(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestNodeProblem(t *testing.T) {
	cases := []struct {
		typ    corev1.NodeConditionType
		status corev1.ConditionStatus
		want   bool
	}{
		{corev1.NodeReady, corev1.ConditionTrue, false},
		{corev1.NodeMemoryPressure, corev1.ConditionTrue, true},
		{corev1.NodeMemoryPressure, corev1.ConditionFalse, false},
		{corev1.NodeNetworkUnavailable, corev1.ConditionTrue, true},
		{"KernelReady", corev1.ConditionTrue, false}, // EKS Auto Mode
		{"StorageReady", corev1.ConditionFalse, true},
		{"KernelDeadlock", corev1.ConditionTrue, true}, // node-problem-detector
		{"DiskPressure", corev1.ConditionUnknown, false},
	}
	for _, c := range cases {
		if got := nodeProblem(corev1.NodeCondition{Type: c.typ, Status: c.status}); got != c.want {
			t.Errorf("%s=%s: got %v, want %v", c.typ, c.status, got, c.want)
		}
	}
}

func TestAbandonedSandboxLine(t *testing.T) {
	line := `2026-10-22T06:14:11.964Z {"service":"orchestrator","level":"error","caller":"/appsrc/pkg/rm/k8s/resource_manager.go:812","message":"Pod go-operator-sandbox-icmncx failed to start: Unschedulable: 0/10 nodes are available: 10 Insufficient memory"}`
	m := abandonedSandbox.FindStringSubmatch(line)
	if m == nil || m[1] != "go-operator-sandbox-icmncx" {
		t.Fatalf("got %v", m)
	}
	if abandonedSandbox.MatchString(`created go-operator-sandbox-icmncx`) {
		t.Error("creation line matched")
	}
}

func TestSandboxesListsAndSumsRequests(t *testing.T) {
	sb := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "go-operator-sandbox-icmncx", Namespace: "prophecy-execution",
		CreationTimestamp: metav1.NewTime(time.Now().Add(-3 * time.Hour))},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "sandbox", Resources: corev1.ResourceRequirements{Requests: res("2", "4Gi")}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	done := sb.DeepCopy()
	done.Name, done.Status.Phase = "go-operator-sandbox-old", corev1.PodSucceeded
	orch := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orchestrator-0", Namespace: "prophecy-execution"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "orchestrator"}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	cl := NewClusterForTest(Infra{ID: "c"}, fake.NewSimpleClientset(sb, done, orch), metricsfake.NewSimpleClientset())
	rep, err := cl.Sandboxes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 1 || !rep.LogsRead || rep.Orphaned != 0 {
		t.Fatalf("%+v", rep)
	}
	if s := rep.Sandboxes[0]; s.CPURequest != 2 || s.MemRequest != 4<<30 || s.AgeHours < 2.9 {
		t.Errorf("%+v", s)
	}
}

func TestOwnedPaths(t *testing.T) {
	var tree map[string]any
	_ = json.Unmarshal([]byte(`{"f:metadata":{"f:labels":{".":{},"f:app":{}}},"f:spec":{"f:resources":{"f:requests":{"f:storage":{}}},"f:containers":{"k:{\"name\":\"c\"}":{}}}}`), &tree)
	var got []string
	for _, p := range ownedPaths(tree, nil) {
		got = append(got, strings.Join(p, "."))
	}
	want := []string{"metadata.labels.app", "spec.containers", "spec.resources.requests.storage"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}
