package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/athena"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
)

const maxAthenaResult = 40 << 10

func athenaTool() Tool {
	names := make([]string, 0, len(athena.Views))
	for k := range athena.Views {
		names = append(names, k)
	}
	sort.Strings(names)
	var desc strings.Builder
	desc.WriteString("Read a control plane's Athena, the service inside each Prophecy deployment that knows its own health, " +
		"upgrades, sandboxes, feature flags and metering (read-only). Views: ")
	for i, n := range names {
		if i > 0 {
			desc.WriteString("; ")
		}
		desc.WriteString(n + " = " + athena.Views[n].Description)
	}
	desc.WriteString(". Use it for questions control-hub cannot answer: why an upgrade is stuck, which Prophecy " +
		"services are down, sandbox pool exhaustion, credit usage.")
	return Tool{
		Name:        "athena_read",
		Description: desc.String(),
		Properties: map[string]any{
			"control_plane": str("Control plane ID, or its customer, tenant, cluster name or hostname (e.g. capgroup)."),
			"view":          enum("What to read.", names...),
			"config_type":   enum("For view feature_config: one section.", "all", "alert", "audit", "backup", "e2etests", "log", "sandbox", "telemetry", "verticals"),
			"metrics_scope": enum("For view metering.", "pipeline", "cumulative"),
		},
		Required: []string{"control_plane", "view"},
		Label:    "Checked the deployment's status service",
		Run:      runAthena,
	}
}

func runAthena(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		ControlPlane string `json:"control_plane"`
		View         string `json:"view"`
		ConfigType   string `json:"config_type"`
		MetricsScope string `json:"metrics_scope"`
	}](input)
	if err != nil {
		return "", err
	}
	if _, ok := athena.Views[in.View]; !ok {
		return "", fmt.Errorf("unknown view %q", in.View)
	}
	cp, err := resolveControlPlane(ctx, env, in.ControlPlane)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	if in.ConfigType != "" {
		q.Set("configType", in.ConfigType)
	}
	if in.MetricsScope != "" {
		q.Set("metricsScope", in.MetricsScope)
	}
	raw, err := env.Athena.Get(ctx, cp.URL, in.View, q)
	if err != nil {
		return "", err
	}
	data := json.RawMessage(maskEmails(controlhub.Redact(raw)))
	if !json.Valid(data) {
		b, _ := json.Marshal(truncateStr(string(data), 4000))
		data = b
	}
	out, err := json.Marshal(map[string]any{
		"controlPlane": map[string]string{"id": cp.ID, "realm": cp.Realm, "customer": cp.CustomerName, "url": cp.URL},
		"view":         in.View,
		"data":         data,
	})
	if err != nil {
		return "", err
	}
	if len(out) > maxAthenaResult {
		return string(out[:maxAthenaResult]) + "\n… result truncated", nil
	}
	return string(out), nil
}

// resolveControlPlane finds one control plane the caller can see in
// control-hub, by ID or by an exact, then a unique partial, name match.
func resolveControlPlane(ctx context.Context, env *Env, ref string) (*ControlPlaneSummary, error) {
	return ResolveControlPlane(ctx, env.CH, env.Token, ref)
}

// ResolveControlPlane finds the one control plane ref names among those the
// token can see: by ID, an exact customer, tenant, cluster or host name, or a
// unique partial match (live planes preferred over deleted or failed ones).
func ResolveControlPlane(ctx context.Context, ch *controlhub.Client, token, ref string) (*ControlPlaneSummary, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("control_plane is required")
	}
	q := strings.ToLower(ref)
	var all []ControlPlaneSummary
	for _, realm := range allRealms {
		cps, err := ListControlPlanes(ctx, ch, token, realm, "")
		if err != nil {
			continue // the caller may not see this realm
		}
		all = append(all, cps...)
	}
	host := func(u string) string {
		u = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(u), "https://"), "http://")
		return strings.TrimRight(u, "/")
	}
	var exact, partial []ControlPlaneSummary
	for _, cp := range all {
		if cp.ID == ref {
			c := cp
			return &c, nil
		}
		h := host(cp.URL)
		switch {
		case strings.EqualFold(cp.CustomerName, ref), strings.EqualFold(cp.TenantName, ref), strings.EqualFold(cp.ClusterName, ref),
			h == host(ref), strings.SplitN(h, ".", 2)[0] == q:
			exact = append(exact, cp)
		case cp.matches(q):
			partial = append(partial, cp)
		}
	}
	pick := exact
	if len(pick) == 0 {
		pick = partial
	}
	// Prefer the live planes when a name also matches deleted or failed ones.
	if len(pick) > 1 {
		var live []ControlPlaneSummary
		for _, cp := range pick {
			if cp.URL != "" && !strings.HasPrefix(cp.Status, "DELET") && cp.Status != "CREATION_FAILED" {
				live = append(live, cp)
			}
		}
		if len(live) > 0 {
			pick = live
		}
	}
	switch len(pick) {
	case 0:
		return nil, fmt.Errorf("no control plane matching %q among those you can access; try find_control_plane", ref)
	case 1:
		return &pick[0], nil
	}
	var names []string
	for i, cp := range pick {
		if i == 8 {
			names = append(names, fmt.Sprintf("… %d more", len(pick)-8))
			break
		}
		names = append(names, fmt.Sprintf("%s (%s, %s, %s)", cp.ID, cp.CustomerName, cp.URL, cp.Status))
	}
	return nil, fmt.Errorf("%q matches %d control planes; pass one ID: %s", ref, len(pick), strings.Join(names, "; "))
}

var emailRE = regexp.MustCompile(`([A-Za-z0-9._%+-])[A-Za-z0-9._%+-]*@([A-Za-z0-9.-]+\.[A-Za-z]{2,})`)

// maskEmails keeps an address's first letter and domain: j***@acme.com.
func maskEmails(b []byte) []byte {
	return emailRE.ReplaceAll(b, []byte("$1***@$2"))
}
