package kube

import (
	"bufio"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/yaml"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

// The view types below are the API shapes documented in API.md.

type Summary struct {
	Nodes            int    `json:"nodes"`
	NodesReady       int    `json:"nodesReady"`
	Pods             int    `json:"pods"`
	PodsNotReady     int    `json:"podsNotReady"`
	CPURequestedPct  int    `json:"cpuRequestedPct"`
	MemRequestedPct  int    `json:"memRequestedPct"`
	CPUDetail        string `json:"cpuDetail"`
	MemDetail        string `json:"memDetail"`
	MetricsAvailable bool   `json:"metricsAvailable"`
	// Source is "thanos" when the counts came from Grafana's metrics, else "kubernetes".
	Source string `json:"source"`
}

type Node struct {
	Name            string   `json:"name"`
	Ready           bool     `json:"ready"`
	Status          string   `json:"status"`
	Pool            string   `json:"pool"`
	InstanceType    string   `json:"instanceType"`
	Version         string   `json:"version"`
	CreatedAt       string   `json:"createdAt"`
	CPURequestedPct int      `json:"cpuRequestedPct"`
	MemRequestedPct int      `json:"memRequestedPct"`
	Pods            int      `json:"pods"`
	CPUUsage        string   `json:"cpuUsage"`
	MemUsage        string   `json:"memUsage"`
	Pressure        []string `json:"pressure"`
}

type Pod struct {
	Name          string   `json:"name"`
	Namespace     string   `json:"namespace"`
	Status        string   `json:"status"`
	Ready         string   `json:"ready"`
	Restarts      int32    `json:"restarts"`
	CPU           string   `json:"cpu"`
	Memory        string   `json:"memory"`
	CPURequest    string   `json:"cpuRequest"`
	MemRequest    string   `json:"memRequest"`
	MemLimit      string   `json:"memLimit"`
	Node          string   `json:"node"`
	CreatedAt     string   `json:"createdAt"`
	Owner         string   `json:"owner"`
	Images        []string `json:"images"`
	Issue         string   `json:"issue"`
	IssueSeverity string   `json:"issueSeverity"`
	// ContainerNames is for internal use (log scans); not part of the API.
	ContainerNames []string `json:"-"`
}

type Workload struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Ready     string   `json:"ready"`
	UpToDate  int32    `json:"upToDate"`
	Available int32    `json:"available"`
	Images    []string `json:"images"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

type Event struct {
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Object    string `json:"object"`
	Message   string `json:"message"`
	Count     int32  `json:"count"`
	LastSeen  string `json:"lastSeen"`
	Namespace string `json:"namespace"`
}

type Service struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"`
	ClusterIP string `json:"clusterIP"`
	External  string `json:"external"`
	Ports     string `json:"ports"`
}

type Container struct {
	Name           string `json:"name"`
	Image          string `json:"image"`
	Ready          bool   `json:"ready"`
	Restarts       int32  `json:"restarts"`
	State          string `json:"state"`
	LastTerminated string `json:"lastTerminated"`
	CPURequest     string `json:"cpuRequest"`
	MemRequest     string `json:"memRequest"`
	CPULimit       string `json:"cpuLimit"`
	MemLimit       string `json:"memLimit"`
}

type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type PodDetail struct {
	Pod
	Labels     map[string]string `json:"labels"`
	Conditions []Condition       `json:"conditions"`
	Containers []Container       `json:"containers"`
	Events     []Event           `json:"events"`
	Manifest   string            `json:"manifest"`
}

type Logs struct {
	Container string   `json:"container"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

const maxEvents = 300

// --- summary ---

func (c *Cluster) Summary(ctx context.Context) (*Summary, error) {
	nodes, err := c.k8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods, err := c.k8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	s := &Summary{Nodes: len(nodes.Items)}
	var allocCPU, allocMem int64
	for _, n := range nodes.Items {
		if nodeReady(&n) {
			s.NodesReady++
		}
		allocCPU += n.Status.Allocatable.Cpu().MilliValue()
		allocMem += n.Status.Allocatable.Memory().Value()
	}
	var reqCPU, reqMem int64
	for i := range pods.Items {
		p := &pods.Items[i]
		if terminal(p) {
			continue
		}
		s.Pods++
		if !podReady(p) {
			s.PodsNotReady++
		}
		cpu, mem, _, _ := podResources(p)
		reqCPU += cpu
		reqMem += mem
	}
	s.CPURequestedPct, s.MemRequestedPct = pct(reqCPU, allocCPU), pct(reqMem, allocMem)
	s.CPUDetail = fmt.Sprintf("%.1f / %.0f cores", float64(reqCPU)/1000, float64(allocCPU)/1000)
	s.MemDetail = fmt.Sprintf("%.0f / %.0f GiB", float64(reqMem)/(1<<30), float64(allocMem)/(1<<30))
	mctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, merr := c.metrics.MetricsV1beta1().NodeMetricses().List(mctx, metav1.ListOptions{Limit: 1})
	s.MetricsAvailable = merr == nil
	s.Source = "kubernetes"
	return s, nil
}

func (c *Cluster) Namespaces(ctx context.Context) ([]string, error) {
	list, err := c.k8s.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list.Items))
	for _, n := range list.Items {
		out = append(out, n.Name)
	}
	sort.Strings(out)
	return out, nil
}

// --- nodes ---

func (c *Cluster) Nodes(ctx context.Context) ([]Node, error) {
	nodes, err := c.k8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods, err := c.k8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	type req struct {
		cpu, mem int64
		pods     int
	}
	byNode := map[string]*req{}
	for i := range pods.Items {
		p := &pods.Items[i]
		if terminal(p) || p.Spec.NodeName == "" {
			continue
		}
		r := byNode[p.Spec.NodeName]
		if r == nil {
			r = &req{}
			byNode[p.Spec.NodeName] = r
		}
		cpu, mem, _, _ := podResources(p)
		r.cpu += cpu
		r.mem += mem
		r.pods++
	}
	usage := map[string][2]string{}
	if nm, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{}); err == nil {
		for _, m := range nm.Items {
			usage[m.Name] = [2]string{fmtCPU(m.Usage.Cpu().MilliValue()), fmtMem(m.Usage.Memory().Value())}
		}
	}
	out := make([]Node, 0, len(nodes.Items))
	for i := range nodes.Items {
		n := &nodes.Items[i]
		r := byNode[n.Name]
		if r == nil {
			r = &req{}
		}
		status := "Ready"
		if !nodeReady(n) {
			status = "NotReady"
		}
		if n.Spec.Unschedulable {
			status += ",SchedulingDisabled"
		}
		pressure := []string{}
		for _, cond := range n.Status.Conditions {
			if nodeProblem(cond) {
				pressure = append(pressure, string(cond.Type))
			}
		}
		u := usage[n.Name]
		out = append(out, Node{
			Name: n.Name, Ready: nodeReady(n), Status: status, Pool: nodePool(n),
			InstanceType: n.Labels["node.kubernetes.io/instance-type"], Version: n.Status.NodeInfo.KubeletVersion,
			CreatedAt:       ts(n.CreationTimestamp.Time),
			CPURequestedPct: pct(r.cpu, n.Status.Allocatable.Cpu().MilliValue()),
			MemRequestedPct: pct(r.mem, n.Status.Allocatable.Memory().Value()),
			Pods:            r.pods, CPUUsage: u[0], MemUsage: u[1], Pressure: pressure,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func nodePool(n *corev1.Node) string {
	for _, k := range []string{"cloud.google.com/gke-nodepool", "eks.amazonaws.com/nodegroup", "karpenter.sh/nodepool", "agentpool", "kubernetes.azure.com/agentpool"} {
		if v := n.Labels[k]; v != "" {
			return v
		}
	}
	return ""
}

// --- pods ---

func (c *Cluster) Pods(ctx context.Context, ns string) ([]Pod, error) {
	list, err := c.k8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	usage := c.podUsage(ctx, ns)
	out := make([]Pod, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, podView(&list.Items[i], usage))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (c *Cluster) podUsage(ctx context.Context, ns string) map[string][2]string {
	out := map[string][2]string{}
	mctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pm, err := c.metrics.MetricsV1beta1().PodMetricses(ns).List(mctx, metav1.ListOptions{})
	if err != nil {
		return out
	}
	for _, m := range pm.Items {
		var cpu, mem int64
		for _, ct := range m.Containers {
			cpu += ct.Usage.Cpu().MilliValue()
			mem += ct.Usage.Memory().Value()
		}
		out[m.Namespace+"/"+m.Name] = [2]string{fmtCPU(cpu), fmtMem(mem)}
	}
	return out
}

func podView(p *corev1.Pod, usage map[string][2]string) Pod {
	cpu, mem, _, memLim := podResources(p)
	ready, total := 0, len(p.Spec.Containers)
	var restarts int32
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
		restarts += cs.RestartCount
	}
	images := make([]string, 0, len(p.Spec.Containers))
	names := make([]string, 0, len(p.Spec.Containers))
	for _, ct := range p.Spec.Containers {
		images = append(images, ct.Image)
		names = append(names, ct.Name)
	}
	owner := ""
	if len(p.OwnerReferences) > 0 {
		o := p.OwnerReferences[0]
		owner = o.Kind + "/" + o.Name
		if o.Kind == "ReplicaSet" { // show the Deployment, as kubectl users think of it
			if i := strings.LastIndex(o.Name, "-"); i > 0 {
				owner = "Deployment/" + o.Name[:i]
			}
		}
	}
	u := usage[p.Namespace+"/"+p.Name]
	v := Pod{
		Name: p.Name, Namespace: p.Namespace, Status: podStatus(p), Ready: fmt.Sprintf("%d/%d", ready, total),
		Restarts: restarts, CPU: u[0], Memory: u[1], CPURequest: fmtCPUOpt(cpu), MemRequest: fmtMemOpt(mem),
		MemLimit: fmtMemOpt(memLim), Node: p.Spec.NodeName, CreatedAt: ts(p.CreationTimestamp.Time),
		Owner: owner, Images: images, ContainerNames: names,
	}
	v.Issue, v.IssueSeverity = podIssue(p, restarts)
	return v
}

// podStatus mirrors the STATUS column of kubectl get pods.
func podStatus(p *corev1.Pod) string {
	reason := string(p.Status.Phase)
	if p.Status.Reason != "" {
		reason = p.Status.Reason
	}
	for _, cs := range p.Status.InitContainerStatuses {
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode == 0 {
			continue
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing" {
			return "Init:" + cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil {
			return "Init:Error"
		}
		if cs.State.Running != nil || cs.State.Waiting != nil {
			return "Init:Running"
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		switch {
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
			reason = cs.State.Waiting.Reason
		case cs.State.Terminated != nil && cs.State.Terminated.Reason != "":
			reason = cs.State.Terminated.Reason
		}
	}
	if p.DeletionTimestamp != nil {
		reason = "Terminating"
	}
	return reason
}

var criticalWaiting = map[string]bool{"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true,
	"CreateContainerConfigError": true, "InvalidImageName": true, "CreateContainerError": true}

// podIssue picks the one thing about a pod worth a pill in the table.
func podIssue(p *corev1.Pod, restarts int32) (string, string) {
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && criticalWaiting[w.Reason] {
			return w.Reason, "critical"
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" {
			sev := "warning"
			if time.Since(t.FinishedAt.Time) < 24*time.Hour {
				sev = "critical"
			}
			return "OOMKilled " + ago(t.FinishedAt.Time) + " ago", sev
		}
	}
	if p.Status.Phase == corev1.PodPending {
		for _, cond := range p.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
				return "Unschedulable: " + shortSchedulingReason(cond.Message), "warning"
			}
		}
	}
	if p.Status.Phase == corev1.PodFailed {
		return "Failed", "critical"
	}
	if restarts > 3 {
		return fmt.Sprintf("%d restarts", restarts), "warning"
	}
	return "", ""
}

func shortSchedulingReason(msg string) string {
	for _, k := range []string{"Insufficient memory", "Insufficient cpu", "untolerated taint", "didn't match Pod's node affinity", "unbound immediate PersistentVolumeClaims"} {
		if strings.Contains(msg, k) {
			return k
		}
	}
	if len(msg) > 60 {
		return msg[:60] + "…"
	}
	return msg
}

// --- workloads, events, services ---

func (c *Cluster) Workloads(ctx context.Context, ns string) ([]Workload, error) {
	apps := c.k8s.AppsV1()
	var out []Workload
	deps, err := apps.Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range deps.Items {
		want := int32(1)
		if d.Spec.Replicas != nil {
			want = *d.Spec.Replicas
		}
		out = append(out, Workload{Kind: "Deployment", Name: d.Name, Namespace: d.Namespace,
			Ready: fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, want), UpToDate: d.Status.UpdatedReplicas,
			Available: d.Status.AvailableReplicas, Images: images(d.Spec.Template.Spec.Containers),
			CreatedAt: ts(d.CreationTimestamp.Time), UpdatedAt: ts(lastProgress(d.Status.Conditions))})
	}
	sets, err := apps.StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, s := range sets.Items {
		want := int32(1)
		if s.Spec.Replicas != nil {
			want = *s.Spec.Replicas
		}
		out = append(out, Workload{Kind: "StatefulSet", Name: s.Name, Namespace: s.Namespace,
			Ready: fmt.Sprintf("%d/%d", s.Status.ReadyReplicas, want), UpToDate: s.Status.UpdatedReplicas,
			Available: s.Status.AvailableReplicas, Images: images(s.Spec.Template.Spec.Containers),
			CreatedAt: ts(s.CreationTimestamp.Time), UpdatedAt: ts(s.CreationTimestamp.Time)})
	}
	dss, err := apps.DaemonSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range dss.Items {
		out = append(out, Workload{Kind: "DaemonSet", Name: d.Name, Namespace: d.Namespace,
			Ready:    fmt.Sprintf("%d/%d", d.Status.NumberReady, d.Status.DesiredNumberScheduled),
			UpToDate: d.Status.UpdatedNumberScheduled, Available: d.Status.NumberAvailable,
			Images: images(d.Spec.Template.Spec.Containers), CreatedAt: ts(d.CreationTimestamp.Time),
			UpdatedAt: ts(d.CreationTimestamp.Time)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func lastProgress(conds []appsv1.DeploymentCondition) time.Time {
	var t time.Time
	for _, c := range conds {
		if c.LastUpdateTime.After(t) {
			t = c.LastUpdateTime.Time
		}
	}
	return t
}

func (c *Cluster) Events(ctx context.Context, ns string, selector fields.Selector) ([]Event, error) {
	opts := metav1.ListOptions{}
	if selector != nil {
		opts.FieldSelector = selector.String()
	}
	list, err := c.k8s.CoreV1().Events(ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(list.Items))
	for _, e := range list.Items {
		last := e.LastTimestamp.Time
		if last.IsZero() {
			last = e.EventTime.Time
		}
		if last.IsZero() {
			last = e.CreationTimestamp.Time
		}
		count := e.Count
		if count == 0 && e.Series != nil {
			count = e.Series.Count
		}
		if count == 0 {
			count = 1
		}
		out = append(out, Event{Type: e.Type, Reason: e.Reason, Namespace: e.Namespace,
			Object:  strings.ToLower(e.InvolvedObject.Kind) + "/" + e.InvolvedObject.Name,
			Message: redact.LogLine(e.Message), Count: count, LastSeen: ts(last)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	if len(out) > maxEvents {
		out = out[:maxEvents]
	}
	return out, nil
}

func (c *Cluster) Services(ctx context.Context, ns string) ([]Service, error) {
	list, err := c.k8s.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(list.Items))
	for _, s := range list.Items {
		ext := ""
		if s.Spec.Type == corev1.ServiceTypeLoadBalancer {
			ext = "<pending>"
			var addrs []string
			for _, in := range s.Status.LoadBalancer.Ingress {
				if in.Hostname != "" {
					addrs = append(addrs, in.Hostname)
				} else if in.IP != "" {
					addrs = append(addrs, in.IP)
				}
			}
			if len(addrs) > 0 {
				ext = strings.Join(addrs, ", ")
			}
		}
		var ports []string
		for _, p := range s.Spec.Ports {
			port := fmt.Sprintf("%d/%s", p.Port, p.Protocol)
			if p.NodePort != 0 {
				port = fmt.Sprintf("%d:%d/%s", p.Port, p.NodePort, p.Protocol)
			}
			ports = append(ports, port)
		}
		out = append(out, Service{Name: s.Name, Namespace: s.Namespace, Type: string(s.Spec.Type),
			ClusterIP: s.Spec.ClusterIP, External: ext, Ports: strings.Join(ports, ", ")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace+out[i].Name < out[j].Namespace+out[j].Name })
	return out, nil
}

// --- pod detail & logs ---

func (c *Cluster) PodDetail(ctx context.Context, ns, name string) (*PodDetail, error) {
	p, err := c.k8s.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	d := &PodDetail{Pod: podView(p, c.podUsage(ctx, ns)), Labels: p.Labels}
	if d.Labels == nil {
		d.Labels = map[string]string{}
	}
	for _, cond := range p.Status.Conditions {
		d.Conditions = append(d.Conditions, Condition{Type: string(cond.Type), Status: string(cond.Status),
			Reason: cond.Reason, Message: cond.Message})
	}
	statuses := map[string]corev1.ContainerStatus{}
	for _, cs := range p.Status.ContainerStatuses {
		statuses[cs.Name] = cs
	}
	for _, ct := range p.Spec.Containers {
		cs := statuses[ct.Name]
		d.Containers = append(d.Containers, Container{
			Name: ct.Name, Image: ct.Image, Ready: cs.Ready, Restarts: cs.RestartCount,
			State: containerState(cs.State), LastTerminated: lastTerminated(cs.LastTerminationState),
			CPURequest: qty(ct.Resources.Requests, corev1.ResourceCPU), MemRequest: qty(ct.Resources.Requests, corev1.ResourceMemory),
			CPULimit: qty(ct.Resources.Limits, corev1.ResourceCPU), MemLimit: qty(ct.Resources.Limits, corev1.ResourceMemory),
		})
	}
	sel := fields.AndSelectors(fields.OneTermEqualSelector("involvedObject.name", name),
		fields.OneTermEqualSelector("involvedObject.kind", "Pod"))
	if d.Events, err = c.Events(ctx, ns, sel); err != nil {
		d.Events = []Event{}
	}
	if d.Manifest, err = podManifest(p); err != nil {
		return nil, err
	}
	return d, nil
}

// podManifest renders the pod as YAML without managedFields, without the
// last-applied annotation (it repeats the spec, env values included), and
// with env values whose names look secret masked.
func podManifest(p *corev1.Pod) (string, error) {
	cp := p.DeepCopy()
	cp.APIVersion, cp.Kind = "v1", "Pod"
	cp.ManagedFields = nil
	delete(cp.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
	mask := func(cs []corev1.Container) {
		for i := range cs {
			for j := range cs[i].Env {
				if cs[i].Env[j].Value != "" && redact.IsSecretKey(cs[i].Env[j].Name) {
					cs[i].Env[j].Value = "[redacted]"
				}
			}
		}
	}
	mask(cp.Spec.Containers)
	mask(cp.Spec.InitContainers)
	raw, err := yaml.Marshal(cp)
	return string(raw), err
}

// ContainerNames lists a pod's (non-init) containers.
func (c *Cluster) ContainerNames(ctx context.Context, ns, name string) ([]string, error) {
	p, err := c.k8s.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(p.Spec.Containers))
	for _, ct := range p.Spec.Containers {
		out = append(out, ct.Name)
	}
	return out, nil
}

const maxLogBytes = 4 << 20

func (c *Cluster) PodLogs(ctx context.Context, ns, name, container string, since time.Duration, tail int64, previous bool) (*Logs, error) {
	return c.PodLogsLimited(ctx, ns, name, container, since, tail, previous, maxLogBytes)
}

// PodLogsLimited is PodLogs with a byte cap. Kubernetes applies the tail
// before the cap, so the result is the most recent lines that fit.
func (c *Cluster) PodLogsLimited(ctx context.Context, ns, name, container string, since time.Duration, tail int64, previous bool, limitBytes int64) (*Logs, error) {
	if container == "" {
		p, err := c.k8s.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		container = p.Spec.Containers[0].Name
	}
	limit := limitBytes
	opts := &corev1.PodLogOptions{Container: container, TailLines: &tail, Previous: previous, LimitBytes: &limit}
	if since > 0 {
		// A time window makes the kubelet read the log file from its start;
		// pass since <= 0 to read only the tail, which is much faster on big logs.
		sinceSec := int64(since.Seconds())
		opts.SinceSeconds = &sinceSec
	}
	stream, err := c.k8s.CoreV1().Pods(ns).GetLogs(name, opts).Stream(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	out := &Logs{Container: container, Lines: []string{}}
	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 256<<10), 1<<20)
	var n int
	for sc.Scan() {
		line := sc.Text()
		n += len(line) + 1
		out.Lines = append(out.Lines, redact.LogLine(line))
	}
	out.Truncated = int64(len(out.Lines)) >= tail || int64(n) >= limitBytes
	return out, sc.Err()
}

// TimedLine is one log line with the kubelet's timestamp.
type TimedLine struct {
	TS   time.Time
	Text string
}

// TimedLogs reads a container's log with timestamps. With since set, only
// lines after it are returned (the kubelet still reads from the file start,
// so callers bound it with tail and limitBytes).
func (c *Cluster) TimedLogs(ctx context.Context, ns, pod, container string, since *time.Time, tail, limitBytes int64, previous bool) ([]TimedLine, error) {
	opts := &corev1.PodLogOptions{Container: container, Timestamps: true, Previous: previous, LimitBytes: &limitBytes}
	if tail > 0 {
		opts.TailLines = &tail
	}
	if since != nil {
		t := metav1.NewTime(*since)
		opts.SinceTime = &t
	}
	stream, err := c.k8s.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var out []TimedLine
	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 256<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		tsPart, text, ok := strings.Cut(line, " ")
		ts, err := time.Parse(time.RFC3339Nano, tsPart)
		if !ok || err != nil {
			out = append(out, TimedLine{Text: line}) // continuation without a timestamp
			continue
		}
		out = append(out, TimedLine{TS: ts, Text: text})
	}
	return out, sc.Err()
}

// --- helpers ---

func terminal(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// nodeProblem reports whether a node condition other than Ready is unhealthy.
// Conditions named *Ready (EKS Auto Mode's KernelReady, StorageReady,
// NetworkingReady…) are healthy when True; the rest (MemoryPressure,
// DiskPressure, NetworkUnavailable, node-problem-detector types) when True.
func nodeProblem(c corev1.NodeCondition) bool {
	if c.Type == corev1.NodeReady || c.Status == corev1.ConditionUnknown {
		return false
	}
	if strings.HasSuffix(string(c.Type), "Ready") {
		return c.Status == corev1.ConditionFalse
	}
	return c.Status == corev1.ConditionTrue
}

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podResources returns the pod's effective requests and limits, the way the
// scheduler counts them: the larger of the sum of containers and any init container.
func podResources(p *corev1.Pod) (cpuReq, memReq, cpuLim, memLim int64) {
	for _, c := range p.Spec.Containers {
		cpuReq += c.Resources.Requests.Cpu().MilliValue()
		memReq += c.Resources.Requests.Memory().Value()
		cpuLim += c.Resources.Limits.Cpu().MilliValue()
		memLim += c.Resources.Limits.Memory().Value()
	}
	for _, c := range p.Spec.InitContainers {
		cpuReq = max(cpuReq, c.Resources.Requests.Cpu().MilliValue())
		memReq = max(memReq, c.Resources.Requests.Memory().Value())
	}
	return
}

func containerState(s corev1.ContainerState) string {
	switch {
	case s.Running != nil:
		return "Running since " + ts(s.Running.StartedAt.Time)
	case s.Waiting != nil:
		return "Waiting: " + s.Waiting.Reason
	case s.Terminated != nil:
		return fmt.Sprintf("Terminated: %s (exit %d)", s.Terminated.Reason, s.Terminated.ExitCode)
	}
	return "Unknown"
}

func lastTerminated(s corev1.ContainerState) string {
	if t := s.Terminated; t != nil {
		return fmt.Sprintf("%s (exit %d) %s ago", t.Reason, t.ExitCode, ago(t.FinishedAt.Time))
	}
	return ""
}

func images(cs []corev1.Container) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Image)
	}
	return out
}

func qty(l corev1.ResourceList, name corev1.ResourceName) string {
	q, ok := l[name]
	if !ok {
		return ""
	}
	if name == corev1.ResourceCPU {
		return fmtCPU(q.MilliValue())
	}
	return fmtMem(q.Value())
}

func pct(used, total int64) int {
	if total <= 0 {
		return 0
	}
	return int(used * 100 / total)
}

func fmtCPU(milli int64) string {
	if milli < 1000 {
		return fmt.Sprintf("%dm", milli)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(milli)/1000), ".0")
}

func fmtMem(b int64) string {
	switch {
	case b >= 1<<30:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(b)/(1<<30)), ".0") + "Gi"
	case b >= 1<<20:
		return fmt.Sprintf("%dMi", b/(1<<20))
	default:
		return resource.NewQuantity(b, resource.BinarySI).String()
	}
}

func fmtCPUOpt(milli int64) string {
	if milli == 0 {
		return ""
	}
	return fmtCPU(milli)
}

func fmtMemOpt(b int64) string {
	if b == 0 {
		return ""
	}
	return fmtMem(b)
}

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
