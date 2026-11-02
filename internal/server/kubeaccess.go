package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

// KubeAccess opens clusters for the Copilot's k8s_* tools. Each call lists
// /api/infrastructures with the calling user's token, so the Copilot reaches
// exactly the clusters the cluster explorer would show that user. A cluster is
// matched by infrastructure ID, control-hub name, or its Grafana cluster name.
func KubeAccess(ch *controlhub.Client, reg *kube.Registry, hub *grafana.Hub) tools.KubeAccess {
	type entry struct {
		list []kube.Infra
		at   time.Time
	}
	var mu sync.Mutex
	cache := map[[32]byte]entry{}
	list := func(ctx context.Context, token string) ([]kube.Infra, error) {
		key := sha256.Sum256([]byte(token))
		mu.Lock()
		e, ok := cache[key]
		mu.Unlock()
		if ok && time.Since(e.at) < time.Minute {
			return e.list, nil
		}
		raw, err := ch.Get(ctx, token, "/api/infrastructures", url.Values{"limit": {"1000"}})
		if err != nil {
			return nil, err
		}
		var resp struct {
			Infrastructures []kube.Infra `json:"infrastructures"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, errors.New("unexpected infrastructure list from control-hub")
		}
		mu.Lock()
		for k, v := range cache { // tokens of signed-out users
			if time.Since(v.at) > time.Minute {
				delete(cache, k)
			}
		}
		cache[key] = entry{list: resp.Infrastructures, at: time.Now()}
		mu.Unlock()
		return resp.Infrastructures, nil
	}
	return func(ctx context.Context, token, cluster string) (*kube.Cluster, error) {
		if metricsOnlyID(cluster) {
			return nil, errMetricsOnly(cluster)
		}
		infras, err := list(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("list clusters: %w", err)
		}
		var found *kube.Infra
		for i := range infras {
			in := &infras[i]
			name := strings.TrimSpace(in.ClusterName)
			if in.ID == cluster || strings.EqualFold(name, cluster) {
				found = in
				break
			}
			if m := hub.Resolve(name); found == nil && m != nil && strings.EqualFold(m.PromCluster, cluster) {
				found = in
			}
		}
		if found == nil {
			return nil, fmt.Errorf("no managed cluster %q among the clusters you can access. Imported and SAP clusters "+
				"have no credentials in control-hub; use the Grafana tools for them", cluster)
		}
		c, err := reg.Client(ctx, *found)
		if err != nil {
			return nil, errors.New(clusterErrorMessage(found.Provider, err))
		}
		return c, nil
	}
}

func errMetricsOnly(id string) error {
	return fmt.Errorf("%s is an imported or SAP cluster: control-hub keeps no credentials for it, so only the Grafana "+
		"tools (current_issues, grafana_query_metrics, grafana_query_logs) work", id)
}
