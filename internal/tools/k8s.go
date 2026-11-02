package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

// KubeAccess opens a cluster the calling user may see in control-hub, by its
// name or infrastructure ID. It returns an error that explains why for
// clusters without credentials (imported and SAP), so the model switches to
// the Grafana tools.
type KubeAccess func(ctx context.Context, token, cluster string) (*kube.Cluster, error)

// Size caps keep one tool result well inside the model's context.
const (
	k8sMaxRows      = 150
	k8sMaxLogLines  = 500
	k8sMaxLogBytes  = 256 << 10
	k8sMaxYAMLBytes = 24 << 10
	k8sMaxResult    = 60 << 10
)

var clusterProp = str("Cluster name (as on the Clusters page, e.g. pc-cznl-1-rg86s-aks) or control-hub infrastructure ID. " +
	"Only managed clusters (control-hub infrastructures) can be explored; imported and SAP clusters are metrics only.")

func k8sTools() []Tool {
	return []Tool{
		{
			Name: "k8s_list_nodes",
			Description: "Nodes of a cluster: readiness, pool, instance type, kubelet version, live CPU and memory usage " +
				"(metrics-server), requested % of allocatable, pod count and pressure conditions.",
			Properties: map[string]any{"cluster": clusterProp},
			Required:   []string{"cluster"},
			Label:      "Listed cluster nodes",
			Run: withCluster(func(ctx context.Context, c *kube.Cluster, _ json.RawMessage) (any, error) {
				return c.Nodes(ctx)
			}),
		},
		{
			Name: "k8s_list_pods",
			Description: "Pods with status, readiness, restarts, live CPU/memory, requests and limits, node and owner, plus the " +
				"console's own issue for unhealthy pods (CrashLoopBackOff, OOMKilled, Pending, …). Use sort=cpu or memory for a " +
				"'kubectl top pods' view, problems_only to find what is broken.",
			Properties: map[string]any{
				"cluster":       clusterProp,
				"namespace":     str("Namespace; empty for all namespaces."),
				"filter":        str("Optional case-insensitive substring of the pod name, owner or node."),
				"problems_only": map[string]any{"type": "boolean", "description": "Only pods that are not Running and ready, or have an issue."},
				"sort":          enum("Order (default: problems first, then name).", "name", "cpu", "memory", "restarts"),
				"limit":         integer(fmt.Sprintf("Max rows, default 50, max %d.", k8sMaxRows)),
			},
			Required: []string{"cluster"},
			Label:    "Listed pods",
			Run: withCluster(func(ctx context.Context, c *kube.Cluster, input json.RawMessage) (any, error) {
				in, err := decode[struct {
					Namespace    string `json:"namespace"`
					Filter       string `json:"filter"`
					ProblemsOnly bool   `json:"problems_only"`
					Sort         string `json:"sort"`
					Limit        int    `json:"limit"`
				}](input)
				if err != nil {
					return nil, err
				}
				pods, err := c.Pods(ctx, in.Namespace)
				if err != nil {
					return nil, err
				}
				f := strings.ToLower(strings.TrimSpace(in.Filter))
				var out []kube.Pod
				for _, p := range pods {
					if f != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.Owner+" "+p.Node), f) {
						continue
					}
					if in.ProblemsOnly && !podProblem(p) {
						continue
					}
					out = append(out, p)
				}
				sortPods(out, in.Sort)
				return capRows(out, in.Limit), nil
			}),
		},
		{
			Name: "k8s_describe_pod",
			Description: "One pod in detail, like kubectl describe: containers with state, last termination reason and exit " +
				"code, requests and limits, conditions, labels and recent events. include_manifest adds the pod YAML " +
				"(secret-looking env values masked).",
			Properties: map[string]any{
				"cluster":          clusterProp,
				"namespace":        str("Namespace of the pod."),
				"pod":              str("Pod name."),
				"include_manifest": map[string]any{"type": "boolean", "description": "Also return the pod YAML."},
			},
			Required: []string{"cluster", "namespace", "pod"},
			Label:    "Described a pod",
			Run: withCluster(func(ctx context.Context, c *kube.Cluster, input json.RawMessage) (any, error) {
				in, err := decode[struct {
					Namespace       string `json:"namespace"`
					Pod             string `json:"pod"`
					IncludeManifest bool   `json:"include_manifest"`
				}](input)
				if err != nil {
					return nil, err
				}
				if err := requireID("namespace", in.Namespace); err != nil {
					return nil, err
				}
				if err := requireID("pod", in.Pod); err != nil {
					return nil, err
				}
				d, err := c.PodDetail(ctx, in.Namespace, in.Pod)
				if err != nil {
					return nil, err
				}
				if in.IncludeManifest {
					d.Manifest = truncateStr(d.Manifest, k8sMaxYAMLBytes)
				} else {
					d.Manifest = ""
				}
				return d, nil
			}),
		},
		{
			Name: "k8s_pod_logs",
			Description: "Recent log lines of one pod container, with secrets masked. previous=true reads the container's " +
				"last crashed instance (what killed a CrashLoopBackOff pod). grep keeps only matching lines (case-insensitive). " +
				"For searching logs across pods or older than the pod, use grafana_query_logs (Loki) instead.",
			Properties: map[string]any{
				"cluster":       clusterProp,
				"namespace":     str("Namespace of the pod."),
				"pod":           str("Pod name."),
				"container":     str("Container; defaults to the first one."),
				"previous":      map[string]any{"type": "boolean", "description": "Read the previous (crashed) container instance."},
				"tail":          integer(fmt.Sprintf("Lines from the end, default 200, max %d.", k8sMaxLogLines)),
				"since_minutes": integer("Only lines from the last N minutes."),
				"grep":          str("Optional case-insensitive substring filter."),
			},
			Required: []string{"cluster", "namespace", "pod"},
			Label:    "Read pod logs",
			Run: withCluster(func(ctx context.Context, c *kube.Cluster, input json.RawMessage) (any, error) {
				in, err := decode[struct {
					Namespace    string `json:"namespace"`
					Pod          string `json:"pod"`
					Container    string `json:"container"`
					Previous     bool   `json:"previous"`
					Tail         int64  `json:"tail"`
					SinceMinutes int    `json:"since_minutes"`
					Grep         string `json:"grep"`
				}](input)
				if err != nil {
					return nil, err
				}
				if err := requireID("namespace", in.Namespace); err != nil {
					return nil, err
				}
				if err := requireID("pod", in.Pod); err != nil {
					return nil, err
				}
				tail := in.Tail
				if tail <= 0 {
					tail = 200
				}
				if in.Grep != "" {
					tail = k8sMaxLogLines * 4 // read more, keep the matches
				}
				tail = min(tail, k8sMaxLogLines*4)
				logs, err := c.PodLogsLimited(ctx, in.Namespace, in.Pod, in.Container,
					time.Duration(max(in.SinceMinutes, 0))*time.Minute, tail, in.Previous, k8sMaxLogBytes)
				if err != nil {
					return nil, err
				}
				if g := strings.ToLower(in.Grep); g != "" {
					kept := logs.Lines[:0]
					for _, l := range logs.Lines {
						if strings.Contains(strings.ToLower(l), g) {
							kept = append(kept, l)
						}
					}
					logs.Lines = kept
				}
				if len(logs.Lines) > k8sMaxLogLines {
					logs.Lines, logs.Truncated = logs.Lines[len(logs.Lines)-k8sMaxLogLines:], true
				}
				return logs, nil
			}),
		},
		{
			Name: "k8s_events",
			Description: "Recent Kubernetes events (scheduling failures, image pull errors, probe failures, OOM kills, " +
				"evictions, node problems), newest first.",
			Properties: map[string]any{
				"cluster":       clusterProp,
				"namespace":     str("Namespace; empty for all namespaces."),
				"warnings_only": map[string]any{"type": "boolean", "description": "Only Warning events (default true)."},
				"object":        str("Optional case-insensitive substring of the involved object, e.g. a pod or node name."),
				"limit":         integer(fmt.Sprintf("Max rows, default 50, max %d.", k8sMaxRows)),
			},
			Required: []string{"cluster"},
			Label:    "Read cluster events",
			Run: withCluster(func(ctx context.Context, c *kube.Cluster, input json.RawMessage) (any, error) {
				in, err := decode[struct {
					Namespace    string `json:"namespace"`
					WarningsOnly *bool  `json:"warnings_only"`
					Object       string `json:"object"`
					Limit        int    `json:"limit"`
				}](input)
				if err != nil {
					return nil, err
				}
				events, err := c.Events(ctx, in.Namespace, nil)
				if err != nil {
					return nil, err
				}
				warnOnly := in.WarningsOnly == nil || *in.WarningsOnly
				obj := strings.ToLower(in.Object)
				var out []kube.Event
				for _, e := range events {
					if warnOnly && e.Type != "Warning" {
						continue
					}
					if obj != "" && !strings.Contains(strings.ToLower(e.Object), obj) {
						continue
					}
					e.Message = redact.LogLine(e.Message)
					out = append(out, e)
				}
				return capRows(out, in.Limit), nil
			}),
		},
		{
			Name: "k8s_sandboxes",
			Description: "The cluster's go-sandbox pods (go-operator-sandbox-*) and which are orphaned: pods the orchestrator " +
				"logged as \"failed to start: Unschedulable\" and gave up on (the run failed) that still exist and hold CPU and " +
				"memory. Use for runs failing with Unschedulable sandboxes, or capacity questions on execution planes.",
			Properties: map[string]any{"cluster": clusterProp},
			Required:   []string{"cluster"},
			Label:      "Check go-sandboxes",
			Run: withCluster(func(ctx context.Context, c *kube.Cluster, _ json.RawMessage) (any, error) {
				return c.Sandboxes(ctx)
			}),
		},
		{
			Name: "k8s_get",
			Description: "Any Kubernetes resource, like kubectl get. Without name: the resource list with the API server's " +
				"printer columns. With name: the object's YAML (Secret values and secret-looking ConfigMap keys masked), owners, " +
				"selected pods and events. kind accepts a plural resource (deployments), a Kind (Deployment), a short form " +
				"(deploy, svc, cm, sts, ds, ing, pvc, ns) or a group/version/resource key (apps/v1/deployments).",
			Properties: map[string]any{
				"cluster":   clusterProp,
				"kind":      str("Resource kind, e.g. deployments, Ingress, certificates.cert-manager.io, apps/v1/statefulsets."),
				"namespace": str("Namespace for namespaced kinds; empty lists across all namespaces."),
				"name":      str("Optional object name; omit to list."),
				"filter":    str("When listing: optional case-insensitive substring of the name."),
				"limit":     integer(fmt.Sprintf("When listing: max rows, default 100, max %d.", k8sMaxRows)),
			},
			Required: []string{"cluster", "kind"},
			Label:    "Read Kubernetes resources",
			Run: withCluster(func(ctx context.Context, c *kube.Cluster, input json.RawMessage) (any, error) {
				in, err := decode[struct {
					Kind      string `json:"kind"`
					Namespace string `json:"namespace"`
					Name      string `json:"name"`
					Filter    string `json:"filter"`
					Limit     int    `json:"limit"`
				}](input)
				if err != nil {
					return nil, err
				}
				all, err := c.APIResources(ctx)
				if err != nil {
					return nil, err
				}
				res, err := resolveKind(all, in.Kind)
				if err != nil {
					return nil, err
				}
				gvr, err := kube.ParseKey(res.Key)
				if err != nil {
					return nil, err
				}
				ns := in.Namespace
				if !res.Namespaced {
					ns = ""
				}
				if in.Name != "" {
					if err := requireID("name", in.Name); err != nil {
						return nil, err
					}
					if res.Namespaced && ns == "" {
						return nil, fmt.Errorf("%s is namespaced: give the namespace", res.Resource)
					}
					d, err := c.Get(ctx, gvr, ns, in.Name)
					if err != nil {
						return nil, err
					}
					d.YAML = truncateStr(d.YAML, k8sMaxYAMLBytes)
					d.Pods = capRows(d.Pods, 30)
					d.Events = capRows(d.Events, 30)
					return d, nil
				}
				t, err := c.List(ctx, gvr, ns)
				if err != nil {
					return nil, err
				}
				f := strings.ToLower(in.Filter)
				rows := t.Rows[:0]
				for _, r := range t.Rows {
					if f == "" || strings.Contains(strings.ToLower(r.Name), f) {
						rows = append(rows, r)
					}
				}
				total := len(rows)
				limit := in.Limit
				if limit <= 0 {
					limit = 100
				}
				t.Rows = capRows(rows, limit)
				return map[string]any{"resource": res.Key, "total": total, "columns": t.Columns, "rows": t.Rows}, nil
			}),
		},
	}
}

// withCluster resolves the cluster argument with the caller's access, runs f
// and renders its result as size-capped JSON.
func withCluster(f func(ctx context.Context, c *kube.Cluster, input json.RawMessage) (any, error)) func(context.Context, *Env, json.RawMessage) (string, error) {
	return func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
		in, err := decode[struct {
			Cluster string `json:"cluster"`
		}](input)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(in.Cluster) == "" {
			return "", fmt.Errorf("cluster is required")
		}
		c, err := env.Kube(ctx, env.Token, strings.TrimSpace(in.Cluster))
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		v, err := f(ctx, c, input)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		if len(b) > k8sMaxResult {
			return string(b[:k8sMaxResult]) + "\n… result truncated; narrow it with namespace, filter or limit", nil
		}
		return string(b), nil
	}
}

func capRows[T any](rows []T, limit int) []T {
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, k8sMaxRows)
	if len(rows) > limit {
		return rows[:limit]
	}
	if rows == nil {
		return []T{}
	}
	return rows
}

func podProblem(p kube.Pod) bool {
	if p.Issue != "" {
		return true
	}
	if p.Status == "Succeeded" || p.Status == "Completed" {
		return false
	}
	if p.Status != "Running" {
		return true
	}
	ready, total, ok := strings.Cut(p.Ready, "/")
	return ok && ready != total
}

func sortPods(pods []kube.Pod, by string) {
	qty := func(s string) int64 {
		q, err := resource.ParseQuantity(s)
		if err != nil {
			return 0
		}
		return q.MilliValue()
	}
	sort.SliceStable(pods, func(i, j int) bool {
		a, b := pods[i], pods[j]
		switch by {
		case "cpu":
			return qty(a.CPU) > qty(b.CPU)
		case "memory":
			return qty(a.Memory) > qty(b.Memory)
		case "restarts":
			return a.Restarts > b.Restarts
		case "name":
			return a.Namespace+"/"+a.Name < b.Namespace+"/"+b.Name
		}
		if pa, pb := podProblem(a), podProblem(b); pa != pb {
			return pa
		}
		return a.Namespace+"/"+a.Name < b.Namespace+"/"+b.Name
	})
}

var kindAliases = map[string]string{
	"po": "pods", "deploy": "deployments", "svc": "services", "cm": "configmaps", "sts": "statefulsets",
	"ds": "daemonsets", "rs": "replicasets", "ing": "ingresses", "pvc": "persistentvolumeclaims",
	"pv": "persistentvolumes", "ns": "namespaces", "no": "nodes", "sa": "serviceaccounts", "ep": "endpoints",
	"hpa": "horizontalpodautoscalers", "pdb": "poddisruptionbudgets", "cj": "cronjobs", "sc": "storageclasses",
	"crd": "customresourcedefinitions", "netpol": "networkpolicies", "ev": "events",
}

// resolveKind finds the API resource a model-supplied kind names. Core and
// well-known groups win over custom resources with the same plural.
func resolveKind(all []kube.APIResource, kind string) (*kube.APIResource, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		return nil, fmt.Errorf("kind is required")
	}
	if a, ok := kindAliases[k]; ok {
		k = a
	}
	group := ""
	if name, g, ok := strings.Cut(k, "."); ok && !strings.Contains(k, "/") {
		k, group = name, g
	}
	var best *kube.APIResource
	rank := func(r kube.APIResource) int {
		switch {
		case r.Group == "":
			return 0
		case !strings.Contains(r.Group, "."), strings.HasSuffix(r.Group, ".k8s.io"):
			return 1
		}
		return 2
	}
	for i := range all {
		r := all[i]
		if strings.Contains(k, "/") {
			if strings.EqualFold(r.Key, k) {
				return &r, nil
			}
			continue
		}
		if group != "" && !strings.EqualFold(r.Group, group) {
			continue
		}
		if !strings.EqualFold(r.Resource, k) && !strings.EqualFold(r.Kind, k) && !strings.EqualFold(r.Resource, k+"s") {
			continue
		}
		if best == nil || rank(r) < rank(*best) {
			best = &all[i]
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no resource %q in this cluster; use a plural resource name such as deployments", kind)
	}
	return best, nil
}
