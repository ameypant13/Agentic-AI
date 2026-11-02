package kube

import (
	"bufio"
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Orphaned go-sandboxes. Since orchestrator 4.2.12.1 a run whose new
// go-sandbox pod is Unschedulable fails at once, but the pod stays behind,
// starts when a node frees up, and no run ever uses it. The orchestrator logs
// the name of every pod it gave up on, so a sandbox that still exists after
// such a line is an orphan; nothing else about the pod tells them apart.

const (
	sandboxPrefix     = "go-operator-sandbox-"
	sandboxLookback   = 7 * 24 * time.Hour
	sandboxLogTail    = 200000
	sandboxLogMaxSize = 64 << 20
)

var abandonedSandbox = regexp.MustCompile(`Pod (go-operator-sandbox-[a-z0-9]+) failed to start: Unschedulable`)

// Sandbox is one go-sandbox pod.
type Sandbox struct {
	Name        string  `json:"name"`
	Namespace   string  `json:"namespace"`
	Phase       string  `json:"phase"`
	Node        string  `json:"node"`
	CreatedAt   string  `json:"createdAt"`
	AgeHours    float64 `json:"ageHours"`
	CPURequest  float64 `json:"cpuRequest"` // cores
	MemRequest  float64 `json:"memRequest"` // bytes
	Orphaned    bool    `json:"orphaned"`
	AbandonedAt string  `json:"abandonedAt,omitempty"` // when the orchestrator gave up on it
}

// SandboxReport lists a cluster's go-sandboxes and which are orphaned.
type SandboxReport struct {
	Total    int `json:"total"`
	Orphaned int `json:"orphaned"`
	// What the orphans hold: requested CPU (cores) and memory (bytes).
	OrphanCPU float64   `json:"orphanCpu"`
	OrphanMem float64   `json:"orphanMem"`
	Sandboxes []Sandbox `json:"sandboxes"`
	// LogsRead is false when no orchestrator log could be read; orphans are
	// then unknown, not absent.
	LogsRead  bool     `json:"logsRead"`
	LogErrors []string `json:"logErrors,omitempty"`
}

// Sandboxes finds the cluster's go-sandbox pods and marks the orphans.
func (c *Cluster) Sandboxes(ctx context.Context) (*SandboxReport, error) {
	list, err := c.k8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	rep := &SandboxReport{Sandboxes: []Sandbox{}}
	byName := map[string]*Sandbox{}
	orchestrators := map[string][]*corev1.Pod{} // namespace -> orchestrator pods
	oldest := now
	for i := range list.Items {
		p := &list.Items[i]
		switch {
		case strings.HasPrefix(p.Name, sandboxPrefix) && !terminal(p):
			cpu, mem := podRequests(p)
			rep.Sandboxes = append(rep.Sandboxes, Sandbox{Name: p.Name, Namespace: p.Namespace, Phase: string(p.Status.Phase),
				Node: p.Spec.NodeName, CreatedAt: ts(p.CreationTimestamp.Time), AgeHours: round1(now.Sub(p.CreationTimestamp.Time).Hours()),
				CPURequest: cpu, MemRequest: mem})
			if p.CreationTimestamp.Time.Before(oldest) {
				oldest = p.CreationTimestamp.Time
			}
		case strings.Contains(p.Name, "orchestrator") && p.Status.Phase == corev1.PodRunning:
			orchestrators[p.Namespace] = append(orchestrators[p.Namespace], p)
		}
	}
	rep.Total = len(rep.Sandboxes)
	if rep.Total == 0 {
		rep.LogsRead = true
		return rep, nil
	}
	for i := range rep.Sandboxes {
		byName[rep.Sandboxes[i].Name] = &rep.Sandboxes[i]
	}

	// The orchestrator gave up on a pod right after creating it, so its log
	// only needs to go back to the oldest sandbox still running.
	since := oldest.Add(-time.Minute)
	if now.Sub(since) > sandboxLookback {
		since = now.Add(-sandboxLookback)
	}
	for _, pods := range orchestrators {
		for _, p := range pods {
			err := c.scanLog(ctx, p, since, func(line string) {
				m := abandonedSandbox.FindStringSubmatch(line)
				if m == nil {
					return
				}
				if s := byName[m[1]]; s != nil && !s.Orphaned {
					s.Orphaned = true
					if t, _, ok := strings.Cut(line, " "); ok {
						s.AbandonedAt = t
					}
				}
			})
			if err != nil {
				rep.LogErrors = append(rep.LogErrors, p.Namespace+"/"+p.Name+": "+err.Error())
				continue
			}
			rep.LogsRead = true
		}
	}
	if len(orchestrators) == 0 {
		rep.LogErrors = append(rep.LogErrors, "no running orchestrator pod on this cluster")
	}
	for _, s := range rep.Sandboxes {
		if s.Orphaned {
			rep.Orphaned++
			rep.OrphanCPU += s.CPURequest
			rep.OrphanMem += s.MemRequest
		}
	}
	sort.Slice(rep.Sandboxes, func(i, j int) bool {
		if rep.Sandboxes[i].Orphaned != rep.Sandboxes[j].Orphaned {
			return rep.Sandboxes[i].Orphaned
		}
		return rep.Sandboxes[i].AgeHours > rep.Sandboxes[j].AgeHours
	})
	return rep, nil
}

// scanLog streams one pod's log (with timestamps) since a time, line by line.
func (c *Cluster) scanLog(ctx context.Context, p *corev1.Pod, since time.Time, fn func(string)) error {
	container := p.Spec.Containers[0].Name
	for _, ct := range p.Spec.Containers {
		if strings.Contains(ct.Name, "orchestrator") {
			container = ct.Name
		}
	}
	tail, limit := int64(sandboxLogTail), int64(sandboxLogMaxSize)
	t := metav1.NewTime(since)
	stream, err := c.k8s.CoreV1().Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{Container: container, Timestamps: true,
		SinceTime: &t, TailLines: &tail, LimitBytes: &limit}).Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 256<<10), 1<<20)
	for sc.Scan() {
		fn(sc.Text())
	}
	return sc.Err()
}

// podRequests sums the containers' requested CPU (cores) and memory (bytes).
func podRequests(p *corev1.Pod) (cpu, mem float64) {
	for _, ct := range p.Spec.Containers {
		cpu += float64(ct.Resources.Requests.Cpu().MilliValue()) / 1000
		mem += float64(ct.Resources.Requests.Memory().Value())
	}
	return cpu, mem
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

// DeletePod deletes one pod. It is the only write in this package, used by
// executed console actions (never while the console is read-only).
func (c *Cluster) DeletePod(ctx context.Context, ns, name string) error {
	return c.k8s.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{})
}
