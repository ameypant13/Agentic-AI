package grafana

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestExploreLive reads metric names, labels, alert rules and dashboards from
// a real Grafana. It is skipped unless GRAFANA_LIVE_URL, GRAFANA_LIVE_TOKEN
// and GRAFANA_LIVE_CLUSTER are set.
func TestExploreLive(t *testing.T) {
	u, tok, cl := os.Getenv("GRAFANA_LIVE_URL"), os.Getenv("GRAFANA_LIVE_TOKEN"), os.Getenv("GRAFANA_LIVE_CLUSTER")
	if u == "" || tok == "" || cl == "" {
		t.Skip("set GRAFANA_LIVE_URL, GRAFANA_LIVE_TOKEN and GRAFANA_LIVE_CLUSTER")
	}
	c := New(Instance{Name: "live", URL: u, Token: tok})
	ctx := context.Background()
	sel := "{cluster=" + Quote(cl) + "}"
	t0 := time.Now()
	names, err := c.SeriesLabelValues(ctx, "__name__", sel, time.Hour)
	t.Logf("metric names: %d (%v) err=%v sample=%v", len(names), time.Since(t0), err, head(names, 5))
	labels, err := c.SeriesLabelNames(ctx, "kube_pod_info"+sel, time.Hour)
	t.Logf("kube_pod_info labels: %v err=%v", labels, err)
	ns, err := c.SeriesLabelValues(ctx, "namespace", "kube_pod_info"+sel, time.Hour)
	t.Logf("namespaces: %d err=%v", len(ns), err)
	alerts, err := c.Query(ctx, "ALERTS"+sel)
	t.Logf("ALERTS series: %d err=%v", len(alerts), err)
	for _, a := range head(alerts, 5) {
		t.Logf("  %v", a.Labels)
	}
	rules, err := c.GrafanaAlertRules(ctx)
	t.Logf("grafana rules: %d err=%v", len(rules), err)
	for _, r := range head(rules, 5) {
		t.Logf("  %s/%s %s alerts=%d labels=%v", r.Folder, r.Name, r.State, len(r.Alerts), r.Labels)
	}
	hits, err := c.SearchDashboards(ctx, "", 500)
	t.Logf("dashboards: %d err=%v", len(hits), err)
	for _, h := range head(hits, 8) {
		t.Logf("  %s %q (%s)", h.UID, h.Title, h.FolderTitle)
	}
	if len(hits) > 0 {
		d, err := c.Dashboard(ctx, hits[0].UID)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%q: %d variables, %d panels", d.Title, len(d.Variables), len(d.Panels))
		for _, p := range head(d.Panels, 3) {
			t.Logf("  %s [%s] %v", p.Title, p.Type, p.Queries)
		}
	}
}

func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}
