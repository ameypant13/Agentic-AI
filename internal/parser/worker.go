package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/logrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/patterns"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// The worker is the only thing that parses cluster logs. Every Interval it
// reads the lines each container wrote since the previous cycle, matches the
// rules, appends new hits to an append-only file per cluster and day, and
// keeps a per-cluster summary that pages read. Clicking around the UI never
// starts a scan, so many users cost the clusters nothing extra.

const (
	activeWindow    = 60 * time.Minute // counts shown in the UI cover this window
	resolvedAfter   = 10 * time.Minute // a rule not seen for this long is "resolved"
	firstReadTail   = 500              // lines read the first time a container is seen
	cycleTail       = 300              // lines read per cycle; more means we may have missed some
	cycleBytes      = 512 << 10
	catchUpBytes    = 2 << 20
	clusterParallel = 4
	maxSample       = 500
	forgetPodsAfter = 24 * time.Hour
)

type WorkerConfig struct {
	Dir           string        // root on the persistent volume
	Interval      time.Duration // 60s by default
	Namespaces    []string      // namespaces scanned on every cluster
	RetentionDays int           // hit files older than this are deleted
	Parallel      int           // concurrent log reads across all clusters
	// PatternIgnore are services whose errors are not mined into patterns
	// (test runners such as sla-tests log expected failures as ERROR).
	PatternIgnore []string
}

// RuleState is one rule's matches in one pod.
type RuleState struct {
	RuleID    string        `json:"ruleId"`
	Title     string        `json:"title"`
	Severity  string        `json:"severity"`
	Meaning   string        `json:"meaning"`
	Fix       string        `json:"fix"`
	FirstSeen time.Time     `json:"firstSeen"`
	LastSeen  time.Time     `json:"lastSeen"`
	Total     int           `json:"total"`
	Minutes   map[int64]int `json:"minutes"` // unix minute -> matches, last activeWindow only
	Sample    string        `json:"sample"`
}

type PodState struct {
	Rules    map[string]*RuleState `json:"rules"`
	LastTS   map[string]time.Time  `json:"lastTs"` // container -> newest line read
	Restarts int32                 `json:"restarts"`
	Seen     time.Time             `json:"seen"` // last cycle the pod existed
}

type ClusterState struct {
	ClusterID  string    `json:"clusterId"`
	Source     string    `json:"source"`     // "kubernetes" or "loki:<instance>"
	LokiCursor time.Time `json:"lokiCursor"` // logs read from Loki up to here
	// ErrorSampleAt is when error lines were last sampled from Loki for the pattern miner.
	ErrorSampleAt time.Time                       `json:"errorSampleAt,omitempty"`
	Name          string                          `json:"name"`
	ScannedAt     time.Time                       `json:"scannedAt"`
	Error         string                          `json:"error,omitempty"`
	Namespaces    map[string]map[string]*PodState `json:"namespaces"` // ns -> pod -> state
}

// PodHit is one rule's status in a pod, as the API shows it.
type PodHit struct {
	RuleID    string    `json:"ruleId"`
	Title     string    `json:"title"`
	Severity  string    `json:"severity"`
	Meaning   string    `json:"meaning"`
	Fix       string    `json:"fix"`
	Count     int       `json:"count"` // matches in the last activeWindow
	Total     int       `json:"total"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Active    bool      `json:"active"`
	Sample    string    `json:"sample"`
}

type PodScan struct {
	Pod      string   `json:"pod"`
	Errors   int      `json:"errors"`
	Warnings int      `json:"warnings"`
	Top      string   `json:"top"`
	Hits     []PodHit `json:"hits"`
	Error    string   `json:"error,omitempty"`
}

type NamespaceScan struct {
	Namespace string    `json:"namespace"`
	Window    string    `json:"window"`
	ScannedAt time.Time `json:"scannedAt"` // zero until the first cycle finished
	Pending   bool      `json:"pending"`   // the worker has not scanned this cluster yet
	Source    string    `json:"source"`    // "kubernetes" or "loki:<instance>"
	Error     string    `json:"error,omitempty"`
	Pods      []PodScan `json:"pods"`
}

type Worker struct {
	cfg      WorkerConfig
	lib      *Library
	st       *store.Store
	registry *kube.Registry
	hub      *grafana.Hub    // optional: read logs from Loki for clusters Grafana covers
	patterns *patterns.Miner // optional: groups error lines no rule matched

	mu       sync.RWMutex
	clusters map[string]kube.Infra
	state    map[string]*ClusterState
	running  map[string]bool
	sem      chan struct{}
}

func NewWorker(cfg WorkerConfig, lib *Library, st *store.Store, registry *kube.Registry) (*Worker, error) {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.Parallel <= 0 {
		cfg.Parallel = 16
	}
	if len(cfg.Namespaces) == 0 {
		cfg.Namespaces = []string{"prophecy"}
	}
	for _, d := range []string{"state", "hits"} {
		if err := os.MkdirAll(filepath.Join(cfg.Dir, d), 0o755); err != nil {
			return nil, err
		}
	}
	w := &Worker{cfg: cfg, lib: lib, st: st, registry: registry, clusters: map[string]kube.Infra{},
		state: map[string]*ClusterState{}, running: map[string]bool{}, sem: make(chan struct{}, cfg.Parallel)}
	w.load()
	return w, nil
}

// WithPatterns collects error lines no rule matched into templates.
func (w *Worker) WithPatterns(m *patterns.Miner) *Worker {
	w.patterns = m
	return w
}

// unmatched feeds an error line no rule matched to the pattern miner. Only
// Prophecy's namespaces count: Loki also returns logging, monitoring and test
// workloads (loki-read, sla-tests…), whose errors are not ours to triage.
func (w *Worker) unmatched(set *logrules.Set, cluster, ns, pod, line string, ts time.Time) {
	if w.patterns == nil || !w.minesNamespace(ns) || !patterns.IsError(set.Level(pod, line), line) {
		return
	}
	service := patterns.Service(pod)
	for _, ig := range w.cfg.PatternIgnore {
		if ig == service {
			return
		}
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	w.patterns.Observe(cluster, service, line, ts)
}

// minesNamespace reports whether error patterns are collected from ns: the
// configured namespaces and every prophecy* namespace (prophecy-execution on
// execution planes). An unknown namespace (no label) is kept.
func (w *Worker) minesNamespace(ns string) bool {
	if ns == "" || strings.HasPrefix(ns, "prophecy") {
		return true
	}
	for _, n := range w.cfg.Namespaces {
		if n == ns {
			return true
		}
	}
	return false
}

// Track adds a cluster to the scan list. Clusters are learned as users list
// them, so the worker needs no control-hub credentials of its own; what each
// user may see is still checked per request.
func (w *Worker) Track(in kube.Infra) {
	w.mu.Lock()
	_, known := w.clusters[in.ID]
	w.clusters[in.ID] = in
	w.mu.Unlock()
	if !known {
		w.saveClusters()
	}
}

// Clusters returns the tracked clusters.
func (w *Worker) Clusters() []kube.Infra {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]kube.Infra, 0, len(w.clusters))
	for _, in := range w.clusters {
		out = append(out, in)
	}
	return out
}

// Start runs a cycle every Interval until ctx ends.
func (w *Worker) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(w.cfg.Interval)
		defer t.Stop()
		lastPrune := time.Time{}
		for {
			w.Cycle(ctx)
			if time.Since(lastPrune) > 6*time.Hour {
				w.prune()
				lastPrune = time.Now()
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// Cycle scans every tracked cluster that is not still busy with the previous cycle.
func (w *Worker) Cycle(ctx context.Context) {
	w.mu.Lock()
	var todo []kube.Infra
	for id, in := range w.clusters {
		if !w.running[id] {
			w.running[id] = true
			todo = append(todo, in)
		}
	}
	w.mu.Unlock()

	sem := make(chan struct{}, clusterParallel)
	var wg sync.WaitGroup
	for _, in := range todo {
		wg.Add(1)
		go func(in kube.Infra) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 3*w.cfg.Interval)
			defer cancel()
			w.scanCluster(cctx, in)
			w.mu.Lock()
			w.running[in.ID] = false
			w.mu.Unlock()
		}(in)
	}
	wg.Wait()
}

func (w *Worker) scanCluster(ctx context.Context, in kube.Infra) {
	w.mu.Lock()
	cs := w.state[in.ID]
	if cs == nil {
		cs = &ClusterState{ClusterID: in.ID, Namespaces: map[string]map[string]*PodState{}}
		w.state[in.ID] = cs
	}
	w.mu.Unlock()

	set := w.lib.Set()
	now := time.Now()
	if w.hub != nil {
		if m := w.hub.Resolve(in.ClusterName); m != nil && m.LokiCluster != "" {
			records, err := w.scanClusterLoki(ctx, in, m, cs, set)
			w.finishCycle(in, cs, now, records, err)
			return
		}
	}

	cl, err := w.registry.Client(ctx, in)
	if err != nil {
		w.setError(cs, in, err)
		return
	}
	var records []hitRecord
	var recMu sync.Mutex

	for _, ns := range w.cfg.Namespaces {
		pods, err := cl.Pods(ctx, ns)
		if err != nil {
			continue // namespace absent on this cluster
		}
		w.mu.Lock()
		nsState := cs.Namespaces[ns]
		if nsState == nil {
			nsState = map[string]*PodState{}
			cs.Namespaces[ns] = nsState
		}
		for _, p := range pods {
			ps := nsState[p.Name]
			if ps == nil {
				ps = &PodState{Rules: map[string]*RuleState{}, LastTS: map[string]time.Time{}}
				nsState[p.Name] = ps
			}
			ps.Seen = now
		}
		w.mu.Unlock()

		var wg sync.WaitGroup
		for _, p := range pods {
			w.mu.RLock()
			ps := nsState[p.Name]
			// Read the crashed run once per restart, and on first sight of a pod that is crash-looping.
			restarted := (len(ps.LastTS) > 0 && p.Restarts > ps.Restarts) || (len(ps.LastTS) == 0 && p.Restarts > 0 && notReady(p))
			w.mu.RUnlock()
			for _, c := range p.ContainerNames {
				wg.Add(1)
				go func(pod kube.Pod, container string, ps *PodState, restarted bool) {
					defer wg.Done()
					w.sem <- struct{}{}
					defer func() { <-w.sem }()
					recs := w.scanContainer(ctx, cl, set, in.ClusterName, ns, pod.Name, container, ps, restarted)
					recMu.Lock()
					records = append(records, recs...)
					recMu.Unlock()
				}(p, c, ps, restarted)
			}
			w.mu.Lock()
			ps.Restarts = p.Restarts
			w.mu.Unlock()
		}
		wg.Wait()
	}

	w.mu.Lock()
	cs.Source = "kubernetes"
	w.mu.Unlock()
	w.finishCycle(in, cs, now, records, nil)
}

// finishCycle stores a cycle's results: summary, append-only hits, counters.
func (w *Worker) finishCycle(in kube.Infra, cs *ClusterState, now time.Time, records []hitRecord, cycleErr error) {
	w.mu.Lock()
	cs.Name = in.ClusterName
	if cycleErr != nil {
		cs.Error = cycleErr.Error()
	} else {
		cs.ScannedAt, cs.Error = now, ""
	}
	trimState(cs, now)
	snapshot, _ := json.Marshal(cs)
	w.mu.Unlock()

	if w.patterns != nil {
		w.patterns.Save(w.st)
	}
	if err := w.appendHits(in.ID, records); err != nil {
		log.Printf("log worker: write hits for %s: %v", in.ClusterName, err)
	}
	if err := writeFileAtomic(filepath.Join(w.cfg.Dir, "state", in.ID+".json"), snapshot); err != nil {
		log.Printf("log worker: save state for %s: %v", in.ClusterName, err)
	}
	w.recordCounts(in.ID, records)
}

// Namespaces lists the namespaces with results for a cluster (Loki-backed
// clusters cover every non-system namespace).
func (w *Worker) Namespaces(clusterID string) []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var out []string
	if cs := w.state[clusterID]; cs != nil {
		for ns := range cs.Namespaces {
			out = append(out, ns)
		}
	}
	sort.Strings(out)
	return out
}

// scanContainer reads lines newer than the last cycle's and matches them.
func (w *Worker) scanContainer(ctx context.Context, cl *kube.Cluster, set *logrules.Set, cluster, ns, pod, container string, ps *PodState, restarted bool) []hitRecord {
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	w.mu.RLock()
	last := ps.LastTS[container]
	w.mu.RUnlock()

	var lines []kube.TimedLine
	var err error
	if last.IsZero() {
		lines, err = cl.TimedLogs(lctx, ns, pod, container, nil, firstReadTail, cycleBytes, false)
	} else {
		lines, err = cl.TimedLogs(lctx, ns, pod, container, nil, cycleTail, cycleBytes, false)
		if err == nil && len(lines) > 0 && !lines[0].TS.IsZero() && lines[0].TS.After(last) {
			// Every line of the tail is new: more was written than we read. Catch up from last.
			lines, err = cl.TimedLogs(lctx, ns, pod, container, &last, 0, catchUpBytes, false)
		}
	}
	if err != nil {
		return nil
	}
	fresh := newLines(lines, last)

	if restarted { // the run that crashed is where the fatal error is
		if prev, err := cl.TimedLogs(lctx, ns, pod, container, nil, cycleTail, cycleBytes, true); err == nil {
			fresh = append(fresh, prev...)
		}
	}

	var out []hitRecord
	newest := last
	for _, l := range fresh {
		if l.TS.After(newest) {
			newest = l.TS
		}
		matched := set.Match(pod, l.Text)
		if len(matched) == 0 {
			w.unmatched(set, cluster, ns, pod, l.Text, l.TS)
		}
		for _, i := range matched {
			r := set.Rules()[i]
			ts := l.TS
			if ts.IsZero() {
				ts = time.Now()
			}
			out = append(out, hitRecord{TS: ts, Namespace: ns, Pod: pod, Container: container, RuleID: r.ID,
				Severity: r.Severity, Line: truncate(redact.LogLine(l.Text), maxSample)})
			w.mu.Lock()
			rs := ps.Rules[r.ID]
			if rs == nil {
				rs = &RuleState{RuleID: r.ID, FirstSeen: ts, Minutes: map[int64]int{}}
				ps.Rules[r.ID] = rs
			}
			rs.Title, rs.Severity, rs.Meaning, rs.Fix = r.Title, r.Severity, r.Meaning, r.Fix
			rs.Total++
			rs.Minutes[ts.Unix()/60]++
			if ts.After(rs.LastSeen) {
				rs.LastSeen, rs.Sample = ts, truncate(redact.LogLine(l.Text), maxSample)
			}
			w.mu.Unlock()
		}
	}
	w.mu.Lock()
	if newest.After(ps.LastTS[container]) {
		ps.LastTS[container] = newest
	}
	w.mu.Unlock()
	return out
}

func notReady(p kube.Pod) bool {
	var ready, total int
	_, err := fmt.Sscanf(p.Ready, "%d/%d", &ready, &total)
	return err == nil && ready < total
}

// newLines keeps lines after last; continuation lines (no timestamp) follow
// the line before them.
func newLines(lines []kube.TimedLine, last time.Time) []kube.TimedLine {
	if last.IsZero() {
		return lines
	}
	var out []kube.TimedLine
	keep := false
	for _, l := range lines {
		if !l.TS.IsZero() {
			keep = l.TS.After(last)
		}
		if keep {
			out = append(out, l)
		}
	}
	return out
}

// trimState drops minute buckets outside the window and pods gone for a day.
func trimState(cs *ClusterState, now time.Time) {
	oldest := now.Add(-activeWindow).Unix() / 60
	for _, pods := range cs.Namespaces {
		for name, ps := range pods {
			for _, rs := range ps.Rules {
				for m := range rs.Minutes {
					if m < oldest {
						delete(rs.Minutes, m)
					}
				}
			}
			if now.Sub(ps.Seen) > forgetPodsAfter {
				delete(pods, name)
			}
		}
	}
}

func (w *Worker) setError(cs *ClusterState, in kube.Infra, err error) {
	w.mu.Lock()
	cs.Name, cs.Error = in.ClusterName, err.Error()
	w.mu.Unlock()
	w.registry.Forget(in.ID)
}

// Summary is what the API serves: stored results only, never a scan.
func (w *Worker) Summary(clusterID, ns string) *NamespaceScan {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := &NamespaceScan{Namespace: ns, Window: "last " + activeWindow.String(), Pods: []PodScan{}}
	if cs := w.state[clusterID]; cs != nil {
		out.Source = cs.Source
	}
	cs := w.state[clusterID]
	if cs == nil || cs.ScannedAt.IsZero() {
		out.Pending = true
		if cs != nil {
			out.Error = cs.Error
		}
		return out
	}
	out.ScannedAt, out.Error = cs.ScannedAt, cs.Error
	now := time.Now()
	oldest := now.Add(-activeWindow).Unix() / 60
	for pod, ps := range cs.Namespaces[ns] {
		p := PodScan{Pod: pod, Hits: []PodHit{}}
		for _, rs := range ps.Rules {
			count := 0
			for m, n := range rs.Minutes {
				if m >= oldest {
					count += n
				}
			}
			active := now.Sub(rs.LastSeen) < resolvedAfter
			p.Hits = append(p.Hits, PodHit{RuleID: rs.RuleID, Title: rs.Title, Severity: rs.Severity, Meaning: rs.Meaning,
				Fix: rs.Fix, Count: count, Total: rs.Total, FirstSeen: rs.FirstSeen, LastSeen: rs.LastSeen, Active: active, Sample: rs.Sample})
			if count > 0 {
				switch rs.Severity {
				case "error":
					p.Errors += count
				case "warning":
					p.Warnings += count
				}
			}
		}
		sort.Slice(p.Hits, func(i, j int) bool {
			a, b := p.Hits[i], p.Hits[j]
			if a.Active != b.Active {
				return a.Active
			}
			if sevRank(a.Severity) != sevRank(b.Severity) {
				return sevRank(a.Severity) < sevRank(b.Severity)
			}
			return a.Count > b.Count
		})
		if len(p.Hits) > 0 && p.Hits[0].Count > 0 {
			p.Top = p.Hits[0].Title
		}
		out.Pods = append(out.Pods, p)
	}
	sort.Slice(out.Pods, func(i, j int) bool { return out.Pods[i].Pod < out.Pods[j].Pod })
	return out
}

func sevRank(s string) int {
	switch s {
	case "error":
		return 0
	case "warning":
		return 1
	}
	return 2
}

// --- persistence ---

type hitRecord struct {
	TS        time.Time `json:"ts"`
	Namespace string    `json:"ns"`
	Pod       string    `json:"pod"`
	Container string    `json:"container"`
	RuleID    string    `json:"rule"`
	Severity  string    `json:"severity"`
	Line      string    `json:"line"`
}

// appendHits appends records to hits/<cluster>/<YYYY-MM-DD>.jsonl. Lines are
// only ever added, never rewritten.
func (w *Worker) appendHits(clusterID string, recs []hitRecord) error {
	if len(recs) == 0 {
		return nil
	}
	byDay := map[string][]hitRecord{}
	for _, r := range recs {
		day := r.TS.UTC().Format("2006-01-02")
		byDay[day] = append(byDay[day], r)
	}
	dir := filepath.Join(w.cfg.Dir, "hits", clusterID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for day, rs := range byDay {
		f, err := os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		for _, r := range rs {
			if err := enc.Encode(r); err != nil {
				f.Close()
				return err
			}
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// recordCounts adds this cycle's new matches to the "hits in 24 h" counters.
func (w *Worker) recordCounts(clusterID string, recs []hitRecord) {
	type key struct{ ns, pod, rule string }
	counts := map[key]int{}
	for _, r := range recs {
		counts[key{r.Namespace, r.Pod, r.RuleID}]++
	}
	for k, n := range counts {
		if err := w.st.AddLogHits(k.rule, clusterID, k.ns, k.pod, n); err != nil {
			log.Printf("log worker: record counts: %v", err)
			return
		}
	}
}

func (w *Worker) saveClusters() {
	w.mu.RLock()
	list := make([]kube.Infra, 0, len(w.clusters))
	for _, in := range w.clusters {
		list = append(list, in)
	}
	w.mu.RUnlock()
	raw, _ := json.Marshal(list)
	if err := writeFileAtomic(filepath.Join(w.cfg.Dir, "state", "clusters.json"), raw); err != nil {
		log.Printf("log worker: save cluster list: %v", err)
	}
}

// load restores the cluster list and summaries after a restart.
func (w *Worker) load() {
	if raw, err := os.ReadFile(filepath.Join(w.cfg.Dir, "state", "clusters.json")); err == nil {
		var list []kube.Infra
		if json.Unmarshal(raw, &list) == nil {
			for _, in := range list {
				w.clusters[in.ID] = in
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join(w.cfg.Dir, "state"))
	for _, e := range entries {
		if e.Name() == "clusters.json" || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(w.cfg.Dir, "state", e.Name()))
		if err != nil {
			continue
		}
		var cs ClusterState
		if json.Unmarshal(raw, &cs) == nil && cs.ClusterID != "" {
			w.state[cs.ClusterID] = &cs
		}
	}
}

// prune deletes hit files older than the retention period and old counters.
func (w *Worker) prune() {
	cutoff := time.Now().AddDate(0, 0, -w.cfg.RetentionDays).UTC().Format("2006-01-02")
	dirs, _ := os.ReadDir(filepath.Join(w.cfg.Dir, "hits"))
	for _, d := range dirs {
		files, _ := os.ReadDir(filepath.Join(w.cfg.Dir, "hits", d.Name()))
		for _, f := range files {
			if day := strings.TrimSuffix(f.Name(), ".jsonl"); day < cutoff {
				_ = os.Remove(filepath.Join(w.cfg.Dir, "hits", d.Name(), f.Name()))
			}
		}
	}
	_ = w.st.PruneLogHits(time.Now().Add(-hitRetention))
	_ = w.st.PruneLogPatterns(time.Now().Add(-patternRetention))
}

// patternRetention drops error patterns nobody acted on once they stop appearing.
const patternRetention = 14 * 24 * time.Hour

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
