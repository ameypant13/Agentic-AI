package kube

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeLabels returns every node's labels (used for instance types and regions).
func (c *Cluster) NodeLabels(ctx context.Context) (map[string]map[string]string, error) {
	list, err := c.k8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(list.Items))
	for _, n := range list.Items {
		out[n.Name] = n.Labels
	}
	return out, nil
}

// Ping makes the cheapest authenticated call (one node), to tell whether the
// cluster accepts our credentials without listing everything.
func (c *Cluster) Ping(ctx context.Context) error {
	_, err := c.k8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	return err
}

// InfraOnly wraps a cluster we hold no credentials for; only its Infra
// (name, cloud, region) is usable, e.g. for Grafana lookups.
func InfraOnly(in Infra) *Cluster { return &Cluster{Infra: in} }
