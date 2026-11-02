// Package cost estimates what each workload costs, from the metrics in our
// Grafana (Thanos) and cloud list prices, using OpenCost's allocation model:
// a node's price is split into a per-core and a per-GiB rate, each pod is
// charged for the larger of what it requested and what it used, and node cost
// no pod accounts for is idle. Everything is read-only; Thanos does the heavy
// aggregation and the console stores one row per workload per day.
package cost

import (
	"math"
	"sort"
)

// OpenCost's default rates (USD per vCPU-hour and per GiB-hour). They price
// nodes whose instance type or list price is unknown, and set the CPU/RAM
// ratio used to split a known node price.
const (
	DefaultCPUHourly = 0.031611
	DefaultRAMHourly = 0.004237
)

// Node is one node's usage of the window.
type Node struct {
	Name         string
	InstanceType string
	Region       string
	Cores        float64
	MemGiB       float64
	Hours        float64 // how long the node existed in the window
	// Hourly is the node's list price per hour; 0 when unknown.
	Hourly float64
}

// Pod is one pod's consumption over the window, in core-hours and GiB-hours.
type Pod struct {
	Namespace, Name, Node string
	CPURequest, CPUUsage  float64
	MemRequest, MemUsage  float64
	// Owner is the controlling workload, resolved by Owners.
	OwnerKind, OwnerName string
}

// Workload is a summed cost line.
type Workload struct {
	Namespace, Kind, Name string
	CPUCost, MemCost      float64
	CPURequest, CPUUsage  float64
	MemRequest, MemUsage  float64
	Pods                  int
}

func (w Workload) Total() float64 { return w.CPUCost + w.MemCost }

// Result is a cluster's allocation for the window.
type Result struct {
	NodeCost, AllocatedCost, IdleCost float64
	NodeHours                         float64
	Nodes, EstimatedNodes             int
	NodeTypes                         map[string]int
	Workloads                         []Workload
}

type rates struct{ cpu, ram float64 }

// nodeRates splits a node's hourly price into per-core and per-GiB rates in
// OpenCost's default CPU:RAM ratio. Unknown prices use the default rates.
func nodeRates(n Node) (rates, bool) {
	if n.Hourly <= 0 || n.Cores <= 0 || n.MemGiB <= 0 {
		return rates{DefaultCPUHourly, DefaultRAMHourly}, false
	}
	cpuShare := n.Cores * DefaultCPUHourly / (n.Cores*DefaultCPUHourly + n.MemGiB*DefaultRAMHourly)
	return rates{cpu: n.Hourly * cpuShare / n.Cores, ram: n.Hourly * (1 - cpuShare) / n.MemGiB}, true
}

// Allocate charges pods to workloads and works out idle cost.
func Allocate(nodes []Node, pods []Pod) Result {
	res := Result{NodeTypes: map[string]int{}}
	byNode := map[string]rates{}
	var totalCores, totalCPUCost, totalGiB, totalRAMCost float64
	for _, n := range nodes {
		r, known := nodeRates(n)
		byNode[n.Name] = r
		cost := (r.cpu*n.Cores + r.ram*n.MemGiB) * n.Hours
		res.NodeCost += cost
		res.NodeHours += n.Hours
		res.Nodes++
		if !known {
			res.EstimatedNodes++
		}
		t := n.InstanceType
		if t == "" {
			t = "unknown"
		}
		res.NodeTypes[t]++
		totalCores += n.Cores * n.Hours
		totalCPUCost += r.cpu * n.Cores * n.Hours
		totalGiB += n.MemGiB * n.Hours
		totalRAMCost += r.ram * n.MemGiB * n.Hours
	}
	// Pods whose node is unknown pay the cluster's average rates.
	avg := rates{DefaultCPUHourly, DefaultRAMHourly}
	if totalCores > 0 && totalGiB > 0 {
		avg = rates{totalCPUCost / totalCores, totalRAMCost / totalGiB}
	}

	type key struct{ ns, kind, name string }
	agg := map[key]*Workload{}
	for _, p := range pods {
		r, ok := byNode[p.Node]
		if !ok {
			r = avg
		}
		cpu := math.Max(p.CPURequest, p.CPUUsage)
		mem := math.Max(p.MemRequest, p.MemUsage)
		if cpu == 0 && mem == 0 {
			continue
		}
		k := key{p.Namespace, p.OwnerKind, p.OwnerName}
		if k.kind == "" {
			k.kind, k.name = "Pod", p.Name
		}
		w := agg[k]
		if w == nil {
			w = &Workload{Namespace: k.ns, Kind: k.kind, Name: k.name}
			agg[k] = w
		}
		w.CPUCost += cpu * r.cpu
		w.MemCost += mem * r.ram
		w.CPURequest += p.CPURequest
		w.CPUUsage += p.CPUUsage
		w.MemRequest += p.MemRequest
		w.MemUsage += p.MemUsage
		w.Pods++
	}
	for _, w := range agg {
		res.AllocatedCost += w.Total()
		res.Workloads = append(res.Workloads, *w)
	}
	sort.Slice(res.Workloads, func(i, j int) bool { return res.Workloads[i].Total() > res.Workloads[j].Total() })
	res.IdleCost = math.Max(0, res.NodeCost-res.AllocatedCost)
	return res
}

// Owners maps pods to their controlling workload: a ReplicaSet's Deployment,
// a Job's CronJob, otherwise the direct owner.
type Owners struct {
	Pod        map[[2]string][2]string // {ns, pod} -> {kind, name}
	ReplicaSet map[[2]string][2]string // {ns, replicaset} -> {kind, name}
	Job        map[[2]string][2]string // {ns, job} -> {kind, name}
}

func (o Owners) Resolve(ns, pod string) (kind, name string) {
	own, ok := o.Pod[[2]string{ns, pod}]
	if !ok {
		return "", ""
	}
	kind, name = own[0], own[1]
	switch kind {
	case "ReplicaSet":
		if up, ok := o.ReplicaSet[[2]string{ns, name}]; ok && up[0] != "" && up[0] != "<none>" {
			return up[0], up[1]
		}
	case "Job":
		if up, ok := o.Job[[2]string{ns, name}]; ok && up[0] != "" && up[0] != "<none>" {
			return up[0], up[1]
		}
	case "<none>", "":
		return "", ""
	}
	return kind, name
}
