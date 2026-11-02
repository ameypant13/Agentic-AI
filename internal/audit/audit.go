// Package audit answers "who changed what" from audit logs. Phase 1 reads
// Google Cloud Audit Logs (Admin Activity) in the configured projects: the
// Kubernetes API audit of GKE clusters (always on, kept 400 days) and the
// GCP resources themselves (IAM, disks, node pools, DNS). Where no audit log
// exists (EKS and AKS clusters, whose audit logging is off), the caller falls
// back to the object's managedFields: which tool last set each field, and when,
// but not who. Nothing here writes or changes logging settings.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	logging "google.golang.org/api/logging/v2"
	"google.golang.org/api/option"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

// Event is one change, from any source.
type Event struct {
	Time      time.Time `json:"time"`
	Actor     string    `json:"actor"`              // person or service account; "" when unknown
	ActorType string    `json:"actorType"`          // person, serviceaccount, system, unknown
	Tool      string    `json:"tool,omitempty"`     // kubectl, helm, argocd, terraform… from the user agent
	SourceIP  string    `json:"sourceIp,omitempty"` //
	Action    string    `json:"action"`             // create, update, patch, delete…
	Resource  string    `json:"resource"`           // e.g. core/v1/namespaces/x/persistentvolumeclaims/y
	Cluster   string    `json:"cluster,omitempty"`  //
	Project   string    `json:"project,omitempty"`  //
	Status    string    `json:"status,omitempty"`   // "" when it succeeded, else the error
	Changes   []string  `json:"changes,omitempty"`  // "spec.resources.requests.storage: 10Gi → 100Gi"
	Source    string    `json:"source"`             // gke-audit, gcp-audit, managed-fields
	after     map[string]string
}

// Query selects events. Empty fields match anything.
type Query struct {
	Cluster   string // GKE cluster name (for Kubernetes)
	Kind      string // resource, e.g. pvc, deployments, persistentvolumes
	Namespace string
	Name      string
	Actions   []string // create, update, patch, delete; default all writes
	Actor     string   // substring of the actor
	Since     time.Time
	Until     time.Time
	// IncludeSystem keeps changes by Kubernetes controllers and nodes, which
	// otherwise outnumber people many times over.
	IncludeSystem bool
	Limit         int
}

// Lister reads log entries; *logging.Service in production, a fake in tests.
type Lister interface {
	List(ctx context.Context, projects []string, filter string, max int) ([]*logging.LogEntry, error)
}

// Searcher queries the configured projects.
type Searcher struct {
	Projects []string
	lister   Lister
}

// New uses the pod's Google identity (roles/logging.viewer on each project).
func New(ctx context.Context, projects []string) (*Searcher, error) {
	svc, err := logging.NewService(ctx, option.WithScopes(logging.LoggingReadScope))
	if err != nil {
		return nil, err
	}
	return &Searcher{Projects: projects, lister: googleLister{svc}}, nil
}

// NewWithLister is for tests.
func NewWithLister(projects []string, l Lister) *Searcher {
	return &Searcher{Projects: projects, lister: l}
}

type googleLister struct{ svc *logging.Service }

func (g googleLister) List(ctx context.Context, projects []string, filter string, max int) ([]*logging.LogEntry, error) {
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = "projects/" + p
	}
	var out []*logging.LogEntry
	req := &logging.ListLogEntriesRequest{ResourceNames: names, Filter: filter, OrderBy: "timestamp desc", PageSize: int64(min(max, 1000))}
	err := g.svc.Entries.List(req).Pages(ctx, func(resp *logging.ListLogEntriesResponse) error {
		out = append(out, resp.Entries...)
		if len(out) >= max {
			return errStop
		}
		return nil
	})
	if err == errStop {
		err = nil
	}
	return out, err
}

var errStop = fmt.Errorf("stop")

const (
	defaultLimit = 50
	maxLimit     = 200
	// fetched before the system filter drops controllers
	fetchFactor = 6
	maxChanges  = 25
)

var shortKinds = map[string]string{
	"pvc": "persistentvolumeclaims", "pv": "persistentvolumes", "deploy": "deployments", "sts": "statefulsets",
	"ds": "daemonsets", "rs": "replicasets", "svc": "services", "cm": "configmaps", "ns": "namespaces", "no": "nodes",
	"node": "nodes", "po": "pods", "pod": "pods", "ing": "ingresses", "ingress": "ingresses", "sa": "serviceaccounts",
	"secret": "secrets", "job": "jobs", "cj": "cronjobs", "cronjob": "cronjobs", "hpa": "horizontalpodautoscalers",
	"sc": "storageclasses", "crb": "clusterrolebindings", "rb": "rolebindings", "role": "roles", "clusterrole": "clusterroles",
	"netpol": "networkpolicies", "pdb": "poddisruptionbudgets", "deployment": "deployments", "statefulset": "statefulsets",
	"service": "services", "configmap": "configmaps", "namespace": "namespaces",
}

// Plural turns a kind or short name into the API resource name.
func Plural(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	if p, ok := shortKinds[k]; ok {
		return p
	}
	switch {
	case k == "" || strings.HasSuffix(k, "s"):
		return k
	case strings.HasSuffix(k, "y"):
		return k[:len(k)-1] + "ies"
	}
	return k + "s"
}

var writeActions = []string{"create", "update", "patch", "delete", "deletecollection"}

// quote makes a value safe inside a Logging filter string.
func quote(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }

// KubernetesFilter builds the Logging filter for a query.
func KubernetesFilter(q Query) string {
	parts := []string{`resource.type="k8s_cluster"`, `logName:"cloudaudit.googleapis.com%2Factivity"`}
	if q.Cluster != "" {
		parts = append(parts, `resource.labels.cluster_name=`+quote(q.Cluster))
	}
	acts := q.Actions
	if len(acts) == 0 {
		acts = writeActions
	}
	// [.] rather than \. : the filter string would need a doubled backslash.
	parts = append(parts, `protoPayload.methodName=~`+quote(`[.](`+strings.Join(acts, "|")+`)$`))
	if !q.IncludeSystem {
		parts = append(parts, `NOT protoPayload.methodName:".status."`)
	}
	if path := resourcePath(q); path != "" {
		parts = append(parts, `protoPayload.resourceName:`+quote(path))
	}
	if q.Actor != "" {
		parts = append(parts, `protoPayload.authenticationInfo.principalEmail:`+quote(q.Actor))
	}
	return strings.Join(append(parts, timeRange(q)...), " AND ")
}

func resourcePath(q Query) string {
	plural := Plural(q.Kind)
	var p string
	if q.Namespace != "" {
		p = "namespaces/" + q.Namespace + "/"
	}
	switch {
	case plural != "" && q.Name != "":
		p += plural + "/" + q.Name
	case plural != "":
		p += plural
	case q.Name != "":
		p += q.Name
	default:
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// GCPFilter selects Admin Activity entries for Google Cloud resources (not
// Kubernetes objects) whose resource name contains name.
func GCPFilter(q Query, service string) string {
	parts := []string{`logName:"cloudaudit.googleapis.com%2Factivity"`, `resource.type!="k8s_cluster"`}
	if q.Name != "" {
		parts = append(parts, `protoPayload.resourceName:`+quote(q.Name))
	}
	if service != "" {
		parts = append(parts, `protoPayload.serviceName=`+quote(service))
	}
	if q.Actor != "" {
		parts = append(parts, `protoPayload.authenticationInfo.principalEmail:`+quote(q.Actor))
	}
	return strings.Join(append(parts, timeRange(q)...), " AND ")
}

func timeRange(q Query) []string {
	since := q.Since
	if since.IsZero() {
		since = time.Now().Add(-30 * 24 * time.Hour)
	}
	out := []string{`timestamp>=` + quote(since.UTC().Format(time.RFC3339))}
	if !q.Until.IsZero() {
		out = append(out, `timestamp<=`+quote(q.Until.UTC().Format(time.RFC3339)))
	}
	return out
}

// Kubernetes returns changes to Kubernetes objects on GKE clusters, newest first.
func (s *Searcher) Kubernetes(ctx context.Context, q Query) ([]Event, error) {
	return s.search(ctx, q, KubernetesFilter(q), "gke-audit")
}

// GCP returns changes to Google Cloud resources, newest first.
func (s *Searcher) GCP(ctx context.Context, q Query, service string) ([]Event, error) {
	return s.search(ctx, q, GCPFilter(q, service), "gcp-audit")
}

func (s *Searcher) search(ctx context.Context, q Query, filter, source string) ([]Event, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	fetch := limit
	if !q.IncludeSystem {
		fetch = limit * fetchFactor
	}
	entries, err := s.lister.List(ctx, s.Projects, filter, fetch)
	if err != nil {
		return nil, describe(err)
	}
	var events []Event
	for _, e := range entries {
		ev, ok := toEvent(e, source)
		if !ok {
			continue
		}
		events = append(events, ev)
	}
	withDiffs(events)
	var out []Event
	for _, ev := range events {
		if !q.IncludeSystem && ev.ActorType == "system" {
			continue
		}
		ev.after = nil
		out = append(out, ev)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func describe(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "403") || strings.Contains(strings.ToLower(msg), "permission") {
		return fmt.Errorf("the console's Google identity cannot read audit logs here (needs roles/logging.viewer): %s", redact.LogLine(msg))
	}
	return fmt.Errorf("read audit logs: %s", redact.LogLine(msg))
}

type auditPayload struct {
	MethodName         string `json:"methodName"`
	ResourceName       string `json:"resourceName"`
	ServiceName        string `json:"serviceName"`
	AuthenticationInfo struct {
		PrincipalEmail string `json:"principalEmail"`
	} `json:"authenticationInfo"`
	RequestMetadata struct {
		CallerIP  string `json:"callerIp"`
		UserAgent string `json:"callerSuppliedUserAgent"`
	} `json:"requestMetadata"`
	Status *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
	Request  map[string]any `json:"request"`
	Response map[string]any `json:"response"`
}

func toEvent(e *logging.LogEntry, source string) (Event, bool) {
	var p auditPayload
	if json.Unmarshal(e.ProtoPayload, &p) != nil {
		return Event{}, false
	}
	t, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
	ev := Event{Time: t, Actor: p.AuthenticationInfo.PrincipalEmail, SourceIP: p.RequestMetadata.CallerIP,
		Tool: toolOf(p.RequestMetadata.UserAgent), Resource: p.ResourceName, Source: source}
	ev.ActorType = actorType(ev.Actor)
	ev.Action = action(p.MethodName)
	if e.Resource != nil {
		ev.Cluster = e.Resource.Labels["cluster_name"]
		ev.Project = e.Resource.Labels["project_id"]
	}
	if p.Status != nil && p.Status.Code != 0 {
		ev.Status = fmt.Sprintf("failed (%d): %s", p.Status.Code, redact.LogLine(p.Status.Message))
	}
	secret := strings.Contains(p.ResourceName, "/secrets/")
	if !secret {
		if t, _ := p.Request["@type"].(string); strings.Contains(t, "Patch") {
			for k, v := range flatten(p.Request) {
				ev.Changes = append(ev.Changes, k+": "+v)
			}
			sort.Strings(ev.Changes)
		}
		ev.after = flatten(p.Response)
	} else if p.Request != nil || p.Response != nil {
		ev.Changes = []string{"(Secret contents are not shown)"}
	}
	return ev, true
}

// withDiffs fills Changes from consecutive snapshots of the same object:
// events are newest first, so each one is compared with the next older one.
func withDiffs(events []Event) {
	for i := range events {
		ev := &events[i]
		if ev.after == nil {
			continue
		}
		for j := i + 1; j < len(events); j++ {
			prev := &events[j]
			if prev.Resource != ev.Resource || prev.after == nil {
				continue
			}
			if d := diff(prev.after, ev.after); len(d) > 0 {
				ev.Changes = d
			}
			break
		}
		if len(ev.Changes) > maxChanges {
			ev.Changes = append(ev.Changes[:maxChanges], fmt.Sprintf("…and %d more", len(ev.Changes)-maxChanges))
		}
	}
}

func diff(before, after map[string]string) []string {
	var out []string
	for k, v := range after {
		if b, ok := before[k]; !ok {
			out = append(out, k+": (none) → "+v)
		} else if b != v {
			out = append(out, k+": "+b+" → "+v)
		}
	}
	for k, b := range before {
		if _, ok := after[k]; !ok {
			out = append(out, k+": "+b+" → (removed)")
		}
	}
	sort.Strings(out)
	return out
}

// skipKeys are bookkeeping that changes on every write.
var skipKeys = map[string]bool{"@type": true, "status": true, "metadata.managedFields": true, "metadata.resourceVersion": true,
	"metadata.generation": true, "metadata.uid": true, "metadata.creationTimestamp": true, "apiVersion": true, "kind": true,
	"metadata.annotations.kubectl.kubernetes.io/last-applied-configuration": true}

// flatten turns an object into dotted paths and short values.
func flatten(obj map[string]any) map[string]string {
	if obj == nil {
		return nil
	}
	out := map[string]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if skipKeys[prefix] {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			if len(x) == 0 {
				out[prefix] = "{}"
			}
			for k, c := range x {
				if prefix == "" {
					walk(k, c)
				} else {
					walk(prefix+"."+k, c)
				}
			}
		case []any:
			b, _ := json.Marshal(x)
			out[prefix] = clip(redact.LogLine(string(b)), 200)
		case nil:
			out[prefix] = "null"
		default:
			out[prefix] = clip(redact.LogLine(fmt.Sprint(x)), 200)
		}
	}
	walk("", obj)
	delete(out, "")
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func action(method string) string {
	if i := strings.LastIndex(method, "."); i >= 0 {
		return method[i+1:]
	}
	return method
}

// actorType tells people from Kubernetes' own controllers and nodes.
func actorType(actor string) string {
	switch {
	case actor == "":
		return "unknown"
	case strings.HasPrefix(actor, "system:serviceaccount:kube-system:"), strings.HasPrefix(actor, "system:node"),
		strings.HasPrefix(actor, "system:kube-"), strings.HasPrefix(actor, "system:") && !strings.HasPrefix(actor, "system:serviceaccount:"),
		strings.HasSuffix(actor, "container-engine-robot.iam.gserviceaccount.com"),
		strings.HasSuffix(actor, "compute-system.iam.gserviceaccount.com"):
		return "system"
	case strings.HasPrefix(actor, "system:serviceaccount:"), strings.HasSuffix(actor, ".gserviceaccount.com"):
		return "serviceaccount"
	}
	return "person"
}

var toolRE = regexp.MustCompile(`(?i)^(kubectl|helm|argocd|terraform|kustomize|k9s|lens|gcloud|google-cloud-sdk|kube-controller-manager|karpenter|python|go-http-client|okhttp|Mozilla)`)

func toolOf(ua string) string {
	if m := toolRE.FindString(ua); m != "" {
		if strings.EqualFold(m, "Mozilla") {
			return "Cloud Console"
		}
		return strings.ToLower(m)
	}
	if i := strings.IndexAny(ua, "/ "); i > 0 {
		return clip(ua[:i], 40)
	}
	return clip(ua, 40)
}
