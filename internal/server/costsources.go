package server

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
)

// CostSources gives the cost worker control-hub's cluster names (for tenant
// labels) and, for clusters whose metrics lack instance types, the node labels
// read through the cluster explorer. It reads with the read-only service token.
func CostSources(ch *controlhub.Client, token string, hub *grafana.Hub, reg *kube.Registry, w *cost.Worker) {
	var mu sync.Mutex
	var infras []kube.Infra
	var fetched time.Time
	list := func(ctx context.Context) []kube.Infra {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(fetched) < 10*time.Minute {
			return infras
		}
		raw, err := ch.Get(ctx, token, "/api/infrastructures", url.Values{"limit": {"1000"}})
		if err != nil {
			log.Printf("cost: list infrastructures: %v", err)
			return infras
		}
		var resp struct {
			Infrastructures []kube.Infra `json:"infrastructures"`
		}
		if json.Unmarshal(raw, &resp) == nil {
			infras, fetched = resp.Infrastructures, time.Now()
		}
		return infras
	}
	w.Tenants = func(ctx context.Context) map[string]string {
		names := []string{}
		for _, in := range list(ctx) {
			names = append(names, in.ClusterName)
		}
		return hub.ReverseIndex(names)
	}
	w.NodeMeta = func(ctx context.Context, instance, cluster string) map[string]cost.NodeMeta {
		if instance == "aws" {
			return nil // node-exporter's DMI info has the instance type
		}
		for _, in := range list(ctx) {
			m := hub.Resolve(in.ClusterName)
			if m == nil || m.Instance != instance || m.PromCluster != cluster {
				continue
			}
			cl, err := reg.Client(ctx, in)
			if err != nil {
				return nil
			}
			labels, err := cl.NodeLabels(ctx)
			if err != nil {
				return nil
			}
			out := map[string]cost.NodeMeta{}
			for node, l := range labels {
				out[node] = cost.NodeMeta{InstanceType: l["node.kubernetes.io/instance-type"], Region: l["topology.kubernetes.io/region"]}
			}
			return out
		}
		return nil
	}
}
