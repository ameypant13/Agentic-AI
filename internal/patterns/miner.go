// Package patterns finds the error messages no log rule knows yet. It groups
// unmatched error-level lines into templates with a small version of the Drain
// algorithm (variable tokens become <*>, lines whose remaining tokens mostly
// agree share a template), and counts each template per cluster and hour, so
// the Log rules page can show what is new or growing. No AI runs here; Claude
// is only asked, on demand, to draft a rule for one template.
package patterns

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

const (
	similarity      = 0.6 // share of equal token positions to join a template
	maxTokens       = 40  // tokens compared; longer lines are cut
	maxPerBucket    = 50  // templates per (service, token count, first token)
	maxTemplates    = 3000
	keepHours       = 48 // hourly counts kept per template
	maxExampleBytes = 400
	wildcard        = "<*>"
)

// Template is one error message shape.
type Template struct {
	ID       string         `json:"id"`
	Service  string         `json:"service"`
	Tokens   []string       `json:"-"`
	Text     string         `json:"template"`
	Example  string         `json:"example"`
	Count    int            `json:"count"`
	Clusters map[string]int `json:"clusters"`
	Hourly   map[int64]int  `json:"hourly"` // unix hour -> lines
	First    time.Time      `json:"firstSeen"`
	Last     time.Time      `json:"lastSeen"`
	dirty    bool
}

// Miner holds the templates in memory.
type Miner struct {
	mu      sync.Mutex
	buckets map[string][]*Template
	byID    map[string]*Template
}

func NewMiner() *Miner {
	return &Miner{buckets: map[string][]*Template{}, byID: map[string]*Template{}}
}

var (
	// Error words as words ("errors" and "errorless" are not errors), plus
	// Java-style class names ending in Exception or Error.
	errorWord = regexp.MustCompile(`(?i)\b(error|fatal|panic|exception|traceback|severe|critical)\b|"level"\s*:\s*"(error|fatal)"|level=(error|fatal)|[a-z](Exception|Error)\b`)
	// A level word near the start of the line, in any case: "[INFO]", " warn ", level=info.
	lowLevel = regexp.MustCompile(`(?i)(^|[\s\[|])(info|debug|trace|warn|warning)([\s\]|:]|$)|level=(info|debug|warn)|"level"\s*:\s*"(info|debug|warn)"`)
	email    = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// Variable tokens: anything with a digit (ids, durations, sizes, ports,
	// timestamps), long hex, UUIDs, emails, URLs and absolute paths.
	variable = regexp.MustCompile(`\d|^[0-9a-f]{12,}$|^[0-9a-f-]{32,}$|@|://|^/|^[A-Za-z]:\\\\`)
	trimTok  = regexp.MustCompile(`^[\[\(\{"'<,;]+|[\]\)\}"'>,;:.]+$`)
)

// IsError reports whether a line reads as an error. level is the line's level
// word when the service's log format gives one.
func IsError(level, line string) bool {
	switch strings.ToLower(level) {
	case "error", "err", "fatal", "critical", "severe", "panic":
		return true
	case "":
		head := line
		if len(head) > 80 {
			head = head[:80]
		}
		if lowLevel.MatchString(head) {
			return false // an explicit lower level wins over "Error" in the message
		}
		return errorWord.MatchString(line)
	}
	return false
}

// Tokenize masks secrets and variable parts and splits a line into tokens.
// JSON lines are reduced to their message: the keys around it (time, caller,
// trace IDs) vary in order and value and would split one error into many
// templates.
func Tokenize(line string) []string {
	fields := strings.Fields(mask(messageText(line)))
	out := make([]string, 0, min(len(fields), maxTokens))
	for _, f := range fields {
		if len(out) == maxTokens {
			break
		}
		core := trimTok.ReplaceAllString(f, "")
		if core == "" || variable.MatchString(core) || strings.Contains(f, "***") {
			out = append(out, wildcard)
			continue
		}
		// Generated pod and object names ("go-operator-sandbox-tdeemg",
		// "postgres-0") keep their stable part.
		if strings.Contains(core, "-") {
			if name := Service(core); name != core {
				out = append(out, name+"-"+wildcard)
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

// messageFields are the JSON keys that carry the error text, in order.
var messageFields = []string{"message", "msg", "error", "err", "exception", "stack_trace"}

// messageText returns a JSON log line's message fields, or the line itself.
func messageText(line string) string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "{") {
		return line
	}
	var obj map[string]any
	if json.Unmarshal([]byte(t), &obj) != nil {
		return line
	}
	var parts []string
	for _, k := range messageFields {
		if v, ok := obj[k].(string); ok && v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return line
	}
	return strings.Join(parts, " ")
}

// mask removes secrets and email addresses: templates and examples are
// stored and may be sent to Claude to draft a rule.
func mask(line string) string { return email.ReplaceAllString(redact.LogLine(line), "<email>") }

// Observe adds one unmatched error line and returns its template.
func (m *Miner) Observe(cluster, service, line string, at time.Time) *Template {
	toks := Tokenize(line)
	if len(toks) < 2 {
		return nil
	}
	first := toks[0]
	if first == wildcard && len(toks) > 1 {
		first = "*" + toks[1]
	}
	key := service + "|" + first + "|" + itoa(len(toks))
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *Template
	bestScore := 0.0
	for _, t := range m.buckets[key] {
		if s := score(t.Tokens, toks); s > bestScore {
			best, bestScore = t, s
		}
	}
	if best == nil || bestScore < similarity {
		if len(m.byID) >= maxTemplates || len(m.buckets[key]) >= maxPerBucket {
			return nil
		}
		best = &Template{ID: templateID(service, toks), Service: service, Tokens: toks, Clusters: map[string]int{}, Hourly: map[int64]int{},
			First: at, Example: clip(mask(line), maxExampleBytes)}
		m.buckets[key] = append(m.buckets[key], best)
		m.byID[best.ID] = best
	} else {
		for i := range best.Tokens {
			if best.Tokens[i] != toks[i] {
				best.Tokens[i] = wildcard
			}
		}
	}
	best.Text = strings.Join(best.Tokens, " ")
	best.Count++
	best.Clusters[cluster]++
	h := at.Unix() / 3600
	best.Hourly[h]++
	for k := range best.Hourly {
		if k < h-keepHours {
			delete(best.Hourly, k)
		}
	}
	if at.After(best.Last) {
		best.Last = at
	}
	if at.Before(best.First) {
		best.First = at
	}
	best.dirty = true
	return best
}

// score is the share of positions where the template and the line agree; a
// wildcard in the template agrees with anything.
func score(tmpl, toks []string) float64 {
	if len(tmpl) != len(toks) || len(toks) == 0 {
		return 0
	}
	same := 0
	for i := range toks {
		if tmpl[i] == toks[i] || tmpl[i] == wildcard {
			same++
		}
	}
	return float64(same) / float64(len(toks))
}

// Dirty returns copies of the templates changed since the last call.
func (m *Miner) Dirty() []Template {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Template
	for _, t := range m.byID {
		if t.dirty {
			t.dirty = false
			c := *t
			c.Clusters, c.Hourly = copyMap(t.Clusters), copyMap(t.Hourly)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Restore loads a template saved earlier (at startup).
func (m *Miner) Restore(t Template) {
	t.Tokens = strings.Fields(t.Text)
	if len(t.Tokens) < 2 {
		return
	}
	first := t.Tokens[0]
	if first == wildcard && len(t.Tokens) > 1 {
		first = "*" + t.Tokens[1]
	}
	key := t.Service + "|" + first + "|" + itoa(len(t.Tokens))
	if t.Clusters == nil {
		t.Clusters = map[string]int{}
	}
	if t.Hourly == nil {
		t.Hourly = map[int64]int{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[t.ID]; ok {
		return
	}
	tt := t
	m.buckets[key] = append(m.buckets[key], &tt)
	m.byID[t.ID] = &tt
}

// templateID is stable for a template's first shape, so restarts keep IDs.
func templateID(service string, toks []string) string {
	h := sha1.Sum([]byte(service + "\x00" + strings.Join(toks, " ")))
	return "P-" + hex.EncodeToString(h[:])[:10]
}

// Trend is a template's activity: lines in the last 6 h and 24 h, and whether
// it first appeared within the last day.
func (t *Template) Trend(now time.Time) (last6h, last24h int, isNew bool) {
	h := now.Unix() / 3600
	for k, v := range t.Hourly {
		if k > h-6 {
			last6h += v
		}
		if k > h-24 {
			last24h += v
		}
	}
	return last6h, last24h, now.Sub(t.First) < 24*time.Hour
}

func copyMap[K comparable](m map[K]int) map[K]int {
	out := make(map[K]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Service names the workload a pod belongs to: the pod name without the
// ReplicaSet hash and pod suffix, or the StatefulSet ordinal.
func Service(pod string) string {
	parts := strings.Split(pod, "-")
	for len(parts) > 1 {
		last := parts[len(parts)-1]
		if isOrdinal(last) || isPodSuffix(last) || (isSandboxSuffix(last) && strings.HasSuffix(parts[len(parts)-2], "sandbox")) {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	return strings.Join(parts, "-")
}

// isSandboxSuffix recognises the random lowercase suffix sandbox pods get,
// vowels included ("go-operator-sandbox-fiaxyh"); only used after "sandbox".
func isSandboxSuffix(s string) bool {
	if len(s) < 5 || len(s) > 8 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func isOrdinal(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// isPodSuffix recognises generated name parts: ReplicaSet hashes and pod
// suffixes (no vowels, from Kubernetes' safe alphabet), and the random
// lowercase-and-digit suffixes sandbox pods get ("ekr5cy", "0ntwm0").
func isPodSuffix(s string) bool {
	if len(s) < 4 || len(s) > 10 {
		return false
	}
	digits, vowels := 0, 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case strings.ContainsRune("aeiou", r):
			vowels++
		case r < 'a' || r > 'z':
			return false
		}
	}
	return digits > 0 || vowels == 0
}
