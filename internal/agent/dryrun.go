package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

// Dry runs. Every proposed action is first planned against the live state:
// what exactly would be called or deleted, what was checked, what could go
// wrong. The plan is stored with the action and shown on its card. While the
// console is read-only nothing executes, so the plan is the deliverable: an
// admin can apply it by hand. When execution is allowed, the plan is computed
// again at click time and the action refuses to run if it changed.

const (
	KindRestartPod              = "restart_pod"
	KindDeleteOrphanedSandboxes = "delete_orphaned_sandboxes"
)

// ActionParams are the inputs of every action kind.
type ActionParams struct {
	ControlPlaneID   string `json:"controlPlaneId,omitempty"`
	ExecutionPlaneID string `json:"executionPlaneId,omitempty"`
	Cluster          string `json:"cluster,omitempty"`
	Namespace        string `json:"namespace,omitempty"`
	Pod              string `json:"pod,omitempty"`
}

// Plan is a dry run's result.
type Plan struct {
	Summary string   `json:"summary"`
	Steps   []string `json:"steps"`  // the exact operations, as commands or API calls
	Checks  []string `json:"checks"` // what the dry run read and found
	Risks   []string `json:"risks,omitempty"`
	// Blocked says why the action must not run as proposed.
	Blocked string `json:"blocked,omitempty"`
	// Targets are the objects the steps touch, compared at execution time.
	Targets []string `json:"targets"`
}

// DryRun plans an action against the live state with the user's access.
func DryRun(ctx context.Context, env *tools.Env, kind string, p ActionParams) (*Plan, error) {
	switch kind {
	case KindRetryCPPostDeploy, KindRetryEPPostDeploy:
		return planPostDeploy(ctx, env, kind, p)
	case KindRestartPod:
		return planRestartPod(ctx, env, p)
	case KindDeleteOrphanedSandboxes:
		return planOrphanedSandboxes(ctx, env, p)
	}
	return nil, fmt.Errorf("unsupported action kind %q", kind)
}

func planPostDeploy(ctx context.Context, env *tools.Env, kind string, p ActionParams) (*Plan, error) {
	if p.ControlPlaneID == "" {
		return nil, fmt.Errorf("control_plane_id is required")
	}
	q := url.Values{"realm": {"sap"}}
	path, step := "/api/control-planes/"+url.PathEscape(p.ControlPlaneID), ""
	if kind == KindRetryEPPostDeploy {
		if p.ExecutionPlaneID == "" {
			return nil, fmt.Errorf("execution_plane_id is required for %s", kind)
		}
		path = "/api/execution-planes/" + url.PathEscape(p.ExecutionPlaneID)
		step = fmt.Sprintf("POST /api/control-planes/%s/execution-planes/postdeploy?realm=sap {\"executionPlaneID\":%q} (your control-hub token)",
			p.ControlPlaneID, p.ExecutionPlaneID)
	} else {
		step = fmt.Sprintf("POST /api/control-planes/%s/postdeploy?realm=sap (your control-hub token)", p.ControlPlaneID)
	}
	raw, err := env.CH.Get(ctx, env.Token, path, q)
	if err != nil {
		return nil, fmt.Errorf("read the current state: %w", err)
	}
	var cur struct {
		Status           string `json:"status"`
		PostDeployStatus string `json:"postDeployStatus"`
		ProphecyVersion  string `json:"prophecyVersion"`
	}
	_ = json.Unmarshal(raw, &cur)
	plan := &Plan{Steps: []string{step}, Targets: []string{path},
		Checks:  []string{fmt.Sprintf("status %s, post-deploy %s, Prophecy %s", orNone(cur.Status), orNone(cur.PostDeployStatus), orNone(cur.ProphecyVersion))},
		Summary: "control-hub re-runs the post-deploy steps (Athena configuration, users, Spark and fabric setup); the deployment itself is not touched."}
	switch s := strings.ToLower(cur.PostDeployStatus); {
	case strings.Contains(s, "progress") || strings.Contains(s, "running") || strings.Contains(s, "pending"):
		plan.Blocked = "post-deploy is still running (" + cur.PostDeployStatus + "); wait for it to finish"
	case strings.Contains(s, "complete") || strings.Contains(s, "success"):
		plan.Risks = append(plan.Risks, "post-deploy already completed; re-running repeats steps that succeeded (they are idempotent, but it takes several minutes)")
	}
	return plan, nil
}

func planRestartPod(ctx context.Context, env *tools.Env, p ActionParams) (*Plan, error) {
	if p.Cluster == "" || p.Namespace == "" || p.Pod == "" {
		return nil, fmt.Errorf("cluster, namespace and pod are required for %s", KindRestartPod)
	}
	cl, err := openCluster(ctx, env, p.Cluster)
	if err != nil {
		return nil, err
	}
	d, err := cl.PodDetail(ctx, p.Namespace, p.Pod)
	if err != nil {
		return nil, fmt.Errorf("read the pod: %w", err)
	}
	plan := &Plan{Steps: []string{fmt.Sprintf("kubectl --context %s -n %s delete pod %s", p.Cluster, p.Namespace, p.Pod)},
		Targets: []string{p.Namespace + "/" + p.Pod},
		Checks:  []string{fmt.Sprintf("pod %s, ready %s, %d restarts, on node %s, owner %s", d.Status, d.Ready, d.Restarts, orNone(d.Node), orNone(d.Owner))},
		Summary: fmt.Sprintf("deletes the pod; %s creates a replacement with the same spec", orNone(d.Owner))}
	kind, _, _ := strings.Cut(d.Owner, "/")
	switch kind {
	case "":
		plan.Blocked = "the pod has no controller, so nothing would recreate it"
	case "Job", "CronJob":
		plan.Blocked = "the pod belongs to a " + kind + "; deleting it can fail or re-run the job instead of restarting a service"
	case "StatefulSet":
		plan.Risks = append(plan.Risks, "StatefulSet pods restart one at a time with the same name and volume; the service is down until it is ready again")
	}
	if strings.HasPrefix(p.Pod, "postgres") || strings.Contains(p.Pod, "-postgres") {
		plan.Risks = append(plan.Risks, "this is a database: every service using it fails until it is back")
	}
	if strings.HasPrefix(d.Ready, "1/") || d.Ready == "0/1" {
		plan.Risks = append(plan.Risks, "if this is the only replica, requests fail until the new pod is ready")
	}
	return plan, nil
}

func planOrphanedSandboxes(ctx context.Context, env *tools.Env, p ActionParams) (*Plan, error) {
	if p.Cluster == "" {
		return nil, fmt.Errorf("cluster is required for %s", KindDeleteOrphanedSandboxes)
	}
	cl, err := openCluster(ctx, env, p.Cluster)
	if err != nil {
		return nil, err
	}
	rep, err := cl.Sandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list go-sandboxes: %w", err)
	}
	plan := &Plan{Checks: []string{fmt.Sprintf("%d go-sandboxes, %d orphaned (named in the orchestrator's \"failed to start: Unschedulable\" log lines)", rep.Total, rep.Orphaned)}}
	if !rep.LogsRead {
		plan.Blocked = "the orchestrator log could not be read, so orphans cannot be told apart from sandboxes in use"
		return plan, nil
	}
	for _, s := range rep.Sandboxes {
		if !s.Orphaned {
			continue
		}
		plan.Steps = append(plan.Steps, fmt.Sprintf("kubectl --context %s -n %s delete pod %s", p.Cluster, s.Namespace, s.Name))
		plan.Targets = append(plan.Targets, s.Namespace+"/"+s.Name)
	}
	if len(plan.Steps) == 0 {
		plan.Blocked = "no orphaned go-sandbox on this cluster"
		return plan, nil
	}
	plan.Summary = fmt.Sprintf("deletes %d orphaned go-sandboxes, freeing %.1f cores and %.1f GiB of requests; no run uses them",
		rep.Orphaned, rep.OrphanCPU, rep.OrphanMem/(1<<30))
	return plan, nil
}

func openCluster(ctx context.Context, env *tools.Env, ref string) (*kube.Cluster, error) {
	if env.Kube == nil {
		return nil, fmt.Errorf("cluster access is not configured")
	}
	return env.Kube(ctx, env.Token, ref)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
