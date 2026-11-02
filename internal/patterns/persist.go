package patterns

import (
	"encoding/json"
	"log"
	"strconv"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Save writes the templates changed since the last save.
func (m *Miner) Save(st *store.Store) {
	dirty := m.Dirty()
	if len(dirty) == 0 {
		return
	}
	rows := make([]store.LogPattern, 0, len(dirty))
	for _, t := range dirty {
		clusters, _ := json.Marshal(t.Clusters)
		hourly := map[string]int{}
		for h, n := range t.Hourly {
			hourly[strconv.FormatInt(h, 10)] = n
		}
		hb, _ := json.Marshal(hourly)
		rows = append(rows, store.LogPattern{ID: t.ID, Service: t.Service, Template: t.Text, Example: t.Example, Count: t.Count,
			Clusters: string(clusters), Hourly: string(hb), FirstSeen: t.First, LastSeen: t.Last, Status: "new", UpdatedAt: time.Now()})
	}
	if err := st.SaveLogPatternCounts(rows); err != nil {
		log.Printf("patterns: save: %v", err)
	}
}

// version changes when grouping changes; Load then drops unreviewed templates
// grouped the old way rather than keep near-duplicates of the new ones.
const version = 4

// Load restores saved templates, so counts and IDs survive restarts.
func (m *Miner) Load(st *store.Store) {
	var v int
	if ok, _ := st.GetKV("log_patterns_version", &v); !ok || v < version {
		if err := st.DeleteLogPatterns("new"); err != nil {
			log.Printf("patterns: reset: %v", err)
		}
		_ = st.PutKV("log_patterns_version", version)
	}
	rows, err := st.ListLogPatterns("")
	if err != nil {
		log.Printf("patterns: load: %v", err)
		return
	}
	for _, r := range rows {
		m.Restore(FromRow(r))
	}
}

// FromRow turns a stored pattern back into a Template.
func FromRow(r store.LogPattern) Template {
	t := Template{ID: r.ID, Service: r.Service, Text: r.Template, Example: r.Example, Count: r.Count, First: r.FirstSeen, Last: r.LastSeen,
		Clusters: map[string]int{}, Hourly: map[int64]int{}}
	_ = json.Unmarshal([]byte(r.Clusters), &t.Clusters)
	var hourly map[string]int
	_ = json.Unmarshal([]byte(r.Hourly), &hourly)
	for k, v := range hourly {
		if h, err := strconv.ParseInt(k, 10, 64); err == nil {
			t.Hourly[h] = v
		}
	}
	return t
}
