package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// Query expansion for learnings search. Anthropic has no embeddings API, so
// "semantic" search asks a small model for rewordings of the question and the
// keyword search runs on all of them (store.SearchLearningsAny). Inside the
// Copilot this costs nothing: the model passes its own rewordings to
// search_learnings. This is for people searching the Learnings page.

const expandModel = "claude-haiku-5-5"

var expandTool = anthropic.ToolParam{
	Name:        "search_terms",
	Description: param.NewOpt("Alternative search queries for the same problem."),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"alternatives": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 5},
		},
		Required: []string{"alternatives"},
	},
}

const expandSystem = `You help search a knowledge base of failure patterns from operating Prophecy (a data platform on
Kubernetes: control planes, execution planes, Helm, Postgres, certificates, Spark, cloud providers). Given a search
query, write 3-5 short alternative queries someone else might have used for the same problem: synonyms, the likely
underlying cause, exact error strings or Kubernetes reasons that go with it, and the component names involved. Keep each
under 12 words. Call search_terms once.`

// ExpandQuery returns rewordings of a learnings search query.
func (a *Agent) ExpandQuery(ctx context.Context, apiKey, query string) ([]string, error) {
	client := anthropic.NewClient(append([]option.RequestOption{option.WithAPIKey(apiKey)}, a.clientOpts...)...)
	msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model: anthropic.Model(expandModel), MaxTokens: 400,
		System:     []anthropic.TextBlockParam{{Text: expandSystem}},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &expandTool}},
		ToolChoice: anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: expandTool.Name}},
		Messages:   []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(clip(query, 500)))},
	})
	if err != nil {
		return nil, describeAPIError(err)
	}
	for _, b := range msg.Content {
		u, ok := b.AsAny().(anthropic.ToolUseBlock)
		if !ok || u.Name != expandTool.Name {
			continue
		}
		var in struct {
			Alternatives []string `json:"alternatives"`
		}
		if json.Unmarshal([]byte(u.JSON.Input.Raw()), &in) != nil {
			break
		}
		var out []string
		for _, q := range in.Alternatives {
			if q = strings.TrimSpace(clip(q, 200)); q != "" && !strings.EqualFold(q, query) {
				out = append(out, q)
			}
			if len(out) == 5 {
				break
			}
		}
		return out, nil
	}
	return nil, nil
}
