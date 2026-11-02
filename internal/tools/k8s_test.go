package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
)

func k8sEnv(t *testing.T) (*Env, *fake.Clientset) {
	t.Helper()
	running := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "app"},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "web"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "web", Ready: true}}}}
	crashing := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "metagraph-0", Namespace: "app"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "metagraph"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "metagraph", RestartCount: 7,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}}}
	warn := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "app"}, Type: "Warning", Reason: "BackOff",
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "metagraph-0"}, Message: "Back-off restarting, token=abcdefghijklmnopqrstuvwxyz123456"}
	normal := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e2", Namespace: "app"}, Type: "Normal", Reason: "Pulled",
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-1"}, Message: "pulled"}
	k := fake.NewSimpleClientset(running, crashing, warn, normal)
	c := kube.NewClusterForTest(kube.Infra{ID: "infra-1", ClusterName: "acme-eks"}, k, metricsfake.NewSimpleClientset())
	env := &Env{Token: "tok", Kube: func(_ context.Context, token, cluster string) (*kube.Cluster, error) {
		if token != "tok" {
			t.Errorf("token %q not passed through", token)
		}
		if cluster != "acme-eks" {
			return nil, errors.New("no managed cluster")
		}
		return c, nil
	}}
	return env, k
}

func runTool(t *testing.T, env *Env, name string, input any) (string, error) {
	t.Helper()
	for _, tool := range Registry(env) {
		if tool.Name == name {
			b, _ := json.Marshal(input)
			return tool.Run(context.Background(), env, b)
		}
	}
	t.Fatalf("tool %s not registered", name)
	return "", nil
}

func TestK8sToolsRegisteredOnlyWithKube(t *testing.T) {
	for _, tool := range Registry(&Env{}) {
		if strings.HasPrefix(tool.Name, "k8s_") {
			t.Errorf("%s registered without cluster access", tool.Name)
		}
	}
	env, _ := k8sEnv(t)
	n := 0
	for _, tool := range Registry(env) {
		if strings.HasPrefix(tool.Name, "k8s_") {
			n++
			if tool.Writes {
				t.Errorf("%s writes", tool.Name)
			}
		}
	}
	if n != 7 {
		t.Errorf("%d k8s tools, want 7", n)
	}
}

func TestK8sListPodsProblemsOnly(t *testing.T) {
	env, _ := k8sEnv(t)
	out, err := runTool(t, env, "k8s_list_pods", map[string]any{"cluster": "acme-eks", "namespace": "app", "problems_only": true})
	if err != nil {
		t.Fatal(err)
	}
	var pods []kube.Pod
	if err := json.Unmarshal([]byte(out), &pods); err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].Name != "metagraph-0" {
		t.Errorf("problem pods = %+v", pods)
	}
}

func TestK8sUnknownClusterErrors(t *testing.T) {
	env, _ := k8sEnv(t)
	if _, err := runTool(t, env, "k8s_list_nodes", map[string]any{"cluster": "other"}); err == nil {
		t.Error("unknown cluster accepted")
	}
	if _, err := runTool(t, env, "k8s_list_nodes", map[string]any{}); err == nil {
		t.Error("missing cluster accepted")
	}
}

func TestK8sEventsWarningsMasked(t *testing.T) {
	env, _ := k8sEnv(t)
	out, err := runTool(t, env, "k8s_events", map[string]any{"cluster": "acme-eks", "namespace": "app"})
	if err != nil {
		t.Fatal(err)
	}
	var evs []kube.Event
	if err := json.Unmarshal([]byte(out), &evs); err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Reason != "BackOff" {
		t.Fatalf("events = %+v", evs)
	}
	if strings.Contains(evs[0].Message, "abcdefghijklmnop") {
		t.Errorf("token leaked: %s", evs[0].Message)
	}
}

func TestK8sDescribePodHidesManifestByDefault(t *testing.T) {
	env, _ := k8sEnv(t)
	out, err := runTool(t, env, "k8s_describe_pod", map[string]any{"cluster": "acme-eks", "namespace": "app", "pod": "metagraph-0"})
	if err != nil {
		t.Fatal(err)
	}
	var d kube.PodDetail
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if d.Name != "metagraph-0" || d.Manifest != "" || len(d.Containers) != 1 {
		t.Errorf("detail = %+v", d)
	}
	if _, err := runTool(t, env, "k8s_describe_pod", map[string]any{"cluster": "acme-eks", "namespace": "app", "pod": "../x"}); err == nil {
		t.Error("bad pod name accepted")
	}
}

func TestResolveKind(t *testing.T) {
	all := []kube.APIResource{
		{Key: "core/v1/pods", Resource: "pods", Kind: "Pod", Version: "v1", Namespaced: true},
		{Key: "acme.example.com/v1/deployments", Group: "acme.example.com", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		{Key: "apps/v1/deployments", Group: "apps", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		{Key: "cert-manager.io/v1/certificates", Group: "cert-manager.io", Resource: "certificates", Kind: "Certificate", Namespaced: true},
	}
	for kind, want := range map[string]string{
		"deploy":                          "apps/v1/deployments",
		"Deployment":                      "apps/v1/deployments", // apps beats a CRD with the same kind
		"pod":                             "core/v1/pods",
		"certificates.cert-manager.io":    "cert-manager.io/v1/certificates",
		"acme.example.com/v1/deployments": "acme.example.com/v1/deployments",
	} {
		r, err := resolveKind(all, kind)
		if err != nil {
			t.Errorf("%s: %v", kind, err)
			continue
		}
		if r.Key != want {
			t.Errorf("%s resolved to %s, want %s", kind, r.Key, want)
		}
	}
	if _, err := resolveKind(all, "widgets"); err == nil {
		t.Error("unknown kind resolved")
	}
}
