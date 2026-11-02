package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	logging "google.golang.org/api/logging/v2"
)

type fakeLister struct {
	entries []*logging.LogEntry
	filter  string
}

func (f *fakeLister) List(_ context.Context, _ []string, filter string, _ int) ([]*logging.LogEntry, error) {
	f.filter = filter
	return f.entries, nil
}

func entry(t *testing.T, ts, actor, method, resource string, request, response map[string]any) *logging.LogEntry {
	t.Helper()
	p := map[string]any{"methodName": method, "resourceName": resource,
		"authenticationInfo": map[string]any{"principalEmail": actor},
		"requestMetadata":    map[string]any{"callerIp": "223.233.82.39", "callerSuppliedUserAgent": "kubectl/v1.34.0 (darwin/arm64)"},
		"status":             map[string]any{"code": 0}}
	if request != nil {
		p["request"] = request
	}
	if response != nil {
		p["response"] = response
	}
	raw, _ := json.Marshal(p)
	return &logging.LogEntry{Timestamp: ts, ProtoPayload: raw,
		Resource: &logging.MonitoredResource{Labels: map[string]string{"cluster_name": "prophecy-cloud-mgmt", "project_id": "prophecy-cloud"}}}
}

func pvc(size string) map[string]any {
	return map[string]any{"kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": "data", "resourceVersion": size},
		"spec": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": size}}}, "status": map[string]any{"phase": "Bound"}}
}

// Who changed the PVC to 100Gi: the person's patch, with before → after from
// the previous snapshot; the controller that followed is left out.
func TestKubernetesWhoChangedPVC(t *testing.T) {
	const res = "core/v1/namespaces/prophecy/persistentvolumeclaims/data"
	f := &fakeLister{entries: []*logging.LogEntry{ // newest first, as the API returns them
		entry(t, "2026-10-21T09:31:00Z", "system:serviceaccount:kube-system:persistent-volume-binder", "io.k8s.core.v1.persistentvolumeclaims.update", res, nil, pvc("100Gi")),
		entry(t, "2026-10-21T09:30:51Z", "pant.amey@prophecy.io", "io.k8s.core.v1.persistentvolumeclaims.patch", res,
			map[string]any{"@type": "k8s.io/Patch", "spec": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "100Gi"}}}}, pvc("100Gi")),
		entry(t, "2026-10-01T08:00:00Z", "helm@prophecy.io", "io.k8s.core.v1.persistentvolumeclaims.create", res, nil, pvc("10Gi")),
	}}
	s := NewWithLister([]string{"prophecy-cloud"}, f)
	events, err := s.Kubernetes(context.Background(), Query{Cluster: "prophecy-cloud-mgmt", Kind: "pvc", Namespace: "prophecy", Name: "data",
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	e := events[0]
	if e.Actor != "pant.amey@prophecy.io" || e.ActorType != "person" || e.Tool != "kubectl" || e.Action != "patch" {
		t.Errorf("event = %+v", e)
	}
	want := "spec.resources.requests.storage: 10Gi → 100Gi"
	if len(e.Changes) != 1 || e.Changes[0] != want {
		t.Errorf("changes = %v, want [%s]", e.Changes, want)
	}
	for _, part := range []string{`resource.labels.cluster_name="prophecy-cloud-mgmt"`, `protoPayload.resourceName:"namespaces/prophecy/persistentvolumeclaims/data"`,
		`NOT protoPayload.methodName:".status."`, `timestamp>="2026-10-01T00:00:00Z"`} {
		if !strings.Contains(f.filter, part) {
			t.Errorf("filter lacks %s: %s", part, f.filter)
		}
	}
}

func TestSecretsAreNotShown(t *testing.T) {
	f := &fakeLister{entries: []*logging.LogEntry{
		entry(t, "2026-10-21T09:30:51Z", "pant.amey@prophecy.io", "io.k8s.core.v1.secrets.patch", "core/v1/namespaces/x/secrets/s",
			map[string]any{"@type": "k8s.io/Patch", "data": map[string]any{"password": "aHVudGVyMg=="}}, map[string]any{"data": map[string]any{"password": "aHVudGVyMg=="}}),
	}}
	events, _ := NewWithLister([]string{"p"}, f).Kubernetes(context.Background(), Query{Cluster: "c"})
	if b, _ := json.Marshal(events); strings.Contains(string(b), "aHVudGVyMg") {
		t.Fatalf("secret leaked: %s", b)
	}
}

func TestActorTypeAndTool(t *testing.T) {
	for actor, want := range map[string]string{
		"pant.amey@prophecy.io": "person", "system:serviceaccount:kube-system:persistent-volume-binder": "system",
		"system:pdcsi-controller": "system", "system:node:gke-x": "system", "system:serviceaccount:argocd:argocd-server": "serviceaccount",
		"control-hub-admin@prophecy-cloud.iam.gserviceaccount.com": "serviceaccount", "": "unknown",
	} {
		if got := actorType(actor); got != want {
			t.Errorf("actorType(%q) = %s, want %s", actor, got, want)
		}
	}
	for ua, want := range map[string]string{"kubectl/v1.34.0 (darwin/arm64)": "kubectl", "Helm/3.15.2": "helm",
		"Mozilla/5.0 (Macintosh)": "Cloud Console", "argocd-application-controller/v2": "argocd"} {
		if got := toolOf(ua); got != want {
			t.Errorf("toolOf(%q) = %s, want %s", ua, got, want)
		}
	}
	if Plural("pvc") != "persistentvolumeclaims" || Plural("Deployment") != "deployments" || Plural("networkpolicy") != "networkpolicies" {
		t.Error("Plural")
	}
}

func TestQuoteEscapes(t *testing.T) {
	f := KubernetesFilter(Query{Actor: `a" OR 1=1`})
	if !strings.Contains(f, `principalEmail:"a\" OR 1=1"`) {
		t.Errorf("filter = %s", f)
	}
}
