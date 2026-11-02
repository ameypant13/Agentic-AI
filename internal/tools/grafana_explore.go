package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

const (
	maxNames      = 200
	maxAlerts     = 100
	maxDashboards = 50
	maxPanels     = 60
	seriesWindow  = time.Hour
)

var (
	metricNameRE = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNameRE  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

func grafanaExploreTools() []Tool {
	return []Tool{
		{
			Name: "grafana_list_metrics",
			Description: "Metric names a cluster sent to Grafana in the last hour. Call this before writing PromQL for a metric you " +
				"are not sure exists. search narrows by case-insensitive substring (e.g. jvm, kafka, http_server).",
			Properties: map[string]any{
				"cluster": str("Control-hub cluster name."),
				"search":  str("Case-insensitive substring of the metric name."),
				"limit":   integer(fmt.Sprintf("Max names, default 100, max %d.", maxNames)),
			},
			Required: []string{"cluster"},
			Label:    "Listed metrics",
			Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
				in, err := decode[struct {
					Cluster string `json:"cluster"`
					Search  string `json:"search"`
					Limit   int    `json:"limit"`
				}](input)
				if err != nil {
					return "", err
				}
				c, m, err := resolve(env, in.Cluster)
				if err != nil {
					return "", err
				}
				names, err := c.SeriesLabelValues(ctx, "__name__", "{cluster="+grafana.Quote(m.PromCluster)+"}", seriesWindow)
				if err != nil {
					return "", err
				}
				kept, total := filterNames(names, in.Search, clamp(in.Limit, 100, maxNames))
				return marshal(map[string]any{"metricsCluster": m.PromCluster, "matching": total, "metrics": kept})
			},
		},
		{
			Name: "grafana_label_values",
			Description: "Labels on a cluster's series. With label: the values it takes (e.g. namespace, pod, job, container), " +
				"optionally only on one metric. Without label: the label names a metric carries. Use it to write exact selectors.",
			Properties: map[string]any{
				"cluster": str("Control-hub cluster name."),
				"metric":  str("Optional metric name to restrict to; required when label is empty."),
				"label":   str("Optional label name, e.g. namespace."),
				"search":  str("Case-insensitive substring of the value."),
				"limit":   integer(fmt.Sprintf("Max values, default 100, max %d.", maxNames)),
			},
			Required: []string{"cluster"},
			Label:    "Listed metric labels",
			Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
				in, err := decode[struct {
					Cluster string `json:"cluster"`
					Metric  string `json:"metric"`
					Label   string `json:"label"`
					Search  string `json:"search"`
					Limit   int    `json:"limit"`
				}](input)
				if err != nil {
					return "", err
				}
				if in.Metric != "" && !metricNameRE.MatchString(in.Metric) {
					return "", fmt.Errorf("metric %q is not a metric name", in.Metric)
				}
				if in.Label != "" && !labelNameRE.MatchString(in.Label) {
					return "", fmt.Errorf("label %q is not a label name", in.Label)
				}
				if in.Label == "" && in.Metric == "" {
					return "", fmt.Errorf("give a label, a metric, or both")
				}
				c, m, err := resolve(env, in.Cluster)
				if err != nil {
					return "", err
				}
				sel := in.Metric + "{cluster=" + grafana.Quote(m.PromCluster) + "}"
				if in.Label == "" {
					names, err := c.SeriesLabelNames(ctx, sel, seriesWindow)
					if err != nil {
						return "", err
					}
					var labels []string
					for _, n := range names {
						if _, scrape := scrapeLabels[n]; !scrape && n != "__name__" {
							labels = append(labels, n)
						}
					}
					return marshal(map[string]any{"metric": in.Metric, "labels": labels})
				}
				vals, err := c.SeriesLabelValues(ctx, in.Label, sel, seriesWindow)
				if err != nil {
					return "", err
				}
				kept, total := filterNames(vals, in.Search, clamp(in.Limit, 100, maxNames))
				return marshal(map[string]any{"label": in.Label, "metric": in.Metric, "matching": total, "values": kept})
			},
		},
		{
			Name: "grafana_alerts",
			Description: "Alerts currently firing or pending on a cluster, from both sources: Prometheus alert rules " +
				"(kube-prometheus-stack, e.g. KubePodCrashLooping, CPUThrottlingHigh) and Grafana-managed alert rules (e.g. the " +
				"Golden-Signals folder). A second opinion next to current_issues; these alerts are often noisier.",
			Properties: map[string]any{
				"cluster": str("Control-hub cluster name."),
				"state":   enum("firing, pending, or both (default).", "firing", "pending", "both"),
				"search":  str("Optional case-insensitive substring of the alert name, namespace or pod."),
			},
			Required: []string{"cluster"},
			Label:    "Checked alerts",
			Run:      runAlerts,
		},
		{
			Name: "grafana_dashboards",
			Description: "The team's Grafana dashboards, to reuse their curated PromQL. Without uid: search dashboards by title on the " +
				"cluster's Grafana. With uid: the dashboard's variables and each panel's title, type, unit and queries " +
				"(variables left as written). Queries that select cluster=~\"$cluster\" can go to grafana_query_metrics as they are; " +
				"replace any other variable ($namespace, $pod, $__rate_interval) with a value first.",
			Properties: map[string]any{
				"cluster": str("Control-hub cluster name; picks its Grafana instance."),
				"search":  str("Words in the dashboard title, e.g. JVM, kafka, golden signals."),
				"uid":     str("Dashboard UID from a search, to read its panels."),
				"panel":   str("With uid: only panels whose title contains this text."),
			},
			Required: []string{"cluster"},
			Label:    "Read Grafana dashboards",
			Run:      runDashboards,
		},
	}
}

var scrapeLabels = map[string]struct{}{"receive": {}, "replica": {}, "prometheus": {}, "prometheus_replica": {}, "tenant_id": {},
	"endpoint": {}, "metrics_path": {}, "instance": {}, "service": {}, "tenant": {}, "control_plane": {}, "data_plane": {}}

// filterNames keeps sorted names containing search, at most limit.
func filterNames(names []string, search string, limit int) ([]string, int) {
	s := strings.ToLower(strings.TrimSpace(search))
	out := []string{}
	total := 0
	sort.Strings(names)
	for _, n := range names {
		if s != "" && !strings.Contains(strings.ToLower(n), s) {
			continue
		}
		total++
		if len(out) < limit {
			out = append(out, n)
		}
	}
	return out, total
}

type alertRow struct {
	Source   string            `json:"source"`
	Name     string            `json:"name"`
	State    string            `json:"state"`
	Severity string            `json:"severity,omitempty"`
	Labels   map[string]string `json:"labels"`
	Since    string            `json:"since,omitempty"`
	Value    string            `json:"value,omitempty"`
	Summary  string            `json:"summary,omitempty"`
}

func runAlerts(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Cluster string `json:"cluster"`
		State   string `json:"state"`
		Search  string `json:"search"`
	}](input)
	if err != nil {
		return "", err
	}
	c, m, err := resolve(env, in.Cluster)
	if err != nil {
		return "", err
	}
	want := func(state string) bool {
		switch in.State {
		case "firing", "pending":
			return state == in.State
		}
		return state == "firing" || state == "pending"
	}
	search := strings.ToLower(in.Search)
	matches := func(r alertRow) bool {
		if search == "" {
			return true
		}
		return strings.Contains(strings.ToLower(r.Name+" "+r.Labels["namespace"]+" "+r.Labels["pod"]), search)
	}
	var rows []alertRow
	var errs []string

	// Prometheus rules: their ALERTS series, one per replica, so deduplicate.
	samples, err := c.Query(ctx, "ALERTS{cluster="+grafana.Quote(m.PromCluster)+"}")
	if err != nil {
		errs = append(errs, "prometheus alerts: "+err.Error())
	}
	seen := map[string]bool{}
	for _, s := range samples {
		l := compactLabels(s.Labels)
		name, state, sev := l["alertname"], l["alertstate"], l["severity"]
		for _, k := range []string{"__name__", "alertname", "alertstate", "severity", "cluster"} {
			delete(l, k)
		}
		r := alertRow{Source: "prometheus", Name: name, State: state, Severity: sev, Labels: l}
		key, _ := json.Marshal(r)
		if seen[string(key)] || !want(state) || !matches(r) {
			continue
		}
		seen[string(key)] = true
		rows = append(rows, r)
	}

	// Grafana-managed rules: the alert instances labelled with this cluster.
	rules, err := c.GrafanaAlertRules(ctx)
	if err != nil {
		errs = append(errs, "grafana alert rules: "+err.Error())
	}
	for _, rule := range rules {
		for _, a := range rule.Alerts {
			state := grafanaState(a.State)
			if !labelledWith(a.Labels, m.PromCluster) || !want(state) {
				continue
			}
			l := compactLabels(a.Labels)
			sev := firstNonEmptyS(l["severity"], rule.Labels["severity"])
			for _, k := range []string{"alertname", "severity", "cluster", "grafana_folder", "__alert_rule_uid__"} {
				delete(l, k)
			}
			r := alertRow{Source: "grafana:" + rule.Folder, Name: rule.Name, State: state, Severity: sev, Labels: l,
				Since: a.ActiveAt, Value: a.Value, Summary: redact.Text(truncateStr(firstNonEmptyS(rule.Annotations["summary"], rule.Annotations["description"]), 300))}
			if matches(r) {
				rows = append(rows, r)
			}
		}
	}

	sevRank := map[string]int{"critical": 0, "error": 1, "warning": 2, "info": 3}
	rank := func(s string) int {
		if r, ok := sevRank[s]; ok {
			return r
		}
		return 4
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := rows[i].State == "firing", rows[j].State == "firing"; a != b {
			return a
		}
		if a, b := rank(rows[i].Severity), rank(rows[j].Severity); a != b {
			return a < b
		}
		return rows[i].Name < rows[j].Name
	})
	byName := map[string]int{}
	for _, r := range rows {
		byName[r.Name+" ("+r.State+")"]++
	}
	total := len(rows)
	if len(rows) > maxAlerts {
		rows = rows[:maxAlerts]
	}
	if rows == nil {
		rows = []alertRow{}
	}
	out := map[string]any{"grafana": m.Instance, "metricsCluster": m.PromCluster, "total": total, "countsByAlert": byName, "alerts": rows}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	return marshal(out)
}

// grafanaState maps Grafana's alert instance states (Alerting, Pending,
// Normal, NoData, Error, "Alerting (Error)") to Prometheus' firing/pending.
func grafanaState(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.HasPrefix(s, "alerting"), s == "firing":
		return "firing"
	case strings.HasPrefix(s, "pending"):
		return "pending"
	}
	return s
}

func labelledWith(labels map[string]string, cluster string) bool {
	for k, v := range labels {
		if v == cluster && (k == "cluster" || strings.Contains(k, "cluster")) {
			return true
		}
	}
	return false
}

func firstNonEmptyS(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

var dashboardUIDRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func runDashboards(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Cluster string `json:"cluster"`
		Search  string `json:"search"`
		UID     string `json:"uid"`
		Panel   string `json:"panel"`
	}](input)
	if err != nil {
		return "", err
	}
	c, m, err := resolve(env, in.Cluster)
	if err != nil {
		return "", err
	}
	if in.UID == "" {
		hits, err := c.SearchDashboards(ctx, strings.TrimSpace(in.Search), maxDashboards)
		if err != nil {
			return "", err
		}
		if hits == nil {
			hits = []grafana.DashboardHit{}
		}
		return marshal(map[string]any{"grafana": m.Instance, "dashboards": hits})
	}
	if !dashboardUIDRE.MatchString(in.UID) {
		return "", fmt.Errorf("uid %q is not a dashboard UID", in.UID)
	}
	d, err := c.Dashboard(ctx, in.UID)
	if err != nil {
		return "", err
	}
	if p := strings.ToLower(in.Panel); p != "" {
		kept := d.Panels[:0]
		for _, x := range d.Panels {
			if strings.Contains(strings.ToLower(x.Title+" "+x.Row), p) {
				kept = append(kept, x)
			}
		}
		d.Panels = kept
	}
	total := len(d.Panels)
	if len(d.Panels) > maxPanels {
		d.Panels = d.Panels[:maxPanels]
	}
	return marshal(map[string]any{"grafana": m.Instance, "metricsCluster": m.PromCluster, "panelCount": total, "dashboard": d})
}
