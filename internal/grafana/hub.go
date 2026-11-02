package grafana

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hub holds every configured Grafana instance and maps control-hub cluster
// names to each instance's cluster labels: `cluster` on metrics (Thanos) and
// `k8s_cluster_name` on logs (Loki). Names usually match; known variations
// (an "-aks" suffix) are tried, and admins can override a mapping.
type Hub struct {
	mu        sync.RWMutex
	clients   map[string]*Client // by instance name
	sources   map[string]string  // "secret" or "settings"
	index     map[string]*clusterIndex
	overrides map[string]Mapping // control-hub cluster name -> mapping
}

type clusterIndex struct {
	prom      []string
	loki      []string
	checkedAt time.Time
	err       string
}

// Mapping says where a control-hub cluster's metrics and logs live.
type Mapping struct {
	Instance    string `json:"instance"`
	PromCluster string `json:"promCluster"`
	LokiCluster string `json:"lokiCluster"`
	Override    bool   `json:"override"`
}

func NewHub() *Hub {
	return &Hub{clients: map[string]*Client{}, sources: map[string]string{}, index: map[string]*clusterIndex{}, overrides: map[string]Mapping{}}
}

// FromEnv adds instances defined as GRAFANA_<NAME>_URL + GRAFANA_<NAME>_TOKEN.
func (h *Hub) FromEnv() {
	for _, kv := range os.Environ() {
		key, url, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(key, "GRAFANA_") || !strings.HasSuffix(key, "_URL") || url == "" {
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(key, "GRAFANA_"), "_URL"))
		token := os.Getenv("GRAFANA_" + strings.ToUpper(name) + "_TOKEN")
		if token == "" {
			log.Printf("grafana %s: %s set but no token; skipped", name, key)
			continue
		}
		h.Add(Instance{Name: name, URL: url, Token: token}, "secret")
	}
}

// MappingsFromEnv applies GRAFANA_MAPPINGS overrides, for clusters whose
// control-hub name differs from their Grafana labels. Format, comma-separated:
// <control-hub cluster>=<instance>:<metrics cluster>[:<loki cluster>]
// e.g. wbcbre-live-9xtxg-aks=azure:prophecy-westpac-prod:prophecy-westpac-prod-aks
// Mappings saved in Settings take precedence when loaded afterwards.
func (h *Hub) MappingsFromEnv() int {
	return h.ParseMappings(os.Getenv("GRAFANA_MAPPINGS"))
}

// ParseMappings applies mappings in the GRAFANA_MAPPINGS format; it returns how many were valid.
func (h *Hub) ParseMappings(spec string) int {
	n := 0
	for _, item := range strings.Split(spec, ",") {
		cluster, target, ok := strings.Cut(strings.TrimSpace(item), "=")
		parts := strings.Split(target, ":")
		if !ok || cluster == "" || len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			if strings.TrimSpace(item) != "" {
				log.Printf("grafana: ignoring GRAFANA_MAPPINGS entry %q", item)
			}
			continue
		}
		m := &Mapping{Instance: parts[0], PromCluster: parts[1]}
		if len(parts) > 2 {
			m.LokiCluster = parts[2]
		}
		h.SetOverride(strings.TrimSpace(cluster), m)
		n++
	}
	return n
}

func (h *Hub) Add(inst Instance, source string) {
	h.mu.Lock()
	h.clients[inst.Name] = New(inst)
	h.sources[inst.Name] = source
	delete(h.index, inst.Name)
	h.mu.Unlock()
}

func (h *Hub) Remove(name string) {
	h.mu.Lock()
	delete(h.clients, name)
	delete(h.sources, name)
	delete(h.index, name)
	h.mu.Unlock()
}

func (h *Hub) SetOverride(cluster string, m *Mapping) {
	h.mu.Lock()
	if m == nil {
		delete(h.overrides, cluster)
	} else {
		m.Override = true
		h.overrides[cluster] = *m
	}
	h.mu.Unlock()
}

func (h *Hub) Client(name string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients[name]
}

// Clients returns the instances sorted by name.
func (h *Hub) Clients() []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Refresh reloads every instance's cluster names. A failed load keeps the
// last good list (Grafana's own errors are often brief), and reports false so
// the caller retries sooner.
func (h *Hub) Refresh(ctx context.Context) (ok bool) {
	ok = true
	for _, c := range h.Clients() {
		idx := &clusterIndex{checkedAt: time.Now()}
		prom, err := c.LabelValues(ctx, "cluster")
		h.mu.RLock()
		prev := h.index[c.Name()]
		h.mu.RUnlock()
		if err != nil {
			ok = false
			idx.err = err.Error()
			if prev != nil {
				idx.prom, idx.loki = prev.prom, prev.loki
			}
		} else {
			idx.prom = prom
			if loki, err := c.LokiLabelValues(ctx, "k8s_cluster_name"); err == nil {
				idx.loki = loki
			} else if prev != nil {
				idx.loki = prev.loki
			}
		}
		h.mu.Lock()
		h.index[c.Name()] = idx
		h.mu.Unlock()
	}
	return ok
}

// retryAfterError is how soon a failed refresh is retried.
const retryAfterError = time.Minute

// Start refreshes cluster names now and every interval, sooner after a failure.
func (h *Hub) Start(ctx context.Context, every time.Duration) {
	go func() {
		for {
			rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			ok := h.Refresh(rctx)
			cancel()
			wait := every
			if !ok && retryAfterError < every {
				wait = retryAfterError
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

// Resolve finds where a control-hub cluster's metrics and logs live.
func (h *Hub) Resolve(cluster string) *Mapping {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if m, ok := h.overrides[cluster]; ok {
		return &m
	}
	names := make([]string, 0, len(h.index))
	for n := range h.index {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		idx := h.index[n]
		prom := match(cluster, idx.prom)
		if prom == "" {
			continue
		}
		return &Mapping{Instance: n, PromCluster: prom, LokiCluster: match(cluster, idx.loki)}
	}
	return nil
}

// match finds a label value for a control-hub cluster name.
func match(cluster string, values []string) string {
	want := strings.ToLower(strings.TrimSpace(cluster))
	candidates := []string{want, strings.TrimSuffix(want, "-aks"), want + "-aks"}
	for _, c := range candidates {
		for _, v := range values {
			if strings.ToLower(v) == c {
				return v
			}
		}
	}
	return ""
}

// ReverseIndex maps (instance, prom cluster) back to control-hub cluster names.
func (h *Hub) ReverseIndex(clusters []string) map[string]string {
	out := map[string]string{}
	for _, c := range clusters {
		if m := h.Resolve(c); m != nil {
			out[m.Instance+"/"+m.PromCluster] = c
		}
	}
	return out
}

type InstanceView struct {
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Source      string   `json:"source"` // "secret" or "settings"
	Clusters    []string `json:"clusters"`
	LogClusters int      `json:"logClusters"`
	CheckedAt   string   `json:"checkedAt"`
	Error       string   `json:"error"`
}

func (h *Hub) Instances() []InstanceView {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []InstanceView
	for name, c := range h.clients {
		v := InstanceView{Name: name, URL: c.URL(), Source: h.sources[name], Clusters: []string{}}
		if idx := h.index[name]; idx != nil {
			v.Clusters, v.LogClusters, v.Error = append(v.Clusters, idx.prom...), len(idx.loki), idx.err
			v.CheckedAt = idx.checkedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Quote renders a string as a PromQL/LogQL label value.
func Quote(v string) string {
	return fmt.Sprintf("%q", v)
}
