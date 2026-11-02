package parser

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/logrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

const (
	lokiDelay      = 30 * time.Second // Loki ingestion lag; don't read the newest seconds
	lokiFirstRead  = 10 * time.Minute
	lokiMaxWindow  = 30 * time.Minute // after a long gap, catch up at most this much
	lokiPageLimit  = 5000
	lokiMaxPages   = 6
	lokiSkipSystem = `kube-system|kube-node-lease|kube-public|monitoring|gmp-system|gmp-public|cert-manager|gatekeeper-system|calico-system|tigera-operator|azure-.*`
)

// WithGrafana lets the worker read logs from Loki for clusters Grafana covers,
// instead of each cluster's API server.
func (w *Worker) WithGrafana(hub *grafana.Hub) *Worker {
	w.hub = hub
	return w
}

// lokiQuery builds the LogQL for one cluster: all non-system namespaces, pod
// logs only (not Kubernetes events), lines containing any rule's literal.
func lokiQuery(lokiCluster string, lits []string, fold bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{k8s_cluster_name=%s, k8s_namespace_name!~%s, service_name!="k8s-events"}`,
		grafana.Quote(lokiCluster), grafana.Quote(lokiSkipSystem))
	if len(lits) > 0 {
		sorted := append([]string(nil), lits...)
		sort.Strings(sorted)
		parts := make([]string, len(sorted))
		for i, l := range sorted {
			parts[i] = regexp.QuoteMeta(l)
		}
		re := strings.Join(parts, "|")
		if fold {
			re = "(?i)" + re
		}
		fmt.Fprintf(&b, " |~ %s", grafana.Quote(re))
	}
	return b.String()
}

// scanClusterLoki reads candidate lines since the cluster's Loki cursor and
// matches them exactly, returning the hits to record.
func (w *Worker) scanClusterLoki(ctx context.Context, in kube.Infra, m *grafana.Mapping, cs *ClusterState, set *logrules.Set) ([]hitRecord, error) {
	c := w.hub.Client(m.Instance)
	if c == nil {
		return nil, fmt.Errorf("grafana instance %s is not configured", m.Instance)
	}
	lits, fold, complete := set.Literals()
	if !complete {
		lits = nil // some rule has no literal: read everything rather than miss its matches
	}
	until := time.Now().Add(-lokiDelay)
	w.mu.RLock()
	since := cs.LokiCursor
	w.mu.RUnlock()
	if since.IsZero() {
		since = until.Add(-lokiFirstRead)
	}
	if until.Sub(since) > lokiMaxWindow {
		since = until.Add(-lokiMaxWindow)
	}
	if !until.After(since) {
		return nil, nil
	}

	query := lokiQuery(m.LokiCluster, lits, fold)
	var recs []hitRecord
	start := since
	for page := 0; page < lokiMaxPages; page++ {
		lines, err := c.Logs(ctx, query, start, until, lokiPageLimit)
		if err != nil {
			return recs, err
		}
		for _, l := range lines {
			ns, pod, container := l.Labels["k8s_namespace_name"], l.Labels["k8s_pod_name"], l.Labels["k8s_container_name"]
			if pod == "" {
				continue
			}
			matched := set.Match(pod, l.Line)
			if len(matched) == 0 {
				w.unmatched(set, in.ClusterName, ns, pod, l.Line, l.TS)
			}
			for _, i := range matched {
				r := set.Rules()[i]
				recs = append(recs, hitRecord{TS: l.TS, Namespace: ns, Pod: pod, Container: container, RuleID: r.ID,
					Severity: r.Severity, Line: truncate(redact.LogLine(l.Line), maxSample)})
				w.recordHit(cs, ns, pod, r, l.TS, l.Line)
			}
		}
		if len(lines) < lokiPageLimit {
			break
		}
		// A full page: continue just after the last line read.
		start = lines[len(lines)-1].TS.Add(time.Nanosecond)
	}
	w.mu.Lock()
	cs.LokiCursor = until
	cs.Source = "loki:" + m.Instance
	w.mu.Unlock()
	w.sampleErrors(ctx, c, in, m, cs, set, until)
	return recs, nil
}

const (
	errorSampleEvery = 30 * time.Minute
	errorSampleLimit = 500
)

// sampleErrors reads a capped sample of recent error lines from Loki for the
// pattern miner. The rule query only returns lines matching the rules'
// literals, so errors no rule knows never reach it otherwise.
func (w *Worker) sampleErrors(ctx context.Context, c *grafana.Client, in kube.Infra, m *grafana.Mapping, cs *ClusterState, set *logrules.Set, until time.Time) {
	if w.patterns == nil {
		return
	}
	w.mu.RLock()
	last := cs.ErrorSampleAt
	w.mu.RUnlock()
	if until.Sub(last) < errorSampleEvery {
		return
	}
	since := until.Add(-errorSampleEvery)
	query := "{k8s_cluster_name=" + grafana.Quote(m.LokiCluster) + `, service_name!="k8s-events"} |~ "(?i)(error|exception|fatal|panic)"`
	lines, err := c.Logs(ctx, query, since, until, errorSampleLimit)
	if err != nil {
		return // best effort; the next cycle tries again
	}
	for _, l := range lines {
		pod := l.Labels["k8s_pod_name"]
		if pod != "" && len(set.Match(pod, l.Line)) == 0 {
			w.unmatched(set, in.ClusterName, l.Labels["k8s_namespace_name"], pod, l.Line, l.TS)
		}
	}
	w.mu.Lock()
	cs.ErrorSampleAt = until
	w.mu.Unlock()
}

// recordHit updates the summary state for one match.
func (w *Worker) recordHit(cs *ClusterState, ns, pod string, r logrules.Rule, ts time.Time, line string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if cs.Namespaces[ns] == nil {
		cs.Namespaces[ns] = map[string]*PodState{}
	}
	ps := cs.Namespaces[ns][pod]
	if ps == nil {
		ps = &PodState{Rules: map[string]*RuleState{}, LastTS: map[string]time.Time{}}
		cs.Namespaces[ns][pod] = ps
	}
	if ts.After(ps.Seen) {
		ps.Seen = ts
	}
	rs := ps.Rules[r.ID]
	if rs == nil {
		rs = &RuleState{RuleID: r.ID, FirstSeen: ts, Minutes: map[int64]int{}}
		ps.Rules[r.ID] = rs
	}
	rs.Title, rs.Severity, rs.Meaning, rs.Fix = r.Title, r.Severity, r.Meaning, r.Fix
	rs.Total++
	rs.Minutes[ts.Unix()/60]++
	if ts.After(rs.LastSeen) {
		rs.LastSeen, rs.Sample = ts, truncate(redact.LogLine(line), maxSample)
	}
}
