// Package readiness decides whether a control plane is safe to upgrade. Each
// check reads one source (control-hub, the console's findings and
// certificates, the cluster, Grafana, Athena) and returns pass, warn, fail or
// unknown; the verdict is no-go on any failure, caution on any warning. No AI
// is involved: the Copilot can explain a report, but the checks are plain rules.
package readiness

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/certs"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

type Status string

const (
	Pass    Status = "pass"
	Warn    Status = "warn"
	Fail    Status = "fail"
	Unknown Status = "unknown"
)

// Check is one readiness check's outcome.
type Check struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	// Hint says what to do about a warning or failure.
	Hint string `json:"hint,omitempty"`
}

// ControlPlane is what the checks need to know about the target.
type ControlPlane struct {
	ID, Realm, Customer, Tenant, URL, Provider, Region string
	Status, StatusMessage, PostDeployStatus            string
	ProphecyVersion, InfrastructureID, ClusterName     string
	FailureReason                                      string
}

// Report is the verdict and every check.
type Report struct {
	ControlPlane ControlPlane `json:"controlPlane"`
	Verdict      string       `json:"verdict"` // go, caution, no-go, unknown
	Summary      string       `json:"summary"`
	Checks       []Check      `json:"checks"`
	At           time.Time    `json:"at"`
}

// Sources are where the checks read from; nil ones report unknown.
type Sources struct {
	// Findings returns the console's open findings about any of names.
	Findings func(names []string) []store.Finding
	// Certs returns the certificate inventory.
	Certs func() []certs.Item
	// Cluster opens the control plane's cluster (the caller's access applies).
	Cluster func(ctx context.Context) (*kube.Cluster, error)
	// Metrics runs an instant PromQL query with cluster="$cluster" substituted.
	Metrics func(ctx context.Context, promql string) ([]grafana.Sample, error)
	// Athena reads one Athena view as decoded JSON.
	Athena func(ctx context.Context, view string) (map[string]any, error)
	// Rightsizing returns right-sizing data for the control plane's cluster.
	Rightsizing func() []cost.Recommendation
	// Namespace is where Prophecy runs (default "prophecy").
	Namespace string
}

// Run evaluates every check concurrently.
func Run(ctx context.Context, cp ControlPlane, src Sources) Report {
	if src.Namespace == "" {
		src.Namespace = "prophecy"
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// The cluster is opened once and shared by the cluster checks.
	var cl *kube.Cluster
	var clErr error
	if src.Cluster != nil {
		cl, clErr = src.Cluster(ctx)
	} else {
		clErr = fmt.Errorf("no cluster access")
	}

	checks := []func() Check{
		func() Check { return controlPlaneCheck(cp) },
		func() Check { return findingsCheck(cp, src) },
		func() Check { return certsCheck(cp, src) },
		func() Check { return nodesCheck(ctx, cl, clErr) },
		func() Check { return podsCheck(ctx, cl, clErr, src.Namespace) },
		func() Check { return helmCheck(ctx, cl, clErr) },
		func() Check { return postgresCheck(ctx, cl, clErr, src.Namespace) },
		func() Check { return diskCheck(ctx, src) },
		func() Check { return athenaCheck(ctx, src) },
		func() Check { return oomCheck(src) },
		func() Check { return changesCheck(ctx, cl, clErr) },
		func() Check { return sandboxCheck(ctx, cl, clErr) },
	}
	out := make([]Check, len(checks))
	var wg sync.WaitGroup
	for i, f := range checks {
		wg.Add(1)
		go func(i int, f func() Check) {
			defer wg.Done()
			out[i] = f()
		}(i, f)
	}
	wg.Wait()
	r := Report{ControlPlane: cp, Checks: out, At: time.Now().UTC()}
	r.Verdict, r.Summary = verdict(out)
	return r
}

func verdict(cs []Check) (string, string) {
	count := map[Status][]string{}
	for _, c := range cs {
		count[c.Status] = append(count[c.Status], c.Name)
	}
	unknown := ""
	if n := len(count[Unknown]); n > 0 {
		unknown = fmt.Sprintf(" %d check(s) could not run: %s.", n, strings.Join(count[Unknown], ", "))
	}
	switch {
	case len(count[Fail]) > 0:
		return "no-go", fmt.Sprintf("Fix before upgrading: %s.%s", strings.Join(count[Fail], ", "), unknown)
	case len(count[Warn]) > 0:
		return "caution", fmt.Sprintf("Safe to upgrade with care; review: %s.%s", strings.Join(count[Warn], ", "), unknown)
	case len(count[Unknown])*2 > len(cs):
		// Never say go without having looked.
		return "unknown", "Not enough could be checked to decide." + unknown
	}
	return "go", "Every check that ran passed." + unknown
}

func controlPlaneCheck(cp ControlPlane) Check {
	c := Check{ID: "control-plane", Name: "Control plane state"}
	switch {
	case strings.HasSuffix(cp.Status, "FAILED"):
		c.Status, c.Detail = Fail, cp.Status
		if cp.FailureReason != "" {
			c.Detail += ": " + cp.FailureReason
		}
		c.Hint = "Resolve the failed operation (Investigate it) before starting another."
	case cp.Status == "OPERATIONAL":
		c.Status, c.Detail = Pass, "OPERATIONAL"
		if cp.ProphecyVersion != "" {
			c.Detail += ", Prophecy " + cp.ProphecyVersion
		}
		if pd := strings.ToLower(cp.PostDeployStatus); pd != "" && pd != "completed" && pd != "success" && pd != "succeeded" {
			c.Status, c.Detail = Warn, c.Detail+"; post-deploy "+cp.PostDeployStatus
			c.Hint = "Post-deploy has not completed; let it finish or retry it first."
		}
	case cp.Status == "":
		c.Status, c.Detail = Unknown, "control-hub returned no status"
	default:
		c.Status, c.Detail = Warn, cp.Status+" — another operation is in progress"
		c.Hint = "Wait for it to finish."
	}
	return c
}

func findingsCheck(cp ControlPlane, src Sources) Check {
	c := Check{ID: "findings", Name: "Open findings"}
	if src.Findings == nil {
		c.Status, c.Detail = Unknown, "findings unavailable"
		return c
	}
	var crit, warn []string
	for _, f := range src.Findings(names(cp)) {
		if f.State == "resolved" {
			continue
		}
		if f.Severity == "critical" {
			crit = append(crit, f.Title)
		} else {
			warn = append(warn, f.Title)
		}
	}
	switch {
	case len(crit) > 0:
		c.Status, c.Detail = Fail, fmt.Sprintf("%d critical: %s", len(crit), list(crit, 4))
		c.Hint = "An upgrade restarts every service; fix or understand the critical findings first."
	case len(warn) > 0:
		c.Status, c.Detail = Warn, fmt.Sprintf("%d warning(s): %s", len(warn), list(warn, 4))
	default:
		c.Status, c.Detail = Pass, "no open findings"
	}
	return c
}

func certsCheck(cp ControlPlane, src Sources) Check {
	c := Check{ID: "certificates", Name: "TLS certificates"}
	if src.Certs == nil {
		c.Status, c.Detail = Unknown, "certificate inventory unavailable"
		return c
	}
	host := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(cp.URL), "https://"), "http://")
	host = strings.TrimRight(host, "/")
	var mine []certs.Item
	for _, it := range src.Certs() {
		if (host != "" && strings.EqualFold(it.Host, host)) || containsFold(it.UsedBy, host) ||
			(cp.Customer != "" && strings.EqualFold(it.Tenant, cp.Customer)) {
			mine = append(mine, it)
		}
	}
	if len(mine) == 0 {
		c.Status, c.Detail = Unknown, "no certificate found for "+host
		return c
	}
	c.Status = Pass
	var notes []string
	for _, it := range mine {
		if it.DaysLeft != nil {
			d := *it.DaysLeft
			switch {
			case d < 7:
				c.Status = Fail
				notes = append(notes, fmt.Sprintf("%s expires in %d days", it.Host, d))
			case d < 21:
				c.Status = worse(c.Status, Warn)
				notes = append(notes, fmt.Sprintf("%s expires in %d days", it.Host, d))
			default:
				notes = append(notes, fmt.Sprintf("%s: %d days left", it.Host, d))
			}
		}
		for _, p := range it.Problems {
			if p.Severity == "error" || p.Severity == "critical" {
				c.Status = worse(c.Status, Warn)
				notes = append(notes, p.Message)
			}
		}
	}
	c.Detail = list(notes, 4)
	if c.Status != Pass {
		c.Hint = "Renew first: an expiry or a failed renewal during the upgrade looks like an upgrade failure."
	}
	return c
}

func nodesCheck(ctx context.Context, cl *kube.Cluster, clErr error) Check {
	c := Check{ID: "nodes", Name: "Nodes and headroom"}
	if clErr != nil {
		c.Status, c.Detail = Unknown, "cluster not reachable: "+clErr.Error()
		return c
	}
	nodes, err := cl.Nodes(ctx)
	if err != nil {
		c.Status, c.Detail = Unknown, err.Error()
		return c
	}
	var notReady, pressure []string
	var cpuSum, memSum int
	for _, n := range nodes {
		if !n.Ready {
			notReady = append(notReady, n.Name)
		}
		if len(n.Pressure) > 0 {
			pressure = append(pressure, n.Name+" ("+strings.Join(n.Pressure, ", ")+")")
		}
		cpuSum += n.CPURequestedPct
		memSum += n.MemRequestedPct
	}
	cpu, mem := 0, 0
	if len(nodes) > 0 {
		cpu, mem = cpuSum/len(nodes), memSum/len(nodes)
	}
	c.Detail = fmt.Sprintf("%d nodes, %d%% CPU and %d%% memory requested", len(nodes), cpu, mem)
	switch {
	case len(notReady) > 0:
		c.Status, c.Detail = Fail, fmt.Sprintf("%d not ready: %s", len(notReady), list(notReady, 4))
		c.Hint = "Upgraded pods may land on broken nodes; fix or drain them."
	case len(pressure) > 0:
		c.Status, c.Detail = Warn, "pressure on "+list(pressure, 3)
		c.Hint = "Nodes under pressure evict pods during the rollout."
	case cpu >= 90 || mem >= 90:
		c.Status = Warn
		c.Hint = "A rolling upgrade starts new pods before stopping old ones; with this little headroom they may stay Pending unless the autoscaler adds nodes."
	default:
		c.Status = Pass
	}
	return c
}

func podsCheck(ctx context.Context, cl *kube.Cluster, clErr error, ns string) Check {
	c := Check{ID: "pods", Name: "Prophecy services"}
	if clErr != nil {
		c.Status, c.Detail = Unknown, "cluster not reachable"
		return c
	}
	pods, err := cl.Pods(ctx, ns)
	if err != nil {
		c.Status, c.Detail = Unknown, err.Error()
		return c
	}
	var broken, pending []string
	for _, p := range pods {
		switch {
		case p.Status == "Pending":
			pending = append(pending, p.Name)
		case p.IssueSeverity == "critical" || (p.Status != "Running" && p.Status != "Succeeded" && p.Status != "Completed"):
			broken = append(broken, p.Name+" ("+firstNonEmpty(p.Issue, p.Status)+")")
		}
	}
	switch {
	case len(pods) == 0:
		c.Status, c.Detail = Unknown, "no pods in namespace "+ns
	case len(broken) > 0:
		c.Status, c.Detail = Fail, fmt.Sprintf("%d of %d unhealthy: %s", len(broken), len(pods), list(broken, 4))
		c.Hint = "An upgrade will not fix a crash-looping service and hides the original cause; investigate first."
	case len(pending) > 0:
		c.Status, c.Detail = Warn, fmt.Sprintf("%d pending: %s", len(pending), list(pending, 4))
		c.Hint = "Pending pods usually mean no capacity; the upgrade needs more."
	default:
		c.Status, c.Detail = Pass, fmt.Sprintf("all %d pods running", len(pods))
	}
	return c
}

func helmCheck(ctx context.Context, cl *kube.Cluster, clErr error) Check {
	c := Check{ID: "helm", Name: "Helm releases"}
	if clErr != nil {
		c.Status, c.Detail = Unknown, "cluster not reachable"
		return c
	}
	rels, err := cl.HelmReleases(ctx, "")
	if err != nil {
		c.Status, c.Detail = Unknown, err.Error()
		return c
	}
	var stuck, ok []string
	for _, r := range rels {
		switch r.Status {
		case "deployed", "superseded", "uninstalled":
			if strings.Contains(r.Chart, "prophecy") {
				ok = append(ok, fmt.Sprintf("%s %s", r.Name, r.ChartVersion))
			}
		default:
			stuck = append(stuck, fmt.Sprintf("%s/%s is %s (revision %d)", r.Namespace, r.Name, r.Status, r.Revision))
		}
	}
	switch {
	case len(stuck) > 0:
		c.Status, c.Detail = Fail, list(stuck, 4)
		c.Hint = "Helm refuses a new upgrade while a release is pending or failed; roll it back or finish it first."
	case len(rels) == 0:
		c.Status, c.Detail = Unknown, "no Helm releases visible"
	default:
		c.Status, c.Detail = Pass, fmt.Sprintf("%d releases deployed", len(rels))
		if len(ok) > 0 {
			c.Detail += ": " + list(ok, 3)
		}
	}
	return c
}

var pgTag = regexp.MustCompile(`postgres(?:ql)?[^:]*:(\d+)`)

func postgresCheck(ctx context.Context, cl *kube.Cluster, clErr error, ns string) Check {
	c := Check{ID: "postgres", Name: "Postgres"}
	if clErr != nil {
		c.Status, c.Detail = Unknown, "cluster not reachable"
		return c
	}
	pods, err := cl.Pods(ctx, ns)
	if err != nil {
		c.Status, c.Detail = Unknown, err.Error()
		return c
	}
	for _, p := range pods {
		for _, img := range p.Images {
			m := pgTag.FindStringSubmatch(img)
			if m == nil {
				continue
			}
			major, _ := strconv.Atoi(m[1])
			c.Status, c.Detail = Pass, fmt.Sprintf("in-cluster Postgres %d (%s)", major, p.Name)
			if major >= 15 {
				// Postgres 15 removed CREATE on the public schema from ordinary
				// users; without an explicit grant metagraph's migrations fail.
				logs, err := cl.PodLogsLimited(ctx, ns, p.Name, "", 24*time.Hour, 2000, false, 2<<20)
				if err == nil {
					for _, l := range logs.Lines {
						if strings.Contains(l, "permission denied for schema public") {
							c.Status = Fail
							c.Detail += `; "permission denied for schema public" in its logs`
							c.Hint = "Grant CREATE on schema public to the application role (addusers.sh) before upgrading; migrations will fail."
							return c
						}
					}
				}
				c.Detail += "; Postgres 15+ needs CREATE on schema public granted explicitly (no permission errors in the last 24 h)"
			}
			return c
		}
	}
	c.Status, c.Detail = Unknown, "no in-cluster Postgres pod (external database?)"
	return c
}

func diskCheck(ctx context.Context, src Sources) Check {
	c := Check{ID: "disk", Name: "Volume headroom"}
	if src.Metrics == nil {
		c.Status, c.Detail = Unknown, "this cluster sends no metrics to a Grafana the console reads"
		return c
	}
	used, err := src.Metrics(ctx, `max by (namespace, persistentvolumeclaim) (kubelet_volume_stats_used_bytes{cluster="$cluster"} / kubelet_volume_stats_capacity_bytes{cluster="$cluster"})`)
	if err != nil {
		c.Status, c.Detail = Unknown, err.Error()
		return c
	}
	if len(used) == 0 {
		c.Status, c.Detail = Unknown, "no volume metrics"
		return c
	}
	sort.Slice(used, func(i, j int) bool { return used[i].Value > used[j].Value })
	top := used[0]
	name := top.Labels["namespace"] + "/" + top.Labels["persistentvolumeclaim"]
	c.Detail = fmt.Sprintf("fullest volume %s at %.0f%% (%d volumes)", name, top.Value*100, len(used))
	switch {
	case top.Value >= 0.9:
		c.Status, c.Hint = Fail, "Migrations and backups need space; expand the volume first."
	case top.Value >= 0.8:
		c.Status, c.Hint = Warn, "Upgrades run migrations and may write snapshots; consider expanding."
	default:
		c.Status = Pass
	}
	return c
}

func athenaCheck(ctx context.Context, src Sources) Check {
	c := Check{ID: "athena", Name: "Athena status"}
	if src.Athena == nil {
		c.Status, c.Detail = Unknown, "Athena access is not configured"
		return c
	}
	st, err := src.Athena(ctx, "prophecy_status")
	if err != nil {
		c.Status, c.Detail = Unknown, err.Error()
		return c
	}
	down, _ := st["isProphecyDown"].(bool)
	anyDown, _ := st["isAnyServiceDown"].(bool)
	msg, _ := st["message"].(string)
	switch {
	case down:
		c.Status, c.Detail = Fail, "Athena reports Prophecy down"
		c.Hint = "Find out why before upgrading."
	case anyDown:
		c.Status, c.Detail = Warn, "Athena reports a service down"
	default:
		c.Status, c.Detail = Pass, "Athena reports every service up"
	}
	if msg != "" {
		c.Detail += ": " + msg
	}
	if info, err := src.Athena(ctx, "update_info"); err == nil {
		if s, ok := info["upgradeStatus"].(string); ok && s != "" && !strings.EqualFold(s, "completed") && !strings.EqualFold(s, "none") {
			c.Status = worse(c.Status, Warn)
			c.Detail += "; Athena upgrade status " + s
		}
	}
	return c
}

func oomCheck(src Sources) Check {
	c := Check{ID: "oom", Name: "Memory history"}
	if src.Rightsizing == nil {
		c.Status, c.Detail = Unknown, "no usage history for this cluster"
		return c
	}
	recs := src.Rightsizing()
	if len(recs) == 0 {
		c.Status, c.Detail = Unknown, "no usage history for this cluster"
		return c
	}
	var oom []string
	for _, r := range recs {
		if r.OOMDays > 0 {
			oom = append(oom, fmt.Sprintf("%s/%s (%d days)", r.Name, r.Container, r.OOMDays))
		}
	}
	if len(oom) > 0 {
		c.Status, c.Detail = Warn, "OOM-killed in the last 30 days: "+list(oom, 4)
		c.Hint = "New versions often use more memory; raise these limits (see Costs → Needs more) before upgrading."
		return c
	}
	c.Status, c.Detail = Pass, fmt.Sprintf("no OOM kills across %d containers in 30 days", len(recs))
	return c
}

func changesCheck(ctx context.Context, cl *kube.Cluster, clErr error) Check {
	c := Check{ID: "changes", Name: "Recent changes"}
	if clErr != nil {
		c.Status, c.Detail = Unknown, "cluster not reachable"
		return c
	}
	changes, err := cl.Changes(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		c.Status, c.Detail = Unknown, err.Error()
		return c
	}
	var helm []string
	rollouts := 0
	for _, ch := range changes {
		switch ch.Source {
		case "helm":
			helm = append(helm, fmt.Sprintf("%s %s (%s ago)", ch.Object, ch.What, time.Since(ch.At).Round(time.Minute)))
		case "rollout":
			rollouts++
		}
	}
	if len(helm) > 0 {
		c.Status, c.Detail = Warn, "Helm ran in the last 24 h: "+list(helm, 2)
		c.Hint = "Let the last change settle (and confirm it worked) before stacking another on it."
		return c
	}
	c.Status, c.Detail = Pass, fmt.Sprintf("no Helm changes in 24 h; %d rollouts", rollouts)
	return c
}

// sandboxCheck looks for go-sandboxes the orchestrator gave up on (they
// failed to schedule when created) that still hold CPU and memory.
func sandboxCheck(ctx context.Context, cl *kube.Cluster, clErr error) Check {
	c := Check{ID: "sandboxes", Name: "Orphaned go-sandboxes"}
	if clErr != nil {
		c.Status, c.Detail = Unknown, "cluster not reachable"
		return c
	}
	rep, err := cl.Sandboxes(ctx)
	switch {
	case err != nil:
		c.Status, c.Detail = Unknown, err.Error()
	case rep.Total == 0:
		c.Status, c.Detail = Pass, "no go-sandbox pods running"
	case !rep.LogsRead:
		c.Status, c.Detail = Unknown, fmt.Sprintf("%d go-sandboxes; orchestrator log unreadable (%s)", rep.Total, list(rep.LogErrors, 2))
	case rep.Orphaned == 0:
		c.Status, c.Detail = Pass, fmt.Sprintf("%d go-sandboxes, none orphaned", rep.Total)
	default:
		var names []string
		ns := ""
		for _, s := range rep.Sandboxes {
			if s.Orphaned {
				names = append(names, fmt.Sprintf("%s (%.0fh old)", s.Name, s.AgeHours))
				ns = s.Namespace
			}
		}
		c.Status = Warn
		c.Detail = fmt.Sprintf("%d of %d go-sandboxes are orphaned, holding %.1f cores and %.1f GiB requested: %s",
			rep.Orphaned, rep.Total, rep.OrphanCPU, rep.OrphanMem/(1<<30), list(names, 4))
		c.Hint = "The orchestrator failed these runs because the pod was Unschedulable (orchestrator 4.2.12.1 regression), " +
			"then left the pod behind; no run uses it. Delete them to free capacity: kubectl -n " + ns + " delete pod <name>."
	}
	return c
}

func names(cp ControlPlane) []string {
	var out []string
	for _, n := range []string{cp.Customer, cp.Tenant, cp.ClusterName, cp.ID} {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

var rank = map[Status]int{Pass: 0, Unknown: 1, Warn: 2, Fail: 3}

func worse(a, b Status) Status {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func list(v []string, n int) string {
	if len(v) > n {
		return strings.Join(v[:n], "; ") + fmt.Sprintf("; +%d more", len(v)-n)
	}
	return strings.Join(v, "; ")
}

func containsFold(v []string, s string) bool {
	for _, x := range v {
		if s != "" && strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
