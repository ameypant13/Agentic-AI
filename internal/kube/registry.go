package kube

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const clientTTL = 30 * time.Minute

// Registry keeps one client per cluster, rebuilt every clientTTL or after a
// connection error, so cloud credentials are only exchanged occasionally.
type Registry struct {
	creds Credentials

	mu      sync.Mutex
	clients map[string]*entry
}

type entry struct {
	k8s     kubernetes.Interface
	metrics metricsclient.Interface
	built   time.Time
}

func NewRegistry(creds Credentials) *Registry {
	return &Registry{creds: creds, clients: map[string]*entry{}}
}

// Client returns a client for the cluster, building it on first use.
func (r *Registry) Client(ctx context.Context, in Infra) (*Cluster, error) {
	r.mu.Lock()
	e, ok := r.clients[in.ID]
	r.mu.Unlock()
	if ok && time.Since(e.built) < clientTTL {
		return &Cluster{Infra: in, k8s: e.k8s, metrics: e.metrics}, nil
	}

	cfg, err := restConfig(ctx, r.creds, in)
	if err != nil {
		return nil, err
	}
	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	m, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	e = &entry{k8s: k8s, metrics: m, built: time.Now()}
	r.mu.Lock()
	r.clients[in.ID] = e
	r.mu.Unlock()
	return &Cluster{Infra: in, k8s: k8s, metrics: m}, nil
}

// Forget drops a cached client, e.g. after the cluster rejected its credentials.
func (r *Registry) Forget(id string) {
	r.mu.Lock()
	delete(r.clients, id)
	r.mu.Unlock()
}

// Cluster is a read-only handle on one cluster. Its methods only get, list and read logs.
type Cluster struct {
	Infra   Infra
	k8s     kubernetes.Interface
	metrics metricsclient.Interface
}

// InjectForTest makes Client return the given (fake) clients for a cluster ID.
func (r *Registry) InjectForTest(id string, k8s kubernetes.Interface, m metricsclient.Interface) {
	r.mu.Lock()
	r.clients[id] = &entry{k8s: k8s, metrics: m, built: time.Now()}
	r.mu.Unlock()
}

// NewClusterForTest wraps given clients; tests use fake clientsets.
func NewClusterForTest(in Infra, k8s kubernetes.Interface, m metricsclient.Interface) *Cluster {
	return &Cluster{Infra: in, k8s: k8s, metrics: m}
}
