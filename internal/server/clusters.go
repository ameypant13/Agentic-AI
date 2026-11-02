package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

const (
	summaryTTL = 60 * time.Second
	accessTTL  = 2 * time.Minute
)

// clusterCache remembers per-cluster summaries and which sessions may read
// which cluster, so the 15-second pod refresh does not hit control-hub each time.
type clusterCache struct {
	mu        sync.Mutex
	summaries map[string]cachedSummary
	access    map[string]cachedInfra // session id + "/" + cluster id
	cards     map[string]*cardEntry  // cluster list cards, by cluster id
	extra     map[string]cachedExtra // per session: imported and SAP clusters
}

type cachedSummary struct {
	summary *kube.Summary
	err     string
	at      time.Time
}

type cachedInfra struct {
	infra kube.Infra
	at    time.Time
}

func newClusterCache() *clusterCache {
	return &clusterCache{summaries: map[string]cachedSummary{}, access: map[string]cachedInfra{}, cards: map[string]*cardEntry{}, extra: map[string]cachedExtra{}}
}

type clusterView struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Provider          string           `json:"provider"`
	Region            string           `json:"region"`
	KubernetesVersion string           `json:"kubernetesVersion"`
	Status            string           `json:"status"`
	Tenants           []string         `json:"tenants"`
	Summary           *kube.Summary    `json:"summary"`
	Error             string           `json:"error"`
	Namespaces        []string         `json:"namespaces,omitempty"`
	Grafana           *grafana.Mapping `json:"grafana"`
	// Kind is infrastructure | imported | sap-control-plane | sap-execution-plane.
	Kind string `json:"kind"`
	// Access is "full" (explorer) or "metrics" (Grafana only; no credentials).
	Access string `json:"access"`
	// Pending means the card is still being computed; ask again shortly.
	Pending bool `json:"pending,omitempty"`
}

func (s *Server) listClusters(w http.ResponseWriter, r *http.Request) {
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
	tenants := s.tenantsByInfra(r.Context(), user.Token)

	out := make([]clusterView, len(resp.Infrastructures))
	var wg sync.WaitGroup
	for i, in := range resp.Infrastructures {
		s.worker.Track(in)
		out[i] = clusterView{ID: in.ID, Name: in.ClusterName, Provider: in.Provider, Region: in.Region,
			KubernetesVersion: in.K8sVersion, Status: in.Status, Tenants: tenants[in.ID], Grafana: s.hub.Resolve(in.ClusterName),
			Kind: KindInfrastructure, Access: "full"}
		if out[i].Tenants == nil {
			out[i].Tenants = []string{}
		}
		wg.Add(1)
		go func(i int, in kube.Infra) {
			defer wg.Done()
			out[i].Summary, out[i].Error, out[i].Pending = s.card(in)
		}(i, in)
	}
	// Imported and SAP clusters from the control-plane lists: Grafana only.
	infraIDs := map[string]bool{}
	for _, in := range resp.Infrastructures {
		infraIDs[in.ID] = true
	}
	extras := s.extraClusters(r.Context(), user.Token, infraIDs)
	s.clusters.mu.Lock()
	s.clusters.extra[session(r).ID+"/extra"] = cachedExtra{list: extras, at: time.Now()}
	s.clusters.mu.Unlock()
	base := len(out)
	out = append(out, make([]clusterView, len(extras))...)
	for j, e := range extras {
		i := base + j
		out[i] = clusterView{ID: e.Infra.ID, Name: e.Infra.ClusterName, Provider: e.Infra.Provider, Region: e.Infra.Region,
			Status: e.Infra.Status, Tenants: e.Tenants, Grafana: s.hub.Resolve(e.Infra.ClusterName), Kind: e.Kind, Access: "metrics"}
		wg.Add(1)
		go func(i int, in kube.Infra) {
			defer wg.Done()
			out[i].Summary, out[i].Error, out[i].Pending = s.card(in)
		}(i, e.Infra)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

// clusterSummary returns a cached summary or computes one, with a time limit
// so one unreachable cluster cannot stall the whole list.
func (s *Server) clusterSummary(ctx context.Context, in kube.Infra) (*kube.Summary, string) {
	s.clusters.mu.Lock()
	c, ok := s.clusters.summaries[in.ID]
	s.clusters.mu.Unlock()
	if ok && time.Since(c.at) < summaryTTL {
		return c.summary, c.err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var sum *kube.Summary
	cl, err := s.kube.Client(ctx, in)
	if err == nil {
		sum, err = cl.Summary(ctx)
	}
	msg := ""
	if err != nil {
		msg = clusterErrorMessage(in.Provider, err)
		s.kube.Forget(in.ID)
		log.Printf("cluster %s (%s): %v", in.ClusterName, in.ID, err)
	}
	s.clusters.mu.Lock()
	s.clusters.summaries[in.ID] = cachedSummary{summary: sum, err: msg, at: time.Now()}
	s.clusters.mu.Unlock()
	return sum, msg
}

// tenantsByInfra maps infrastructure IDs to the customer names of their
// control planes, across realms the user can read.
func (s *Server) tenantsByInfra(ctx context.Context, token string) map[string][]string {
	out := map[string][]string{}
	for _, realm := range []string{"sap", "dedicated-saas", "public-saas"} {
		cps, err := tools.ListControlPlanes(ctx, s.ch, token, realm, "")
		if err != nil {
			continue
		}
		for _, cp := range cps {
			if cp.InfrastructureID != "" && cp.CustomerName != "" {
				out[cp.InfrastructureID] = append(out[cp.InfrastructureID], cp.CustomerName)
			}
		}
	}
	return out
}

// clusterFor checks, through control-hub with the caller's token, that the
// caller may read this infrastructure, and returns a client for it.
func (s *Server) clusterFor(w http.ResponseWriter, r *http.Request) *kube.Cluster {
	sess := session(r)
	id := mux.Vars(r)["id"]
	if metricsOnlyID(id) {
		noAccess(w)
		return nil
	}
	key := sess.ID + "/" + id

	s.clusters.mu.Lock()
	c, ok := s.clusters.access[key]
	s.clusters.mu.Unlock()
	if !ok || time.Since(c.at) > accessTTL {
		user, err := s.user(sess)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "session is invalid; sign in again")
			return nil
		}
		raw, err := s.ch.Get(r.Context(), user.Token, "/api/infrastructures/"+url.PathEscape(id), nil)
		if err != nil {
			writeCHError(w, err)
			return nil
		}
		if err := json.Unmarshal(raw, &c.infra); err != nil || c.infra.ID == "" {
			writeError(w, http.StatusNotFound, "cluster not found")
			return nil
		}
		c.at = time.Now()
		s.clusters.mu.Lock()
		s.clusters.access[key] = c
		s.clusters.mu.Unlock()
	}
	s.worker.Track(c.infra)
	cl, err := s.kube.Client(r.Context(), c.infra)
	if err != nil {
		writeError(w, http.StatusBadGateway, clusterErrorMessage(c.infra.Provider, err))
		return nil
	}
	return cl
}

func (s *Server) getCluster(w http.ResponseWriter, r *http.Request) {
	if metricsOnlyID(mux.Vars(r)["id"]) {
		e, ok := s.metricsOnlyCluster(w, r)
		if !ok {
			return
		}
		in := e.Infra
		v := clusterView{ID: in.ID, Name: in.ClusterName, Provider: in.Provider, Region: in.Region, Status: in.Status,
			Tenants: e.Tenants, Grafana: s.hub.Resolve(in.ClusterName), Kind: e.Kind, Access: "metrics", Namespaces: []string{}}
		v.Summary, v.Error = s.metricsCard(r.Context(), in)
		writeJSON(w, http.StatusOK, v)
		return
	}
	cl := s.clusterFor(w, r)
	if cl == nil {
		return
	}
	in := cl.Infra
	v := clusterView{ID: in.ID, Name: in.ClusterName, Provider: in.Provider, Region: in.Region,
		KubernetesVersion: in.K8sVersion, Status: in.Status, Tenants: []string{}, Grafana: s.hub.Resolve(in.ClusterName),
		Kind: KindInfrastructure, Access: "full"}
	if user, err := s.user(session(r)); err == nil {
		if t := s.tenantsByInfra(r.Context(), user.Token)[in.ID]; t != nil {
			v.Tenants = t
		}
	}
	v.Summary, v.Error = s.clusterSummary(r.Context(), in)
	ns, err := cl.Namespaces(r.Context())
	if err != nil && v.Error == "" {
		v.Error = kubeErrorMessage(err)
	}
	v.Namespaces = ns
	if v.Namespaces == nil {
		v.Namespaces = []string{}
	}
	writeJSON(w, http.StatusOK, v)
}

// clusterRead wraps a read of one cluster: access check, timeout, error mapping.
func (s *Server) clusterRead(read func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cl := s.clusterFor(w, r)
		if cl == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		out, err := read(ctx, cl, r)
		if err != nil {
			status := http.StatusBadGateway
			if apierrors.IsNotFound(err) {
				status = http.StatusNotFound
			}
			writeError(w, status, kubeErrorMessage(err))
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// metricsRead is clusterRead for routes that only need the cluster's name
// (Grafana issues and charts), so they also work for metrics-only clusters.
func (s *Server) metricsRead(read func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error)) http.HandlerFunc {
	full := s.clusterRead(read)
	return func(w http.ResponseWriter, r *http.Request) {
		if !metricsOnlyID(mux.Vars(r)["id"]) {
			full(w, r)
			return
		}
		e, ok := s.metricsOnlyCluster(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		out, err := read(ctx, kube.InfraOnly(e.Infra), r)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) registerClusterRoutes(rt *mux.Router) {
	ns := func(r *http.Request) string { return r.URL.Query().Get("ns") }
	rt.HandleFunc("/clusters", s.listClusters).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}", s.getCluster).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/nodes", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return nonNil(cl.Nodes(ctx))
	})).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/pods", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return nonNil(cl.Pods(ctx, ns(r)))
	})).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/workloads", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return nonNil(cl.Workloads(ctx, ns(r)))
	})).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/events", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return nonNil(cl.Events(ctx, ns(r), nil))
	})).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/services", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return nonNil(cl.Services(ctx, ns(r)))
	})).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/pods/{ns}/{name}", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		v := mux.Vars(r)
		return cl.PodDetail(ctx, v["ns"], v["name"])
	})).Methods(http.MethodGet)
	rt.HandleFunc("/clusters/{id}/pods/{ns}/{name}/logs", s.clusterRead(s.podLogsWithParser)).Methods(http.MethodGet)
}

// nonNil turns a nil slice into an empty one so the API returns [] not null.
func nonNil[T any](v []T, err error) (any, error) {
	if v == nil {
		v = []T{}
	}
	return v, err
}

func clampInt(raw string, def, max int) int {
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func writeCHError(w http.ResponseWriter, err error) {
	var apiErr *controlhub.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			writeError(w, http.StatusUnauthorized, "control-hub rejected your session; sign in again")
			return
		case http.StatusForbidden:
			writeError(w, http.StatusForbidden, "control-hub says you can't read infrastructures")
			return
		case http.StatusNotFound:
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
	}
	log.Printf("control-hub: %v", err)
	writeError(w, http.StatusBadGateway, "could not reach control-hub")
}

// clusterErrorMessage explains why a cluster could not be reached, in one line.
// On dev most failures are clusters deleted in the cloud but still recorded in control-hub.
func clusterErrorMessage(provider string, err error) string {
	msg := err.Error()
	cloud := map[string]string{"aws": "AWS", "gcp": "GCP", "azure": "Azure"}[provider]
	switch {
	case strings.Contains(msg, "ResourceGroupNotFound"), strings.Contains(msg, "ResourceNotFound"),
		strings.Contains(msg, "Error 404"), strings.Contains(msg, "StatusCode: 404"), strings.Contains(msg, "ResourceNotFoundException"):
		return "Cluster not found in " + cloud + ". It was probably deleted outside control-hub."
	case strings.Contains(msg, "no such host"):
		return "The cluster's API endpoint no longer resolves. It was probably deleted outside control-hub."
	case strings.Contains(msg, "AuthorizationFailed"), strings.Contains(msg, "AccessDenied"), strings.Contains(msg, "Error 403"),
		strings.Contains(msg, "Unauthorized"), strings.Contains(msg, "forbidden"):
		return "control-hub's " + cloud + " credentials were refused for this cluster."
	}
	return kubeErrorMessage(err)
}

// kubeErrorMessage shortens cloud and Kubernetes errors for the UI.
func kubeErrorMessage(err error) string {
	msg := err.Error()
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "deadline exceeded") {
		return "timed out talking to the cluster"
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}
