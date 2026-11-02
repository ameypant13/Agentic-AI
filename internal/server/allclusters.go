package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
)

// Clusters beyond /api/infrastructures. control-hub's existing endpoints also
// describe imported clusters (v2 control planes with isImported) and the SAP
// GKE clusters (v1 control and execution planes). control-hub keeps no
// credentials for these, so the console shows them from Grafana's metrics
// only: summary, issues and charts, but no node or pod explorer.
//
// Their IDs carry a prefix so they never collide with infrastructure IDs:
// cp.<control plane id>, sapcp.<control plane id>, sapep.<execution plane id>.

const (
	KindInfrastructure = "infrastructure"
	KindImported       = "imported"
	KindSAPControl     = "sap-control-plane"
	KindSAPExecution   = "sap-execution-plane"
)

type extraCluster struct {
	Infra   kube.Infra
	Kind    string
	Tenants []string
}

// metricsOnlyID reports whether an ID names a cluster without credentials.
func metricsOnlyID(id string) bool {
	return strings.HasPrefix(id, "cp.") || strings.HasPrefix(id, "sapcp.") || strings.HasPrefix(id, "sapep.")
}

// planeRow is the part of control-hub's control-plane list used here. v2 rows
// are flat; SAP (v1) rows nest config/state and carry their execution planes.
type planeRow struct {
	// GormID absorbs control-hub's numeric "ID" (gorm.Model); without it Go's
	// case-insensitive matching puts that number into ID and the list fails.
	GormID           any    `json:"ID"`
	ID               string `json:"id"`
	CustomerName     string `json:"customerName"`
	ClusterName      string `json:"clusterName"`
	Provider         string `json:"provider"`
	Region           string `json:"region"`
	Status           string `json:"status"`
	InfrastructureID string `json:"infrastructureId"`
	IsImported       bool   `json:"isImported"`
	Config           *struct {
		CustomerName    string `json:"customerName"`
		ClusterName     string `json:"clusterName"`
		ControlPlaneURL string `json:"controlPlaneUrl"`
		Request         struct {
			Provider string `json:"provider"`
			Region   string `json:"region"`
		} `json:"request"`
	} `json:"config"`
	State *struct {
		Status string `json:"status"`
	} `json:"state"`
	ExecutionPlanes []struct {
		GormID any    `json:"ID"`
		ID     string `json:"id"`
		Config struct {
			ClusterName string `json:"clusterName"`
			Request     struct {
				Provider string `json:"provider"`
				Region   string `json:"region"`
				Params   struct {
					Region string `json:"region"`
				} `json:"params"`
			} `json:"request"`
		} `json:"config"`
		State struct {
			Status string `json:"status"`
		} `json:"state"`
	} `json:"executionPlanes"`
}

// extraClusters lists imported and SAP clusters visible to the caller, from
// /api/control-planes with the caller's token. infraIDs are the clusters
// /api/infrastructures already returned.
func (s *Server) extraClusters(ctx context.Context, token string, infraIDs map[string]bool) []extraCluster {
	var out []extraCluster
	seen := map[string]bool{}
	add := func(e extraCluster) {
		// control-hub sometimes stores names with stray spaces ("pc-cznl-1-rg86s-aks ").
		e.Infra.ClusterName = strings.TrimSpace(e.Infra.ClusterName)
		if e.Infra.ClusterName == "" || seen[e.Infra.ID] {
			return
		}
		seen[e.Infra.ID] = true
		out = append(out, e)
	}
	for _, realm := range []string{"dedicated-saas", "public-saas", "sap"} {
		raw, err := s.ch.Get(ctx, token, "/api/control-planes", url.Values{"realm": {realm}, "limit": {"2000"}})
		if err != nil {
			continue // the caller may not see this realm
		}
		var rows []planeRow
		if json.Unmarshal(raw, &rows) != nil {
			continue
		}
		for _, r := range rows {
			if r.Config == nil {
				// v2: only clusters /api/infrastructures does not cover.
				if infraIDs[r.InfrastructureID] {
					continue
				}
				add(extraCluster{Kind: KindImported, Tenants: []string{r.CustomerName},
					Infra: kube.Infra{ID: "cp." + r.ID, ClusterName: r.ClusterName, Provider: r.Provider, Region: r.Region, Status: r.Status}})
				continue
			}
			tenant := strings.SplitN(r.Config.ControlPlaneURL, ".", 2)[0]
			if tenant == "" {
				tenant = r.Config.CustomerName
			}
			status := ""
			if r.State != nil {
				status = r.State.Status
			}
			provider := firstNonEmptyStr(r.Config.Request.Provider, "gcp")
			add(extraCluster{Kind: KindSAPControl, Tenants: []string{tenant},
				Infra: kube.Infra{ID: "sapcp." + r.ID, ClusterName: r.Config.ClusterName, Provider: provider, Region: r.Config.Request.Region, Status: status}})
			for _, ep := range r.ExecutionPlanes {
				add(extraCluster{Kind: KindSAPExecution, Tenants: []string{tenant},
					Infra: kube.Infra{ID: "sapep." + ep.ID, ClusterName: ep.Config.ClusterName,
						Provider: firstNonEmptyStr(ep.Config.Request.Provider, provider),
						Region:   firstNonEmptyStr(ep.Config.Request.Region, ep.Config.Request.Params.Region, r.Config.Request.Region),
						Status:   ep.State.Status}})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Infra.ClusterName < out[j].Infra.ClusterName })
	return out
}

func firstNonEmptyStr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// metricsCard is the card for a cluster without credentials: Grafana only.
func (s *Server) metricsCard(ctx context.Context, in kube.Infra) (*kube.Summary, string) {
	m := s.hub.Resolve(in.ClusterName)
	if m == nil {
		return nil, "No cluster access, and this cluster sends no metrics to a Grafana the console reads."
	}
	sum, err := thanosSummary(ctx, s.hub.Client(m.Instance), m.PromCluster)
	if err != nil {
		return nil, "Grafana: " + err.Error()
	}
	if sum.Nodes == 0 {
		return nil, "No cluster access, and Grafana has no current metrics for it."
	}
	return sum, ""
}

// metricsOnlyCluster resolves a metrics-only cluster the caller can see.
func (s *Server) metricsOnlyCluster(w http.ResponseWriter, r *http.Request) (*extraCluster, bool) {
	id := mux.Vars(r)["id"]
	user, err := s.user(session(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session is invalid; sign in again")
		return nil, false
	}
	key := session(r).ID + "/extra"
	s.clusters.mu.Lock()
	c, ok := s.clusters.extra[key]
	s.clusters.mu.Unlock()
	if !ok || time.Since(c.at) > accessTTL {
		c = cachedExtra{list: s.extraClusters(r.Context(), user.Token, nil), at: time.Now()}
		s.clusters.mu.Lock()
		s.clusters.extra[key] = c
		s.clusters.mu.Unlock()
	}
	for i := range c.list {
		if c.list[i].Infra.ID == id {
			return &c.list[i], true
		}
	}
	writeError(w, http.StatusNotFound, "cluster not found")
	return nil, false
}

type cachedExtra struct {
	list []extraCluster
	at   time.Time
}

// noAccess answers explorer routes for metrics-only clusters.
func noAccess(w http.ResponseWriter) {
	writeError(w, http.StatusConflict, "control-hub keeps no credentials for this cluster, so only its Grafana metrics, issues and charts are available")
}
