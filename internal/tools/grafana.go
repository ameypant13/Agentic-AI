package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

const (
	maxRangeMinutes = 7 * 24 * 60
	maxSeries       = 20
	maxPoints       = 60
	maxSamples      = 50
	maxLogMinutes   = 24 * 60
	maxLogLines     = 200
)

func grafanaTools() []Tool {
	return []Tool{
		{
			Name: "grafana_query_metrics",
			Description: "Run a read-only PromQL query against the Grafana (Thanos) that monitors a cluster. Write `cluster=\"$cluster\"` " +
				"in every selector; it is replaced with the cluster's metrics label. Useful metrics: container_memory_working_set_bytes, " +
				"container_cpu_usage_seconds_total, kube_pod_container_resource_limits/requests{resource=...}, " +
				"kube_pod_container_status_restarts_total, kube_pod_container_status_waiting_reason, kube_pod_container_status_last_terminated_reason, " +
				"kube_node_status_condition, kubelet_volume_stats_used_bytes/capacity_bytes. Prophecy JVM services (label job = service, e.g. " +
				"metagraph, app, execution, edweb): jvm_memory_used_bytes_sum/_count{region=\"heap\"} (average = rate(sum)/rate(count)), " +
				"jvm_memory_max_bytes{region=\"heap\"}, jvm_gc_seconds_sum, akka_system_dead_letters_total, and HTTP " +
				"span_processing_time_seconds_count/_bucket{span_kind=\"server\", http_status_code, operation}. " +
				"range_minutes 0 runs an instant query; otherwise a range query (max 7 days) returning at most 20 series × 60 points.",
			Properties: map[string]any{
				"cluster":       str("Control-hub cluster name (from find_control_plane / the Clusters page)."),
				"promql":        str("PromQL with cluster=\"$cluster\" in each selector."),
				"range_minutes": integer("0 for an instant query, else how far back (max 10080)."),
			},
			Required: []string{"cluster", "promql"},
			Label:    "Queried metrics",
			Run:      runQueryMetrics,
		},
		{
			Name: "grafana_query_logs",
			Description: "Search a cluster's pod logs in Loki through Grafana (read-only). Narrow by namespace and pod name prefix, and " +
				"filter lines with a regular expression (RE2). Returns at most 200 lines, oldest first, with pod and container. Secrets are masked.",
			Properties: map[string]any{
				"cluster":       str("Control-hub cluster name."),
				"namespace":     str("Optional namespace, e.g. prophecy or the tenant namespace."),
				"pod_prefix":    str("Optional pod name prefix, e.g. metagraph."),
				"filter":        str("Optional RE2 regexp lines must match, e.g. (?i)error|exception."),
				"since_minutes": integer("How far back to search. Default 60, max 1440."),
				"limit":         integer("Lines to return. Default 100, max 200."),
			},
			Required: []string{"cluster"},
			Label:    "Searched logs",
			Run:      runQueryLogs,
		},
	}
}

func issuesTool() Tool {
	return Tool{
		Name: "current_issues",
		Description: "List the problems the console's monitors currently see, from the metrics parser (memory near limit, OOM kills, " +
			"restarts, JVM heap, HTTP errors, volumes, nodes) and the log parser (known error messages), for one cluster or all. " +
			"Start here for questions like \"what is wrong with X\" or \"anything broken right now\".",
		Properties: map[string]any{"cluster": str("Optional control-hub cluster name; empty for every cluster.")},
		Label:      "Checked current issues",
		Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				Cluster string `json:"cluster"`
			}](input)
			if err != nil {
				return "", err
			}
			return env.Issues(ctx, in.Cluster)
		},
	}
}

func resolve(env *Env, cluster string) (*grafana.Client, *grafana.Mapping, error) {
	if strings.TrimSpace(cluster) == "" {
		return nil, nil, fmt.Errorf("cluster is required")
	}
	m := env.Grafana.Resolve(cluster)
	if m == nil {
		return nil, nil, fmt.Errorf("cluster %q is not monitored by any configured Grafana", cluster)
	}
	c := env.Grafana.Client(m.Instance)
	if c == nil {
		return nil, nil, fmt.Errorf("grafana instance %s is not configured", m.Instance)
	}
	return c, m, nil
}

var clusterPlaceholder = regexp.MustCompile(`\$cluster\b`)

func runQueryMetrics(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Cluster      string `json:"cluster"`
		PromQL       string `json:"promql"`
		RangeMinutes int    `json:"range_minutes"`
	}](input)
	if err != nil {
		return "", err
	}
	c, m, err := resolve(env, in.Cluster)
	if err != nil {
		return "", err
	}
	if !strings.Contains(in.PromQL, "$cluster") {
		return "", fmt.Errorf(`write cluster="$cluster" in each selector so the query stays on %s`, in.Cluster)
	}
	expr := clusterPlaceholder.ReplaceAllString(in.PromQL, m.PromCluster)
	if in.RangeMinutes <= 0 {
		samples, err := c.Query(ctx, expr)
		if err != nil {
			return "", err
		}
		total := len(samples)
		if len(samples) > maxSamples {
			samples = samples[:maxSamples]
		}
		out := make([]map[string]any, 0, len(samples))
		for _, s := range samples {
			out = append(out, map[string]any{"labels": compactLabels(s.Labels), "value": s.Value})
		}
		return marshal(map[string]any{"grafana": m.Instance, "metricsCluster": m.PromCluster, "series": total, "results": out})
	}
	minutes := clamp(in.RangeMinutes, 60, maxRangeMinutes)
	end := time.Now()
	start := end.Add(-time.Duration(minutes) * time.Minute)
	step := time.Duration(minutes) * time.Minute / maxPoints
	if step < 30*time.Second {
		step = 30 * time.Second
	}
	series, err := c.QueryRange(ctx, expr, start, end, step)
	if err != nil {
		return "", err
	}
	total := len(series)
	if len(series) > maxSeries {
		series = series[:maxSeries]
	}
	out := make([]map[string]any, 0, len(series))
	for _, s := range series {
		pts := make([][2]any, 0, len(s.Points))
		for _, p := range s.Points {
			pts = append(pts, [2]any{time.Unix(int64(p[0]), 0).UTC().Format("01-02 15:04"), roundSig(p[1])})
		}
		out = append(out, map[string]any{"labels": compactLabels(s.Labels), "points": pts})
	}
	return marshal(map[string]any{"grafana": m.Instance, "metricsCluster": m.PromCluster, "step": step.String(), "series": total, "results": out})
}

func runQueryLogs(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Cluster      string `json:"cluster"`
		Namespace    string `json:"namespace"`
		PodPrefix    string `json:"pod_prefix"`
		Filter       string `json:"filter"`
		SinceMinutes int    `json:"since_minutes"`
		Limit        int    `json:"limit"`
	}](input)
	if err != nil {
		return "", err
	}
	c, m, err := resolve(env, in.Cluster)
	if err != nil {
		return "", err
	}
	if m.LokiCluster == "" {
		return "", fmt.Errorf("cluster %q has no logs in Grafana's Loki", in.Cluster)
	}
	if in.Filter != "" {
		if _, err := regexp.Compile(in.Filter); err != nil {
			return "", fmt.Errorf("filter is not a valid regexp: %w", err)
		}
	}
	sel := []string{"k8s_cluster_name=" + grafana.Quote(m.LokiCluster), `service_name!="k8s-events"`}
	if in.Namespace != "" {
		sel = append(sel, "k8s_namespace_name="+grafana.Quote(in.Namespace))
	}
	if in.PodPrefix != "" {
		sel = append(sel, "k8s_pod_name=~"+grafana.Quote(regexp.QuoteMeta(in.PodPrefix)+".*"))
	}
	query := "{" + strings.Join(sel, ", ") + "}"
	if in.Filter != "" {
		query += " |~ " + grafana.Quote(in.Filter)
	}
	minutes := clamp(in.SinceMinutes, 60, maxLogMinutes)
	limit := clamp(in.Limit, 100, maxLogLines)
	end := time.Now()
	lines, err := c.Logs(ctx, query, end.Add(-time.Duration(minutes)*time.Minute), end, limit)
	if err != nil {
		return "", err
	}
	out := make([]map[string]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, map[string]string{"ts": l.TS.UTC().Format(time.RFC3339), "pod": l.Labels["k8s_pod_name"],
			"container": l.Labels["k8s_container_name"], "line": redact.LogLine(truncateStr(l.Line, 1000))})
	}
	return marshal(map[string]any{"grafana": m.Instance, "logsCluster": m.LokiCluster, "query": query, "lines": out})
}

// compactLabels drops labels that only describe the scrape.
func compactLabels(l map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range l {
		switch k {
		case "receive", "replica", "prometheus", "prometheus_replica", "tenant_id", "endpoint", "metrics_path", "instance", "service", "tenant", "control_plane", "data_plane":
			continue
		}
		out[k] = v
	}
	return out
}

// roundSig keeps four significant digits, which is plenty for the model.
func roundSig(v float64) float64 {
	r, err := strconv.ParseFloat(fmt.Sprintf("%.4g", v), 64)
	if err != nil {
		return v
	}
	return r
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
