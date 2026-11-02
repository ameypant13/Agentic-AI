package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
)

var allRealms = []string{"sap", "dedicated-saas", "public-saas"}

func controlHubTools() []Tool {
	return []Tool{
		{
			Name: "find_control_plane",
			Description: "Find control planes whose customer name, cluster name, tenant name or URL contains the given text, " +
				"across every realm. Start here when someone names a tenant (e.g. \"capgroup\"). Returns a compact summary per " +
				"match: id, realm, status, provider/region, URL, post-deploy status and failureReason when the plane failed.",
			Properties: map[string]any{"query": str("Case-insensitive text to match, e.g. a customer name or hostname.")},
			Required:   []string{"query"},
			Label:      "Looked up the control plane",
			Run:        runFindControlPlane,
		},
		{
			Name: "list_control_planes",
			Description: "List control planes in one realm as compact summaries, optionally filtered by status " +
				"(CREATING, CREATION_FAILED, DELETING, DELETION_FAILED, OPERATIONAL, UPDATING, UPDATE_FAILED, UNKNOWN).",
			Properties: map[string]any{
				"realm":  realmProp,
				"status": str("Optional control plane status to filter by."),
			},
			Required: []string{"realm"},
			Label:    "Listed control planes",
			Run:      runListControlPlanes,
		},
		{
			Name:        "get_control_plane",
			Description: "Full record of one control plane (configuration, request, status, post-deploy state, failureReason). Secrets are redacted.",
			Properties:  map[string]any{"id": str("Control plane ID."), "realm": realmProp},
			Required:    []string{"id", "realm"},
			Label:       "Checked the control plane",
			Run:         getByID("/api/control-planes/%s", true),
		},
		{
			Name:        "get_control_plane_env_status",
			Description: "Runtime status Athena reports from inside the control plane cluster (service health, upgrade status).",
			Properties:  map[string]any{"id": str("Control plane ID."), "realm": realmProp},
			Required:    []string{"id", "realm"},
			Label:       "Checked the cluster's health",
			Run:         getByID("/api/control-planes/%s/envstatus", true),
		},
		{
			Name:        "get_post_deploy_info",
			Description: "Post-deploy details for a control plane (SAP): what post-deploy configured, per execution plane. Secrets are redacted.",
			Properties:  map[string]any{"id": str("Control plane ID."), "realm": realmProp},
			Required:    []string{"id", "realm"},
			Label:       "Checked post-deploy",
			Run:         getByID("/api/control-planes/%s/post-deploy-info", true),
		},
		{
			Name:        "list_execution_planes",
			Description: "Execution planes of one control plane, with their status and failureReason.",
			Properties:  map[string]any{"control_plane_id": str("Control plane ID."), "realm": realmProp},
			Required:    []string{"control_plane_id", "realm"},
			Label:       "Checked the execution planes",
			Run:         runListExecutionPlanes,
		},
		{
			Name:        "get_execution_plane",
			Description: "Full record of one execution plane. Secrets are redacted.",
			Properties:  map[string]any{"id": str("Execution plane ID."), "realm": realmProp},
			Required:    []string{"id", "realm"},
			Label:       "Checked the execution plane",
			Run:         getByID("/api/execution-planes/%s", true),
		},
		{
			Name:        "get_infrastructure",
			Description: "The cluster (infrastructure) record behind a control plane: provider, region, cluster name, network, status.",
			Properties:  map[string]any{"id": str("Infrastructure ID (infrastructureId on the control plane).")},
			Required:    []string{"id"},
			Label:       "Checked the cluster",
			Run:         getByID("/api/infrastructures/%s", false),
		},
		{
			Name: "get_job",
			Description: "An async job by request ID: status, progress percentage, the step message and errorDetails (the raw " +
				"error). A failed create's job ID is failureReason.reference on the control plane.",
			Properties: map[string]any{"job_id": str("Job request ID.")},
			Required:   []string{"job_id"},
			Label:      "Read the provisioning job",
			Run:        runGetJob,
		},
		{
			Name:        "get_default_prophecy_version",
			Description: "The global default Prophecy version new control planes are created with.",
			Properties:  map[string]any{},
			Label:       "Checked the default Prophecy version",
			Run: func(ctx context.Context, env *Env, _ json.RawMessage) (string, error) {
				return getJSON(ctx, env, "/api/configs/prophecy-version", nil)
			},
		},
	}
}

func getJSON(ctx context.Context, env *Env, path string, q url.Values) (string, error) {
	raw, err := env.CH.Get(ctx, env.Token, path, q)
	if err != nil {
		return "", err
	}
	return string(controlhub.Redact(raw)), nil
}

func getByID(pathFmt string, withRealm bool) func(context.Context, *Env, json.RawMessage) (string, error) {
	return func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
		in, err := decode[struct {
			ID    string `json:"id"`
			Realm string `json:"realm"`
		}](input)
		if err != nil {
			return "", err
		}
		if err := requireID("id", in.ID); err != nil {
			return "", err
		}
		q := url.Values{}
		if withRealm && in.Realm != "" {
			q.Set("realm", in.Realm)
		}
		return getJSON(ctx, env, fmt.Sprintf(pathFmt, url.PathEscape(in.ID)), q)
	}
}

func runGetJob(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		JobID string `json:"job_id"`
	}](input)
	if err != nil {
		return "", err
	}
	if err := requireID("job_id", in.JobID); err != nil {
		return "", err
	}
	return getJSON(ctx, env, "/api/jobs/"+url.PathEscape(in.JobID), nil)
}

func runListExecutionPlanes(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		ControlPlaneID string `json:"control_plane_id"`
		Realm          string `json:"realm"`
	}](input)
	if err != nil {
		return "", err
	}
	if err := requireID("control_plane_id", in.ControlPlaneID); err != nil {
		return "", err
	}
	q := url.Values{}
	if in.Realm != "" {
		q.Set("realm", in.Realm)
	}
	return getJSON(ctx, env, "/api/control-planes/"+url.PathEscape(in.ControlPlaneID)+"/execution-planes", q)
}

func runListControlPlanes(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Realm  string `json:"realm"`
		Status string `json:"status"`
	}](input)
	if err != nil {
		return "", err
	}
	if in.Realm == "" {
		in.Realm = "sap"
	}
	cps, err := ListControlPlanes(ctx, env.CH, env.Token, in.Realm, in.Status)
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"realm": in.Realm, "count": len(cps), "controlPlanes": cps})
}

func runFindControlPlane(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Query string `json:"query"`
	}](input)
	if err != nil {
		return "", err
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	if q == "" {
		return "", fmt.Errorf("query is required")
	}
	var matches []ControlPlaneSummary
	var errs []string
	for _, realm := range allRealms {
		cps, err := ListControlPlanes(ctx, env.CH, env.Token, realm, "")
		if err != nil {
			// A user may lack access to a realm; report it and keep searching the others.
			errs = append(errs, fmt.Sprintf("%s: %v", realm, err))
			continue
		}
		for _, cp := range cps {
			if cp.matches(q) {
				matches = append(matches, cp)
			}
		}
	}
	out := map[string]any{"query": in.Query, "matches": matches}
	if len(errs) > 0 {
		out["realmErrors"] = errs
	}
	return marshal(out)
}

// ControlPlaneSummary flattens control-hub's V1 (nested config/state) and V2
// (flat) control plane shapes into one compact record.
type ControlPlaneSummary struct {
	ID               string         `json:"id"`
	Realm            string         `json:"realm"`
	CustomerName     string         `json:"customerName"`
	TenantName       string         `json:"tenantName,omitempty"`
	ClusterName      string         `json:"clusterName,omitempty"`
	URL              string         `json:"url,omitempty"`
	Provider         string         `json:"provider,omitempty"`
	Region           string         `json:"region,omitempty"`
	Status           string         `json:"status"`
	StatusMessage    string         `json:"statusMessage,omitempty"`
	PostDeployStatus string         `json:"postDeployStatus,omitempty"`
	ProphecyVersion  string         `json:"prophecyVersion,omitempty"`
	InfrastructureID string         `json:"infrastructureId,omitempty"`
	FailureReason    map[string]any `json:"failureReason,omitempty"`
}

func (c ControlPlaneSummary) matches(q string) bool {
	for _, f := range []string{c.CustomerName, c.TenantName, c.ClusterName, c.URL, c.ID} {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// ListControlPlanes returns every control plane in a realm as summaries.
func ListControlPlanes(ctx context.Context, ch *controlhub.Client, token, realm, status string) ([]ControlPlaneSummary, error) {
	q := url.Values{"realm": {realm}, "limit": {strconv.Itoa(1000)}}
	if status != "" {
		q.Set("status", status)
	}
	raw, err := ch.Get(ctx, token, "/api/control-planes", q)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode control plane list: %w", err)
	}
	out := make([]ControlPlaneSummary, 0, len(items))
	for _, it := range items {
		out = append(out, summarize(it, realm))
	}
	return out, nil
}

func summarize(m map[string]any, realm string) ControlPlaneSummary {
	s := ControlPlaneSummary{ID: getStr(m, "id"), Realm: realm}
	if cfg, ok := m["config"].(map[string]any); ok { // V1 (SAP)
		req, _ := cfg["request"].(map[string]any)
		state, _ := m["state"].(map[string]any)
		s.CustomerName = getStr(cfg, "customerName")
		s.ClusterName = getStr(cfg, "clusterName")
		s.URL = getStr(cfg, "controlPlaneUrl")
		s.TenantName = getStr(req, "tenantName")
		s.Provider = getStr(req, "provider")
		s.Region = getStr(req, "region")
		s.Status = firstNonEmpty(getStr(state, "status"), getStr(m, "status"))
		s.PostDeployStatus = getStr(state, "postDeployStatus")
		s.ProphecyVersion = getStr(state, "prophecyVersion")
		s.InfrastructureID = getStr(state, "infrastructureId")
	} else { // V2
		s.CustomerName = getStr(m, "customerName")
		s.ClusterName = getStr(m, "clusterName")
		s.URL = getStr(m, "controlPlaneUrl")
		s.Provider = getStr(m, "provider")
		s.Region = getStr(m, "region")
		s.Status = getStr(m, "status")
		s.StatusMessage = getStr(m, "statusMessage")
		s.PostDeployStatus = getStr(m, "postDeployStatus")
		s.ProphecyVersion = getStr(m, "prophecyVersion")
		s.InfrastructureID = getStr(m, "infrastructureId")
		if req, ok := m["request"].(map[string]any); ok {
			s.TenantName = getStr(req, "tenantName")
		}
	}
	if fr, ok := m["failureReason"].(map[string]any); ok {
		s.FailureReason = fr
	}
	return s
}

func getStr(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v, _ := m[k].(string)
	return v
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func marshal(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
