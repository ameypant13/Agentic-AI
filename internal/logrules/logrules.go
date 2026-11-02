// Package logrules is the log parser: it matches log lines against a library
// of known messages (rules.yaml, embedded; see FORMAT.md). No AI is involved;
// the Copilot only explains what a hit means.
package logrules

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

//go:embed rules.yaml
var defaultRules []byte

type Rule struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Services    []string `json:"services"`
	Pattern     string   `json:"pattern"`
	Severity    string   `json:"severity"`
	Meaning     string   `json:"meaning"`
	Fix         string   `json:"fix"`
	Source      string   `json:"source"`
	Examples    []string `json:"examples"`
	NotExamples []string `json:"not_examples"`

	// Set by the console, not the YAML.
	Builtin bool `json:"builtin"`
	Enabled bool `json:"enabled"`
	Edited  bool `json:"edited"` // a built-in rule whose content an admin changed
}

// normalize makes list fields non-nil so the API always returns arrays.
func (r *Rule) normalize() {
	if r.Services == nil {
		r.Services = []string{}
	}
	if r.Examples == nil {
		r.Examples = []string{}
	}
	if r.NotExamples == nil {
		r.NotExamples = []string{}
	}
}

// Normalize is normalize for callers outside the package.
func (r *Rule) Normalize() { r.normalize() }

type Format struct {
	Services []string `json:"services"`
	Example  string   `json:"example"`
	Level    string   `json:"level"`
}

type File struct {
	Version int      `json:"version"`
	Formats []Format `json:"formats"`
	Rules   []Rule   `json:"rules"`
}

// Defaults parses the embedded rules.yaml.
func Defaults() (*File, error) {
	var f File
	if err := yaml.Unmarshal(defaultRules, &f); err != nil {
		return nil, fmt.Errorf("parse embedded rules.yaml: %w", err)
	}
	for i := range f.Rules {
		f.Rules[i].Builtin, f.Rules[i].Enabled = true, true
		f.Rules[i].normalize()
	}
	return &f, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate checks one rule: id, severity, that the pattern compiles, that every
// example matches and no not_example does.
func Validate(r Rule) error {
	if !idRe.MatchString(r.ID) {
		return fmt.Errorf("id %q must be kebab-case", r.ID)
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("%s: title is required", r.ID)
	}
	switch r.Severity {
	case "error", "warning", "info":
	default:
		return fmt.Errorf("%s: severity must be error, warning or info", r.ID)
	}
	re, err := regexp.Compile(r.Pattern)
	if err != nil {
		return fmt.Errorf("%s: pattern does not compile: %w", r.ID, err)
	}
	if r.Pattern == "" || re.MatchString("") {
		return fmt.Errorf("%s: pattern matches every line", r.ID)
	}
	for _, ex := range r.Examples {
		if !re.MatchString(ex) {
			return fmt.Errorf("%s: example does not match: %q", r.ID, ex)
		}
	}
	for _, ex := range r.NotExamples {
		if re.MatchString(ex) {
			return fmt.Errorf("%s: not_example matches: %q", r.ID, ex)
		}
	}
	return nil
}

// Set is a compiled, ready-to-match rule library.
type Set struct {
	rules   []Rule
	res     []*regexp.Regexp
	pre     []*prefilter // nil entries always run the regexp
	anyFold bool
	formats []compiledFormat
}

type compiledFormat struct {
	services []string
	level    *regexp.Regexp
}

// Compile builds a Set from enabled rules. Invalid rules are skipped and
// reported, so one bad custom rule cannot disable the parser.
func Compile(rules []Rule, formats []Format) (*Set, []error) {
	s := &Set{}
	var errs []error
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.ID, err))
			continue
		}
		s.rules = append(s.rules, r)
		s.res = append(s.res, re)
		pf := newPrefilter(r.Pattern)
		s.pre = append(s.pre, pf)
		s.anyFold = s.anyFold || (pf != nil && pf.fold)
	}
	for _, f := range formats {
		re, err := regexp.Compile(f.Level)
		if err != nil || re.NumSubexp() < 1 {
			continue
		}
		s.formats = append(s.formats, compiledFormat{services: f.Services, level: re})
	}
	return s, errs
}

func (s *Set) Rules() []Rule { return s.rules }

// AppliesTo reports whether a rule's service list covers a pod. Services are
// pod-name prefixes; an empty list covers every pod.
func AppliesTo(services []string, pod string) bool {
	if len(services) == 0 {
		return true
	}
	for _, svc := range services {
		if pod == svc || strings.HasPrefix(pod, svc+"-") {
			return true
		}
	}
	return false
}

// Match returns the indexes (into Rules()) of rules matching one line of a pod.
func (s *Set) Match(pod, line string) []int {
	lower := ""
	if s.anyFold {
		lower = strings.ToLower(line)
	}
	var out []int
	for i, r := range s.rules {
		if AppliesTo(r.Services, pod) && s.pre[i].pass(line, lower) && s.res[i].MatchString(line) {
			out = append(out, i)
		}
	}
	return out
}

// MatchRule matches one line against rule i, ignoring its service list.
func (s *Set) MatchRule(i int, line string) bool { return s.res[i].MatchString(line) }

// Literals returns the union of every enabled rule's prefilter strings, so a
// log backend (Loki) can return only candidate lines. complete is false when
// some rule has no prefilter, i.e. a filter would hide its matches.
func (s *Set) Literals() (lits []string, fold bool, complete bool) {
	seen := map[string]bool{}
	complete = true
	for _, pf := range s.pre {
		if pf == nil {
			complete = false
			continue
		}
		fold = fold || pf.fold
		for _, l := range pf.anyOf {
			if !seen[l] {
				seen[l] = true
				lits = append(lits, l)
			}
		}
	}
	return lits, fold, complete
}

// Level extracts the level word of a line using the service's format, or "".
func (s *Set) Level(pod, line string) string {
	for _, f := range s.formats {
		if AppliesTo(f.services, pod) {
			if m := f.level.FindStringSubmatch(line); len(m) > 1 {
				return m[1]
			}
		}
	}
	return ""
}

// Hit is one rule's matches within a scan.
type Hit struct {
	RuleID    string `json:"ruleId"`
	Title     string `json:"title"`
	Severity  string `json:"severity"`
	Meaning   string `json:"meaning"`
	Fix       string `json:"fix"`
	Count     int    `json:"count"`
	FirstLine int    `json:"firstLine"` // index into the scanned lines
	LastLine  int    `json:"lastLine"`
	Sample    string `json:"sample"`
}

// LineMatch tags one line with the rules it matched.
type LineMatch struct {
	Line  int      `json:"line"`
	Rules []string `json:"rules"`
}

type Result struct {
	Hits    []Hit       `json:"hits"`
	Matches []LineMatch `json:"matches"`
}

var sevRank = map[string]int{"error": 0, "warning": 1, "info": 2}

// Scan matches every line of one pod's log. Hits are sorted errors first,
// then by count.
func (s *Set) Scan(pod string, lines []string) Result {
	byRule := map[int]*Hit{}
	res := Result{Hits: []Hit{}, Matches: []LineMatch{}}
	for li, line := range lines {
		idx := s.Match(pod, line)
		if len(idx) == 0 {
			continue
		}
		lm := LineMatch{Line: li}
		for _, i := range idx {
			r := s.rules[i]
			lm.Rules = append(lm.Rules, r.ID)
			h := byRule[i]
			if h == nil {
				h = &Hit{RuleID: r.ID, Title: r.Title, Severity: r.Severity, Meaning: r.Meaning, Fix: r.Fix, FirstLine: li, Sample: truncate(line, 400)}
				byRule[i] = h
			}
			h.Count++
			h.LastLine = li
		}
		res.Matches = append(res.Matches, lm)
	}
	for _, h := range byRule {
		res.Hits = append(res.Hits, *h)
	}
	sort.Slice(res.Hits, func(i, j int) bool {
		a, b := res.Hits[i], res.Hits[j]
		if sevRank[a.Severity] != sevRank[b.Severity] {
			return sevRank[a.Severity] < sevRank[b.Severity]
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.RuleID < b.RuleID
	})
	return res
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
