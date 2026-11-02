package cost

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

// ContainerDay is one workload container's usage over a window (a day): the
// inputs of right-sizing. Values are per replica, taking the largest replica.
type ContainerDay struct {
	Namespace, Kind, Name, Container string
	CPUP95, CPUMax                   float64 // cores
	MemMax                           float64 // bytes (working set)
	CPURequest, CPULimit             float64 // cores, 0 when unset
	MemRequest, MemLimit             float64 // bytes, 0 when unset
	OOMKilled                        bool
	ThrottledPct                     float64 // share of CFS periods throttled, 0..100
	PodHours                         float64 // replica-hours observed
	Pods                             int
}

// CollectContainers reads per-container usage percentiles, peaks, current
// requests and limits, OOM kills and throttling for [start, end), grouped by
// controlling workload and container. Like Collect, it takes the max per pod
// and container first, so Prometheus replicas are never counted twice.
func CollectContainers(ctx context.Context, q Querier, cluster string, start, end time.Time) ([]ContainerDay, error) {
	window := end.Sub(start).Truncate(step)
	if window < step {
		window = step
	}
	c := fmt.Sprintf("cluster=%s", grafana.Quote(cluster))
	d := fmt.Sprintf("%ds", int(window.Seconds()))
	sub := fmt.Sprintf("[%s:%ds]", d, int(step.Seconds()))
	run := func(expr string) ([]grafana.Sample, error) { return q.QueryAt(ctx, expr, end) }
	sel := c + `,container!="",container!="POD"`
	by := "namespace, pod, container"

	type key struct{ ns, pod, container string }
	type pc struct {
		cpuP95, cpuMax, memMax, cpuReq, cpuLim, memReq, memLim, throttled, samples float64
		oom                                                                        bool
	}
	rows := map[key]*pc{}
	get := func(l map[string]string) *pc {
		k := key{l["namespace"], l["pod"], l["container"]}
		if k.pod == "" || k.container == "" {
			return nil
		}
		p := rows[k]
		if p == nil {
			p = &pc{}
			rows[k] = p
		}
		return p
	}
	rate := `max by (` + by + `) (rate(container_cpu_usage_seconds_total{` + sel + `}[5m]))`
	queries := []struct {
		what, expr string
		required   bool
		set        func(p *pc, v float64, l map[string]string)
	}{
		{"cpu p95", `quantile_over_time(0.95, ` + rate + sub + `)`, true, func(p *pc, v float64, _ map[string]string) { p.cpuP95 = v }},
		{"cpu max", `max_over_time(` + rate + sub + `)`, true, func(p *pc, v float64, _ map[string]string) { p.cpuMax = v }},
		{"memory max", `max by (` + by + `) (max_over_time(container_memory_working_set_bytes{` + sel + `}[` + d + `]))`, true,
			func(p *pc, v float64, _ map[string]string) { p.memMax = v }},
		{"samples", `max by (` + by + `) (count_over_time(container_memory_working_set_bytes{` + sel + `}` + sub + `))`, true,
			func(p *pc, v float64, _ map[string]string) { p.samples = v }},
		{"requests", `max by (` + by + `, resource) (last_over_time(kube_pod_container_resource_requests{` + c + `,resource=~"cpu|memory"}[` + d + `]))`, false,
			func(p *pc, v float64, l map[string]string) {
				if l["resource"] == "cpu" {
					p.cpuReq = v
				} else {
					p.memReq = v
				}
			}},
		{"limits", `max by (` + by + `, resource) (last_over_time(kube_pod_container_resource_limits{` + c + `,resource=~"cpu|memory"}[` + d + `]))`, false,
			func(p *pc, v float64, l map[string]string) {
				if l["resource"] == "cpu" {
					p.cpuLim = v
				} else {
					p.memLim = v
				}
			}},
		{"oom", `max by (` + by + `) (max_over_time(kube_pod_container_status_last_terminated_reason{` + c + `,reason="OOMKilled"}[` + d + `]))`, false,
			func(p *pc, v float64, _ map[string]string) { p.oom = v > 0 }},
		{"throttling", `max by (` + by + `) (increase(container_cpu_cfs_throttled_periods_total{` + sel + `}[` + d + `])) / ` +
			`max by (` + by + `) (increase(container_cpu_cfs_periods_total{` + sel + `}[` + d + `]))`, false,
			func(p *pc, v float64, _ map[string]string) {
				if !math.IsNaN(v) && !math.IsInf(v, 0) {
					p.throttled = v * 100
				}
			}},
	}
	for _, qq := range queries {
		out, err := run(qq.expr)
		if err != nil {
			if qq.required {
				return nil, fmt.Errorf("%s: %w", qq.what, err)
			}
			continue
		}
		for _, s := range out {
			if p := get(s.Labels); p != nil {
				qq.set(p, s.Value, s.Labels)
			}
		}
	}

	owners := collectOwners(run, c, d)
	type wkey struct{ ns, kind, name, container string }
	agg := map[wkey]*ContainerDay{}
	pods := map[wkey]map[string]bool{}
	for k, p := range rows {
		kind, name := owners.Resolve(k.ns, k.pod)
		if kind == "" || kind == "Job" || kind == "Node" {
			continue // bare pods, one-off jobs and static pods have nothing to resize
		}
		if p.samples == 0 && p.cpuP95 == 0 && p.memMax == 0 {
			continue
		}
		wk := wkey{k.ns, kind, name, k.container}
		a := agg[wk]
		if a == nil {
			a = &ContainerDay{Namespace: k.ns, Kind: kind, Name: name, Container: k.container}
			agg[wk] = a
			pods[wk] = map[string]bool{}
		}
		a.CPUP95 = math.Max(a.CPUP95, p.cpuP95)
		a.CPUMax = math.Max(a.CPUMax, p.cpuMax)
		a.MemMax = math.Max(a.MemMax, p.memMax)
		a.CPURequest = math.Max(a.CPURequest, p.cpuReq)
		a.CPULimit = math.Max(a.CPULimit, p.cpuLim)
		a.MemRequest = math.Max(a.MemRequest, p.memReq)
		a.MemLimit = math.Max(a.MemLimit, p.memLim)
		a.ThrottledPct = math.Max(a.ThrottledPct, p.throttled)
		a.OOMKilled = a.OOMKilled || p.oom
		a.PodHours += math.Min(p.samples*step.Hours(), window.Hours())
		pods[wk][k.pod] = true
	}
	out := make([]ContainerDay, 0, len(agg))
	for wk, a := range agg {
		a.Pods = len(pods[wk])
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Namespace+a.Kind+a.Name+a.Container < b.Namespace+b.Kind+b.Name+b.Container
	})
	return out, nil
}
