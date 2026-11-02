package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestChangeTimeline(t *testing.T) {
	env, k := k8sEnv(t)
	now := time.Now()
	mk := func(name, rev, img string, at time.Time) {
		_, err := k.AppsV1().ReplicaSets("app").Create(context.Background(), &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app",
			CreationTimestamp: metav1.NewTime(at), Annotations: map[string]string{"deployment.kubernetes.io/revision": rev},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web"}}},
			Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: img}}}}}},
			metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}
	mk("web-1", "1", "web:1", now.Add(-10*24*time.Hour))
	mk("web-2", "2", "web:2", now.Add(-3*time.Hour))
	var gotNames []string
	env.Console = func(_ context.Context, names []string, _ time.Time) []Event {
		gotNames = names
		return []Event{{At: now.Add(-2 * time.Hour), Source: "finding", Object: "acme", What: "critical appeared: web CrashLoopBackOff"}}
	}
	out, err := runTool(t, env, "change_timeline", map[string]any{"target": "acme-eks", "hours": 24})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	// The rollout comes before the symptom it likely caused.
	if len(res.Events) != 2 || res.Events[0].What != "web:1 → web:2" || res.Events[1].Source != "finding" {
		t.Errorf("events = %+v", res.Events)
	}
	if len(gotNames) == 0 || gotNames[0] != "acme-eks" {
		t.Errorf("console asked about %v", gotNames)
	}
	out, err = runTool(t, env, "change_timeline", map[string]any{"target": "nobody"})
	if err != nil || len(out) == 0 {
		t.Errorf("unknown target: %s %v", out, err)
	}
}
