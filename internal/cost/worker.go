package cost

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Config tunes the worker.
type Config struct {
	Interval      time.Duration // how often today is recomputed (1h)
	BackfillDays  int           // past days to fill in (30)
	BackfillPause time.Duration // pause between backfill steps (2m)
	RetentionDays int           // days kept (400)
}

// Worker recomputes each cluster's cost for today every Interval and fills in
// past days one day per cluster per step, pausing between steps, so a fresh
// install does not hit Thanos with a month of queries at once.
type Worker struct {
	cfg    Config
	hub    *grafana.Hub
	st     *store.Store
	prices *Prices
	// Tenants maps Grafana clusters (instance/cluster) to control-hub cluster names; optional.
	Tenants func(ctx context.Context) map[string]string
	// NodeMeta returns node instance types/regions for clusters whose metrics
	// lack them (Azure), keyed by Grafana cluster; optional.
	NodeMeta func(ctx context.Context, instance, cluster string) map[string]NodeMeta
	now      func() time.Time

	mu      sync.Mutex
	status  map[string]string // cluster -> last error
	lastRun time.Time
	// version counts stored days, so readers can cache what they derive.
	version atomic.Int64
}

func NewWorker(cfg Config, hub *grafana.Hub, st *store.Store, prices *Prices) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.BackfillDays <= 0 {
		cfg.BackfillDays = 30
	}
	if cfg.BackfillPause <= 0 {
		cfg.BackfillPause = 2 * time.Minute
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 400
	}
	return &Worker{cfg: cfg, hub: hub, st: st, prices: prices, now: time.Now, status: map[string]string{}}
}

type target struct{ instance, cluster string }

func (w *Worker) targets() []target {
	var out []target
	for _, inst := range w.hub.Instances() {
		for _, c := range inst.Clusters {
			out = append(out, target{inst.Name, c})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].instance+out[i].cluster < out[j].instance+out[j].cluster })
	return out
}

// Start runs today's recompute every Interval and the backfill loop.
func (w *Worker) Start(ctx context.Context) {
	go func() {
		// Give the Grafana hub a moment to load cluster names.
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		t := time.NewTicker(w.cfg.Interval)
		defer t.Stop()
		for {
			w.RunToday(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Minute):
		}
		for {
			worked := w.BackfillStep(ctx)
			pause := w.cfg.BackfillPause
			if !worked {
				pause = w.cfg.Interval // nothing missing; look again later
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(pause):
			}
		}
	}()
}

// RunToday recomputes today (UTC, midnight to now) for every cluster.
func (w *Worker) RunToday(ctx context.Context) {
	now := w.now().UTC()
	start := now.Truncate(24 * time.Hour)
	tenants := w.tenants(ctx)
	for _, tg := range w.targets() {
		w.computeDay(ctx, tg, start, now, tenants)
	}
	w.mu.Lock()
	w.lastRun = now
	w.mu.Unlock()
	if err := w.st.PruneCosts(now.AddDate(0, 0, -w.cfg.RetentionDays).Format("2006-01-02")); err != nil {
		log.Printf("cost: prune: %v", err)
	}
}

// BackfillStep computes, for each cluster, its most recent past day that is
// missing or was stored before the day ended. It reports whether it did any work.
func (w *Worker) BackfillStep(ctx context.Context) bool {
	today := w.now().UTC().Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -w.cfg.BackfillDays).Format("2006-01-02")
	tenants := map[string]string(nil)
	worked := false
	for _, tg := range w.targets() {
		days, err := w.st.CostDays(from, today.AddDate(0, 0, -1).Format("2006-01-02"), tg.cluster)
		if err != nil {
			continue
		}
		// A day is done once its costs cover the whole day and right-sizing
		// data was collected (days stored before that existed are redone once).
		complete := map[string]bool{}
		for _, d := range days {
			if d.Hours >= 23.9 && d.Rightsizing {
				complete[d.Day] = true
			}
		}
		for i := 1; i <= w.cfg.BackfillDays; i++ {
			day := today.AddDate(0, 0, -i)
			if complete[day.Format("2006-01-02")] {
				continue
			}
			if tenants == nil {
				tenants = w.tenants(ctx)
			}
			w.computeDay(ctx, tg, day, day.Add(24*time.Hour), tenants)
			worked = true
			break
		}
	}
	return worked
}

func (w *Worker) tenants(ctx context.Context) map[string]string {
	if w.Tenants == nil {
		return map[string]string{}
	}
	return w.Tenants(ctx)
}

func (w *Worker) computeDay(ctx context.Context, tg target, start, end time.Time, tenants map[string]string) {
	c := w.hub.Client(tg.instance)
	if c == nil {
		return
	}
	var meta map[string]NodeMeta
	if w.NodeMeta != nil {
		meta = w.NodeMeta(ctx, tg.instance, tg.cluster)
	}
	qctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	nodes, pods, err := Collect(qctx, c, tg.cluster, start, end, meta)
	w.mu.Lock()
	if err != nil {
		w.status[tg.cluster] = err.Error()
	} else {
		delete(w.status, tg.cluster)
	}
	w.mu.Unlock()
	if err != nil {
		log.Printf("cost: %s/%s %s: %v", tg.instance, tg.cluster, start.Format("2006-01-02"), err)
		return
	}
	if len(nodes) == 0 {
		return // no metrics for that day (cluster did not exist yet)
	}
	cloud := cloudOf(tg.instance)
	for i := range nodes {
		nodes[i].Hourly = w.prices.Hourly(ctx, cloud, nodes[i].Region, nodes[i].InstanceType)
	}
	// Right-sizing inputs; a failure here leaves the day's costs intact.
	cts, err := CollectContainers(qctx, c, tg.cluster, start, end)
	if err != nil {
		log.Printf("cost: containers %s/%s %s: %v", tg.instance, tg.cluster, start.Format("2006-01-02"), err)
	}
	res := Allocate(nodes, pods)
	types, _ := json.Marshal(res.NodeTypes)
	day := start.Format("2006-01-02")
	row := &store.CostClusterDay{Day: day, Cluster: tg.cluster, Instance: tg.instance, Tenant: tenants[tg.instance+"/"+tg.cluster],
		NodeCost: res.NodeCost, AllocatedCost: res.AllocatedCost, IdleCost: res.IdleCost, NodeHours: res.NodeHours,
		Nodes: res.Nodes, EstimatedNodes: res.EstimatedNodes, NodeTypes: string(types),
		Hours: end.Sub(start).Hours(), ComputedAt: w.now().UTC(), Rightsizing: true}
	wl := make([]store.CostWorkloadDay, 0, len(res.Workloads))
	for _, x := range res.Workloads {
		wl = append(wl, store.CostWorkloadDay{Day: day, Cluster: tg.cluster, Namespace: x.Namespace, Kind: x.Kind, Name: x.Name,
			CPUCost: x.CPUCost, MemCost: x.MemCost, CPURequest: x.CPURequest, CPUUsage: x.CPUUsage,
			MemRequest: x.MemRequest, MemUsage: x.MemUsage, Pods: x.Pods})
	}
	ct := make([]store.CostContainerDay, 0, len(cts))
	for _, x := range cts {
		ct = append(ct, store.CostContainerDay{Day: day, Cluster: tg.cluster, Namespace: x.Namespace, Kind: x.Kind, Name: x.Name,
			Container: x.Container, CPUP95: x.CPUP95, CPUMax: x.CPUMax, MemMax: x.MemMax, CPURequest: x.CPURequest, CPULimit: x.CPULimit,
			MemRequest: x.MemRequest, MemLimit: x.MemLimit, OOMKilled: x.OOMKilled, ThrottledPct: x.ThrottledPct, PodHours: x.PodHours, Pods: x.Pods})
	}
	if err := w.st.ReplaceCostDay(row, wl, ct); err != nil {
		log.Printf("cost: store %s %s: %v", tg.cluster, day, err)
		return
	}
	w.version.Add(1)
}

// cloudOf maps a Grafana instance to its cloud. Instances are named after
// their cloud (GRAFANA_AWS_*, GRAFANA_AZURE_*).
func cloudOf(instance string) string {
	switch instance {
	case "aws", "azure", "gcp":
		return instance
	}
	return ""
}

// Status returns per-cluster errors from the last computation and when today last ran.
func (w *Worker) Status() (map[string]string, time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]string, len(w.status))
	for k, v := range w.status {
		out[k] = v
	}
	return out, w.lastRun
}

// Version changes whenever a day is stored.
func (w *Worker) Version() int64 { return w.version.Load() }

// PriceErrors lists instance types whose list price could not be found.
func (w *Worker) PriceErrors() map[string]string { return w.prices.Errors() }
