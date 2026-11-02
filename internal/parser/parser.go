// Package parser runs the log rules (internal/logrules) over cluster logs: it
// keeps the effective rule library (built-ins plus admin changes), and its
// Worker parses every cluster's new log lines in the background.
package parser

import (
	"encoding/json"
	"errors"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/logrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

const hitRetention = 7 * 24 * time.Hour // "hits in 24 h" counters kept this long

var ErrNotFound = errors.New("rule not found")

// Library is the effective rule set: embedded defaults with admin overrides,
// custom rules and disables layered on top.
type Library struct {
	st       *store.Store
	defaults *logrules.File

	mu    sync.RWMutex
	rules []logrules.Rule // effective, enabled or not, sorted by id
	set   *logrules.Set   // compiled enabled rules
}

func NewLibrary(st *store.Store) (*Library, error) {
	def, err := logrules.Defaults()
	if err != nil {
		return nil, err
	}
	l := &Library{st: st, defaults: def}
	return l, l.Reload()
}

// Reload rebuilds the effective rules from the defaults and the database.
func (l *Library) Reload() error {
	rows, err := l.st.ListLogRules()
	if err != nil {
		return err
	}
	byID := map[string]logrules.Rule{}
	for _, r := range l.defaults.Rules {
		byID[r.ID] = r
	}
	for _, row := range rows {
		var r logrules.Rule
		if err := json.Unmarshal([]byte(row.RuleJSON), &r); err != nil {
			log.Printf("log rule %s: bad stored JSON: %v", row.ID, err)
			continue
		}
		_, builtin := byID[row.ID]
		edited := builtin && r.Pattern != ""
		if r.Pattern == "" && builtin { // a disable-only row keeps the built-in rule
			r = byID[row.ID]
		}
		r.ID, r.Builtin, r.Enabled, r.Edited = row.ID, builtin, row.Enabled, edited
		r.Normalize()
		byID[row.ID] = r
	}
	rules := make([]logrules.Rule, 0, len(byID))
	for _, r := range byID {
		rules = append(rules, r)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	set, errs := logrules.Compile(rules, l.defaults.Formats)
	for _, e := range errs {
		log.Printf("log rule skipped: %v", e)
	}
	l.mu.Lock()
	l.rules, l.set = rules, set
	l.mu.Unlock()
	return nil
}

func (l *Library) Rules() []logrules.Rule {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]logrules.Rule(nil), l.rules...)
}

func (l *Library) Set() *logrules.Set {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.set
}

// Save stores a custom rule or an edit of a built-in one, after validating it.
func (l *Library) Save(r logrules.Rule, by string) error {
	r.Builtin, r.Enabled = false, true
	if err := logrules.Validate(r); err != nil {
		return err
	}
	raw, _ := json.Marshal(r)
	if err := l.st.SaveLogRule(&store.LogRule{ID: r.ID, RuleJSON: string(raw), Enabled: true, UpdatedBy: by, UpdatedAt: time.Now()}); err != nil {
		return err
	}
	return l.Reload()
}

// SetEnabled turns a rule on or off. For a built-in rule with no edit, only
// the enabled flag is stored.
func (l *Library) SetEnabled(id string, enabled bool, by string) error {
	row, err := l.st.GetLogRule(id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if !l.isBuiltin(id) {
			return ErrNotFound
		}
		row = &store.LogRule{ID: id, RuleJSON: "{}"}
	case err != nil:
		return err
	}
	row.Enabled, row.UpdatedBy, row.UpdatedAt = enabled, by, time.Now()
	if err := l.st.SaveLogRule(row); err != nil {
		return err
	}
	return l.Reload()
}

// Reset removes an admin change: a custom rule is deleted, a built-in returns to its default.
func (l *Library) Reset(id string) error {
	if _, err := l.st.GetLogRule(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := l.st.DeleteLogRule(id); err != nil {
		return err
	}
	return l.Reload()
}

func (l *Library) isBuiltin(id string) bool {
	for _, r := range l.defaults.Rules {
		if r.ID == id {
			return true
		}
	}
	return false
}

// Test runs one line against the enabled rules, optionally for a given pod.
func (l *Library) Test(pod, line string) []logrules.Rule {
	set := l.Set()
	var out []logrules.Rule
	if pod == "" {
		// Without a pod, ignore service scoping so any matching rule is shown.
		for i, r := range set.Rules() {
			if set.MatchRule(i, line) {
				out = append(out, r)
			}
		}
		return out
	}
	for _, i := range set.Match(pod, line) {
		out = append(out, set.Rules()[i])
	}
	return out
}
