package metricrules

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

const keepResolved = 24 * time.Hour

// Issue is one rule firing for one entity in one cluster.
type Issue struct {
	Instance   string            `json:"instance"`
	Cluster    string            `json:"cluster"` // the metrics `cluster` label
	RuleID     string            `json:"ruleId"`
	Title      string            `json:"title"`
	Severity   string            `json:"severity"`
	Entity     string            `json:"entity"`
	EntityKey  string            `json:"entityKey"`
	EntityName string            `json:"entityName"`
	Namespace  string            `json:"namespace"`
	Pod        string            `json:"pod"`
	Labels     map[string]string `json:"labels"`
	Value      float64           `json:"value"`
	ValueText  string            `json:"valueText"`
	Meaning    string            `json:"meaning"`
	Fix        string            `json:"fix"`
	FirstSeen  time.Time         `json:"firstSeen"`
	LastSeen   time.Time         `json:"lastSeen"`
	Active     bool              `json:"active"`
	ResolvedAt *time.Time        `json:"resolvedAt,omitempty"`
}

func (i *Issue) key() string {
	return i.Instance + "|" + i.Cluster + "|" + i.RuleID + "|" + i.EntityKey
}

// Worker evaluates every rule against every Grafana instance each interval.
type Worker struct {
	hub      *grafana.Hub
	rules    []Rule
	dir      string
	interval time.Duration

	mu          sync.RWMutex
	issues      map[string]*Issue
	evaluatedAt map[string]time.Time // instance -> last complete evaluation
	ruleErrors  map[string]string    // instance|rule -> last error
}

func NewWorker(hub *grafana.Hub, dir string, interval time.Duration) (*Worker, error) {
	rules, err := Defaults()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o755); err != nil {
		return nil, err
	}
	w := &Worker{hub: hub, rules: rules, dir: dir, interval: interval, issues: map[string]*Issue{},
		evaluatedAt: map[string]time.Time{}, ruleErrors: map[string]string{}}
	w.load()
	return w, nil
}

func (w *Worker) Rules() []Rule { return w.rules }

func (w *Worker) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(w.interval)
		defer t.Stop()
		for {
			for _, c := range w.hub.Clients() {
				ectx, cancel := context.WithTimeout(ctx, 2*w.interval)
				w.Evaluate(ectx, c)
				cancel()
			}
			w.save()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// Evaluate runs every rule on one instance and updates issue state. A rule
// whose query fails keeps its previous issues rather than resolving them.
func (w *Worker) Evaluate(ctx context.Context, c *grafana.Client) {
	now := time.Now()
	var events []event
	for _, r := range w.rules {
		res, err := c.Query(ctx, r.Expr)
		w.mu.Lock()
		if err != nil {
			w.ruleErrors[c.Name()+"|"+r.ID] = err.Error()
			w.mu.Unlock()
			continue
		}
		delete(w.ruleErrors, c.Name()+"|"+r.ID)
		seen := map[string]bool{}
		for _, s := range res {
			key, name := EntityKey(r.Entity, s.Labels)
			is := &Issue{Instance: c.Name(), Cluster: s.Labels["cluster"], RuleID: r.ID, EntityKey: key}
			k := is.key()
			seen[k] = true
			if cur, ok := w.issues[k]; ok && cur.Active {
				is = cur
			} else {
				is.FirstSeen = now
				events = append(events, event{At: now, Kind: "opened", Issue: is})
				w.issues[k] = is
			}
			is.Title, is.Severity, is.Entity, is.EntityName = r.Title, r.Severity, r.Entity, name
			is.Namespace, is.Pod, is.Labels = s.Labels["namespace"], s.Labels["pod"], trimLabels(s.Labels)
			is.Value, is.ValueText, is.Meaning, is.Fix = s.Value, FormatValue(r.Value, s.Value), r.Meaning, r.Fix
			is.LastSeen, is.Active, is.ResolvedAt = now, true, nil
		}
		for k, is := range w.issues {
			if is.Instance == c.Name() && is.RuleID == r.ID && is.Active && !seen[k] {
				t := now
				is.Active, is.ResolvedAt = false, &t
				events = append(events, event{At: now, Kind: "resolved", Issue: is})
			}
		}
		w.mu.Unlock()
	}
	w.mu.Lock()
	w.evaluatedAt[c.Name()] = now
	for k, is := range w.issues {
		if !is.Active && is.ResolvedAt != nil && now.Sub(*is.ResolvedAt) > keepResolved {
			delete(w.issues, k)
		}
	}
	w.mu.Unlock()
	w.appendEvents(events)
}

// trimLabels drops labels that only describe the scrape, not the entity.
func trimLabels(l map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range l {
		switch k {
		case "receive", "replica", "prometheus", "prometheus_replica", "tenant_id", "endpoint", "instance", "metrics_path", "service", "__name__":
			continue
		}
		out[k] = v
	}
	return out
}

// Issues returns issues for one instance's cluster label ("" = every cluster), active first.
func (w *Worker) Issues(instance, cluster string) []Issue {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := []Issue{}
	for _, is := range w.issues {
		if (instance == "" || is.Instance == instance) && (cluster == "" || is.Cluster == cluster) {
			out = append(out, *is)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Active != b.Active {
			return a.Active
		}
		if rank(a.Severity) != rank(b.Severity) {
			return rank(a.Severity) < rank(b.Severity)
		}
		return a.FirstSeen.Before(b.FirstSeen)
	})
	return out
}

func (w *Worker) EvaluatedAt(instance string) time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.evaluatedAt[instance]
}

// RuleErrors returns rules whose last query failed, by instance.
func (w *Worker) RuleErrors() map[string]string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := map[string]string{}
	for k, v := range w.ruleErrors {
		out[k] = v
	}
	return out
}

func rank(s string) int {
	switch s {
	case "error":
		return 0
	case "warning":
		return 1
	}
	return 2
}

// --- persistence: current state, and an append-only log of opened/resolved ---

type event struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	Issue *Issue    `json:"issue"`
}

func (w *Worker) appendEvents(evs []event) {
	if len(evs) == 0 {
		return
	}
	path := filepath.Join(w.dir, "events", time.Now().UTC().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("metric worker: %v", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range evs {
		_ = enc.Encode(e)
	}
}

type snapshot struct {
	Issues      []*Issue             `json:"issues"`
	EvaluatedAt map[string]time.Time `json:"evaluatedAt"`
}

func (w *Worker) save() {
	w.mu.RLock()
	snap := snapshot{EvaluatedAt: w.evaluatedAt}
	for _, is := range w.issues {
		snap.Issues = append(snap.Issues, is)
	}
	raw, err := json.Marshal(snap)
	w.mu.RUnlock()
	if err != nil {
		return
	}
	tmp := filepath.Join(w.dir, "state.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o644); err == nil {
		_ = os.Rename(tmp, filepath.Join(w.dir, "state.json"))
	}
}

func (w *Worker) load() {
	raw, err := os.ReadFile(filepath.Join(w.dir, "state.json"))
	if err != nil {
		return
	}
	var snap snapshot
	if json.Unmarshal(raw, &snap) != nil {
		return
	}
	for _, is := range snap.Issues {
		w.issues[is.key()] = is
	}
	if snap.EvaluatedAt != nil {
		w.evaluatedAt = snap.EvaluatedAt
	}
}
