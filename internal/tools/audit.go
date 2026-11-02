package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/audit"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
)

// AuditTool answers "who changed / deleted X". It names people, so the agent
// offers it to admins only. GKE clusters in the configured projects are read
// from Cloud Audit Logs; other clusters fall back to the object's
// managedFields (which tool changed which field, and when, but not who).
func AuditTool(s *audit.Searcher) Tool {
	return Tool{
		Name: "audit_search",
		Description: "Who changed, created or deleted something, from audit logs. scope=kubernetes (default): changes to " +
			"Kubernetes objects; each event has the actor (person or service account), the tool (kubectl, helm, argocd…), " +
			"the source IP and the changes (field: before → after). Full audit history exists only for GKE clusters in " +
			"the projects " + strings.Join(s.Projects, ", ") + "; for EKS and AKS clusters (audit logging off) give kind, " +
			"namespace and name to get the object's field owners instead: which tool last set each field and when, " +
			"without who. scope=gcp: changes to Google Cloud resources (IAM, disks, node pools, DNS, buckets) by " +
			"resource-name substring. Controllers and nodes are left out unless include_system. Say plainly when the " +
			"source cannot tell who.",
		Properties: map[string]any{
			"scope":          map[string]any{"type": "string", "enum": []string{"kubernetes", "gcp"}},
			"cluster":        str("Cluster: control-hub name or ID, or a GKE cluster name such as prophecy-cloud-mgmt."),
			"kind":           str("Resource kind or short name: pvc, pv, deployment, statefulset, configmap, secret, node…"),
			"namespace":      str("Namespace, for namespaced objects."),
			"name":           str("Object name (kubernetes) or a resource-name substring (gcp)."),
			"actions":        map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"create", "update", "patch", "delete", "deletecollection"}}},
			"actor":          str("Substring of the person or service account, e.g. an email."),
			"since":          str("How far back: a duration such as 24h or 30d, or an RFC 3339 time. Default 30d."),
			"until":          str("Optional RFC 3339 end time."),
			"service":        str("scope=gcp: a Google API service, e.g. compute.googleapis.com, iam.googleapis.com."),
			"include_system": map[string]any{"type": "boolean", "description": "Keep changes by Kubernetes controllers and nodes."},
			"limit":          integer("Max events, default 50, max 200."),
		},
		Label: "Searched the audit logs",
		Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				Scope         string   `json:"scope"`
				Cluster       string   `json:"cluster"`
				Kind          string   `json:"kind"`
				Namespace     string   `json:"namespace"`
				Name          string   `json:"name"`
				Actions       []string `json:"actions"`
				Actor         string   `json:"actor"`
				Since         string   `json:"since"`
				Until         string   `json:"until"`
				Service       string   `json:"service"`
				IncludeSystem bool     `json:"include_system"`
				Limit         int      `json:"limit"`
			}](input)
			if err != nil {
				return "", err
			}
			q := audit.Query{Kind: in.Kind, Namespace: in.Namespace, Name: strings.TrimSpace(in.Name), Actions: in.Actions,
				Actor: in.Actor, IncludeSystem: in.IncludeSystem, Limit: in.Limit}
			if q.Since, err = parseSince(in.Since, time.Now()); err != nil {
				return "", err
			}
			if in.Until != "" {
				if q.Until, err = time.Parse(time.RFC3339, in.Until); err != nil {
					return "", fmt.Errorf("until: %w", err)
				}
			}
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			var out any
			if in.Scope == "gcp" {
				events, err := s.GCP(ctx, q, in.Service)
				if err != nil {
					return "", err
				}
				out = map[string]any{"source": "gcp-audit", "projects": s.Projects, "events": events}
			} else {
				out, err = kubernetesAudit(ctx, env, s, q, strings.TrimSpace(in.Cluster))
				if err != nil {
					return "", err
				}
			}
			b, err := json.Marshal(out)
			return string(b), err
		},
	}
}

func kubernetesAudit(ctx context.Context, env *Env, s *audit.Searcher, q audit.Query, cluster string) (any, error) {
	if cluster == "" {
		return nil, fmt.Errorf("cluster is required for scope=kubernetes")
	}
	// A cluster control-hub manages: GKE in an audited project reads audit
	// logs; anything else falls back to field owners.
	var cl *kube.Cluster
	if env.Kube != nil {
		cl, _ = env.Kube(ctx, env.Token, cluster)
	}
	gke := cluster
	if cl != nil {
		gke = ""
		if g := cl.Infra.Details.GCP; g != nil && slices.Contains(s.Projects, g.ProjectID) {
			gke = firstNonEmpty(g.GKEClusterName, cl.Infra.ClusterName)
		}
	}
	if gke != "" {
		q.Cluster = gke
		events, err := s.Kubernetes(ctx, q)
		if err != nil {
			return nil, err
		}
		res := map[string]any{"source": "gke-audit", "cluster": gke, "events": events}
		if len(events) == 0 {
			res["note"] = "No matching changes in the audit log for this window (it keeps 400 days). If the cluster is not " +
				"a GKE cluster in " + strings.Join(s.Projects, ", ") + ", there is no audit log for it here."
			if cl != nil && q.Kind != "" && q.Name != "" {
				if owners, err := fieldOwners(ctx, cl, q); err == nil {
					res["fieldOwners"] = owners
				}
			}
		}
		return res, nil
	}
	note := "No audit log is collected for this cluster (audit logging is off on EKS and AKS clusters), so who made a " +
		"change cannot be told. fieldOwners shows which tool last set each field and when."
	if q.Kind == "" || q.Name == "" {
		return map[string]any{"source": "none", "note": note + " Give kind, namespace and name to read them."}, nil
	}
	owners, err := fieldOwners(ctx, cl, q)
	if err != nil {
		return nil, err
	}
	return map[string]any{"source": "managed-fields", "note": note, "fieldOwners": owners}, nil
}

func fieldOwners(ctx context.Context, cl *kube.Cluster, q audit.Query) ([]kube.FieldOwner, error) {
	all, err := cl.APIResources(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resolveKind(all, q.Kind)
	if err != nil {
		return nil, err
	}
	gvr, err := kube.ParseKey(res.Key)
	if err != nil {
		return nil, err
	}
	ns := q.Namespace
	if !res.Namespaced {
		ns = ""
	} else if ns == "" {
		return nil, fmt.Errorf("%s is namespaced: give the namespace", res.Resource)
	}
	return cl.FieldOwners(ctx, gvr, ns, q.Name)
}

// parseSince accepts "", a duration ("24h", "30d") or an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now.Add(-30 * 24 * time.Hour), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 || n > 400 {
			return time.Time{}, fmt.Errorf("since: %q is not a day count between 1 and 400", s)
		}
		return now.Add(-time.Duration(n) * 24 * time.Hour), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("since: use a duration such as 24h or 30d, or an RFC 3339 time")
	}
	return now.Add(-d), nil
}
