package agent

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
)

// Bulk right-sizing review: Claude checks many recommendations at once
// through the Message Batches API (half price; results within a day,
// usually minutes). Each request sees one recommendation and returns a
// verdict through a forced tool call.

const reviewSystem = `You review Kubernetes right-sizing recommendations for Prophecy's services before an SRE applies them
through the Helm chart. The recommendation follows Robusta KRR: CPU request = the highest daily 95th-percentile use, no
CPU limit; memory request = limit = peak working set + 15%.

Judge whether applying it is safe:
- safe: the usage data supports it and nothing suggests a risk.
- adjust: the direction is right but the numbers need changing; give the values you would use.
- risky: do not apply as is (for example a JVM service whose heap -Xmx is tied to the memory limit, a workload with too
  little history, bursty batch work whose peaks the window may have missed, OOM kills or heavy throttling).
Prophecy's JVM services (metagraph, app, execution, edweb, sparkedge, transpiler…) size the heap from the container limit
or a fixed -Xmx: lowering memory without lowering the heap causes OOM kills. Be specific and brief. Call
review_recommendation exactly once.`

// Review is Claude's verdict on one recommendation.
type Review struct {
	Verdict string `json:"verdict"` // safe, adjust, risky
	Reason  string `json:"reason"`
	// Suggested values when the verdict is adjust (cores, bytes); 0 = keep the recommendation.
	CPURequest float64 `json:"cpuRequest"`
	MemRequest float64 `json:"memRequest"`
	MemLimit   float64 `json:"memLimit"`
}

// ReviewKey identifies a recommendation across runs.
func ReviewKey(r cost.Recommendation) string {
	return r.Cluster + "|" + r.Namespace + "|" + r.Kind + "|" + r.Name + "|" + r.Container
}

// ReviewCustomID is the batch-safe ID for a key (^[a-zA-Z0-9_-]{1,64}$).
func ReviewCustomID(key string) string {
	h := sha1.Sum([]byte(key))
	return "rs-" + hex.EncodeToString(h[:])[:20]
}

var reviewTool = anthropic.ToolParam{
	Name:        "review_recommendation",
	Description: param.NewOpt("Your verdict on one right-sizing recommendation."),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"verdict":          map[string]any{"type": "string", "enum": []string{"safe", "adjust", "risky"}},
			"reason":           map[string]any{"type": "string", "description": "One or two sentences."},
			"cpu_request":      map[string]any{"type": "number", "description": "With adjust: CPU request in cores."},
			"memory_mib":       map[string]any{"type": "number", "description": "With adjust: memory request and limit in MiB."},
			"memory_limit_mib": map[string]any{"type": "number", "description": "With adjust, when the limit should differ from the request."},
		},
		Required: []string{"verdict", "reason"},
	},
}

// SubmitReview sends one batch request per recommendation and returns the batch ID.
func (a *Agent) SubmitReview(ctx context.Context, apiKey string, recs []cost.Recommendation) (string, error) {
	if len(recs) == 0 {
		return "", fmt.Errorf("nothing to review")
	}
	reqs := make([]anthropic.MessageBatchNewParamsRequest, 0, len(recs))
	for _, r := range recs {
		data, _ := json.Marshal(map[string]any{
			"cluster": r.Cluster, "namespace": r.Namespace, "workload": r.Kind + "/" + r.Name, "container": r.Container,
			"days": r.Days, "replicas": r.Replicas, "confidence": r.Confidence,
			"current": map[string]string{"cpuRequest": cost.CPUString(r.Current.CPURequest), "cpuLimit": cpuOrNone(r.Current.CPULimit),
				"memRequest": cost.MemString(r.Current.MemRequest), "memLimit": memOrNone(r.Current.MemLimit)},
			"usage": map[string]any{"cpuP95": cost.CPUString(r.CPUP95), "cpuMax": cost.CPUString(r.CPUMax), "memPeak": cost.MemString(r.MemMax),
				"oomDays": r.OOMDays, "throttledPct": r.ThrottledPct},
			"recommended": map[string]string{"cpuRequest": cost.CPUString(r.Recommended.CPURequest), "cpuLimit": "none",
				"memRequest": cost.MemString(r.Recommended.MemRequest), "memLimit": memOrNone(r.Recommended.MemLimit)},
			"monthlySaving": r.MonthlySaving, "notes": r.Notes,
		})
		reqs = append(reqs, anthropic.MessageBatchNewParamsRequest{
			CustomID: ReviewCustomID(ReviewKey(r)),
			Params: anthropic.MessageBatchNewParamsRequestParams{
				Model: anthropic.Model(a.model), MaxTokens: 1500,
				System:     []anthropic.TextBlockParam{{Text: reviewSystem, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
				Tools:      []anthropic.ToolUnionParam{{OfTool: &reviewTool}},
				ToolChoice: anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: reviewTool.Name}},
				Messages:   []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(string(data)))},
			},
		})
	}
	client := anthropic.NewClient(append([]option.RequestOption{option.WithAPIKey(apiKey)}, a.clientOpts...)...)
	b, err := client.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: reqs})
	if err != nil {
		return "", describeAPIError(err)
	}
	return b.ID, nil
}

// BatchState is a batch's progress.
type BatchState struct {
	Ended                                         bool
	Status                                        string
	Processing, Succeeded, Errored, Expired, Gone int64
}

// ReviewStatus reads a batch's progress.
func (a *Agent) ReviewStatus(ctx context.Context, apiKey, batchID string) (*BatchState, error) {
	client := anthropic.NewClient(append([]option.RequestOption{option.WithAPIKey(apiKey)}, a.clientOpts...)...)
	b, err := client.Messages.Batches.Get(ctx, batchID, anthropic.MessageBatchGetParams{})
	if err != nil {
		return nil, describeAPIError(err)
	}
	c := b.RequestCounts
	return &BatchState{Ended: b.ProcessingStatus == anthropic.MessageBatchProcessingStatusEnded, Status: string(b.ProcessingStatus),
		Processing: c.Processing, Succeeded: c.Succeeded, Errored: c.Errored, Expired: c.Expired, Gone: c.Canceled}, nil
}

// ReviewResults reads an ended batch's verdicts, keyed by custom ID; failed
// requests carry an error instead.
func (a *Agent) ReviewResults(ctx context.Context, apiKey, batchID string) (map[string]Review, map[string]string, error) {
	client := anthropic.NewClient(append([]option.RequestOption{option.WithAPIKey(apiKey)}, a.clientOpts...)...)
	stream := client.Messages.Batches.ResultsStreaming(ctx, batchID, anthropic.MessageBatchResultsParams{})
	reviews, failed := map[string]Review{}, map[string]string{}
	for stream.Next() {
		line := stream.Current()
		res := line.Result
		if res.Type != "succeeded" {
			failed[line.CustomID] = res.Type
			continue
		}
		found := false
		for _, b := range res.Message.Content {
			u, ok := b.AsAny().(anthropic.ToolUseBlock)
			if !ok || u.Name != reviewTool.Name {
				continue
			}
			var in struct {
				Verdict        string  `json:"verdict"`
				Reason         string  `json:"reason"`
				CPURequest     float64 `json:"cpu_request"`
				MemoryMiB      float64 `json:"memory_mib"`
				MemoryLimitMiB float64 `json:"memory_limit_mib"`
			}
			if json.Unmarshal([]byte(u.JSON.Input.Raw()), &in) != nil {
				break
			}
			switch in.Verdict {
			case "safe", "adjust", "risky":
			default:
				in.Verdict = "risky"
			}
			rv := Review{Verdict: in.Verdict, Reason: clip(in.Reason, 600), CPURequest: in.CPURequest,
				MemRequest: in.MemoryMiB * (1 << 20), MemLimit: in.MemoryLimitMiB * (1 << 20)}
			if rv.MemLimit == 0 {
				rv.MemLimit = rv.MemRequest
			}
			reviews[line.CustomID] = rv
			found = true
			break
		}
		if !found {
			failed[line.CustomID] = "no verdict"
		}
	}
	if err := stream.Err(); err != nil {
		return reviews, failed, describeAPIError(err)
	}
	return reviews, failed, nil
}

func cpuOrNone(v float64) string {
	if v == 0 {
		return "none"
	}
	return cost.CPUString(v)
}

func memOrNone(v float64) string {
	if v == 0 {
		return "none"
	}
	return cost.MemString(v)
}
