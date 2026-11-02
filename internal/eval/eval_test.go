package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/testutil"
)

// The shipped cases must parse (strict: unknown fields are errors).
func TestShippedCasesLoad(t *testing.T) {
	cases, err := Load("../../evals/cases")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Errorf("%d cases", len(cases))
	}
	for _, c := range cases {
		if len(c.Expect.RootCause) == 0 {
			t.Errorf("%s: no rootCause expectation", c.ID)
		}
	}
}

func TestRunReplaysAndGrades(t *testing.T) {
	claude := testutil.NewFakeClaude(
		[]testutil.Block{{Tool: "get_job", ID: "t1", Input: map[string]any{"job_id": "job-1"}}},
		[]testutil.Block{{Tool: "get_infrastructure", ID: "t2", Input: map[string]any{"id": "infra-x"}}},
		[]testutil.Block{{Tool: "record_investigation", ID: "t3", Input: map[string]any{
			"title": "acme: DNS step timed out", "entity_id": "cp-acme", "entity_name": "acme", "category": "networking-dns",
			"confidence": "high", "summary_for_requester": "DNS step failed.",
			"root_cause": "The public subnets lack the kubernetes.io/role/elb tag, so no load balancer was created."}}},
		[]testutil.Block{{Text: "Done."}},
	)
	t.Cleanup(claude.Close)
	c := Case{ID: "x", Question: "why?",
		Fixtures: []Fixture{{Tool: "get_job", Match: map[string]string{"job_id": "JOB-1"}, Output: map[string]any{"errorDetails": "failed to get ingress address"}}},
		Expect:   Expect{Categories: []string{"networking-dns"}, RootCause: [][]string{{"subnet"}, {"kubernetes.io/role/elb"}}, MustNot: []string{"certificate"}}}
	newAgent := func(st *store.Store) *agent.Agent {
		return agent.New(st, controlhub.New("http://127.0.0.1:1"), nil, nil, "claude-opus-5-5", "high").
			WithClientOptions(option.WithBaseURL(claude.URL), option.WithMaxRetries(0))
	}
	res := Run(context.Background(), c, newAgent, "sk-test")
	if !res.Pass || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	// The fixture reached the model; the unrecorded call got the placeholder.
	sent := claude.Requests[1]["messages"]
	if !strings.Contains(strings.Join(flatten(sent), " "), "failed to get ingress address") {
		t.Errorf("replayed output not sent: %v", sent)
	}
	if len(res.Unrecorded) != 1 || !strings.HasPrefix(res.Unrecorded[0], "get_infrastructure") {
		t.Errorf("unrecorded = %v", res.Unrecorded)
	}
	if len(res.ToolCalls) != 3 || res.Category != "networking-dns" {
		t.Errorf("tool calls %v category %s", res.ToolCalls, res.Category)
	}
}

func TestGrade(t *testing.T) {
	c := Case{Expect: Expect{Categories: []string{"certificates"}, RootCause: [][]string{{"cname", "validation record"}}, MustNot: []string{"subnet"}, MaxToolCalls: 3}}
	inv := &store.Investigation{Category: "networking-dns", RootCause: "subnet tags"}
	_, f := Grade(c, inv, "", 5)
	if len(f) != 4 { // category, root cause, mustNot, tool budget
		t.Errorf("failures = %v", f)
	}
	_, f = Grade(Case{Expect: Expect{NoInvestigation: true, RootCause: [][]string{{"healthy"}}}}, nil, "Everything looks healthy.", 2)
	if len(f) != 0 {
		t.Errorf("healthy failures = %v", f)
	}
}

func flatten(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, flatten(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return nil
}
