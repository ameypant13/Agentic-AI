package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/athena"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
)

func athenaEnv(t *testing.T) (*Env, *[]string) {
	t.Helper()
	calls := &[]string{}
	ath := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"services":[{"name":"metagraph","status":"DOWN"}],"admin":"jane.doe@capgroup.com","password":"hunter2"}`))
	}))
	t.Cleanup(ath.Close)
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var rows []map[string]any
		switch r.URL.Query().Get("realm") {
		case "sap":
			rows = []map[string]any{{"ID": 0, "id": "cp-cap", "config": map[string]any{"customerName": "capgroup",
				"controlPlaneUrl": ath.URL, "request": map[string]any{"tenantName": "cap"}}, "state": map[string]any{"status": "OPERATIONAL"}}}
		case "dedicated-saas":
			rows = []map[string]any{
				{"ID": 0, "id": "cp-acme-old", "customerName": "acme", "controlPlaneUrl": "acme.app.prophecy.ai", "status": "DELETED"},
				{"ID": 0, "id": "cp-acme", "customerName": "acme", "controlPlaneUrl": ath.URL, "status": "OPERATIONAL"},
				{"ID": 0, "id": "cp-acme2", "customerName": "acme-two", "controlPlaneUrl": ath.URL, "status": "OPERATIONAL"},
			}
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	t.Cleanup(ch.Close)
	cl := athena.New("dGVzdDp0ZXN0").WithHTTPClient(ath.Client())
	return &Env{CH: controlhub.New(ch.URL), Token: "user-token", Athena: cl}, calls
}

func TestAthenaRead(t *testing.T) {
	env, calls := athenaEnv(t)
	out, err := runTool(t, env, "athena_read", map[string]any{"control_plane": "capgroup", "view": "service_status"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"id":"cp-cap"`) || !strings.Contains(out, "metagraph") {
		t.Errorf("result = %s", out)
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "jane.doe") || !strings.Contains(out, "j***@capgroup.com") {
		t.Errorf("not masked: %s", out)
	}
	if (*calls)[0] != "GET /athena/api/v1/service/status" {
		t.Errorf("athena call = %v", *calls)
	}
}

func TestAthenaResolveControlPlane(t *testing.T) {
	env, _ := athenaEnv(t)
	// Exact customer name wins over the partial match on acme-two; the deleted plane is skipped.
	out, err := runTool(t, env, "athena_read", map[string]any{"control_plane": "acme", "view": "cluster"})
	if err != nil || !strings.Contains(out, `"id":"cp-acme"`) {
		t.Errorf("acme = %s %v", out, err)
	}
	if _, err := runTool(t, env, "athena_read", map[string]any{"control_plane": "acm", "view": "cluster"}); err == nil ||
		!strings.Contains(err.Error(), "matches 2") {
		t.Errorf("ambiguous: %v", err)
	}
	if _, err := runTool(t, env, "athena_read", map[string]any{"control_plane": "nobody", "view": "cluster"}); err == nil {
		t.Error("unknown control plane accepted")
	}
	if _, err := runTool(t, env, "athena_read", map[string]any{"control_plane": "cp-cap", "view": "ondemandrestore"}); err == nil {
		t.Error("write view accepted")
	}
	env.Token = "someone-else"
	if _, err := runTool(t, env, "athena_read", map[string]any{"control_plane": "cp-cap", "view": "cluster"}); err == nil {
		t.Error("a caller control-hub rejects reached Athena")
	}
}
