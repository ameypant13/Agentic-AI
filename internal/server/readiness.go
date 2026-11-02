package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/athena"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/certs"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/readiness"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

// Upgrade readiness: GET /api/readiness?cp=<id or name>. The control plane is
// resolved with the caller's control-hub token, and the cluster is opened
// with the caller's access, as everywhere else.

func (s *Server) readinessHandler(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.URL.Query().Get("cp"))
	if ref == "" {
		writeError(w, http.StatusBadRequest, "cp is required")
		return
	}
	user, err := s.user(session(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session is invalid; sign in again")
		return
	}
	rep, err := s.readiness(r.Context(), user.Token, ref)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// ReadinessJSON is the Copilot's upgrade_readiness tool.
func (s *Server) ReadinessJSON(ctx context.Context, token, ref string) (string, error) {
	rep, err := s.readiness(ctx, token, ref)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(rep)
	return string(b), err
}

func (s *Server) readiness(ctx context.Context, token, ref string) (*readiness.Report, error) {
	cp, err := tools.ResolveControlPlane(ctx, s.ch, token, ref)
	if err != nil {
		return nil, err
	}
	target := readiness.ControlPlane{ID: cp.ID, Realm: cp.Realm, Customer: cp.CustomerName, Tenant: cp.TenantName, URL: cp.URL,
		Provider: cp.Provider, Region: cp.Region, Status: cp.Status, StatusMessage: cp.StatusMessage, PostDeployStatus: cp.PostDeployStatus,
		ProphecyVersion: cp.ProphecyVersion, InfrastructureID: cp.InfrastructureID, ClusterName: strings.TrimSpace(cp.ClusterName)}
	if m, ok := cp.FailureReason["message"].(string); ok {
		target.FailureReason = m
	}

	src := readiness.Sources{
		Findings: func(names []string) []store.Finding {
			fs, _ := s.st.ListFindings("open")
			var out []store.Finding
			for _, f := range fs {
				for _, n := range names {
					if strings.EqualFold(f.EntityName, n) || f.EntityID == n {
						out = append(out, f)
						break
					}
				}
			}
			return out
		},
	}
	if s.certs != nil {
		src.Certs = func() []certs.Item {
			if inv := s.certs.Inventory(); inv != nil {
				return inv.Items
			}
			return nil
		}
	}
	clusterRef := firstNonEmptyStr(target.InfrastructureID, target.ClusterName)
	if clusterRef != "" {
		access := KubeAccess(s.ch, s.kube, s.hub)
		src.Cluster = func(ctx context.Context) (*kube.Cluster, error) { return access(ctx, token, clusterRef) }
	}
	if m := s.hub.Resolve(target.ClusterName); m != nil {
		if c := s.hub.Client(m.Instance); c != nil {
			src.Metrics = func(ctx context.Context, promql string) ([]grafana.Sample, error) {
				return c.Query(ctx, strings.ReplaceAll(promql, "$cluster", m.PromCluster))
			}
			if s.costs != nil {
				src.Rightsizing = func() []cost.Recommendation {
					to := time.Now().UTC()
					recs, _ := s.cachedRightsizing(to.AddDate(0, 0, -29).Format("2006-01-02"), to.Format("2006-01-02"), m.PromCluster, "")
					return recs
				}
			}
		}
	}
	if s.cfg.AthenaToken != "" && target.URL != "" {
		client := athena.New(s.cfg.AthenaToken)
		src.Athena = func(ctx context.Context, view string) (map[string]any, error) {
			raw, err := client.Get(ctx, target.URL, view, nil)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, fmt.Errorf("unexpected Athena reply")
			}
			return out, nil
		}
	}
	rep := readiness.Run(ctx, target, src)
	for i := range rep.Checks {
		rep.Checks[i].Detail = scrubDetail(rep.Checks[i].Detail)
	}
	return &rep, nil
}

var bearer = regexp.MustCompile(`(?i)bearer [a-z0-9._-]+`)

// scrubDetail keeps tokens out of error text that reaches the page.
func scrubDetail(s string) string { return bearer.ReplaceAllString(s, "Bearer ***") }
