package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
)

// Card summaries never make the cluster list wait. A cached card (however
// old) is returned at once and refreshed in the background when stale; a
// cluster seen for the first time gets at most cardWait before the list is
// returned without it (Pending), and the page asks again. Clusters that failed
// are retried every cardErrorTTL, not on every refresh.
var (
	cardWait     = 3 * time.Second
	cardErrorTTL = 5 * time.Minute
)

type cardEntry struct {
	summary    *kube.Summary
	err        string
	at         time.Time
	refreshing bool
	done       chan struct{} // closed when the running refresh finishes
}

func (s *Server) card(in kube.Infra) (sum *kube.Summary, errMsg string, pending bool) {
	s.clusters.mu.Lock()
	e := s.clusters.cards[in.ID]
	if e == nil {
		e = &cardEntry{}
		s.clusters.cards[in.ID] = e
	}
	ttl := summaryTTL
	if e.summary == nil && e.err != "" {
		ttl = cardErrorTTL
	}
	if !e.refreshing && (e.at.IsZero() || time.Since(e.at) > ttl) {
		e.refreshing, e.done = true, make(chan struct{})
		go s.refreshCard(in, e)
	}
	have, done := !e.at.IsZero(), e.done
	sum, errMsg = e.summary, e.err
	s.clusters.mu.Unlock()
	if have {
		return sum, errMsg, false
	}
	select {
	case <-done:
	case <-time.After(cardWait):
		return nil, "", true
	}
	s.clusters.mu.Lock()
	defer s.clusters.mu.Unlock()
	return e.summary, e.err, false
}

func (s *Server) refreshCard(in kube.Infra, e *cardEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	compute := s.computeCard
	if s.cardCompute != nil {
		compute = s.cardCompute
	}
	sum, msg := compute(ctx, in)
	s.clusters.mu.Lock()
	e.summary, e.err, e.at, e.refreshing = sum, msg, time.Now(), false
	close(e.done)
	s.clusters.mu.Unlock()
}

// computeCard builds a card. For clusters Grafana monitors, the counts come
// from Thanos (a few small queries) and the cluster itself only gets a
// one-node list to confirm our credentials still work; listing every pod in
// every namespace is left to the cluster's own page. Clusters without metrics
// fall back to the live summary.
func (s *Server) computeCard(ctx context.Context, in kube.Infra) (*kube.Summary, string) {
	if metricsOnlyID(in.ID) {
		return s.metricsCard(ctx, in)
	}
	m := s.hub.Resolve(in.ClusterName)
	if m == nil {
		return s.clusterSummary(ctx, in)
	}
	var sum *kube.Summary
	var qerr error
	var msg string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sum, qerr = thanosSummary(ctx, s.hub.Client(m.Instance), m.PromCluster)
	}()
	go func() {
		defer wg.Done()
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cl, err := s.kube.Client(pctx, in)
		if err == nil {
			err = cl.Ping(pctx)
		}
		if err != nil {
			msg = clusterErrorMessage(in.Provider, err)
			s.kube.Forget(in.ID)
			log.Printf("cluster %s (%s): %v", in.ClusterName, in.ID, err)
		}
	}()
	wg.Wait()
	if qerr != nil || sum == nil || sum.Nodes == 0 {
		// No usable metrics (Grafana down, or the cluster stopped reporting).
		if msg != "" {
			return nil, msg
		}
		return s.clusterSummary(ctx, in)
	}
	return sum, msg
}

// thanosSummary builds the card counts from kube-state-metrics in Thanos.
// Every query takes the max per node or pod first, so Prometheus replica
// duplicates are not counted twice. Only Running/Pending pods count, as in
// the live summary.
func thanosSummary(ctx context.Context, c *grafana.Client, cluster string) (*kube.Summary, error) {
	if c == nil {
		return nil, fmt.Errorf("grafana instance not configured")
	}
	sel := "cluster=" + grafana.Quote(cluster)
	live := `(max by (namespace, pod) (kube_pod_status_phase{` + sel + `,phase=~"Running|Pending"}) == 1)`
	queries := map[string]string{
		"nodes":      `count(max by (node) (kube_node_info{` + sel + `}))`,
		"nodesReady": `count(max by (node) (kube_node_status_condition{` + sel + `,condition="Ready",status="true"}) == 1)`,
		"pods":       `count(` + live + `)`,
		"podsNotReady": `count(` + live + ` unless on (namespace, pod) (max by (namespace, pod) (kube_pod_status_ready{` + sel +
			`,condition="true"}) == 1))`,
		"allocCPU": `sum(max by (node) (kube_node_status_allocatable{` + sel + `,resource="cpu"}))`,
		"allocMem": `sum(max by (node) (kube_node_status_allocatable{` + sel + `,resource="memory"}))`,
		"reqCPU": `sum(max by (namespace, pod, container) (kube_pod_container_resource_requests{` + sel +
			`,resource="cpu"}) and on (namespace, pod) ` + live + `)`,
		"reqMem": `sum(max by (namespace, pod, container) (kube_pod_container_resource_requests{` + sel +
			`,resource="memory"}) and on (namespace, pod) ` + live + `)`,
	}
	vals := map[string]float64{}
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	for k, q := range queries {
		wg.Add(1)
		go func(k, q string) {
			defer wg.Done()
			out, err := c.Query(ctx, q)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			if len(out) > 0 {
				vals[k] = out[0].Value // count() of nothing returns no sample: 0
			}
		}(k, q)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	s := &kube.Summary{Nodes: int(vals["nodes"]), NodesReady: int(vals["nodesReady"]), Pods: int(vals["pods"]),
		PodsNotReady: int(vals["podsNotReady"]), Source: "thanos"}
	s.CPURequestedPct = pctf(vals["reqCPU"], vals["allocCPU"])
	s.MemRequestedPct = pctf(vals["reqMem"], vals["allocMem"])
	s.CPUDetail = fmt.Sprintf("%.1f / %.0f cores", vals["reqCPU"], vals["allocCPU"])
	s.MemDetail = fmt.Sprintf("%.0f / %.0f GiB", vals["reqMem"]/(1<<30), vals["allocMem"]/(1<<30))
	return s, nil
}

func pctf(part, whole float64) int {
	if whole <= 0 {
		return 0
	}
	return int(part/whole*100 + 0.5)
}
