package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/metricrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// LoadGrafanaSettings adds Settings-managed instances and mapping overrides to the hub.
func LoadGrafanaSettings(st *store.Store, box interface{ Open(string) (string, error) }, hub *grafana.Hub) error {
	insts, err := st.ListGrafanaInstances()
	if err != nil {
		return err
	}
	for _, g := range insts {
		tok, err := box.Open(g.TokenSealed)
		if err != nil {
			continue
		}
		hub.Add(grafana.Instance{Name: g.Name, URL: g.URL, Token: tok}, "settings")
	}
	maps, err := st.ListGrafanaMappings()
	if err != nil {
		return err
	}
	for _, m := range maps {
		hub.SetOverride(m.Cluster, &grafana.Mapping{Instance: m.Instance, PromCluster: m.PromCluster, LokiCluster: m.LokiCluster})
	}
	return nil
}

func (s *Server) registerGrafanaRoutes(rt *mux.Router) {
	rt.HandleFunc("/grafana", s.grafanaStatus).Methods(http.MethodGet)
	rt.HandleFunc("/grafana/instances", s.addGrafanaInstance).Methods(http.MethodPost)
	rt.HandleFunc("/grafana/instances/{name}", s.deleteGrafanaInstance).Methods(http.MethodDelete)
	rt.HandleFunc("/grafana/refresh", s.refreshGrafana).Methods(http.MethodPost)
	rt.HandleFunc("/grafana/mappings/{cluster}", s.putGrafanaMapping).Methods(http.MethodPut)
	rt.HandleFunc("/grafana/mappings/{cluster}", s.deleteGrafanaMapping).Methods(http.MethodDelete)
	rt.HandleFunc("/metric-rules", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, s.metrics.Rules()) }).Methods(http.MethodGet)
	rt.HandleFunc("/issues", s.fleetIssues).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/issues", s.metricsRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return s.clusterIssues(cl.Infra), nil
	})).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/metrics", s.metricsRead(s.clusterMetrics)).Methods(http.MethodGet)
}

func (s *Server) grafanaStatus(w http.ResponseWriter, _ *http.Request) {
	insts := s.hub.Instances()
	type instOut struct {
		grafana.InstanceView
		EvaluatedAt *time.Time `json:"evaluatedAt"`
		RuleErrors  []string   `json:"ruleErrors"`
	}
	errs := s.metrics.RuleErrors()
	out := make([]instOut, 0, len(insts))
	for _, iv := range insts {
		o := instOut{InstanceView: iv, RuleErrors: []string{}}
		if t := s.metrics.EvaluatedAt(iv.Name); !t.IsZero() {
			o.EvaluatedAt = &t
		}
		for k, v := range errs {
			if inst, rule, _ := strings.Cut(k, "|"); inst == iv.Name {
				o.RuleErrors = append(o.RuleErrors, rule+": "+v)
			}
		}
		out = append(out, o)
	}
	maps, _ := s.st.ListGrafanaMappings()
	if maps == nil {
		maps = []store.GrafanaMapping{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": out, "overrides": maps})
}

func (s *Server) addGrafanaInstance(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in struct{ Name, URL, Token string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if in.Name == "" || !strings.HasPrefix(in.URL, "https://") || in.Token == "" {
		writeError(w, http.StatusBadRequest, "name, an https:// URL and a service-account token are required")
		return
	}
	for _, iv := range s.hub.Instances() {
		if iv.Name == in.Name && iv.Source == "secret" {
			writeError(w, http.StatusConflict, "an instance with this name comes from the deployment secret")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, _, err := grafana.New(grafana.Instance{Name: in.Name, URL: in.URL, Token: in.Token}).Datasources(ctx); err != nil {
		writeError(w, http.StatusBadRequest, "Grafana rejected the token or has no Prometheus datasource: "+err.Error())
		return
	}
	sealed, err := s.box.Seal(in.Token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store the token")
		return
	}
	if err := s.st.SaveGrafanaInstance(&store.GrafanaInstance{Name: in.Name, URL: in.URL, TokenSealed: sealed,
		Last4: in.Token[len(in.Token)-4:], UpdatedBy: session(r).Email, UpdatedAt: time.Now()}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Add(grafana.Instance{Name: in.Name, URL: in.URL, Token: in.Token}, "settings")
	s.hub.Refresh(ctx)
	s.st.Audit(session(r).Email, "add_grafana_instance", in.Name, "ok", in.URL)
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) deleteGrafanaInstance(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	name := mux.Vars(r)["name"]
	for _, iv := range s.hub.Instances() {
		if iv.Name == name && iv.Source == "secret" {
			writeError(w, http.StatusConflict, "this instance comes from the deployment secret; remove it there")
			return
		}
	}
	if err := s.st.DeleteGrafanaInstance(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Remove(name)
	s.st.Audit(session(r).Email, "remove_grafana_instance", name, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) refreshGrafana(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	s.hub.Refresh(ctx)
	s.grafanaStatus(w, r)
}

func (s *Server) putGrafanaMapping(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in struct{ Instance, PromCluster, LokiCluster string }
	if err := readJSON(r, &in); err != nil || in.Instance == "" || in.PromCluster == "" {
		writeError(w, http.StatusBadRequest, "instance and promCluster are required")
		return
	}
	if s.hub.Client(in.Instance) == nil {
		writeError(w, http.StatusBadRequest, "unknown Grafana instance")
		return
	}
	cluster := mux.Vars(r)["cluster"]
	m := &store.GrafanaMapping{Cluster: cluster, Instance: in.Instance, PromCluster: in.PromCluster, LokiCluster: in.LokiCluster,
		UpdatedBy: session(r).Email, UpdatedAt: time.Now()}
	if err := s.st.SaveGrafanaMapping(m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.SetOverride(cluster, &grafana.Mapping{Instance: in.Instance, PromCluster: in.PromCluster, LokiCluster: in.LokiCluster})
	s.st.Audit(session(r).Email, "map_grafana_cluster", cluster, "ok", in.Instance+"/"+in.PromCluster)
	writeJSON(w, http.StatusOK, s.hub.Resolve(cluster))
}

func (s *Server) deleteGrafanaMapping(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	cluster := mux.Vars(r)["cluster"]
	if err := s.st.DeleteGrafanaMapping(cluster); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.SetOverride(cluster, nil)
	writeJSON(w, http.StatusOK, s.hub.Resolve(cluster))
}

// --- issues ---

type clusterIssuesView struct {
	Grafana     *grafana.Mapping    `json:"grafana"`
	EvaluatedAt *time.Time          `json:"evaluatedAt"`
	Metrics     []metricrules.Issue `json:"metrics"`
}

func (s *Server) clusterIssues(in kube.Infra) clusterIssuesView {
	v := clusterIssuesView{Metrics: []metricrules.Issue{}}
	m := s.hub.Resolve(in.ClusterName)
	if m == nil {
		return v
	}
	v.Grafana = m
	if t := s.metrics.EvaluatedAt(m.Instance); !t.IsZero() {
		v.EvaluatedAt = &t
	}
	v.Metrics = s.metrics.Issues(m.Instance, m.PromCluster)
	return v
}

type fleetIssue struct {
	Source      string    `json:"source"` // "metrics" or "logs"
	ClusterID   string    `json:"clusterId"`
	ClusterName string    `json:"clusterName"`
	RuleID      string    `json:"ruleId"`
	Title       string    `json:"title"`
	Severity    string    `json:"severity"`
	Entity      string    `json:"entity"`
	EntityName  string    `json:"entityName"`
	Namespace   string    `json:"namespace"`
	Pod         string    `json:"pod"`
	ValueText   string    `json:"valueText"`
	Meaning     string    `json:"meaning"`
	Fix         string    `json:"fix"`
	FirstSeen   time.Time `json:"firstSeen"`
	LastSeen    time.Time `json:"lastSeen"`
	Active      bool      `json:"active"`
}

// fleetIssues lists active metric and log issues on every cluster the caller
// can see in control-hub.
func (s *Server) fleetIssues(w http.ResponseWriter, r *http.Request) {
	user, err := s.user(session(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session is invalid; sign in again")
		return
	}
	raw, err := s.ch.Get(r.Context(), user.Token, "/api/infrastructures", url.Values{"limit": {"1000"}})
	if err != nil {
		writeCHError(w, err)
		return
	}
	var resp struct {
		Infrastructures []kube.Infra `json:"infrastructures"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		writeError(w, http.StatusBadGateway, "unexpected infrastructure list from control-hub")
		return
	}
	out := []fleetIssue{}
	for _, in := range resp.Infrastructures {
		s.worker.Track(in)
		for _, is := range s.clusterIssues(in).Metrics {
			if !is.Active {
				continue
			}
			out = append(out, fleetIssue{Source: "metrics", ClusterID: in.ID, ClusterName: in.ClusterName, RuleID: is.RuleID,
				Title: is.Title, Severity: is.Severity, Entity: is.Entity, EntityName: is.EntityName, Namespace: is.Namespace,
				Pod: is.Pod, ValueText: is.ValueText, Meaning: is.Meaning, Fix: is.Fix, FirstSeen: is.FirstSeen, LastSeen: is.LastSeen, Active: true})
		}
		for _, ns := range s.worker.Namespaces(in.ID) {
			for _, p := range s.worker.Summary(in.ID, ns).Pods {
				for _, h := range p.Hits {
					if !h.Active || h.Count == 0 {
						continue
					}
					out = append(out, fleetIssue{Source: "logs", ClusterID: in.ID, ClusterName: in.ClusterName, RuleID: h.RuleID,
						Title: h.Title, Severity: h.Severity, Entity: "pod", EntityName: p.Pod, Namespace: ns, Pod: p.Pod,
						ValueText: fmt.Sprintf("×%d in 1 h", h.Count), Meaning: h.Meaning, Fix: h.Fix, FirstSeen: h.FirstSeen, LastSeen: h.LastSeen, Active: true})
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// --- charts ---

type chartSeries struct {
	Name   string            `json:"name"`
	Unit   string            `json:"unit"` // cores | bytes | count | ratio
	Labels map[string]string `json:"labels"`
	Points [][2]float64      `json:"points"`
}

// clusterMetrics serves fixed chart panels; the UI never sends PromQL.
func (s *Server) clusterMetrics(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
	m := s.hub.Resolve(cl.Infra.ClusterName)
	if m == nil {
		return map[string]any{"available": false, "series": []chartSeries{}}, nil
	}
	c := s.hub.Client(m.Instance)
	q := r.URL.Query()
	rng, err := parseRange(q.Get("range"))
	if err != nil || rng <= 0 || rng > 30*24*time.Hour {
		rng = 6 * time.Hour
	}
	step := rng / 120
	if step < 30*time.Second {
		step = 30 * time.Second
	}
	sel := "cluster=" + grafana.Quote(m.PromCluster)
	type panel struct{ name, unit, expr string }
	var panels []panel
	switch q.Get("panel") {
	case "pod":
		ps := sel + ",namespace=" + grafana.Quote(q.Get("ns")) + ",pod=" + grafana.Quote(q.Get("pod"))
		panels = []panel{
			{"CPU", "cores", `sum by (container) (rate(container_cpu_usage_seconds_total{` + ps + `,container!="",container!="POD"}[5m]))`},
			{"Memory", "bytes", `sum by (container) (container_memory_working_set_bytes{` + ps + `,container!="",container!="POD"})`},
			{"Memory limit", "bytes", `max by (container) (kube_pod_container_resource_limits{` + ps + `,resource="memory"})`},
			{"Restarts", "count", `max by (container) (kube_pod_container_status_restarts_total{` + ps + `})`},
			{"JVM heap used", "bytes", `sum(rate(jvm_memory_used_bytes_sum{` + ps + `,region="heap"}[5m])) / sum(rate(jvm_memory_used_bytes_count{` + ps + `,region="heap"}[5m]))`},
			{"JVM heap max", "bytes", `max(jvm_memory_max_bytes{` + ps + `,region="heap"})`},
		}
	case "node":
		ns := sel + ",node=" + grafana.Quote(q.Get("node"))
		panels = []panel{
			{"CPU", "cores", `sum(rate(container_cpu_usage_seconds_total{` + ns + `,container!="",container!="POD"}[5m]))`},
			{"Memory", "bytes", `sum(container_memory_working_set_bytes{` + ns + `,container!="",container!="POD"})`},
			{"CPU allocatable", "cores", `max(kube_node_status_allocatable{` + ns + `,resource="cpu"})`},
			{"Memory allocatable", "bytes", `max(kube_node_status_allocatable{` + ns + `,resource="memory"})`},
		}
	default:
		panels = []panel{
			{"CPU used", "cores", `sum(rate(container_cpu_usage_seconds_total{` + sel + `,container!="",container!="POD"}[5m]))`},
			{"CPU allocatable", "cores", `sum(kube_node_status_allocatable{` + sel + `,resource="cpu"})`},
			{"Memory used", "bytes", `sum(container_memory_working_set_bytes{` + sel + `,container!="",container!="POD"})`},
			{"Memory requested", "bytes", `sum(kube_pod_container_resource_requests{` + sel + `,resource="memory"})`},
			{"Memory allocatable", "bytes", `sum(kube_node_status_allocatable{` + sel + `,resource="memory"})`},
		}
	}
	end := time.Now()
	start := end.Add(-rng)
	out := []chartSeries{}
	for _, p := range panels {
		series, err := c.QueryRange(ctx, p.expr, start, end, step)
		if err != nil {
			return nil, err
		}
		for _, sr := range series {
			name := p.name
			if ct := sr.Labels["container"]; ct != "" {
				name += " · " + ct
			}
			out = append(out, chartSeries{Name: name, Unit: p.unit, Labels: sr.Labels, Points: sr.Points})
		}
	}
	return map[string]any{"available": true, "instance": m.Instance, "cluster": m.PromCluster,
		"from": start, "to": end, "stepSeconds": int(step.Seconds()), "series": out}, nil
}

// parseRange is time.ParseDuration plus a "d" (days) unit, e.g. "7d".
func parseRange(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%d", &days); err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
