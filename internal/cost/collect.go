package cost

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

// Querier runs instant PromQL queries (grafana.Client).
type Querier interface {
	QueryAt(ctx context.Context, expr string, t time.Time) ([]grafana.Sample, error)
}

// NodeMeta is what the cluster itself says about a node, for clouds whose
// metrics do not carry the instance type (Azure).
type NodeMeta struct{ InstanceType, Region string }

// step is the subquery resolution: one sample per 5 minutes.
const step = 5 * time.Minute

const gib = 1 << 30

// Collect reads one cluster's nodes and pods for [start, end) from Thanos.
// Every query takes the max per node, pod or container before summing, so
// duplicate series from Prometheus replicas are never counted twice.
func Collect(ctx context.Context, q Querier, cluster string, start, end time.Time, meta map[string]NodeMeta) ([]Node, []Pod, error) {
	window := end.Sub(start).Truncate(step)
	if window < step {
		window = step
	}
	c := fmt.Sprintf("cluster=%s", grafana.Quote(cluster))
	d := fmt.Sprintf("%ds", int(window.Seconds()))
	sub := fmt.Sprintf("[%s:%ds]", d, int(step.Seconds()))
	stepHours := step.Hours()
	run := func(expr string) ([]grafana.Sample, error) {
		return q.QueryAt(ctx, expr, end)
	}

	// --- nodes ---
	info, err := run(`max by (node, internal_ip, provider_id) (max_over_time(kube_node_info{` + c + `}[` + d + `]))`)
	if err != nil {
		return nil, nil, fmt.Errorf("node info: %w", err)
	}
	nodes := map[string]*Node{}
	ipToNode := map[string]string{}
	for _, s := range info {
		name := s.Labels["node"]
		n := &Node{Name: name}
		if strings.HasPrefix(s.Labels["provider_id"], "aws://") {
			n.Region = awsRegion(s.Labels["provider_id"])
		} else if strings.HasPrefix(s.Labels["provider_id"], "azure://") {
			n.Region = azureRegion(s.Labels["provider_id"])
		}
		if m, ok := meta[name]; ok {
			if m.InstanceType != "" {
				n.InstanceType = m.InstanceType
			}
			if m.Region != "" {
				n.Region = m.Region
			}
		}
		nodes[name] = n
		if ip := s.Labels["internal_ip"]; ip != "" {
			ipToNode[ip] = name
		}
	}
	perNode := func(expr string, set func(n *Node, v float64)) error {
		out, err := run(expr)
		if err != nil {
			return err
		}
		for _, s := range out {
			if n := nodes[s.Labels["node"]]; n != nil {
				set(n, s.Value)
			}
		}
		return nil
	}
	if err := perNode(`max by (node) (max_over_time(kube_node_status_capacity{`+c+`,resource="cpu"}[`+d+`]))`,
		func(n *Node, v float64) { n.Cores = v }); err != nil {
		return nil, nil, fmt.Errorf("node cpu: %w", err)
	}
	if err := perNode(`max by (node) (max_over_time(kube_node_status_capacity{`+c+`,resource="memory"}[`+d+`]))`,
		func(n *Node, v float64) { n.MemGiB = v / gib }); err != nil {
		return nil, nil, fmt.Errorf("node memory: %w", err)
	}
	if err := perNode(`max by (node) (count_over_time(kube_node_status_capacity{`+c+`,resource="cpu"}`+sub+`))`,
		func(n *Node, v float64) { n.Hours = v * stepHours }); err != nil {
		return nil, nil, fmt.Errorf("node hours: %w", err)
	}
	// AWS puts the instance type in the DMI product name (node-exporter).
	if dmi, err := run(`max by (instance, product_name) (max_over_time(node_dmi_info{` + c + `}[` + d + `]))`); err == nil {
		for _, s := range dmi {
			ip, _, _ := strings.Cut(s.Labels["instance"], ":")
			if n := nodes[ipToNode[ip]]; n != nil && n.InstanceType == "" && looksLikeInstanceType(s.Labels["product_name"]) {
				n.InstanceType = s.Labels["product_name"]
			}
		}
	}

	// --- pods ---
	pods := map[[2]string]*Pod{}
	pod := func(l map[string]string) *Pod {
		k := [2]string{l["namespace"], l["pod"]}
		p := pods[k]
		if p == nil {
			p = &Pod{Namespace: k[0], Name: k[1]}
			pods[k] = p
		}
		return p
	}
	perPod := func(what, expr string, set func(p *Pod, v float64)) error {
		out, err := run(expr)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		for _, s := range out {
			if s.Labels["pod"] != "" {
				set(pod(s.Labels), s.Value)
			}
		}
		return nil
	}
	// Requests count only while the pod is Running or Pending: completed job
	// pods keep their request metrics but use no node.
	live := `and on (namespace, pod) (max by (namespace, pod) (kube_pod_status_phase{` + c + `,phase=~"Running|Pending"}) == 1)`
	reqExpr := func(resource string) string {
		return `sum by (namespace, pod) (max by (namespace, pod, container) (sum_over_time((kube_pod_container_resource_requests{` +
			c + `,resource="` + resource + `"} ` + live + `)` + sub + `)))`
	}
	queries := []struct {
		what, expr string
		set        func(p *Pod, v float64)
	}{
		{"cpu requests", reqExpr("cpu"), func(p *Pod, v float64) { p.CPURequest = v * stepHours }},
		{"memory requests", reqExpr("memory"), func(p *Pod, v float64) { p.MemRequest = v * stepHours / gib }},
		{"cpu usage", `sum by (namespace, pod) (max by (namespace, pod, container, id) (increase(container_cpu_usage_seconds_total{` +
			c + `,container!="",container!="POD"}[` + d + `])))`, func(p *Pod, v float64) { p.CPUUsage = v / 3600 }},
		{"memory usage", `sum by (namespace, pod) (max by (namespace, pod, container) (sum_over_time(container_memory_working_set_bytes{` +
			c + `,container!="",container!="POD"}` + sub + `)))`, func(p *Pod, v float64) { p.MemUsage = v * stepHours / gib }},
	}
	for _, qq := range queries {
		if err := perPod(qq.what, qq.expr, qq.set); err != nil {
			return nil, nil, err
		}
	}
	if out, err := run(`max by (namespace, pod, node) (max_over_time(kube_pod_info{` + c + `,node!=""}[` + d + `]))`); err == nil {
		for _, s := range out {
			if p := pods[[2]string{s.Labels["namespace"], s.Labels["pod"]}]; p != nil {
				p.Node = s.Labels["node"]
			}
		}
	}
	owners := collectOwners(run, c, d)

	nodeList := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		// Sampling includes both ends of the window, so cap at its length.
		if n.Hours > window.Hours() {
			n.Hours = window.Hours()
		}
		if n.Hours > 0 {
			nodeList = append(nodeList, *n)
		}
	}
	podList := make([]Pod, 0, len(pods))
	for _, p := range pods {
		p.OwnerKind, p.OwnerName = owners.Resolve(p.Namespace, p.Name)
		podList = append(podList, *p)
	}
	return nodeList, podList, nil
}

// awsRegion reads the region from "aws:///us-east-1a/i-0123".
func awsRegion(providerID string) string {
	parts := strings.Split(strings.TrimPrefix(providerID, "aws:///"), "/")
	if len(parts) == 0 || len(parts[0]) < 2 {
		return ""
	}
	az := parts[0]
	if last := az[len(az)-1]; last >= 'a' && last <= 'z' {
		return az[:len(az)-1]
	}
	return az
}

// azureRegion reads the region from an AKS node's resource group,
// "MC_<group>_<cluster>_<region>".
func azureRegion(providerID string) string {
	parts := strings.Split(providerID, "/")
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "resourceGroups") {
			rg := parts[i+1]
			if strings.HasPrefix(strings.ToUpper(rg), "MC_") {
				segs := strings.Split(rg, "_")
				return strings.ToLower(segs[len(segs)-1])
			}
		}
	}
	return ""
}

// looksLikeInstanceType rejects DMI product names that are not instance types
// ("Virtual Machine" on Azure, "Google Compute Engine" on GCP).
func looksLikeInstanceType(s string) bool {
	return s != "" && !strings.Contains(s, " ") && strings.Contains(s, ".")
}

// collectOwners reads which workload controls each pod in the window.
func collectOwners(run func(string) ([]grafana.Sample, error), c, d string) Owners {
	owners := Owners{Pod: map[[2]string][2]string{}, ReplicaSet: map[[2]string][2]string{}, Job: map[[2]string][2]string{}}
	ownerQueries := []struct {
		expr, key string
		into      map[[2]string][2]string
	}{
		{`max by (namespace, pod, owner_kind, owner_name) (max_over_time(kube_pod_owner{` + c + `}[` + d + `]))`, "pod", owners.Pod},
		{`max by (namespace, replicaset, owner_kind, owner_name) (max_over_time(kube_replicaset_owner{` + c + `}[` + d + `]))`, "replicaset", owners.ReplicaSet},
		{`max by (namespace, job_name, owner_kind, owner_name) (max_over_time(kube_job_owner{` + c + `}[` + d + `]))`, "job_name", owners.Job},
	}
	for _, oq := range ownerQueries {
		out, err := run(oq.expr)
		if err != nil {
			continue // owners only name the workload; pods still get costed
		}
		for _, s := range out {
			oq.into[[2]string{s.Labels["namespace"], s.Labels[oq.key]}] = [2]string{s.Labels["owner_kind"], s.Labels["owner_name"]}
		}
	}
	return owners
}
