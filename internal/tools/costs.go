package tools

import (
	"context"
	"encoding/json"
)

func costTool() Tool {
	return Tool{
		Name: "query_costs",
		Description: "Estimated Kubernetes compute cost per cluster, day, namespace or workload, from the console's cost worker " +
			"(Grafana metrics × cloud on-demand list prices, OpenCost's model: each pod pays for the larger of its request and " +
			"its usage; node cost no pod accounts for is idle). group_by rightsizing returns recommended CPU and memory requests and limits " +
			"per workload container from the window's usage (KRR policy: CPU request = p95, no CPU limit; memory = peak + 15%) with " +
			"the monthly saving; check notes (OOM kills, throttling, JVM heap) before recommending a change. group_by changes " +
			"returns clusters whose last 3 days cost 25%+ and $20+/day more or less than the 14 days before, with the drivers " +
			"(node count and instance types, idle capacity, the workloads whose cost moved most): use it for \"why did cost go up\". Clusters use their Grafana metrics names (management-saascluster, " +
			"preview, prophecy-westpac-prod, …). Excludes discounts, storage, network and managed control-plane fees; say so " +
			"when quoting numbers. Workload rows include requested vs used core-hours and GiB-hours, which show over-provisioning.",
		Properties: map[string]any{
			"cluster":   str("Optional Grafana cluster name."),
			"namespace": str("Optional namespace (with group_by workload)."),
			"group_by":  enum("cluster (default): totals per cluster; day: daily series; namespace; workload; rightsizing: resource recommendations; changes: recent cost changes and why.", "cluster", "day", "namespace", "workload", "rightsizing", "changes"),
			"days":      integer("How many days back, including today. Default 7, max 400."),
		},
		Label: "Checked costs",
		Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				Cluster   string `json:"cluster"`
				Namespace string `json:"namespace"`
				GroupBy   string `json:"group_by"`
				Days      int    `json:"days"`
			}](input)
			if err != nil {
				return "", err
			}
			return env.Costs(ctx, in.Cluster, in.Namespace, in.GroupBy, in.Days)
		},
	}
}
