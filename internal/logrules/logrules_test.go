package logrules

import (
	"regexp"
	"testing"
)

// Every shipped rule must be valid, unique, and match its own examples.
func TestDefaultRulesAreValid(t *testing.T) {
	f, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Rules) == 0 {
		t.Fatal("rules.yaml has no rules")
	}
	seen := map[string]bool{}
	for _, r := range f.Rules {
		if seen[r.ID] {
			t.Errorf("duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		if len(r.Examples) == 0 {
			t.Errorf("%s: needs at least one example", r.ID)
		}
		if err := Validate(r); err != nil {
			t.Error(err)
		}
	}
	for _, fm := range f.Formats {
		re, err := regexp.Compile(fm.Level)
		if err != nil {
			t.Errorf("format %v: level regexp: %v", fm.Services, err)
			continue
		}
		if m := re.FindStringSubmatch(fm.Example); len(m) < 2 || m[1] == "" {
			t.Errorf("format %v: level not found in its example %q", fm.Services, fm.Example)
		}
	}
}

func TestScanScopesByServiceAndRanks(t *testing.T) {
	rules := []Rule{
		{ID: "jvm-oom", Title: "JVM out of memory", Services: []string{"metagraph"}, Pattern: `java\.lang\.OutOfMemoryError`, Severity: "error", Enabled: true},
		{ID: "pool-timeout", Title: "DB pool exhausted", Pattern: `Connection is not available`, Severity: "warning", Enabled: true},
		{ID: "off", Title: "Disabled", Pattern: `anything`, Severity: "error", Enabled: false},
	}
	set, errs := Compile(rules, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	lines := []string{
		"INFO start anything",
		"WARN Connection is not available, request timed out after 30000ms",
		"WARN Connection is not available, request timed out after 30000ms",
		"ERROR java.lang.OutOfMemoryError: Java heap space",
	}
	res := set.Scan("metagraph-prophecy-7d9f8c6b5-qm4tz", lines)
	if len(res.Hits) != 2 || res.Hits[0].RuleID != "jvm-oom" || res.Hits[1].Count != 2 {
		t.Fatalf("hits = %+v", res.Hits)
	}
	if res.Hits[0].FirstLine != 3 || len(res.Matches) != 3 {
		t.Errorf("first line / matches = %d / %+v", res.Hits[0].FirstLine, res.Matches)
	}
	// The OOM rule is scoped to metagraph; another pod only gets the generic rule.
	if other := set.Scan("athena-6c9d7b5f4-x2kqp", lines); len(other.Hits) != 1 || other.Hits[0].RuleID != "pool-timeout" {
		t.Errorf("athena hits = %+v", other.Hits)
	}
}

func TestAppliesTo(t *testing.T) {
	cases := []struct {
		services []string
		pod      string
		want     bool
	}{
		{nil, "anything", true},
		{[]string{"app"}, "app-prophecy-5898ddc84f-rkdff", true},
		{[]string{"app"}, "apps-thing-1", false},
		{[]string{"sql"}, "sql-sql-km2zf", true},
		{[]string{"postgres"}, "postgres", true},
	}
	for _, c := range cases {
		if got := AppliesTo(c.services, c.pod); got != c.want {
			t.Errorf("AppliesTo(%v, %q) = %v", c.services, c.pod, got)
		}
	}
}

func TestValidateRejectsBadRules(t *testing.T) {
	bad := []Rule{
		{ID: "Bad_ID", Title: "x", Pattern: "x", Severity: "error"},
		{ID: "no-title", Pattern: "x", Severity: "error"},
		{ID: "bad-sev", Title: "x", Pattern: "x", Severity: "fatal"},
		{ID: "bad-re", Title: "x", Pattern: "(", Severity: "error"},
		{ID: "matches-all", Title: "x", Pattern: ".*", Severity: "error"},
		{ID: "example-misses", Title: "x", Pattern: "foo", Severity: "error", Examples: []string{"bar"}},
		{ID: "not-example-hits", Title: "x", Pattern: "foo", Severity: "error", NotExamples: []string{"foo"}},
	}
	for _, r := range bad {
		if Validate(r) == nil {
			t.Errorf("%s: expected a validation error", r.ID)
		}
	}
}
