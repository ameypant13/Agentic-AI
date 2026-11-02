package monitor

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/parser"
)

const (
	kindLogErrors = "log_errors"
	logNamespace  = "prophecy"
)

// LogScan lets the monitor read the log worker's results. Without it the
// log-parser check is skipped.
type LogScan struct {
	Worker *parser.Worker
}

// WithLogScan enables the log-parser check.
func (m *Monitor) WithLogScan(ls LogScan) *Monitor {
	m.logs = &ls
	return m
}

// podHash strips the ReplicaSet/StatefulSet suffix so findings follow the
// workload, not one pod name that changes on every restart.
var podHash = regexp.MustCompile(`(-[a-z0-9]{8,10})?-[a-z0-9]{5}$|-\d+$`)

func workloadOf(pod string) string { return podHash.ReplaceAllString(pod, "") }

// checkLogs turns the worker's latest results into findings: workloads with
// error-level matches in the prophecy namespace. checked gets
// "logs:<cluster id>" for each cluster the worker has results for.
func (m *Monitor) checkLogs(_ context.Context, checked map[string]bool) []observation {
	if m.logs == nil {
		return nil
	}
	var obs []observation
	for _, in := range m.logs.Worker.Clusters() {
		scan := m.logs.Worker.Summary(in.ID, logNamespace)
		if scan.Pending {
			continue
		}
		checked["logs:"+in.ID] = true
		obs = append(obs, logObservations(in, scan)...)
	}
	return obs
}

func logObservations(in kube.Infra, scan *parser.NamespaceScan) []observation {
	type agg struct {
		errors int
		top    string
		rule   string
	}
	byWorkload := map[string]*agg{}
	for _, p := range scan.Pods {
		if p.Errors == 0 {
			continue
		}
		w := workloadOf(p.Pod)
		a := byWorkload[w]
		if a == nil {
			a = &agg{}
			byWorkload[w] = a
		}
		a.errors += p.Errors
		for _, h := range p.Hits {
			if h.Severity == "error" && a.top == "" {
				a.top, a.rule = h.Title, h.RuleID
			}
		}
	}
	var out []observation
	for w, a := range byWorkload {
		sev := "warning"
		if a.errors >= 10 {
			sev = "critical"
		}
		out = append(out, observation{
			key: kindLogErrors + ":" + in.ID + "/" + w, kind: kindLogErrors, severity: sev,
			title:      fmt.Sprintf("%s in %s", a.top, w),
			detail:     fmt.Sprintf("Log parser: %d error-level matches in the last %s (rule %s).", a.errors, scan.Window, a.rule),
			entityType: "workload", entityID: in.ID + "/" + logNamespace + "/" + w, entityName: in.ClusterName,
			realm: "", provider: in.Provider, region: in.Region,
		})
	}
	return out
}

// findingSource is the check a finding belongs to, for deciding whether its
// check ran this time (see reconcile).
func findingSource(kind, realm, entityType, entityID string) string {
	switch {
	case kind == kindLogErrors:
		return "logs:" + strings.SplitN(entityID, "/", 2)[0]
	case entityType == "execution_plane":
		return "execution_planes"
	}
	return realm
}
