package server

import (
	"testing"
)

func TestAllClustersListed(t *testing.T) {
	h := newHarness(t)
	// SAP control plane with an execution plane, as control-hub nests them.
	h.ch.ControlPlanes["sap"][0]["executionPlanes"] = []any{map[string]any{"ID": 0, "id": "ep-1",
		"config": map[string]any{"clusterName": "sap-ep-cap", "request": map[string]any{"params": map[string]any{"region": "us-east1"}}},
		"state":  map[string]any{"status": "OPERATIONAL"}}}
	h.ch.ControlPlanes["sap"][0]["config"].(map[string]any)["clusterName"] = "0001234567"
	h.ch.ControlPlanes["dedicated-saas"][0]["isImported"] = true
	h.ch.ControlPlanes["dedicated-saas"][0]["clusterName"] = "wbcbre-live-aks"
	// One v2 cluster with credentials: listed once, as infrastructure, not again as imported.
	h.ch.Infrastructures = []map[string]any{{"ID": 0, "id": "infra-1", "clusterName": "acme-eks", "provider": "aws", "region": "us-east-1"}}
	h.ch.ControlPlanes["dedicated-saas"] = append(h.ch.ControlPlanes["dedicated-saas"],
		map[string]any{"ID": 0, "id": "cp-acme", "customerName": "acme", "clusterName": "acme-eks", "infrastructureId": "infra-1"})
	c := h.login("admin@prophecy.io")

	var list []map[string]any
	if st := h.call(c, "GET", "/api/clusters", nil, &list); st != 200 {
		t.Fatalf("status %d", st)
	}
	byID := map[string]map[string]any{}
	for _, x := range list {
		byID[x["id"].(string)] = x
	}
	if x := byID["infra-1"]; x == nil || x["kind"] != KindInfrastructure || x["access"] != "full" {
		t.Errorf("infrastructure cluster = %v", x)
	}
	if _, dup := byID["cp.cp-acme"]; dup {
		t.Error("a control plane on a listed infrastructure was listed again as imported")
	}
	want := map[string]string{"cp.cp-ok": KindImported, "sapcp.cp-cap": KindSAPControl, "sapep.ep-1": KindSAPExecution}
	for id, kind := range want {
		x, ok := byID[id]
		if !ok {
			t.Errorf("missing %s; have %v", id, keys(byID))
			continue
		}
		if x["kind"] != kind || x["access"] != "metrics" {
			t.Errorf("%s: kind %v access %v", id, x["kind"], x["access"])
		}
	}
	if x := byID["sapep.ep-1"]; x != nil && (x["name"] != "sap-ep-cap" || x["region"] != "us-east1") {
		t.Errorf("execution plane cluster = %v", x)
	}
	if x := byID["cp.cp-ok"]; x != nil && x["name"] != "wbcbre-live-aks" {
		t.Errorf("imported cluster = %v", x)
	}

	// Detail and Grafana routes work; explorer routes say there is no access.
	var d map[string]any
	if st := h.call(c, "GET", "/api/clusters/sapep.ep-1", nil, &d); st != 200 || d["access"] != "metrics" {
		t.Errorf("detail: %d %v", st, d)
	}
	if st := h.call(c, "GET", "/api/clusters/sapep.ep-1/issues", nil, nil); st != 200 {
		t.Errorf("issues: %d", st)
	}
	if st := h.call(c, "GET", "/api/clusters/sapep.ep-1/nodes", nil, nil); st != 409 {
		t.Errorf("nodes on a metrics-only cluster: %d, want 409", st)
	}
	if st := h.call(c, "GET", "/api/clusters/sapcp.nope", nil, nil); st != 404 {
		t.Errorf("unknown metrics-only cluster: %d", st)
	}
}

func keys(m map[string]map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
