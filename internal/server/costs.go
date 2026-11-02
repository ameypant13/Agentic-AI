package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func (s *Server) registerCostRoutes(rt *mux.Router) {
	rt.HandleFunc("/costs", s.costSummary).Methods(http.MethodGet)
	rt.HandleFunc("/costs/workloads", s.costWorkloads).Methods(http.MethodGet)
	rt.HandleFunc("/costs/recommendations", s.costRecommendations).Methods(http.MethodGet)
	rt.HandleFunc("/costs/changes", s.costChanges).Methods(http.MethodGet)
	rt.HandleFunc("/costs/review", s.getReviews).Methods(http.MethodGet)
	rt.HandleFunc("/costs/review", s.submitReview).Methods(http.MethodPost)
}

// costRange reads ?days= (default 30, max 400) or ?from=&to= (YYYY-MM-DD).
func costRange(r *http.Request) (from, to string) {
	q := r.URL.Query()
	today := time.Now().UTC()
	from, to = q.Get("from"), q.Get("to")
	if _, err := time.Parse("2006-01-02", to); err != nil {
		to = today.Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", from); err != nil {
		days, _ := strconv.Atoi(q.Get("days"))
		if days <= 0 || days > 400 {
			days = 30
		}
		from = today.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	}
	return from, to
}

type clusterCost struct {
	Cluster        string  `json:"cluster"`
	Instance       string  `json:"instance"`
	Tenant         string  `json:"tenant"`
	NodeCost       float64 `json:"nodeCost"`
	AllocatedCost  float64 `json:"allocatedCost"`
	IdleCost       float64 `json:"idleCost"`
	Days           int     `json:"days"`
	EstimatedNodes int     `json:"estimatedNodes"` // on the latest day
	Nodes          int     `json:"nodes"`          // on the latest day
	NodeTypes      string  `json:"nodeTypes"`      // on the latest day
}

func summarize(days []store.CostClusterDay) []clusterCost {
	by := map[string]*clusterCost{}
	latest := map[string]string{}
	for _, d := range days {
		c := by[d.Cluster]
		if c == nil {
			c = &clusterCost{Cluster: d.Cluster, Instance: d.Instance}
			by[d.Cluster] = c
		}
		c.NodeCost += d.NodeCost
		c.AllocatedCost += d.AllocatedCost
		c.IdleCost += d.IdleCost
		c.Days++
		if d.Day >= latest[d.Cluster] {
			latest[d.Cluster] = d.Day
			c.Tenant, c.EstimatedNodes, c.Nodes, c.NodeTypes = d.Tenant, d.EstimatedNodes, d.Nodes, d.NodeTypes
		}
	}
	out := make([]clusterCost, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeCost > out[j].NodeCost })
	return out
}

// costSummary returns per-cluster totals and the daily series.
func (s *Server) costSummary(w http.ResponseWriter, r *http.Request) {
	if s.costs == nil {
		writeError(w, http.StatusServiceUnavailable, "workload cost is turned off (COST_ENABLED)")
		return
	}
	from, to := costRange(r)
	days, err := s.st.CostDays(from, to, r.URL.Query().Get("cluster"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	errs, lastRun := s.costs.Status()
	var total, idle float64
	for _, d := range days {
		total += d.NodeCost
		idle += d.IdleCost
	}
	if days == nil {
		days = []store.CostClusterDay{}
	}
	var last any // null until the worker's first run, not Go's zero time
	if !lastRun.IsZero() {
		last = lastRun
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "total": total, "idle": idle,
		"clusters": summarize(days), "days": days, "errors": errs, "priceErrors": s.costs.PriceErrors(),
		"lastRun": last, "note": costNote})
}

const costNote = "Estimated from Grafana metrics at on-demand list prices (OpenCost's model). Discounts, " +
	"savings plans, spot, storage, network and managed control-plane fees are not included."

// costWorkloads returns workloads by cost for the range.
func (s *Server) costWorkloads(w http.ResponseWriter, r *http.Request) {
	if s.costs == nil {
		writeError(w, http.StatusServiceUnavailable, "workload cost is turned off (COST_ENABLED)")
		return
	}
	from, to := costRange(r)
	q := r.URL.Query()
	rows, err := s.st.CostWorkloads(from, to, q.Get("cluster"), q.Get("namespace"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []store.CostWorkloadDay{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "workloads": rows})
}

// CostsFunc answers the Copilot's query_costs tool from the stored costs.
func CostsFunc(st *store.Store) func(ctx context.Context, cluster, namespace, groupBy string, days int) (string, error) {
	return func(_ context.Context, cluster, namespace, groupBy string, days int) (string, error) {
		if days <= 0 || days > 400 {
			days = 7
		}
		today := time.Now().UTC()
		from, to := today.AddDate(0, 0, -(days-1)).Format("2006-01-02"), today.Format("2006-01-02")
		out := map[string]any{"from": from, "to": to, "note": costNote}
		cd, err := st.CostDays(from, to, cluster)
		if err != nil {
			return "", err
		}
		if len(cd) == 0 {
			out["result"] = "no cost data for this range yet"
			raw, _ := json.Marshal(out)
			return string(raw), nil
		}
		switch strings.ToLower(groupBy) {
		case "day":
			type day struct {
				Day           string  `json:"day"`
				Cluster       string  `json:"cluster"`
				NodeCost      float64 `json:"nodeCost"`
				IdleCost      float64 `json:"idleCost"`
				Nodes         int     `json:"nodes"`
				PartialDayHrs float64 `json:"hoursCovered"`
			}
			var rows []day
			for _, d := range cd {
				rows = append(rows, day{d.Day, d.Cluster, round2(d.NodeCost), round2(d.IdleCost), d.Nodes, round2(d.Hours)})
			}
			out["days"] = rows
		case "rightsizing":
			recs, err := rightsizing(st, from, to, cluster, namespace)
			if err != nil {
				return "", err
			}
			type row struct {
				Workload, Container, Confidence string
				Current, Recommended            string
				CPUP95, PeakMemory              string
				OOMDays                         int     `json:",omitempty"`
				ThrottledPct                    float64 `json:",omitempty"`
				MonthlySaving                   float64
				Notes                           []string `json:",omitempty"`
			}
			res := func(x cost.Resources) string {
				s := "cpu " + cost.CPUString(x.CPURequest)
				if x.CPULimit > 0 {
					s += " (limit " + cost.CPUString(x.CPULimit) + ")"
				}
				s += ", memory " + cost.MemString(x.MemRequest)
				if x.MemLimit > 0 && x.MemLimit != x.MemRequest {
					s += " (limit " + cost.MemString(x.MemLimit) + ")"
				}
				return s
			}
			var rows []row
			for _, x := range recs {
				rows = append(rows, row{x.Cluster + "/" + x.Namespace + "/" + x.Kind + "/" + x.Name, x.Container, x.Confidence,
					res(x.Current), res(x.Recommended), cost.CPUString(x.CPUP95), cost.MemString(x.MemMax), x.OOMDays,
					round2(x.ThrottledPct), x.MonthlySaving, x.Notes})
			}
			if len(rows) > 40 {
				rows = rows[:40]
			}
			out["policy"] = "KRR-style: CPU request = highest daily p95 usage, no CPU limit; memory request = limit = peak + 15%"
			out["recommendations"] = rows
		case "changes":
			changes, err := costChanges(st, cluster)
			if err != nil {
				return "", err
			}
			out["rule"] = "last 3 complete days against the 14 before; flagged at 25% and $20/day"
			out["changes"] = changes
		case "namespace", "workload":
			wl, err := st.CostWorkloads(from, to, cluster, namespace)
			if err != nil {
				return "", err
			}
			if groupBy == "namespace" {
				type ns struct {
					Cluster, Namespace string
					Cost               float64
				}
				agg := map[[2]string]float64{}
				for _, x := range wl {
					agg[[2]string{x.Cluster, x.Namespace}] += x.CPUCost + x.MemCost
				}
				var rows []ns
				for k, v := range agg {
					rows = append(rows, ns{k[0], k[1], round2(v)})
				}
				sort.Slice(rows, func(i, j int) bool { return rows[i].Cost > rows[j].Cost })
				if len(rows) > 40 {
					rows = rows[:40]
				}
				out["namespaces"] = rows
			} else {
				type row struct {
					Cluster, Namespace, Kind, Name string
					Cost, CPURequestCoreH          float64
					CPUUsageCoreH, MemRequestGiBH  float64
					MemUsageGiBH                   float64
				}
				var rows []row
				for i, x := range wl {
					if i == 40 {
						break
					}
					rows = append(rows, row{x.Cluster, x.Namespace, x.Kind, x.Name, round2(x.CPUCost + x.MemCost),
						round2(x.CPURequest), round2(x.CPUUsage), round2(x.MemRequest), round2(x.MemUsage)})
				}
				out["workloads"] = rows
				out["workloadsTotal"] = len(wl)
			}
		default:
			out["clusters"] = summarize(cd)
		}
		raw, err := json.Marshal(out)
		return string(raw), err
	}
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// rightsizing computes recommendations for a range from stored container-days.
func rightsizing(st *store.Store, from, to, cluster, namespace string) ([]cost.Recommendation, error) {
	aggs, err := st.CostContainerAggs(from, to, cluster, namespace)
	if err != nil {
		return nil, err
	}
	wl, err := st.CostWorkloads(from, to, cluster, namespace)
	if err != nil {
		return nil, err
	}
	return cost.RecommendAggs(aggs, cost.UnitPricesFrom(wl)), nil
}

// recCache keeps computed recommendations until the cost worker stores a new
// day: computing them reads every container-day in the range, which is slow
// while the worker keeps the database and CPU busy.
type recCache struct {
	mu      sync.Mutex
	m       map[string]recEntry
	changes map[string]changeEntry
}

type changeEntry struct {
	version int64
	changes []cost.Change
}

type recEntry struct {
	version int64
	recs    []cost.Recommendation
}

func (s *Server) cachedRightsizing(from, to, cluster, namespace string) ([]cost.Recommendation, error) {
	key := from + "|" + to + "|" + cluster + "|" + namespace
	v := s.costs.Version()
	s.recs.mu.Lock()
	e, ok := s.recs.m[key]
	s.recs.mu.Unlock()
	if ok && e.version == v {
		return e.recs, nil
	}
	recs, err := rightsizing(s.st, from, to, cluster, namespace)
	if err != nil {
		return nil, err
	}
	s.recs.mu.Lock()
	if len(s.recs.m) > 200 {
		s.recs.m = map[string]recEntry{}
	}
	s.recs.m[key] = recEntry{version: v, recs: recs}
	s.recs.mu.Unlock()
	return recs, nil
}

func (s *Server) costRecommendations(w http.ResponseWriter, r *http.Request) {
	if s.costs == nil {
		writeError(w, http.StatusServiceUnavailable, "workload cost is turned off (COST_ENABLED)")
		return
	}
	from, to := costRange(r)
	q := r.URL.Query()
	recs, err := s.cachedRightsizing(from, to, q.Get("cluster"), q.Get("namespace"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if q.Get("summary") != "" {
		writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "clusters": rightsizingByCluster(recs)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "recommendations": recs,
		"policy": "CPU request = highest daily 95th-percentile usage, no CPU limit; memory request = limit = peak working set + 15%."})
}

type rightsizingTotal struct {
	MonthlySaving float64 `json:"monthlySaving"` // positive savings only
	NeedsMore     int     `json:"needsMore"`     // OOM-killed, throttled or under-requested
	Count         int     `json:"count"`
}

// rightsizingByCluster totals recommendations per cluster for the cluster list.
func rightsizingByCluster(recs []cost.Recommendation) map[string]rightsizingTotal {
	out := map[string]rightsizingTotal{}
	for _, r := range recs {
		t := out[r.Cluster]
		t.Count++
		if r.MonthlySaving > 0 {
			t.MonthlySaving += r.MonthlySaving
		}
		if NeedsMore(r) {
			t.NeedsMore++
		}
		out[r.Cluster] = t
	}
	for k, t := range out {
		t.MonthlySaving = round2(t.MonthlySaving)
		out[k] = t
	}
	return out
}

// NeedsMore reports a container that was OOM-killed, CPU-throttled or asks for
// more than it requests.
func NeedsMore(r cost.Recommendation) bool {
	return r.OOMDays > 0 || r.ThrottledPct >= 10 || r.MonthlySaving < -0.5
}

// costChangeWindows: the last 3 complete days against the 14 before them.
const (
	recentWindow   = 3
	baselineWindow = 14
)

// costChanges computes cost changes for every cluster (optionally one).
func costChanges(st *store.Store, cluster string) ([]cost.Change, error) {
	bFrom, bTo, rFrom, rTo := cost.Windows(time.Now(), recentWindow, baselineWindow)
	days, err := st.CostDays(bFrom, rTo, cluster)
	if err != nil {
		return nil, err
	}
	wl, err := st.CostWorkloadRows(bFrom, rTo, cluster)
	if err != nil {
		return nil, err
	}
	out := cost.Changes(days, wl, bFrom, bTo, rFrom, rTo)
	if out == nil {
		out = []cost.Change{}
	}
	return out, nil
}

func (s *Server) costChanges(w http.ResponseWriter, r *http.Request) {
	if s.costs == nil {
		writeError(w, http.StatusServiceUnavailable, "workload cost is turned off (COST_ENABLED)")
		return
	}
	cluster := r.URL.Query().Get("cluster")
	key := "changes|" + cluster
	v := s.costs.Version()
	s.recs.mu.Lock()
	e, ok := s.recs.changes[key]
	s.recs.mu.Unlock()
	if !ok || e.version != v {
		changes, err := costChanges(s.st, cluster)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		e = changeEntry{version: v, changes: changes}
		s.recs.mu.Lock()
		s.recs.changes[key] = e
		s.recs.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": e.changes,
		"rule": fmt.Sprintf("last %d complete days against the %d before; flagged at %.0f%% and $%.0f/day", recentWindow, baselineWindow, cost.MinChangePct, cost.MinChangePerDay)})
}
