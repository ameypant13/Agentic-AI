// Package tools defines the read-only tools the Copilot (and the standalone
// MCP server) use to investigate control-hub. No tool here changes state;
// state-changing operations are proposed as actions and run only when a user
// clicks them (see internal/agent).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/athena"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

// Env is what a tool may use. Token is the calling user's control-hub token.
type Env struct {
	CH    *controlhub.Client
	Token string
	KG    *KnowledgeGraph // nil disables the knowledge-graph tools
	Logs  *LogReader      // nil disables the control-hub logs tool
	Probe ProbePolicy
	// Grafana enables metric and log queries; nil disables them.
	Grafana *grafana.Hub
	// Issues summarizes current monitor findings; nil disables current_issues.
	Issues func(ctx context.Context, cluster string) (string, error)
	// Certs reads the certificate inventory; nil disables list_certificates.
	Certs func(ctx context.Context, query string, problemsOnly bool) (string, error)
	// Costs reads stored workload costs; nil disables query_costs.
	Costs func(ctx context.Context, cluster, namespace, groupBy string, days int) (string, error)
	// Athena reads control planes' Athena API; nil disables athena_read.
	Athena *athena.Client
	// Readiness runs the upgrade readiness checks for a control plane.
	Readiness func(ctx context.Context, token, controlPlane string) (string, error)
	// Console lists the console's own findings and investigations for change_timeline.
	Console ConsoleEvents
	// Kube opens clusters for the read-only k8s_* tools; nil disables them.
	Kube KubeAccess
}

type Tool struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	// Label is the step text shown to non-admins, e.g. "Checked DNS". It must
	// not contain resource IDs or internal details.
	Label string
	// Writes marks the one tool that changes state (repair_certificate).
	Writes bool
	Run    func(ctx context.Context, env *Env, input json.RawMessage) (string, error)
}

// Registry returns every tool whose dependencies are present in env.
func Registry(env *Env) []Tool {
	all := append(controlHubTools(), networkTools()...)
	if env.Logs != nil {
		all = append(all, logTools()...)
	}
	if env.KG != nil {
		all = append(all, kgTools()...)
	}
	if env.Grafana != nil {
		all = append(all, grafanaTools()...)
		all = append(all, grafanaExploreTools()...)
	}
	if env.Issues != nil {
		all = append(all, issuesTool())
	}
	if env.Certs != nil {
		all = append(all, certTool())
	}
	if env.Costs != nil {
		all = append(all, costTool())
	}
	if env.Athena != nil {
		all = append(all, athenaTool())
	}
	if env.Kube != nil {
		all = append(all, k8sTools()...)
	}
	if env.Kube != nil || env.Console != nil {
		all = append(all, timelineTool())
	}
	if env.Readiness != nil {
		all = append(all, readinessTool())
	}
	return all
}

// --- schema helpers ---

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

var realmProp = enum("Realm of the control plane. SAP planes live in the v1 table, dedicated-saas and public-saas in v2; "+
	"control-hub treats a missing realm as sap.", "sap", "dedicated-saas", "public-saas")

func decode[T any](input json.RawMessage) (T, error) {
	var v T
	if len(input) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(input, &v); err != nil {
		return v, fmt.Errorf("invalid tool input: %w", err)
	}
	return v, nil
}

func requireID(name, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.ContainsAny(v, "/?#") {
		return fmt.Errorf("%s contains invalid characters", name)
	}
	return nil
}
