package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Event is one line of a change timeline.
type Event struct {
	At     time.Time `json:"at"`
	Source string    `json:"source"`
	Object string    `json:"object,omitempty"`
	What   string    `json:"what"`
}

// ConsoleEvents returns what the console itself recorded about any of names
// (tenants or clusters) since a time: findings appearing and resolving,
// investigations.
type ConsoleEvents func(ctx context.Context, names []string, since time.Time) []Event

const maxTimelineHours = 14 * 24

func timelineTool() Tool {
	return Tool{
		Name: "change_timeline",
		Description: "What changed around a tenant or cluster, oldest first: Helm release revisions (Prophecy chart and app " +
			"versions), Deployment rollouts with the images that changed, StatefulSet/DaemonSet rollouts, nodes joining, and " +
			"the console's own findings (first seen, resolved) and investigations. Most incidents follow a change: call it " +
			"early, then line the first symptom up with the changes just before it.",
		Properties: map[string]any{
			"target": str("Tenant (customer) name, control plane ID, or cluster name / infrastructure ID."),
			"hours":  integer(fmt.Sprintf("How far back. Default 72, max %d.", maxTimelineHours)),
		},
		Required: []string{"target"},
		Label:    "Checked what changed recently",
		Run:      runTimeline,
	}
}

func runTimeline(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Target string `json:"target"`
		Hours  int    `json:"hours"`
	}](input)
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(in.Target)
	if target == "" {
		return "", fmt.Errorf("target is required")
	}
	since := time.Now().Add(-time.Duration(clamp(in.Hours, 72, maxTimelineHours)) * time.Hour)
	names := []string{target}
	var notes []string
	var events []Event

	// The cluster: by name or ID, else through the tenant's control plane.
	var clusterRef string
	if env.Kube != nil {
		if _, err := env.Kube(ctx, env.Token, target); err == nil {
			clusterRef = target
		} else if env.CH == nil {
			notes = append(notes, "no cluster matches "+target)
		} else if cp, cerr := resolveControlPlane(ctx, env, target); cerr == nil {
			names = append(names, cp.CustomerName, cp.TenantName, cp.ClusterName)
			switch {
			case cp.InfrastructureID != "":
				clusterRef = cp.InfrastructureID
			case cp.ClusterName != "":
				clusterRef = cp.ClusterName
			}
		} else {
			notes = append(notes, "no cluster or control plane matches "+target+": "+cerr.Error())
		}
	}
	if clusterRef != "" {
		if c, err := env.Kube(ctx, env.Token, clusterRef); err != nil {
			notes = append(notes, "cluster changes unavailable: "+err.Error())
		} else {
			names = append(names, c.Infra.ClusterName)
			kctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			changes, err := c.Changes(kctx, since)
			cancel()
			if err != nil {
				notes = append(notes, "cluster changes: "+err.Error())
			}
			for _, ch := range changes {
				obj := ch.Object
				if ch.Namespace != "" {
					obj = ch.Namespace + "/" + obj
				}
				events = append(events, Event{At: ch.At, Source: ch.Source, Object: obj, What: ch.What})
			}
		}
	}
	if env.Console != nil {
		events = append(events, env.Console(ctx, uniqueNonEmpty(names), since)...)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	total := len(events)
	if len(events) > 200 {
		events = events[len(events)-200:]
		notes = append(notes, fmt.Sprintf("showing the latest 200 of %d events; narrow hours", total))
	}
	if events == nil {
		events = []Event{}
	}
	out := map[string]any{"since": since.UTC().Format(time.RFC3339), "events": events}
	if len(notes) > 0 {
		out["notes"] = notes
	}
	return marshal(out)
}

func uniqueNonEmpty(v []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range v {
		k := strings.ToLower(strings.TrimSpace(s))
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}
