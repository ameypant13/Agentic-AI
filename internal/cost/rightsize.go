package cost

import (
	"fmt"
	"math"
	"sort"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Right-sizing follows Robusta KRR's simple strategy:
//   - CPU request = the 95th percentile of usage, and no CPU limit (limits
//     only throttle; the request is what the scheduler reserves).
//   - Memory request = memory limit = peak working set + 15%, because memory
//     cannot be reclaimed like CPU and running out means an OOM kill.
//
// Daily rows hold each day's 95th percentile and peak, so the window's CPU
// figure is the highest daily 95th percentile: slightly above a true 30-day
// percentile, which errs towards keeping headroom.
const (
	MemHeadroom = 0.15
	minCPU      = 0.01      // 10m
	minMem      = 100 << 20 // 100Mi
	oomRaise    = 1.25      // raise the limit by this much after an OOM kill
)

type Resources struct {
	CPURequest float64 `json:"cpuRequest"` // cores
	CPULimit   float64 `json:"cpuLimit"`   // cores, 0 = none
	MemRequest float64 `json:"memRequest"` // bytes
	MemLimit   float64 `json:"memLimit"`   // bytes, 0 = none
}

type Recommendation struct {
	Cluster      string    `json:"cluster"`
	Namespace    string    `json:"namespace"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	Container    string    `json:"container"`
	Days         int       `json:"days"`
	Replicas     float64   `json:"replicas"` // average running replicas
	Current      Resources `json:"current"`
	Recommended  Resources `json:"recommended"`
	CPUP95       float64   `json:"cpuP95"`
	CPUMax       float64   `json:"cpuMax"`
	MemMax       float64   `json:"memMax"`
	OOMDays      int       `json:"oomDays"`
	ThrottledPct float64   `json:"throttledPct"`
	Confidence   string    `json:"confidence"` // high, medium, low
	Notes        []string  `json:"notes"`
	// MonthlySaving is the estimated monthly cost change from applying the
	// recommended requests (negative when it asks for more).
	MonthlySaving float64 `json:"monthlySaving"`
}

// UnitPrices are a workload's cost per requested core-hour and GiB-hour.
type UnitPrices struct{ CPU, Mem float64 }

// UnitPricesFrom derives per-workload prices from summed workload costs.
func UnitPricesFrom(wl []store.CostWorkloadDay) map[[4]string]UnitPrices {
	out := map[[4]string]UnitPrices{}
	for _, w := range wl {
		var u UnitPrices
		if a := math.Max(w.CPURequest, w.CPUUsage); a > 0 {
			u.CPU = w.CPUCost / a
		}
		if a := math.Max(w.MemRequest, w.MemUsage); a > 0 {
			u.Mem = w.MemCost / a
		}
		out[[4]string{w.Cluster, w.Namespace, w.Kind, w.Name}] = u
	}
	return out
}

// Recommend turns container-days into one recommendation per workload
// container.
func Recommend(rows []store.CostContainerDay, prices map[[4]string]UnitPrices) []Recommendation {
	return RecommendAggs(Aggregate(rows), prices)
}

// Aggregate is what Store.CostContainerAggs computes in SQL, for rows in memory.
func Aggregate(rows []store.CostContainerDay) []store.ContainerAgg {
	type key struct{ cluster, ns, kind, name, container string }
	groups := map[key][]store.CostContainerDay{}
	for _, r := range rows {
		k := key{r.Cluster, r.Namespace, r.Kind, r.Name, r.Container}
		groups[k] = append(groups[k], r)
	}
	out := make([]store.ContainerAgg, 0, len(groups))
	for k, days := range groups {
		sort.Slice(days, func(i, j int) bool { return days[i].Day < days[j].Day })
		last := days[len(days)-1]
		a := store.ContainerAgg{Cluster: k.cluster, Namespace: k.ns, Kind: k.kind, Name: k.name, Container: k.container, Days: len(days),
			CPURequest: last.CPURequest, CPULimit: last.CPULimit, MemRequest: last.MemRequest, MemLimit: last.MemLimit}
		for _, d := range days {
			a.CPUP95 = math.Max(a.CPUP95, d.CPUP95)
			a.CPUMax = math.Max(a.CPUMax, d.CPUMax)
			a.MemMax = math.Max(a.MemMax, d.MemMax)
			a.ThrottledPct = math.Max(a.ThrottledPct, d.ThrottledPct)
			if d.OOMKilled {
				a.OOMDays++
			}
			a.PodHours += d.PodHours
		}
		out = append(out, a)
	}
	return out
}

// RecommendAggs turns aggregated container usage into recommendations.
func RecommendAggs(aggs []store.ContainerAgg, prices map[[4]string]UnitPrices) []Recommendation {
	out := make([]Recommendation, 0, len(aggs))
	for _, a := range aggs {
		if a.Days == 0 {
			continue
		}
		r := Recommendation{Cluster: a.Cluster, Namespace: a.Namespace, Kind: a.Kind, Name: a.Name, Container: a.Container, Days: a.Days,
			Current: Resources{CPURequest: a.CPURequest, CPULimit: a.CPULimit, MemRequest: a.MemRequest, MemLimit: a.MemLimit},
			CPUP95:  a.CPUP95, CPUMax: a.CPUMax, MemMax: a.MemMax, ThrottledPct: a.ThrottledPct, OOMDays: a.OOMDays,
			Notes: []string{}}
		podHours := a.PodHours
		r.Replicas = round2(podHours / (24 * float64(a.Days)))

		// CPU
		r.Recommended.CPURequest = ceilTo(math.Max(minCPU, r.CPUP95), 0.01)
		if r.Current.CPULimit > 0 {
			if r.ThrottledPct >= 10 {
				r.Notes = append(r.Notes, fmt.Sprintf("CPU-throttled %.0f%% of periods at its %s limit: removing the limit fixes this.",
					r.ThrottledPct, CPUString(r.Current.CPULimit)))
			} else {
				r.Notes = append(r.Notes, "CPU limit removed (requests reserve CPU; limits only throttle).")
			}
		}

		// Memory
		switch {
		case r.MemMax <= 0:
			r.Recommended.MemRequest, r.Recommended.MemLimit = r.Current.MemRequest, r.Current.MemLimit
			r.Notes = append(r.Notes, "No memory usage recorded; memory left unchanged.")
		default:
			mem := math.Max(minMem, r.MemMax*(1+MemHeadroom))
			if r.OOMDays > 0 && r.Current.MemLimit > 0 {
				mem = math.Max(mem, r.Current.MemLimit*oomRaise)
				r.Notes = append(r.Notes, fmt.Sprintf("OOM-killed on %d of %d days: memory raised, never lowered.", r.OOMDays, a.Days))
			}
			mem = ceilTo(mem, 16<<20)
			r.Recommended.MemRequest, r.Recommended.MemLimit = mem, mem
			if r.Current.MemLimit > 0 && r.MemMax > 0.85*r.Current.MemLimit && r.OOMDays == 0 {
				r.Notes = append(r.Notes, "Peak memory is within 15% of the limit: this is often a JVM heap (-Xmx) filling up; "+
					"change the heap setting together with the limit.")
			}
		}

		// Confidence
		switch {
		case a.Days >= 14 && podHours >= 24*7:
			r.Confidence = "high"
		case a.Days >= 7:
			r.Confidence = "medium"
		default:
			r.Confidence = "low"
			r.Notes = append(r.Notes, fmt.Sprintf("Only %d day(s) of data.", a.Days))
		}

		// Savings: request change × replica-hours × the workload's unit price,
		// scaled from the days observed to a 30-day month.
		u := prices[[4]string{a.Cluster, a.Namespace, a.Kind, a.Name}]
		var saving float64
		if r.Current.CPURequest > 0 {
			saving += (r.Current.CPURequest - r.Recommended.CPURequest) * podHours * u.CPU
		}
		if r.Current.MemRequest > 0 && r.MemMax > 0 {
			saving += (r.Current.MemRequest - r.Recommended.MemRequest) / gib * podHours * u.Mem
		}
		r.MonthlySaving = round2(saving * 30 / float64(a.Days))
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MonthlySaving != out[j].MonthlySaving {
			return out[i].MonthlySaving > out[j].MonthlySaving
		}
		return out[i].Name+out[i].Container < out[j].Name+out[j].Container
	})
	return out
}

func ceilTo(v, unit float64) float64 { return math.Ceil(v/unit) * unit }

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// CPUString renders cores the way Kubernetes manifests do: 250m, 2.
func CPUString(cores float64) string {
	m := int64(math.Round(cores * 1000))
	if m%1000 == 0 {
		return fmt.Sprint(m / 1000)
	}
	return fmt.Sprintf("%dm", m)
}

// MemString renders bytes as Mi or Gi.
func MemString(b float64) string {
	mi := int64(math.Ceil(b / (1 << 20)))
	if mi%1024 == 0 {
		return fmt.Sprintf("%dGi", mi/1024)
	}
	return fmt.Sprintf("%dMi", mi)
}
