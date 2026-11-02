package logrules

import (
	"os"
	"strings"
	"testing"
)

// The prefilter may only skip lines the regexp would not match: check every
// shipped rule against its own examples, and a real log against all rules.
func TestPrefilterNeverHidesAMatch(t *testing.T) {
	f, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	filtered := 0
	for _, r := range f.Rules {
		pf := newPrefilter(r.Pattern)
		if pf != nil {
			filtered++
		}
		for _, ex := range r.Examples {
			if !pf.pass(ex, strings.ToLower(ex)) {
				t.Errorf("%s: prefilter %v rejects its own example %q", r.ID, pf.anyOf, ex)
			}
		}
	}
	t.Logf("%d of %d rules have a prefilter", filtered, len(f.Rules))
	if filtered < len(f.Rules)/2 {
		t.Errorf("only %d of %d rules got a prefilter; expected most", filtered, len(f.Rules))
	}

	lines := sampleLines()
	set, _ := Compile(f.Rules, nil)
	for _, line := range lines {
		for i, r := range set.rules {
			want := set.res[i].MatchString(line)
			got := set.pre[i].pass(line, strings.ToLower(line)) && want
			if want != got {
				t.Errorf("%s: prefilter hides a match on %q", r.ID, line)
			}
		}
	}
}

func TestPrefilterExtraction(t *testing.T) {
	cases := map[string][]string{
		`java\.lang\.OutOfMemoryError`:           {"java.lang.OutOfMemoryError"},
		`Failed to create snapshot .* for disk`:  {"Failed to create snapshot "},
		`(no live upstreams|upstream timed out)`: {"no live upstreams", "upstream timed out"},
		`(?i)connection refused`:                 {"connection refused"},
		`^\S+ (ERROR|FATAL) .*db`:                {"ERROR", "FATAL"},
		`\d+ ms`:                                 nil, // " ms" is too short to be worth it
	}
	for pattern, want := range cases {
		pf := newPrefilter(pattern)
		var got []string
		if pf != nil {
			got = pf.anyOf
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: got %q, want %q", pattern, got, want)
		}
	}
}

// sampleLines is a mix of synthetic lines and, when present, a real log.
func sampleLines() []string {
	lines := []string{
		"2026-10-08 10:58:13.552 ERROR [akka-7] c.p.m.GraphIndexer - java.lang.OutOfMemoryError: Java heap space",
		"INFO all good", "", "CONNECTION REFUSED to postgres:5432",
	}
	if raw, err := os.ReadFile(os.Getenv("LOGRULES_SAMPLE")); err == nil {
		lines = append(lines, strings.Split(string(raw), "\n")...)
	}
	return lines
}
