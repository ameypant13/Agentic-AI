package server

import (
	"context"
	"log"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Bulk right-sizing review: an admin submits the fleet's most valuable
// recommendations to Claude as one Message Batch; a poller collects the
// verdicts when it ends. The submitting admin's Claude key is read again
// (sealed in the database) whenever the poller needs it.

const (
	reviewDefault = 50
	reviewMax     = 200
	reviewPoll    = 2 * time.Minute
)

type reviewView struct {
	Key        string     `json:"key"`
	Status     string     `json:"status"`
	Verdict    string     `json:"verdict"`
	Reason     string     `json:"reason"`
	CPURequest float64    `json:"cpuRequest"`
	MemRequest float64    `json:"memRequest"`
	MemLimit   float64    `json:"memLimit"`
	ReviewedAt *time.Time `json:"reviewedAt"`
}

func (s *Server) getReviews(w http.ResponseWriter, _ *http.Request) {
	batches, _ := s.st.ReviewBatches("", 1)
	rows, err := s.st.RightsizingReviews("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]reviewView, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewView{Key: r.Key, Status: r.Status, Verdict: r.Verdict, Reason: r.Reason, CPURequest: r.CPURequest,
			MemRequest: r.MemRequest, MemLimit: r.MemLimit, ReviewedAt: r.ReviewedAt})
	}
	var latest any
	if len(batches) > 0 {
		latest = batches[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{"batch": latest, "reviews": out})
}

// reviewCandidates picks what is worth Claude's time: the biggest savings
// and everything that needs more, skipping low-confidence data.
func reviewCandidates(recs []cost.Recommendation, limit int) []cost.Recommendation {
	var out []cost.Recommendation
	for _, r := range recs {
		if r.Confidence == "low" {
			continue
		}
		if r.MonthlySaving >= 10 || NeedsMore(r) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := NeedsMore(out[i]), NeedsMore(out[j]); a != b {
			return a
		}
		return math.Abs(out[i].MonthlySaving) > math.Abs(out[j].MonthlySaving)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Server) submitReview(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	if s.costs == nil {
		writeError(w, http.StatusServiceUnavailable, "workload cost is turned off (COST_ENABLED)")
		return
	}
	if active, _ := s.st.ReviewBatches("in_progress", 1); len(active) > 0 {
		writeError(w, http.StatusConflict, "a review is already running")
		return
	}
	sess := session(r)
	key, _, err := s.claudeKey(sess.Email)
	if err != nil || key == "" {
		writeError(w, http.StatusPreconditionFailed, "add your Claude API key in Settings to run a review")
		return
	}
	var in struct {
		Limit int `json:"limit"`
	}
	_ = readJSON(r, &in)
	if in.Limit <= 0 {
		in.Limit = reviewDefault
	}
	in.Limit = min(in.Limit, reviewMax)
	to := time.Now().UTC()
	recs, err := s.cachedRightsizing(to.AddDate(0, 0, -29).Format("2006-01-02"), to.Format("2006-01-02"), "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	picked := reviewCandidates(recs, in.Limit)
	if len(picked) == 0 {
		writeError(w, http.StatusConflict, "no recommendation is worth a review yet (they need 7+ days of data and a $10/month change or a shortfall)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	id, err := s.agent.SubmitReview(ctx, key, picked)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	now := time.Now()
	b := &store.ReviewBatch{ID: id, SubmittedBy: sess.Email, SubmittedAt: now, Status: "in_progress", Requests: len(picked)}
	if err := s.st.SaveReviewBatch(b); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, rec := range picked {
		k := agent.ReviewKey(rec)
		row, err := s.st.GetRightsizingReview(k)
		if err != nil {
			row = &store.RightsizingReview{Key: k}
		}
		// The previous verdict stays visible until the new one arrives.
		row.CustomID, row.BatchID, row.Status = agent.ReviewCustomID(k), id, "pending"
		_ = s.st.SaveRightsizingReview(row)
	}
	s.st.Audit(sess.Email, "rightsizing_review", id, "submitted", "")
	writeJSON(w, http.StatusAccepted, b)
}

// StartReviewPoller collects verdicts from running batches.
func (s *Server) StartReviewPoller(ctx context.Context) {
	go func() {
		t := time.NewTicker(reviewPoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			s.PollReviews(ctx)
		}
	}()
}

// PollReviews checks every running batch once.
func (s *Server) PollReviews(ctx context.Context) {
	active, err := s.st.ReviewBatches("in_progress", 10)
	if err != nil {
		return
	}
	for i := range active {
		b := &active[i]
		key, _, err := s.claudeKey(b.SubmittedBy)
		if err != nil || key == "" {
			b.Status, b.Error = "failed", "the submitter's Claude key is gone"
			_ = s.st.SaveReviewBatch(b)
			continue
		}
		st, err := s.agent.ReviewStatus(ctx, key, b.ID)
		if err != nil {
			log.Printf("review %s: %v", b.ID, err)
			continue
		}
		b.Succeeded, b.Errored = int(st.Succeeded), int(st.Errored+st.Expired+st.Gone)
		if !st.Ended {
			_ = s.st.SaveReviewBatch(b)
			continue
		}
		reviews, failed, err := s.agent.ReviewResults(ctx, key, b.ID)
		if err != nil {
			log.Printf("review %s results: %v", b.ID, err)
			continue
		}
		rows, _ := s.st.RightsizingReviews(b.ID)
		now := time.Now()
		for j := range rows {
			row := &rows[j]
			if rv, ok := reviews[row.CustomID]; ok {
				row.Status, row.Verdict, row.Reason = "done", rv.Verdict, rv.Reason
				row.CPURequest, row.MemRequest, row.MemLimit, row.ReviewedAt = rv.CPURequest, rv.MemRequest, rv.MemLimit, &now
			} else if _, bad := failed[row.CustomID]; bad || row.Status == "pending" {
				row.Status = "failed"
			}
			_ = s.st.SaveRightsizingReview(row)
		}
		b.Status, b.EndedAt = "ended", &now
		_ = s.st.SaveReviewBatch(b)
	}
}
