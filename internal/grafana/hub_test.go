package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMatchAndResolve(t *testing.T) {
	h := NewHub()
	h.index["azure"] = &clusterIndex{prom: []string{"medmutual", "prophecy-westpac-prod", "gchp-prod-vfndl-aks"},
		loki: []string{"medmutual", "prophecy-westpac-prod-aks", "gchp-prod-vfndl-aks"}}
	h.index["aws"] = &clusterIndex{prom: []string{"capgroup-prod-6568q-nl7vd"}, loki: []string{"capgroup-prod-6568q-nl7vd"}}

	cases := map[string]Mapping{
		"medmutual":                 {Instance: "azure", PromCluster: "medmutual", LokiCluster: "medmutual"},
		"prophecy-westpac-prod":     {Instance: "azure", PromCluster: "prophecy-westpac-prod", LokiCluster: "prophecy-westpac-prod-aks"},
		"gchp-prod-vfndl":           {Instance: "azure", PromCluster: "gchp-prod-vfndl-aks", LokiCluster: "gchp-prod-vfndl-aks"},
		"CAPGROUP-prod-6568q-nl7vd": {Instance: "aws", PromCluster: "capgroup-prod-6568q-nl7vd", LokiCluster: "capgroup-prod-6568q-nl7vd"},
	}
	for name, want := range cases {
		got := h.Resolve(name)
		if got == nil || *got != want {
			t.Errorf("Resolve(%q) = %+v, want %+v", name, got, want)
		}
	}
	if h.Resolve("unknown-cluster") != nil {
		t.Error("unknown cluster resolved")
	}
	h.SetOverride("unknown-cluster", &Mapping{Instance: "aws", PromCluster: "x"})
	if m := h.Resolve("unknown-cluster"); m == nil || !m.Override || m.PromCluster != "x" {
		t.Errorf("override = %+v", m)
	}
}

func TestQuote(t *testing.T) {
	if got := Quote(`a"b`); got != `"a\"b"` {
		t.Errorf("Quote = %s", got)
	}
}

func TestParseMappings(t *testing.T) {
	h := NewHub()
	n := h.ParseMappings("wbcbre-live-9xtxg-aks=azure:prophecy-westpac-prod:prophecy-westpac-prod-aks, x=aws:y ,bad,=azure:z")
	if n != 2 {
		t.Fatalf("valid mappings = %d, want 2", n)
	}
	m := h.Resolve("wbcbre-live-9xtxg-aks")
	if m == nil || m.Instance != "azure" || m.PromCluster != "prophecy-westpac-prod" || m.LokiCluster != "prophecy-westpac-prod-aks" || !m.Override {
		t.Errorf("mapping = %+v", m)
	}
	if m := h.Resolve("x"); m == nil || m.PromCluster != "y" || m.LokiCluster != "" {
		t.Errorf("x = %+v", m)
	}
}

func TestRefreshKeepsLastGoodListOnError(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/datasources":
			w.Write([]byte(`[{"uid":"prom","type":"prometheus"},{"uid":"loki","type":"loki"}]`))
		case fail:
			http.Error(w, `{"message":"InternalError"}`, http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "/loki/"):
			w.Write([]byte(`{"status":"success","data":["c1-aks"]}`))
		default:
			w.Write([]byte(`{"status":"success","data":["c1"]}`))
		}
	}))
	defer srv.Close()
	h := NewHub()
	h.Add(Instance{Name: "azure", URL: srv.URL, Token: "t"}, "secret")
	if !h.Refresh(context.Background()) || h.Resolve("c1") == nil {
		t.Fatal("first refresh failed")
	}
	fail = true
	if h.Refresh(context.Background()) {
		t.Error("failed refresh reported ok")
	}
	if m := h.Resolve("c1"); m == nil || m.LokiCluster != "c1-aks" {
		t.Errorf("mapping lost after a failed refresh: %+v", m)
	}
	if v := h.Instances(); len(v) != 1 || v[0].Error == "" {
		t.Errorf("error not reported: %+v", v)
	}
}
