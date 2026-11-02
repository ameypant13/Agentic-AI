package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
)

func TestBatchReview(t *testing.T) {
	recs := []cost.Recommendation{
		{Cluster: "westpac", Namespace: "prophecy", Kind: "Deployment", Name: "edweb", Container: "edweb", Days: 30,
			Current: cost.Resources{CPURequest: 26, MemRequest: 30000 << 20}, Recommended: cost.Resources{CPURequest: 0.41, MemRequest: 15296 << 20, MemLimit: 15296 << 20}},
		{Cluster: "preview", Namespace: "prophecy", Kind: "Deployment", Name: "app", Container: "app", Days: 3},
	}
	idA, idB := ReviewCustomID(ReviewKey(recs[0])), ReviewCustomID(ReviewKey(recs[1]))
	var submitted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/messages/batches":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &submitted)
			w.Write([]byte(`{"id":"msgbatch_1","type":"message_batch","processing_status":"in_progress","request_counts":{"processing":2}}`))
		case r.URL.Path == "/v1/messages/batches/msgbatch_1":
			w.Write([]byte(`{"id":"msgbatch_1","type":"message_batch","processing_status":"ended","request_counts":{"succeeded":1,"errored":1}}`))
		case r.URL.Path == "/v1/messages/batches/msgbatch_1/results":
			w.Header().Set("Content-Type", "application/x-jsonl")
			ok := map[string]any{"custom_id": idA, "result": map[string]any{"type": "succeeded", "message": map[string]any{
				"id": "m", "type": "message", "role": "assistant", "model": "claude-opus-5-5", "stop_reason": "tool_use",
				"content": []any{map[string]any{"type": "tool_use", "id": "t", "name": "review_recommendation",
					"input": map[string]any{"verdict": "adjust", "reason": "JVM: lower -Xmx with the limit.", "cpu_request": 1, "memory_mib": 20480}}},
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 10}}}}
			bad := map[string]any{"custom_id": idB, "result": map[string]any{"type": "errored", "error": map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": "x"}}}}
			for _, l := range []any{ok, bad} {
				b, _ := json.Marshal(l)
				w.Write(append(b, '\n'))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a := New(nil, nil, nil, nil, "claude-opus-5-5", "high").WithClientOptions(option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	ctx := context.Background()

	id, err := a.SubmitReview(ctx, "sk-test", recs)
	if err != nil || id != "msgbatch_1" {
		t.Fatalf("submit = %s %v", id, err)
	}
	reqs := submitted["requests"].([]any)
	first, _ := json.Marshal(reqs[0])
	for _, want := range []string{idA, `"tool_choice":{"name":"review_recommendation","type":"tool"}`, `\"cpuRequest\":\"26\"`, `\"recommended\"`} {
		if !strings.Contains(string(first), want) {
			t.Errorf("request missing %s: %s", want, first)
		}
	}
	st, err := a.ReviewStatus(ctx, "sk-test", id)
	if err != nil || !st.Ended || st.Succeeded != 1 || st.Errored != 1 {
		t.Fatalf("status = %+v %v", st, err)
	}
	reviews, failed, err := a.ReviewResults(ctx, "sk-test", id)
	if err != nil {
		t.Fatal(err)
	}
	r := reviews[idA]
	if r.Verdict != "adjust" || r.CPURequest != 1 || r.MemRequest != 20480*(1<<20) || r.MemLimit != r.MemRequest {
		t.Errorf("review = %+v", r)
	}
	if failed[idB] != "errored" {
		t.Errorf("failed = %v", failed)
	}
}
