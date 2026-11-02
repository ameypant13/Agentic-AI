package kube

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/yaml"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

// APIResource is one listable resource type, grouped the way Lens groups them.
type APIResource struct {
	Key        string `json:"key"` // "<group or core>/<version>/<resource>", used in URLs
	Group      string `json:"group"`
	Version    string `json:"version"`
	Resource   string `json:"resource"`
	Kind       string `json:"kind"`
	Namespaced bool   `json:"namespaced"`
	Category   string `json:"category"`
}

var categories = map[string]string{
	"nodes": "Cluster", "namespaces": "Cluster", "events": "Cluster",
	"pods": "Workloads", "deployments": "Workloads", "daemonsets": "Workloads", "statefulsets": "Workloads",
	"replicasets": "Workloads", "jobs": "Workloads", "cronjobs": "Workloads",
	"configmaps": "Config", "secrets": "Config", "resourcequotas": "Config", "limitranges": "Config",
	"horizontalpodautoscalers": "Config", "poddisruptionbudgets": "Config", "priorityclasses": "Config",
	"runtimeclasses": "Config", "leases": "Config", "mutatingwebhookconfigurations": "Config",
	"validatingwebhookconfigurations": "Config",
	"services":                        "Network", "endpoints": "Network", "endpointslices": "Network", "ingresses": "Network",
	"ingressclasses": "Network", "networkpolicies": "Network", "gateways": "Network", "httproutes": "Network",
	"gatewayclasses":         "Network",
	"persistentvolumeclaims": "Storage", "persistentvolumes": "Storage", "storageclasses": "Storage",
	"volumesnapshots": "Storage", "volumesnapshotclasses": "Storage", "volumeattachments": "Storage", "csidrivers": "Storage",
	"serviceaccounts": "Access Control", "roles": "Access Control", "rolebindings": "Access Control",
	"clusterroles": "Access Control", "clusterrolebindings": "Access Control",
	"customresourcedefinitions": "Custom Resources",
}

// Built-in groups whose other resources are plumbing and not worth a tree entry.
var hiddenGroups = map[string]bool{
	"authentication.k8s.io": true, "authorization.k8s.io": true, "metrics.k8s.io": true,
	"apiregistration.k8s.io": true, "flowcontrol.apiserver.k8s.io": true, "certificates.k8s.io": true,
	"internal.apiserver.k8s.io": true, "resource.k8s.io": true,
}

func (c *Cluster) APIResources(ctx context.Context) ([]APIResource, error) {
	lists, err := c.k8s.Discovery().ServerPreferredResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, err // a broken aggregated API (e.g. metrics) only removes its own group
	}
	var out []APIResource
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") || !hasVerb(r.Verbs, "list") || !hasVerb(r.Verbs, "get") {
				continue
			}
			cat, known := categories[r.Name]
			if !known {
				switch {
				case hiddenGroups[gv.Group]:
					continue
				case gv.Group == "" || strings.HasSuffix(gv.Group, ".k8s.io"):
					cat = "Other"
				default:
					cat = "Custom Resources"
				}
			}
			group := gv.Group
			if group == "" {
				group = "core"
			}
			out = append(out, APIResource{Key: group + "/" + gv.Version + "/" + r.Name, Group: gv.Group, Version: gv.Version,
				Resource: r.Name, Kind: r.Kind, Namespaced: r.Namespaced, Category: cat})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func hasVerb(verbs []string, v string) bool {
	for _, x := range verbs {
		if x == v {
			return true
		}
	}
	return false
}

// ParseKey splits a resource key "<group|core>/<version>/<resource>".
func ParseKey(key string) (schema.GroupVersionResource, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return schema.GroupVersionResource{}, fmt.Errorf("bad resource key %q", key)
	}
	group := parts[0]
	if group == "core" {
		group = ""
	}
	return schema.GroupVersionResource{Group: group, Version: parts[1], Resource: parts[2]}, nil
}

func resourcePath(gvr schema.GroupVersionResource, ns, name string) string {
	var b strings.Builder
	if gvr.Group == "" {
		b.WriteString("/api/" + gvr.Version)
	} else {
		b.WriteString("/apis/" + gvr.Group + "/" + gvr.Version)
	}
	if ns != "" {
		b.WriteString("/namespaces/" + ns)
	}
	b.WriteString("/" + gvr.Resource)
	if name != "" {
		b.WriteString("/" + name)
	}
	return b.String()
}

// Table is a resource list as kubectl would print it.
type Table struct {
	Columns []TableColumn `json:"columns"`
	Rows    []TableRow    `json:"rows"`
}

type TableColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Priority int32  `json:"priority"` // 0 = shown by default; > 0 = "wide" output only
}

type TableRow struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	CreatedAt string   `json:"createdAt"`
	Cells     []string `json:"cells"`
}

const maxTableRows = 2000

// List returns a resource's table, using the API server's own printer columns
// (the same columns kubectl get shows, including for custom resources).
func (c *Cluster) List(ctx context.Context, gvr schema.GroupVersionResource, ns string) (*Table, error) {
	raw, err := c.k8s.CoreV1().RESTClient().Get().AbsPath(resourcePath(gvr, ns, "")).
		Param("limit", fmt.Sprint(maxTableRows)).
		SetHeader("Accept", "application/json;as=Table;v=v1;g=meta.k8s.io,application/json").
		DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var t metav1.Table
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("decode table: %w", err)
	}
	out := &Table{Columns: []TableColumn{}, Rows: []TableRow{}}
	skip := map[int]bool{}
	for i, col := range t.ColumnDefinitions {
		if col.Name == "Namespace" { // shown from metadata instead
			skip[i] = true
			continue
		}
		out.Columns = append(out.Columns, TableColumn{Name: col.Name, Type: col.Type, Priority: col.Priority})
	}
	for _, r := range t.Rows {
		row := TableRow{Cells: []string{}}
		var m metav1.PartialObjectMetadata
		if len(r.Object.Raw) > 0 && json.Unmarshal(r.Object.Raw, &m) == nil {
			row.Name, row.Namespace, row.CreatedAt = m.Name, m.Namespace, ts(m.CreationTimestamp.Time)
		}
		for i, cell := range r.Cells {
			if skip[i] {
				continue
			}
			row.Cells = append(row.Cells, redact.LogLine(fmt.Sprint(cellString(cell))))
		}
		if row.Name == "" && len(row.Cells) > 0 {
			row.Name = row.Cells[0]
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

func cellString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprint(int64(x))
		}
		return fmt.Sprint(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// Detail is one object: its masked YAML, metadata, events, and for workloads the pods it selects.
type Detail struct {
	Kind        string            `json:"kind"`
	APIVersion  string            `json:"apiVersion"`
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	CreatedAt   string            `json:"createdAt"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Owners      []Owner           `json:"owners"`
	Selector    string            `json:"selector"`
	Pods        []Pod             `json:"pods"`
	Events      []Event           `json:"events"`
	YAML        string            `json:"yaml"`
}

type Owner struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Key  string `json:"key"` // resource key for linking, "" when unknown
}

var ownerKeys = map[string]string{
	"Deployment": "apps/v1/deployments", "ReplicaSet": "apps/v1/replicasets", "StatefulSet": "apps/v1/statefulsets",
	"DaemonSet": "apps/v1/daemonsets", "Job": "batch/v1/jobs", "CronJob": "batch/v1/cronjobs", "Node": "core/v1/nodes",
}

func (c *Cluster) Get(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) (*Detail, error) {
	raw, err := c.k8s.CoreV1().RESTClient().Get().AbsPath(resourcePath(gvr, ns, name)).
		SetHeader("Accept", "application/json").DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var u unstructured.Unstructured
	if err := json.Unmarshal(raw, &u.Object); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}
	MaskObject(&u)
	d := &Detail{Kind: u.GetKind(), APIVersion: u.GetAPIVersion(), Name: u.GetName(), Namespace: u.GetNamespace(),
		CreatedAt: ts(u.GetCreationTimestamp().Time), Labels: nonNilMap(u.GetLabels()),
		Annotations: nonNilMap(u.GetAnnotations()), Owners: []Owner{}, Pods: []Pod{}, Events: []Event{}}
	for _, o := range u.GetOwnerReferences() {
		d.Owners = append(d.Owners, Owner{Kind: o.Kind, Name: o.Name, Key: ownerKeys[o.Kind]})
	}
	y, err := yaml.Marshal(u.Object)
	if err != nil {
		return nil, err
	}
	d.YAML = string(y)

	// Workloads (anything with spec.selector.matchLabels) list the pods they select.
	if ml, found, _ := unstructured.NestedStringMap(u.Object, "spec", "selector", "matchLabels"); found && len(ml) > 0 && ns != "" {
		sel := labels.SelectorFromSet(ml).String()
		d.Selector = sel
		if pods, err := c.k8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel}); err == nil {
			usage := c.podUsage(ctx, ns)
			for i := range pods.Items {
				d.Pods = append(d.Pods, podView(&pods.Items[i], usage))
			}
		}
	} else if gvr.Resource == "services" {
		if sm, found, _ := unstructured.NestedStringMap(u.Object, "spec", "selector"); found && len(sm) > 0 {
			sel := labels.SelectorFromSet(sm).String()
			d.Selector = sel
			if pods, err := c.k8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel}); err == nil {
				for i := range pods.Items {
					d.Pods = append(d.Pods, podView(&pods.Items[i], nil))
				}
			}
		}
	}

	evNS := ns
	sel := fields.OneTermEqualSelector("involvedObject.name", name)
	if kind := u.GetKind(); kind != "" {
		sel = fields.AndSelectors(sel, fields.OneTermEqualSelector("involvedObject.kind", kind))
	}
	if ev, err := c.Events(ctx, evNS, sel); err == nil {
		d.Events = ev
	}
	return d, nil
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// MaskObject removes what the console must never show: managedFields, the
// last-applied annotation (it repeats the spec), every Secret value, ConfigMap
// values under secret-looking keys or containing credentials, and env values
// with secret-looking names anywhere in the object.
func MaskObject(u *unstructured.Unstructured) {
	unstructured.RemoveNestedField(u.Object, "metadata", "managedFields")
	if ann := u.GetAnnotations(); ann != nil {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		u.SetAnnotations(ann)
	}
	switch u.GetKind() {
	case "Secret":
		for _, field := range []string{"data", "stringData"} {
			if m, ok := u.Object[field].(map[string]any); ok {
				for k, v := range m {
					m[k] = fmt.Sprintf("[hidden: %d bytes]", len(fmt.Sprint(v)))
				}
			}
		}
	case "ConfigMap":
		if m, ok := u.Object["data"].(map[string]any); ok {
			for k, v := range m {
				s := fmt.Sprint(v)
				if redact.IsSecretKey(k) {
					m[k] = "[redacted]"
				} else {
					m[k] = redact.Text(s)
				}
			}
		}
		if m, ok := u.Object["binaryData"].(map[string]any); ok {
			for k, v := range m {
				m[k] = fmt.Sprintf("[binary: %d bytes]", len(fmt.Sprint(v)))
			}
		}
	}
	maskEnv(u.Object)
}

// maskEnv walks the object and masks {name, value} pairs whose name looks secret.
func maskEnv(v any) {
	switch x := v.(type) {
	case map[string]any:
		if name, ok := x["name"].(string); ok {
			if val, ok := x["value"].(string); ok && val != "" && redact.IsSecretKey(name) {
				x["value"] = "[redacted]"
			}
		}
		for _, child := range x {
			maskEnv(child)
		}
	case []any:
		for _, child := range x {
			maskEnv(child)
		}
	}
}

// NodeDetail is a node with its pods.
type NodeDetail struct {
	Node
	Labels        map[string]string `json:"labels"`
	Taints        []string          `json:"taints"`
	Addresses     []string          `json:"addresses"`
	Conditions    []Condition       `json:"conditions"`
	Capacity      map[string]string `json:"capacity"`
	Allocatable   map[string]string `json:"allocatable"`
	OS            string            `json:"os"`
	Kernel        string            `json:"kernel"`
	Runtime       string            `json:"runtime"`
	Pods          []Pod             `json:"pods"`
	Events        []Event           `json:"events"`
	ProviderID    string            `json:"providerId"`
	Unschedulable bool              `json:"unschedulable"`
}

func (c *Cluster) NodeDetail(ctx context.Context, name string) (*NodeDetail, error) {
	nodes, err := c.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	var base *Node
	for i := range nodes {
		if nodes[i].Name == name {
			base = &nodes[i]
		}
	}
	n, err := c.k8s.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if base == nil {
		base = &Node{Name: name, Pressure: []string{}}
	}
	d := &NodeDetail{Node: *base, Labels: nonNilMap(n.Labels), Taints: []string{}, Addresses: []string{},
		Capacity: resourceMap(n.Status.Capacity), Allocatable: resourceMap(n.Status.Allocatable),
		OS: n.Status.NodeInfo.OSImage, Kernel: n.Status.NodeInfo.KernelVersion, Runtime: n.Status.NodeInfo.ContainerRuntimeVersion,
		ProviderID: n.Spec.ProviderID, Unschedulable: n.Spec.Unschedulable, Pods: []Pod{}, Events: []Event{}}
	for _, t := range n.Spec.Taints {
		d.Taints = append(d.Taints, fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect))
	}
	for _, a := range n.Status.Addresses {
		d.Addresses = append(d.Addresses, fmt.Sprintf("%s: %s", a.Type, a.Address))
	}
	for _, cond := range n.Status.Conditions {
		d.Conditions = append(d.Conditions, Condition{Type: string(cond.Type), Status: string(cond.Status), Reason: cond.Reason, Message: cond.Message})
	}
	pods, err := c.k8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + name})
	if err == nil {
		usage := c.podUsage(ctx, "")
		for i := range pods.Items {
			d.Pods = append(d.Pods, podView(&pods.Items[i], usage))
		}
	}
	if ev, err := c.Events(ctx, "", fields.AndSelectors(fields.OneTermEqualSelector("involvedObject.name", name),
		fields.OneTermEqualSelector("involvedObject.kind", "Node"))); err == nil {
		d.Events = ev
	}
	return d, nil
}

func resourceMap(l corev1.ResourceList) map[string]string {
	out := map[string]string{}
	for k, q := range l {
		switch k {
		case corev1.ResourceCPU:
			out[string(k)] = fmtCPU(q.MilliValue())
		case corev1.ResourceMemory, corev1.ResourceEphemeralStorage:
			out[string(k)] = fmtMem(q.Value())
		default:
			out[string(k)] = q.String()
		}
	}
	return out
}

// HelmRelease is a release's metadata. Values are never decoded into the
// response: they can hold credentials.
type HelmRelease struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Chart        string `json:"chart"`
	ChartVersion string `json:"chartVersion"`
	AppVersion   string `json:"appVersion"`
	Revision     int    `json:"revision"`
	Status       string `json:"status"`
	Updated      string `json:"updated"`
	Description  string `json:"description"`
}

// HelmReleases reads Helm 3 release secrets (owner=helm) and returns the latest
// revision of each release.
func (c *Cluster) HelmReleases(ctx context.Context, ns string) ([]HelmRelease, error) {
	secrets, err := c.k8s.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm"})
	if err != nil {
		return nil, err
	}
	latest := map[string]HelmRelease{}
	for _, s := range secrets.Items {
		rel, err := decodeHelmRelease(s.Data["release"])
		if err != nil {
			continue
		}
		rel.Namespace = s.Namespace
		key := rel.Namespace + "/" + rel.Name
		if cur, ok := latest[key]; !ok || rel.Revision > cur.Revision {
			latest[key] = rel
		}
	}
	out := make([]HelmRelease, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace+out[i].Name < out[j].Namespace+out[j].Name })
	return out, nil
}

func decodeHelmRelease(data []byte) (HelmRelease, error) {
	raw, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return HelmRelease{}, err
	}
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return HelmRelease{}, err
		}
		if raw, err = io.ReadAll(io.LimitReader(zr, 64<<20)); err != nil {
			return HelmRelease{}, err
		}
	}
	// Only the fields below are decoded; config/values and manifests are skipped.
	var rel struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
		Info    struct {
			Status       string    `json:"status"`
			LastDeployed time.Time `json:"last_deployed"`
			Description  string    `json:"description"`
		} `json:"info"`
		Chart struct {
			Metadata struct {
				Name       string `json:"name"`
				Version    string `json:"version"`
				AppVersion string `json:"appVersion"`
			} `json:"metadata"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(raw, &rel); err != nil {
		return HelmRelease{}, err
	}
	return HelmRelease{Name: rel.Name, Chart: rel.Chart.Metadata.Name, ChartVersion: rel.Chart.Metadata.Version,
		AppVersion: rel.Chart.Metadata.AppVersion, Revision: rel.Version, Status: rel.Info.Status,
		Updated: ts(rel.Info.LastDeployed), Description: redact.LogLine(rel.Info.Description)}, nil
}

// FieldOwner is one managedFields entry: the tool (field manager) that last
// set some of an object's fields, and when. It says what changed and when,
// not who: the fallback where no audit log exists.
type FieldOwner struct {
	Manager     string   `json:"manager"`
	Operation   string   `json:"operation"`
	Subresource string   `json:"subresource,omitempty"`
	Time        string   `json:"time"`
	Fields      []string `json:"fields"` // current values of the fields it owns, "path: value"
}

const maxOwnedFields = 30

// FieldOwners reads an object's managedFields, newest first.
func (c *Cluster) FieldOwners(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) ([]FieldOwner, error) {
	raw, err := c.k8s.CoreV1().RESTClient().Get().AbsPath(resourcePath(gvr, ns, name)).
		SetHeader("Accept", "application/json").DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var u unstructured.Unstructured
	if err := json.Unmarshal(raw, &u.Object); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}
	MaskObject(&u)
	var out []FieldOwner
	for _, mf := range u.GetManagedFields() {
		fo := FieldOwner{Manager: mf.Manager, Operation: string(mf.Operation), Subresource: mf.Subresource, Fields: []string{}}
		if mf.Time != nil {
			fo.Time = ts(mf.Time.Time)
		}
		if mf.FieldsV1 != nil {
			var tree map[string]any
			if json.Unmarshal(mf.FieldsV1.Raw, &tree) == nil {
				for _, path := range ownedPaths(tree, nil) {
					v, found, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
					val := "(set)"
					if found {
						b, _ := json.Marshal(v)
						val = redact.LogLine(string(b))
						if len(val) > 120 {
							val = val[:120] + "…"
						}
					}
					fo.Fields = append(fo.Fields, strings.Join(path, ".")+": "+val)
					if len(fo.Fields) == maxOwnedFields {
						fo.Fields = append(fo.Fields, "…")
						break
					}
				}
			}
		}
		out = append(out, fo)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	return out, nil
}

// ownedPaths walks a FieldsV1 tree ("f:spec": {"f:replicas": {}}) to the leaf
// field paths. List items ("k:{…}", "v:…") stop the walk at the list.
func ownedPaths(tree map[string]any, prefix []string) [][]string {
	var out [][]string
	leaf := true
	for k, v := range tree {
		name, ok := strings.CutPrefix(k, "f:")
		if !ok {
			continue // ".", "k:{…}", "v:…", "i:…": an element of a list or map entry
		}
		leaf = false
		path := append(append([]string{}, prefix...), name)
		child, _ := v.(map[string]any)
		if sub := ownedPaths(child, path); len(sub) > 0 {
			out = append(out, sub...)
		} else {
			out = append(out, path)
		}
	}
	if leaf {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return strings.Join(out[i], ".") < strings.Join(out[j], ".") })
	return out
}
