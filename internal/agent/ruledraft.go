package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/logrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

// RuleSource is what a log rule is drafted from: a pattern the miner found
// (Template + Examples are real, masked lines) or an approved learning.
type RuleSource struct {
	Kind     string   // "pattern" or "learning"
	Service  string   // pod-name prefix, when known
	Template string   // pattern template with <*> for variable parts
	Examples []string // real lines the rule must match
	// From a learning:
	Title, Symptoms, RootCause, Fix string
	// ExistingIDs are rule IDs already taken.
	ExistingIDs []string
}

// RuleDraft is Claude's proposal, validated; nothing is saved.
type RuleDraft struct {
	Applicable bool          `json:"applicable"`
	Reason     string        `json:"reason,omitempty"`
	Rule       logrules.Rule `json:"rule"`
}

const draftSystem = `You write log rules for the Prophecy AI console's log parser. A rule is a Go RE2 regular expression
that marks one specific, actionable error message, plus a one-sentence meaning and fix.

Rules:
- Anchor on the stable text of the message, never on IDs, numbers, hosts, paths or timestamps; use [^ ]+ or .* for
  variable parts. RE2 has no look-around or backreferences. Escape regex metacharacters ( . ( ) [ ] { } + ? * | ^ $ \ ).
- Never a bare "ERROR" or "Exception" rule. If the message is not specific or actionable enough for a rule (generic
  test-runner noise, a success message logged at error level, a retry that recovers), say applicable=false and why.
- services: pod-name prefixes without the "-prophecy" suffix (e.g. "metagraph" for metagraph-prophecy-0). Use the
  service you are given unless the message is generic across services.
- id: kebab-case, specific, under 40 characters.
- severity: error when it breaks something for users, warning when it degrades or may break, info otherwise.
- examples: at least two realistic full log lines that must match, including the real lines you are given.
  not_examples: one or two similar lines that must NOT match (the healthy or unrelated case).
Call propose_log_rule exactly once.`

var ruleIDRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// DraftLogRule asks Claude for a log rule and validates it: the regexp must
// compile, match every example (and every real line it was drafted from) and
// none of the not-examples. One retry gets the validation errors.
func (a *Agent) DraftLogRule(ctx context.Context, apiKey string, src RuleSource) (*RuleDraft, error) {
	client := anthropic.NewClient(append([]option.RequestOption{option.WithAPIKey(apiKey)}, a.clientOpts...)...)
	tool := anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
		Name:        "propose_log_rule",
		Description: param.NewOpt("Propose one log rule, or say none is warranted."),
		InputSchema: anthropic.BetaToolInputSchemaParam{
			Properties: map[string]any{
				"applicable":   map[string]any{"type": "boolean"},
				"reason":       map[string]any{"type": "string", "description": "Why no rule is warranted (when applicable is false)."},
				"id":           map[string]any{"type": "string"},
				"title":        map[string]any{"type": "string"},
				"services":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"pattern":      map[string]any{"type": "string"},
				"severity":     map[string]any{"type": "string", "enum": []string{"error", "warning", "info"}},
				"meaning":      map[string]any{"type": "string"},
				"fix":          map[string]any{"type": "string"},
				"examples":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"not_examples": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			Required: []string{"applicable"},
		},
	}}
	messages := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(describeSource(src)))}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		stream := client.Beta.Messages.NewStreaming(ctx, anthropic.BetaMessageNewParams{
			Model: anthropic.Model(a.model), MaxTokens: 4000,
			System:       []anthropic.BetaTextBlockParam{{Text: draftSystem}},
			Tools:        []anthropic.BetaToolUnionParam{tool},
			Messages:     messages,
			OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort("medium")},
		})
		msg := anthropic.BetaMessage{}
		for stream.Next() {
			if err := msg.Accumulate(stream.Current()); err != nil {
				return nil, fmt.Errorf("reading the Claude stream: %w", err)
			}
		}
		if err := stream.Err(); err != nil {
			return nil, describeAPIError(err)
		}
		var use *anthropic.BetaToolUseBlock
		for _, b := range msg.Content {
			if u, ok := b.AsAny().(anthropic.BetaToolUseBlock); ok && u.Name == "propose_log_rule" {
				use = &u
				break
			}
		}
		if use == nil {
			return nil, errors.New("Claude did not propose a rule; try again")
		}
		var in struct {
			Applicable  bool     `json:"applicable"`
			Reason      string   `json:"reason"`
			ID          string   `json:"id"`
			Title       string   `json:"title"`
			Services    []string `json:"services"`
			Pattern     string   `json:"pattern"`
			Severity    string   `json:"severity"`
			Meaning     string   `json:"meaning"`
			Fix         string   `json:"fix"`
			Examples    []string `json:"examples"`
			NotExamples []string `json:"not_examples"`
		}
		if err := json.Unmarshal([]byte(use.JSON.Input.Raw()), &in); err != nil {
			return nil, fmt.Errorf("unreadable proposal: %w", err)
		}
		if !in.Applicable {
			return &RuleDraft{Applicable: false, Reason: in.Reason}, nil
		}
		r := logrules.Rule{ID: uniqueID(in.ID, src.ExistingIDs), Title: in.Title, Services: in.Services, Pattern: in.Pattern,
			Severity: in.Severity, Meaning: in.Meaning, Fix: in.Fix, Examples: in.Examples, NotExamples: in.NotExamples,
			Source: "drafted by the Copilot from a " + src.Kind}
		problems := validateRule(r, src.Examples)
		if len(problems) == 0 {
			r.Normalize()
			return &RuleDraft{Applicable: true, Rule: r}, nil
		}
		lastErr = errors.New(strings.Join(problems, "; "))
		messages = append(messages, msg.ToParam(), anthropic.NewBetaUserMessage(anthropic.NewBetaToolResultBlock(use.ID,
			"The rule failed validation: "+lastErr.Error()+". Fix it and call propose_log_rule again.", true)))
	}
	return nil, fmt.Errorf("the drafted rule did not validate: %w", lastErr)
}

func describeSource(src RuleSource) string {
	var b strings.Builder
	if src.Kind == "pattern" {
		fmt.Fprintf(&b, "This error message appears in service %q and no existing rule matches it.\n\nTemplate (<*> = variable part):\n%s\n\nReal lines (secrets masked):\n",
			src.Service, src.Template)
		for _, e := range src.Examples {
			fmt.Fprintf(&b, "%s\n", e)
		}
	} else {
		fmt.Fprintf(&b, "Write a log rule that detects this known failure pattern from its log lines.\n\nTitle: %s\nSymptoms: %s\nRoot cause: %s\nFix: %s\n\n"+
			"If the symptoms contain no log message a regexp can match (only statuses, metrics or job errors), say applicable=false.",
			src.Title, redact.Text(src.Symptoms), redact.Text(src.RootCause), redact.Text(src.Fix))
	}
	return b.String()
}

// validateRule returns what is wrong with a drafted rule.
func validateRule(r logrules.Rule, mustMatch []string) []string {
	var p []string
	if r.Title == "" || r.Pattern == "" || r.Meaning == "" {
		p = append(p, "title, pattern and meaning are required")
	}
	switch r.Severity {
	case "error", "warning", "info":
	default:
		p = append(p, "severity must be error, warning or info")
	}
	if strings.TrimSpace(strings.ToLower(r.Pattern)) == "error" || strings.TrimSpace(strings.ToLower(r.Pattern)) == "(?i)error" {
		p = append(p, "the pattern is too generic")
	}
	re, err := regexp.Compile(r.Pattern)
	if err != nil {
		return append(p, "the pattern is not valid RE2: "+err.Error())
	}
	if len(r.Examples) == 0 {
		p = append(p, "at least one example is required")
	}
	for _, e := range append(append([]string{}, r.Examples...), mustMatch...) {
		if !re.MatchString(e) {
			p = append(p, fmt.Sprintf("the pattern does not match %q", clip(e, 160)))
		}
	}
	for _, e := range r.NotExamples {
		if re.MatchString(e) {
			p = append(p, fmt.Sprintf("the pattern matches the not-example %q", clip(e, 160)))
		}
	}
	return p
}

// uniqueID makes a valid rule ID that no existing rule uses.
func uniqueID(id string, taken []string) string {
	id = strings.Trim(strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(id, "-")), "-")
	if !ruleIDRE.MatchString(id) {
		id = "drafted-rule"
	}
	used := map[string]bool{}
	for _, t := range taken {
		used[t] = true
	}
	out := id
	for i := 2; used[out]; i++ {
		out = fmt.Sprintf("%s-%d", id, i)
	}
	return out
}
