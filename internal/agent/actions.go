package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

var (
	ErrActionNotPending = errors.New("this action is no longer pending")
	ErrReadOnly         = errors.New("the console is read-only; actions are turned off")
	ErrPlanChanged      = errors.New("the state changed since the dry run; ask the Copilot to propose it again")
)

// ExecuteAction runs a proposed action with the clicking user's access
// (their control-hub token and cluster access), so control-hub and the
// cluster decide whether that user may do it. It refuses on a read-only
// console, and when the dry run computed now differs from the one the user
// saw: then the state changed and the action is re-proposed, not run.
func ExecuteAction(ctx context.Context, st *store.Store, env *tools.Env, act *store.Action, user User, readOnly bool) error {
	if readOnly {
		return ErrReadOnly
	}
	var p ActionParams
	if err := json.Unmarshal([]byte(act.Params), &p); err != nil {
		return fmt.Errorf("decode action params: %w", err)
	}
	if act.Plan != "" {
		var shown Plan
		_ = json.Unmarshal([]byte(act.Plan), &shown)
		now, err := DryRun(ctx, env, act.Kind, p)
		if err != nil {
			return fmt.Errorf("dry run before executing: %w", err)
		}
		if now.Blocked != "" || !slices.Equal(now.Targets, shown.Targets) {
			return fmt.Errorf("%w: %s", ErrPlanChanged, firstNonEmpty(now.Blocked, "the targets differ from the dry run you saw"))
		}
	}

	claimed, err := st.ClaimAction(act.ID)
	if err != nil {
		return err
	}
	if !claimed {
		return ErrActionNotPending
	}
	act.Status = "running"

	q := url.Values{"realm": {"sap"}}.Encode()
	cpPath := "/api/control-planes/" + url.PathEscape(p.ControlPlaneID)
	var raw []byte
	switch act.Kind {
	case KindRetryCPPostDeploy:
		raw, err = env.CH.Post(ctx, user.Token, cpPath+"/postdeploy?"+q, nil)
	case KindRetryEPPostDeploy:
		raw, err = env.CH.Post(ctx, user.Token, cpPath+"/execution-planes/postdeploy?"+q,
			map[string]string{"executionPlaneID": p.ExecutionPlaneID})
	case KindRestartPod, KindDeleteOrphanedSandboxes:
		raw, err = deletePods(ctx, env, act)
	default:
		err = fmt.Errorf("unsupported action kind %q", act.Kind)
	}

	now := time.Now()
	act.ExecutedAt, act.ExecutedBy = &now, user.Email
	decision := "succeeded"
	if err != nil {
		act.Status, act.Result, decision = "failed", err.Error(), "failed"
	} else {
		act.Status, act.Result = "succeeded", string(controlhub.Redact(raw))
	}
	if saveErr := st.SaveAction(act); saveErr != nil {
		return saveErr
	}
	st.Audit(user.Email, act.Kind, act.TargetName, decision, act.ID+": "+truncate(act.Result, 300))
	return nil
}

// deletePods deletes the pods the action's plan names.
func deletePods(ctx context.Context, env *tools.Env, act *store.Action) ([]byte, error) {
	var p ActionParams
	var plan Plan
	_ = json.Unmarshal([]byte(act.Params), &p)
	if err := json.Unmarshal([]byte(act.Plan), &plan); err != nil || len(plan.Targets) == 0 {
		return nil, fmt.Errorf("the action has no dry-run plan")
	}
	cl, err := openCluster(ctx, env, p.Cluster)
	if err != nil {
		return nil, err
	}
	var done []string
	for _, t := range plan.Targets {
		ns, name, _ := strings.Cut(t, "/")
		if err := cl.DeletePod(ctx, ns, name); err != nil {
			return []byte("deleted " + strings.Join(done, ", ")), fmt.Errorf("delete %s: %w", t, err)
		}
		done = append(done, t)
	}
	return []byte("deleted " + strings.Join(done, ", ")), nil
}
