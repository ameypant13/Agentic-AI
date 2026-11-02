package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

// Action kinds. Phase 1 allows only retries of SAP post-deploy.
const (
	KindRetryCPPostDeploy = "retry_cp_postdeploy"
	KindRetryEPPostDeploy = "retry_ep_postdeploy"
)

var categories = []string{"networking-dns", "certificates", "cloud-quota", "cloud-permissions", "cloud-capacity",
	"helm-kubernetes", "athena-postdeploy", "control-hub-bug", "configuration", "transient", "unknown"}

// consoleTools need the turn's context (store, chat, emitter), so they are
// built per turn rather than living in the shared registry.
func (t *turn) consoleTools() []tools.Tool {
	all := append(t.allConsoleTools(), t.learningTools()...)
	// Audit answers name people: admins only.
	if t.a.audit != nil && t.user.IsAdmin {
		all = append(all, tools.AuditTool(t.a.audit))
	}
	return all
}

func (t *turn) allConsoleTools() []tools.Tool {
	return []tools.Tool{
		{
			Name: "record_investigation",
			Description: "Record the diagnosis of a problem with one control plane or execution plane. Call it once, " +
				"after diagnosing and before the final reply. Admins see everything; the requester sees only summary_for_requester.",
			Properties: map[string]any{
				"title":       map[string]any{"type": "string", "description": "One line, e.g. \"capgroup — provisioning failed at the DNS step\"."},
				"entity_id":   map[string]any{"type": "string", "description": "Control plane or execution plane ID."},
				"entity_name": map[string]any{"type": "string", "description": "Customer or tenant name."},
				"category":    map[string]any{"type": "string", "enum": categories},
				"confidence":  map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}},
				"summary_for_requester": map[string]any{"type": "string", "description": "What a non-admin requester is told: " +
					"the outcome and next step in plain terms, no internal details."},
				"root_cause": map[string]any{"type": "string", "description": "Full technical root cause with the evidence " +
					"(errors, resource names, timestamps). Admins only."},
				"recommended_fix": map[string]any{"type": "string", "description": "What should be done, including any " +
					"permanent fix in control-hub. Admins only."},
			},
			Required: []string{"title", "entity_id", "entity_name", "category", "confidence", "summary_for_requester", "root_cause"},
			Label:    "Recorded the findings",
			Run:      t.recordInvestigation,
		},
		{
			Name:        "propose_action",
			Description: t.proposeDescription(),
			Properties: map[string]any{
				"kind": map[string]any{"type": "string", "enum": []string{KindRetryCPPostDeploy, KindRetryEPPostDeploy, KindRestartPod,
					KindDeleteOrphanedSandboxes}},
				"control_plane_id":   map[string]any{"type": "string", "description": "For the post-deploy kinds."},
				"execution_plane_id": map[string]any{"type": "string", "description": "Required for " + KindRetryEPPostDeploy + "."},
				"cluster":            map[string]any{"type": "string", "description": "For " + KindRestartPod + " and " + KindDeleteOrphanedSandboxes + ": the cluster, as for the k8s_* tools."},
				"namespace":          map[string]any{"type": "string", "description": "For " + KindRestartPod + "."},
				"pod":                map[string]any{"type": "string", "description": "For " + KindRestartPod + "."},
				"target_name":        map[string]any{"type": "string", "description": "Customer, tenant or cluster name shown on the card."},
				"reason":             map[string]any{"type": "string", "description": "One sentence: why this should help."},
			},
			Required: []string{"kind", "target_name", "reason"},
			Label:    "Prepared an action for you",
			Run:      t.proposeAction,
		},
	}
}

func (t *turn) proposeDescription() string {
	d := "Propose an operation. It is first dry-run against the live state with the user's access; the user sees exactly what " +
		"would be called or deleted, what was checked and the risks, and nothing runs unless they click. Kinds: " +
		KindRetryCPPostDeploy + " (re-run post-deploy of a SAP control plane), " +
		KindRetryEPPostDeploy + " (re-run post-deploy of one SAP execution plane), " +
		KindRestartPod + " (delete a pod so its controller recreates it; refused for pods without a controller or Jobs), " +
		KindDeleteOrphanedSandboxes + " (delete the go-sandboxes the orchestrator abandoned after Unschedulable, see k8s_sandboxes). " +
		"If the dry run finds it unsafe, nothing is proposed and you get the reason."
	if t.a.readOnly {
		d += " The console is read-only: the action is a dry run only and cannot be executed; say so, and that an admin can " +
			"apply the shown steps by hand."
	}
	return d
}

func (t *turn) recordInvestigation(_ context.Context, _ *tools.Env, input json.RawMessage) (string, error) {
	var in struct {
		Title               string `json:"title"`
		EntityID            string `json:"entity_id"`
		EntityName          string `json:"entity_name"`
		Category            string `json:"category"`
		Confidence          string `json:"confidence"`
		SummaryForRequester string `json:"summary_for_requester"`
		RootCause           string `json:"root_cause"`
		RecommendedFix      string `json:"recommended_fix"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Title == "" || in.RootCause == "" || in.SummaryForRequester == "" {
		return "", fmt.Errorf("title, root_cause and summary_for_requester are required")
	}
	inv := &store.Investigation{
		ID:                  "INC-" + strings.ToUpper(secret.RandomID(4)),
		ChatID:              t.chat.ID,
		Requester:           t.user.Email,
		Title:               in.Title,
		EntityID:            in.EntityID,
		EntityName:          in.EntityName,
		Category:            in.Category,
		Confidence:          in.Confidence,
		SummaryForRequester: in.SummaryForRequester,
		RootCause:           in.RootCause,
		RecommendedFix:      in.RecommendedFix,
		CreatedAt:           time.Now(),
	}
	if err := t.a.store.SaveInvestigation(inv); err != nil {
		return "", err
	}
	t.item.InvestigationID = inv.ID
	t.a.linkFindings(inv)
	t.a.store.Audit("Copilot for "+t.user.Email, "record_investigation", in.EntityName, "recorded", inv.ID+": "+in.Title)
	t.emit("investigation", map[string]string{"id": inv.ID, "title": inv.Title})
	return fmt.Sprintf("Recorded as %s. Quote this reference in your reply.", inv.ID), nil
}

func (t *turn) proposeAction(ctx context.Context, env *tools.Env, input json.RawMessage) (string, error) {
	var in struct {
		Kind             string `json:"kind"`
		ControlPlaneID   string `json:"control_plane_id"`
		ExecutionPlaneID string `json:"execution_plane_id"`
		Cluster          string `json:"cluster"`
		Namespace        string `json:"namespace"`
		Pod              string `json:"pod"`
		TargetName       string `json:"target_name"`
		Reason           string `json:"reason"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	title, ok := actionTitles[in.Kind]
	if !ok {
		return "", fmt.Errorf("unsupported action kind %q", in.Kind)
	}
	p := ActionParams{ControlPlaneID: in.ControlPlaneID, ExecutionPlaneID: in.ExecutionPlaneID, Cluster: in.Cluster,
		Namespace: in.Namespace, Pod: in.Pod}
	plan, err := DryRun(ctx, env, in.Kind, p)
	if err != nil {
		return "", fmt.Errorf("dry run failed: %w", err)
	}
	if plan.Blocked != "" {
		t.a.store.Audit("Copilot for "+t.user.Email, in.Kind, in.TargetName, "refused", plan.Blocked)
		return "Not proposed: the dry run found it unsafe: " + plan.Blocked + ". Explain this to the user instead.", nil
	}
	params, _ := json.Marshal(p)
	planJSON, _ := json.Marshal(plan)
	act := &store.Action{
		ID:          "ACT-" + strings.ToUpper(secret.RandomID(4)),
		ChatID:      t.chat.ID,
		ChatItemID:  t.item.ID,
		Kind:        in.Kind,
		Title:       title,
		Description: in.Reason,
		TargetID:    firstNonEmpty(in.Pod, in.ExecutionPlaneID, in.ControlPlaneID, in.Cluster),
		TargetName:  in.TargetName,
		Params:      string(params),
		Plan:        string(planJSON),
		Status:      "proposed",
		ProposedFor: t.user.Email,
		CreatedAt:   time.Now(),
	}
	if err := t.a.store.SaveAction(act); err != nil {
		return "", err
	}
	t.a.store.Audit("Copilot for "+t.user.Email, in.Kind, in.TargetName, "proposed", in.Reason)
	t.emit("action", ActionView(act))
	if t.a.readOnly {
		return "Dry run shown to the user (read-only console: it cannot be executed). Plan: " + string(planJSON) +
			". Summarise the plan and say an admin can apply the steps by hand.", nil
	}
	return "Proposed. The user sees the dry run and a button; nothing has run. Plan: " + string(planJSON), nil
}

var actionTitles = map[string]string{
	KindRetryCPPostDeploy:       "Retry post-deploy",
	KindRetryEPPostDeploy:       "Retry execution plane post-deploy",
	KindRestartPod:              "Restart pod",
	KindDeleteOrphanedSandboxes: "Delete orphaned go-sandboxes",
}

// ActionView is the API shape of an action.
func ActionView(a *store.Action) map[string]any {
	return map[string]any{
		"id": a.ID, "kind": a.Kind, "title": a.Title, "description": a.Description,
		"targetName": a.TargetName, "status": a.Status, "result": a.Result, "plan": json.RawMessage(orJSONNull(a.Plan)),
	}
}

func orJSONNull(s string) string {
	if s == "" {
		return "null"
	}
	return s
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
