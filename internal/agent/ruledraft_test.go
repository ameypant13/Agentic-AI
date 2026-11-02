package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/logrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/testutil"
)

func TestDraftLogRuleValidatesAndRetries(t *testing.T) {
	line := "2026-10-17 10:00:01 ERROR [pool-3] c.p.m.Db - Connection to 10.0.4.12:5432 refused after 3000ms"
	bad := map[string]any{"applicable": true, "id": "Postgres Refused!", "title": "Postgres connection refused", "services": []string{"metagraph"},
		"pattern": `Connection to 10\.0\.4\.12:5432 refused`, "severity": "error", "meaning": "m", "fix": "f",
		"examples": []string{line, "ERROR Connection to 10.0.9.9:5432 refused after 1ms"}}
	good := map[string]any{"applicable": true, "id": "Postgres Refused!", "title": "Postgres connection refused", "services": []string{"metagraph"},
		"pattern": `Connection to [^ ]+:5432 refused`, "severity": "error", "meaning": "Metagraph cannot reach Postgres.", "fix": "Check the postgres pod.",
		"examples": []string{line}, "not_examples": []string{"INFO Connection to 10.0.4.12:5432 established"}}
	claude := testutil.NewFakeClaude(
		[]testutil.Block{{Tool: "propose_log_rule", ID: "t1", Input: bad}},
		[]testutil.Block{{Tool: "propose_log_rule", ID: "t2", Input: good}},
	)
	t.Cleanup(claude.Close)
	a := New(nil, nil, nil, nil, "claude-opus-5-5", "high").WithClientOptions(option.WithBaseURL(claude.URL), option.WithMaxRetries(0))
	d, err := a.DraftLogRule(context.Background(), "sk-test", RuleSource{Kind: "pattern", Service: "metagraph",
		Template: "<*> ERROR <*> c.p.m.Db - Connection to <*> refused after <*>", Examples: []string{line}, ExistingIDs: []string{"postgres-refused"}})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Applicable || d.Rule.Pattern != `Connection to [^ ]+:5432 refused` {
		t.Errorf("draft = %+v", d)
	}
	// The ID is cleaned up and does not clash with an existing rule.
	if d.Rule.ID != "postgres-refused-2" {
		t.Errorf("id = %s", d.Rule.ID)
	}
	// The retry told Claude why the first proposal failed.
	second := claude.Requests[1]["messages"]
	if b, _ := jsonString(second); !strings.Contains(b, "does not match") {
		t.Errorf("retry did not explain the failure: %s", b)
	}
}

func TestDraftLogRuleNotApplicable(t *testing.T) {
	claude := testutil.NewFakeClaude([]testutil.Block{{Tool: "propose_log_rule", ID: "t1",
		Input: map[string]any{"applicable": false, "reason": "test-runner noise"}}})
	t.Cleanup(claude.Close)
	a := New(nil, nil, nil, nil, "claude-opus-5-5", "high").WithClientOptions(option.WithBaseURL(claude.URL), option.WithMaxRetries(0))
	d, err := a.DraftLogRule(context.Background(), "sk-test", RuleSource{Kind: "learning", Title: "x", Symptoms: "status FAILED"})
	if err != nil || d.Applicable || d.Reason != "test-runner noise" {
		t.Errorf("draft = %+v %v", d, err)
	}
}

func TestValidateRule(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    string
	}{{"error", "too generic"}, {"(unclosed", "not valid RE2"}, {"timeout", "does not match"}} {
		r := ruleFor(tc.pattern)
		if p := strings.Join(validateRule(r, []string{"ERROR connection refused"}), " "); !strings.Contains(p, tc.want) {
			t.Errorf("%s: %s", tc.pattern, p)
		}
	}
}

func ruleFor(pattern string) logrules.Rule {
	return logrules.Rule{Title: "t", Pattern: pattern, Severity: "error", Meaning: "m", Examples: []string{"ERROR connection refused"}}
}

func jsonString(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
