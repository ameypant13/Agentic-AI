package kube

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Change is something that changed in a cluster: a Helm release revision, a
// workload rollout or a node joining. Kubernetes keeps no history beyond these
// objects, so this is what can be reconstructed after the fact.
type Change struct {
	At        time.Time `json:"at"`
	Source    string    `json:"source"` // helm, rollout, node
	Namespace string    `json:"namespace,omitempty"`
	Object    string    `json:"object"`
	What      string    `json:"what"`
}

// maxChanges bounds one timeline.
const maxChanges = 300

// Changes lists the cluster's changes since since, oldest first.
func (c *Cluster) Changes(ctx context.Context, since time.Time) ([]Change, error) {
	var out []Change
	var errs []string

	// Helm: every stored revision (Helm keeps the last few per release).
	if secrets, err := c.k8s.CoreV1().Secrets("").List(ctx, metav1.ListOptions{LabelSelector: "owner=helm"}); err != nil {
		errs = append(errs, "helm: "+err.Error())
	} else {
		for _, s := range secrets.Items {
			rel, err := decodeHelmRelease(s.Data["release"])
			if err != nil {
				continue
			}
			at, err := time.Parse(time.RFC3339, rel.Updated)
			if err != nil {
				at = s.CreationTimestamp.Time
			}
			if at.Before(since) {
				continue
			}
			what := fmt.Sprintf("revision %d: chart %s %s", rel.Revision, rel.Chart, rel.ChartVersion)
			if rel.AppVersion != "" && rel.AppVersion != rel.ChartVersion {
				what += " (app " + rel.AppVersion + ")"
			}
			what += ", " + rel.Status
			if rel.Description != "" {
				what += " — " + rel.Description
			}
			out = append(out, Change{At: at, Source: "helm", Namespace: s.Namespace, Object: "release/" + rel.Name, What: what})
		}
	}

	// Deployment rollouts: each new ReplicaSet, with the images it changed.
	if rss, err := c.k8s.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{}); err != nil {
		errs = append(errs, "replicasets: "+err.Error())
	} else {
		byDeploy := map[string][]appsv1.ReplicaSet{}
		for _, rs := range rss.Items {
			for _, o := range rs.OwnerReferences {
				if o.Kind == "Deployment" {
					k := rs.Namespace + "/" + o.Name
					byDeploy[k] = append(byDeploy[k], rs)
				}
			}
		}
		for k, list := range byDeploy {
			sort.Slice(list, func(i, j int) bool { return revision(&list[i]) < revision(&list[j]) })
			for i, rs := range list {
				if rs.CreationTimestamp.Time.Before(since) {
					continue
				}
				ns, name, _ := strings.Cut(k, "/")
				what := "new ReplicaSet " + rs.Name
				if i > 0 {
					const restarted = "kubectl.kubernetes.io/restartedAt"
					prev := &list[i-1]
					switch d := imageDiff(rsImages(prev), rsImages(&rs)); {
					case d != "":
						what = d
					case rs.Spec.Template.Annotations[restarted] != prev.Spec.Template.Annotations[restarted]:
						what = "restarted (rollout restart, same spec)"
					default:
						what = "pod spec changed, same images (env, resources, volumes or labels)"
					}
				} else {
					what += ": " + strings.Join(rsImages(&rs), ", ")
				}
				out = append(out, Change{At: rs.CreationTimestamp.Time, Source: "rollout", Namespace: ns, Object: "deployment/" + name, What: what})
			}
		}
	}

	// StatefulSet and DaemonSet rollouts: new controller revisions.
	if revs, err := c.k8s.AppsV1().ControllerRevisions("").List(ctx, metav1.ListOptions{}); err != nil {
		errs = append(errs, "controllerrevisions: "+err.Error())
	} else {
		for _, r := range revs.Items {
			if r.CreationTimestamp.Time.Before(since) || r.Revision <= 1 {
				continue // revision 1 is the workload's creation, not a change
			}
			for _, o := range r.OwnerReferences {
				if o.Kind == "StatefulSet" || o.Kind == "DaemonSet" {
					out = append(out, Change{At: r.CreationTimestamp.Time, Source: "rollout", Namespace: r.Namespace,
						Object: strings.ToLower(o.Kind) + "/" + o.Name, What: fmt.Sprintf("rolled out revision %d", r.Revision)})
				}
			}
		}
	}

	// Nodes that joined: node rotation, scale-ups, upgrades.
	if nodes, err := c.k8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err != nil {
		errs = append(errs, "nodes: "+err.Error())
	} else {
		for _, n := range nodes.Items {
			if n.CreationTimestamp.Time.Before(since) {
				continue
			}
			l := n.Labels
			pool := firstLabel(l, "agentpool", "eks.amazonaws.com/nodegroup", "cloud.google.com/gke-nodepool", "karpenter.sh/nodepool")
			what := "node joined"
			if t := l["node.kubernetes.io/instance-type"]; t != "" {
				what += " (" + t
				if pool != "" {
					what += ", pool " + pool
				}
				what += ")"
			}
			what += ", kubelet " + n.Status.NodeInfo.KubeletVersion
			out = append(out, Change{At: n.CreationTimestamp.Time, Source: "node", Object: "node/" + n.Name, What: what})
		}
	}

	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if len(out) > maxChanges {
		out = out[len(out)-maxChanges:]
	}
	return out, nil
}

func revision(rs *appsv1.ReplicaSet) int {
	n, _ := strconv.Atoi(rs.Annotations["deployment.kubernetes.io/revision"])
	return n
}

func rsImages(rs *appsv1.ReplicaSet) []string {
	var out []string
	for _, c := range rs.Spec.Template.Spec.Containers {
		out = append(out, c.Image)
	}
	return out
}

// imageDiff describes changed images: "metagraph: …:4.2.10 → …:4.2.11".
func imageDiff(before, after []string) string {
	repo := func(img string) string {
		if i := strings.LastIndexAny(img, ":@"); i > strings.LastIndex(img, "/") {
			return img[:i]
		}
		return img
	}
	prev := map[string]string{}
	for _, img := range before {
		prev[repo(img)] = img
	}
	var parts []string
	for _, img := range after {
		if old, ok := prev[repo(img)]; !ok {
			parts = append(parts, "added "+img)
		} else if old != img {
			parts = append(parts, old+" → "+img)
		}
	}
	return strings.Join(parts, "; ")
}

func firstLabel(l map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := l[k]; v != "" {
			return v
		}
	}
	return ""
}
