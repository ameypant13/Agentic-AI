package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

func TestRightsizingReview(t *testing.T) {
	h := newHarness(t)
	h.server.costs = cost.NewWorker(cost.Config{}, grafana.NewHub(), h.st, cost.NewPrices(filepath.Join(t.TempDir(), "p"), "", ""))
	now := time.Now().UTC()
	for i := 1; i <= 10; i++ {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		wl := []store.CostWorkloadDay{{Day: day, Cluster: "westpac", Namespace: "prophecy", Kind: "Deployment", Name: "edweb", CPUCost: 26, MemCost: 4, CPURequest: 26 * 24, MemRequest: 29 * 24}}
		ct := []store.CostContainerDay{{Day: day, Cluster: "westpac", Namespace: "prophecy", Kind: "Deployment", Name: "edweb", Container: "edweb",
			CPUP95: 0.4, CPUMax: 0.9, MemMax: 13 << 30, CPURequest: 26, MemRequest: 29 << 30, MemLimit: 29 << 30, PodHours: 24, Pods: 1}}
		if err := h.st.ReplaceCostDay(&store.CostClusterDay{Day: day, Cluster: "westpac", NodeCost: 40, Hours: 24}, wl, ct); err != nil {
			t.Fatal(err)
		}
	}
	key := "westpac|prophecy|Deployment|edweb|edweb"
	customID := agent.ReviewCustomID(key)
	ended := false
	batch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/messages/batches":
			_, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"id":"msgbatch_9","type":"message_batch","processing_status":"in_progress","request_counts":{"processing":1}}`))
		case r.URL.Path == "/v1/messages/batches/msgbatch_9":
			status := "in_progress"
			if ended {
				status = "ended"
			}
			fmt.Fprintf(w, `{"id":"msgbatch_9","type":"message_batch","processing_status":%q,"request_counts":{"succeeded":1}}`, status)
		case r.URL.Path == "/v1/messages/batches/msgbatch_9/results":
			line, _ := json.Marshal(map[string]any{"custom_id": customID, "result": map[string]any{"type": "succeeded", "message": map[string]any{
				"id": "m", "type": "message", "role": "assistant", "model": "claude-opus-5-5", "stop_reason": "tool_use",
				"content": []any{map[string]any{"type": "tool_use", "id": "t", "name": "review_recommendation",
					"input": map[string]any{"verdict": "risky", "reason": "JVM heap is pinned at 28g."}}},
				"usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}}})
			w.Write(append(line, '\n'))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(batch.Close)
	h.server.agent.WithClientOptions(option.WithBaseURL(batch.URL))

	sre, admin := h.login("sre@prophecy.io"), h.login("admin@prophecy.io")
	if st := h.call(sre, "POST", "/api/costs/review", map[string]int{}, nil); st != 403 {
		t.Errorf("non-admin submit %d", st)
	}
	if st := h.call(admin, "POST", "/api/costs/review", map[string]int{"limit": 10}, nil); st != 202 {
		t.Fatalf("submit %d", st)
	}
	if st := h.call(admin, "POST", "/api/costs/review", map[string]int{}, nil); st != 409 {
		t.Errorf("second submit while running %d", st)
	}
	var got struct {
		Batch   store.ReviewBatch `json:"batch"`
		Reviews []reviewView      `json:"reviews"`
	}
	h.call(sre, "GET", "/api/costs/review", nil, &got)
	if got.Batch.ID != "msgbatch_9" || len(got.Reviews) != 1 || got.Reviews[0].Status != "pending" || got.Reviews[0].Key != key {
		t.Fatalf("after submit = %+v", got)
	}
	h.server.PollReviews(context.Background()) // still running
	ended = true
	h.server.PollReviews(context.Background())
	h.call(sre, "GET", "/api/costs/review", nil, &got)
	if got.Batch.Status != "ended" || got.Reviews[0].Verdict != "risky" || got.Reviews[0].Status != "done" {
		t.Errorf("after poll = %+v", got)
	}
}
