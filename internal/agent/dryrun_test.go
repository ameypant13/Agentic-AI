package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

func testPod(name, ownerKind, ownerName string) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prophecy", CreationTimestamp: metav1.NewTime(time.Now())},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if ownerKind != "" {
		p.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind, Name: ownerName}}
	}
	return p
}

func kubeEnv(k *fake.Clientset) *tools.Env {
	cl := kube.NewClusterForTest(kube.Infra{ID: "c1"}, k, metricsfake.NewSimpleClientset())
	return &tools.Env{Kube: func(context.Context, string, string) (*kube.Cluster, error) { return cl, nil }}
}

func TestDryRunRestartPod(t *testing.T) {
	env := kubeEnv(fake.NewSimpleClientset(testPod("metagraph-5d9f7c8b6-x2kqp", "ReplicaSet", "metagraph-5d9f7c8b6"),
		testPod("debug", "", ""), testPod("backup-123", "Job", "backup")))
	ctx := context.Background()
	plan, err := DryRun(ctx, env, KindRestartPod, ActionParams{Cluster: "c1", Namespace: "prophecy", Pod: "metagraph-5d9f7c8b6-x2kqp"})
	if err != nil || plan.Blocked != "" || len(plan.Steps) != 1 || plan.Targets[0] != "prophecy/metagraph-5d9f7c8b6-x2kqp" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	for _, pod := range []string{"debug", "backup-123"} {
		plan, err := DryRun(ctx, env, KindRestartPod, ActionParams{Cluster: "c1", Namespace: "prophecy", Pod: pod})
		if err != nil || plan.Blocked == "" {
			t.Errorf("%s: want blocked, got %+v %v", pod, plan, err)
		}
	}
	if _, err := DryRun(ctx, env, KindRestartPod, ActionParams{Cluster: "c1"}); err == nil {
		t.Error("missing pod accepted")
	}
}

func TestDryRunOrphanedSandboxesNoneFound(t *testing.T) {
	sb := testPod("go-operator-sandbox-abcdef", "", "")
	env := kubeEnv(fake.NewSimpleClientset(sb, testPod("orchestrator-0", "StatefulSet", "orchestrator")))
	plan, err := DryRun(context.Background(), env, KindDeleteOrphanedSandboxes, ActionParams{Cluster: "c1"})
	if err != nil || plan.Blocked == "" || len(plan.Steps) != 0 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}

func TestExecuteActionGuards(t *testing.T) {
	k := fake.NewSimpleClientset(testPod("metagraph-5d9f7c8b6-x2kqp", "ReplicaSet", "metagraph-5d9f7c8b6"))
	env := kubeEnv(k)
	st, err := store.Open("file:" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(ActionParams{Cluster: "c1", Namespace: "prophecy", Pod: "metagraph-5d9f7c8b6-x2kqp"})
	plan, _ := DryRun(context.Background(), env, KindRestartPod, ActionParams{Cluster: "c1", Namespace: "prophecy", Pod: "metagraph-5d9f7c8b6-x2kqp"})
	planJSON, _ := json.Marshal(plan)
	act := &store.Action{ID: "ACT-1", Kind: KindRestartPod, Params: string(params), Plan: string(planJSON), Status: "proposed"}
	if err := st.SaveAction(act); err != nil {
		t.Fatal(err)
	}
	user := User{Email: "a@b.c"}

	// Read-only: refused, nothing deleted.
	if err := ExecuteAction(context.Background(), st, env, act, user, true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("read-only: %v", err)
	}
	if pods, _ := k.CoreV1().Pods("prophecy").List(context.Background(), metav1.ListOptions{}); len(pods.Items) != 1 {
		t.Fatal("pod deleted in read-only mode")
	}

	// The plan the user saw names a different pod: refused.
	stale := *act
	stale.Plan = `{"targets":["prophecy/other"]}`
	if err := ExecuteAction(context.Background(), st, env, &stale, user, false); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("stale plan: %v", err)
	}

	// Writable and current: the pod is deleted.
	if err := ExecuteAction(context.Background(), st, env, act, user, false); err != nil || act.Status != "succeeded" {
		t.Fatalf("execute: %v %+v", err, act)
	}
	if pods, _ := k.CoreV1().Pods("prophecy").List(context.Background(), metav1.ListOptions{}); len(pods.Items) != 0 {
		t.Error("pod not deleted")
	}
}
