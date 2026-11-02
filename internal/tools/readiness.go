package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func readinessTool() Tool {
	return Tool{
		Name: "upgrade_readiness",
		Description: "Check whether a control plane is safe to upgrade: control-plane state, open findings, TLS certificates, " +
			"node readiness and headroom, Prophecy pods, stuck Helm releases, Postgres (15+ public-schema grant), volume space, " +
			"Athena's status, OOM history and recent changes. Returns go, caution, no-go or unknown with each check's detail and " +
			"what to do. Use it before any upgrade question, and explain the failing checks.",
		Properties: map[string]any{"control_plane": str("Control plane ID or customer / tenant name.")},
		Required:   []string{"control_plane"},
		Label:      "Checked upgrade readiness",
		Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				ControlPlane string `json:"control_plane"`
			}](input)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(in.ControlPlane) == "" {
				return "", fmt.Errorf("control_plane is required")
			}
			return env.Readiness(ctx, env.Token, in.ControlPlane)
		},
	}
}
