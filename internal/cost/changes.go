package cost

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Cost changes: a cluster whose recent daily cost moved well away from its
// baseline, and what moved it. Plain arithmetic over the stored days: the
// recent window's average per day against the baseline window's, split into
// nodes (count and instance types), idle capacity and the workloads whose
// cost changed most.

const (
	MinChangePct    = 25.0 // flag a change of at least this much…
	MinChangePerDay = 20.0 // …and at least this many dollars a day
)

type Driver struct {
	Kind   string  `json:"kind"` // nodes, idle, workload
	What   string  `json:"what"`
	PerDay float64 `json:"perDay"` // change in dollars a day
}

type Change struct {
	Cluster        string   `json:"cluster"`
	Tenant         string   `json:"tenant,omitempty"`
	BaselineFrom   string   `json:"baselineFrom"`
	BaselineTo     string   `json:"baselineTo"`
	RecentFrom     string   `json:"recentFrom"`
	RecentTo       string   `json:"recentTo"`
	BaselinePerDay float64  `json:"baselinePerDay"`
	RecentPerDay   float64  `json:"recentPerDay"`
	DeltaPerDay    float64  `json:"deltaPerDay"`
	Pct            float64  `json:"pct"`
	Drivers        []Driver `json:"drivers"`
	Summary        string   `json:"summary"`
}

// Windows returns the recent and baseline windows ending yesterday (today is
// partial): recent = the last recentDays complete days, baseline = the
// baselineDays before them.
func Windows(today time.Time, recentDays, baselineDays int) (bFrom, bTo, rFrom, rTo string) {
	day := func(n int) string { return today.UTC().AddDate(0, 0, -n).Format("2006-01-02") }
	return day(recentDays + baselineDays), day(recentDays + 1), day(recentDays), day(1)
}

// Changes compares the windows for every cluster in the rows.
func Changes(days []store.CostClusterDay, workloads []store.CostWorkloadDay, bFrom, bTo, rFrom, rTo string) []Change {
	type acc struct {
		cost, idle, nodes float64
		n                 int
		types             map[string]float64
		tenant            string
	}
	in := func(d, from, to string) bool { return d >= from && d <= to }
	base, recent := map[string]*acc{}, map[string]*acc{}
	get := func(m map[string]*acc, c string) *acc {
		if m[c] == nil {
			m[c] = &acc{types: map[string]float64{}}
		}
		return m[c]
	}
	for _, d := range days {
		if d.Hours < 23.9 {
			continue // partial days would skew averages
		}
		var a *acc
		switch {
		case in(d.Day, bFrom, bTo):
			a = get(base, d.Cluster)
		case in(d.Day, rFrom, rTo):
			a = get(recent, d.Cluster)
		default:
			continue
		}
		a.cost += d.NodeCost
		a.idle += d.IdleCost
		a.nodes += float64(d.Nodes)
		a.n++
		a.tenant = d.Tenant
		var types map[string]int
		if json.Unmarshal([]byte(d.NodeTypes), &types) == nil {
			for t, n := range types {
				a.types[t] += float64(n)
			}
		}
	}
	// Workload cost per day, per window.
	type wkey struct{ cluster, ns, kind, name string }
	wBase, wRecent := map[wkey]float64{}, map[wkey]float64{}
	wPodsBase, wPodsRecent := map[wkey]int{}, map[wkey]int{}
	for _, w := range workloads {
		k := wkey{w.Cluster, w.Namespace, w.Kind, w.Name}
		c := w.CPUCost + w.MemCost
		switch {
		case in(w.Day, bFrom, bTo):
			wBase[k] += c
			wPodsBase[k] = max(wPodsBase[k], w.Pods)
		case in(w.Day, rFrom, rTo):
			wRecent[k] += c
			wPodsRecent[k] = max(wPodsRecent[k], w.Pods)
		}
	}

	var out []Change
	for cluster, r := range recent {
		b := base[cluster]
		if b == nil || b.n < 3 || r.n == 0 {
			continue // not enough history to call anything a change
		}
		bc, rc := b.cost/float64(b.n), r.cost/float64(r.n)
		delta := rc - bc
		pct := 100 * delta / math.Max(bc, 1)
		if math.Abs(pct) < MinChangePct || math.Abs(delta) < MinChangePerDay {
			continue
		}
		ch := Change{Cluster: cluster, Tenant: r.tenant, BaselineFrom: bFrom, BaselineTo: bTo, RecentFrom: rFrom, RecentTo: rTo,
			BaselinePerDay: round2(bc), RecentPerDay: round2(rc), DeltaPerDay: round2(delta), Pct: math.Round(pct)}

		// Nodes: average count and instance-type mix.
		bn, rn := b.nodes/float64(b.n), r.nodes/float64(r.n)
		if math.Abs(rn-bn) >= 0.5 {
			ch.Drivers = append(ch.Drivers, Driver{Kind: "nodes", PerDay: round2(delta),
				What: fmt.Sprintf("nodes %.1f → %.1f on average%s", bn, rn, typeDiff(b.types, b.n, r.types, r.n))})
		} else if d := typeDiff(b.types, b.n, r.types, r.n); d != "" {
			ch.Drivers = append(ch.Drivers, Driver{Kind: "nodes", What: "instance types changed" + d})
		}
		// Idle capacity.
		if di := r.idle/float64(r.n) - b.idle/float64(b.n); math.Abs(di) >= MinChangePerDay/2 {
			ch.Drivers = append(ch.Drivers, Driver{Kind: "idle", PerDay: round2(di),
				What: fmt.Sprintf("idle capacity %s/day → %s/day", money(b.idle/float64(b.n)), money(r.idle/float64(r.n)))})
		}
		// Workloads with the largest change.
		var wd []Driver
		keys := map[wkey]bool{}
		for k := range wBase {
			if k.cluster == cluster {
				keys[k] = true
			}
		}
		for k := range wRecent {
			if k.cluster == cluster {
				keys[k] = true
			}
		}
		for k := range keys {
			bw, rw := wBase[k]/float64(b.n), wRecent[k]/float64(r.n)
			d := rw - bw
			if math.Abs(d) < 2 {
				continue
			}
			what := fmt.Sprintf("%s/%s %s", k.ns, k.kind, k.name)
			switch {
			case bw == 0:
				what += fmt.Sprintf(" is new (%s/day)", money(rw))
			case rw == 0:
				what += " is gone"
			default:
				what += fmt.Sprintf(" %s/day → %s/day", money(bw), money(rw))
				if pb, pr := wPodsBase[k], wPodsRecent[k]; pb != pr && pb > 0 {
					what += fmt.Sprintf(", up to %d → %d pods", pb, pr)
				}
			}
			wd = append(wd, Driver{Kind: "workload", What: what, PerDay: round2(d)})
		}
		sort.Slice(wd, func(i, j int) bool { return math.Abs(wd[i].PerDay) > math.Abs(wd[j].PerDay) })
		if len(wd) > 5 {
			wd = wd[:5]
		}
		ch.Drivers = append(ch.Drivers, wd...)
		ch.Summary = summary(ch)
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return math.Abs(out[i].DeltaPerDay) > math.Abs(out[j].DeltaPerDay) })
	return out
}

func typeDiff(b map[string]float64, bn int, r map[string]float64, rn int) string {
	keys := map[string]bool{}
	for k := range b {
		keys[k] = true
	}
	for k := range r {
		keys[k] = true
	}
	var parts []string
	for k := range keys {
		x, y := b[k]/float64(bn), r[k]/float64(rn)
		if math.Abs(y-x) >= 0.5 {
			parts = append(parts, fmt.Sprintf("%s %.0f → %.0f", k, x, y))
		}
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func summary(c Change) string {
	dir := "up"
	if c.DeltaPerDay < 0 {
		dir = "down"
	}
	s := fmt.Sprintf("%s is %s %.0f%%: %s/day over %s–%s against %s/day before", c.Cluster, dir, math.Abs(c.Pct),
		money(c.RecentPerDay), c.RecentFrom, c.RecentTo, money(c.BaselinePerDay))
	if len(c.Drivers) > 0 {
		s += "; mainly " + c.Drivers[0].What
	}
	return s + "."
}

func money(v float64) string {
	if math.Abs(v) >= 1000 {
		return fmt.Sprintf("$%.1fk", v/1000)
	}
	return fmt.Sprintf("$%.0f", v)
}
