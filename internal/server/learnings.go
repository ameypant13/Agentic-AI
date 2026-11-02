package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Learnings are reviewed by admins: the Copilot only drafts them, and a draft
// is searchable only after an admin approves it.

func (s *Server) registerLearningRoutes(r *mux.Router) {
	r.HandleFunc("/learnings", s.listLearnings).Methods(http.MethodGet)
	r.HandleFunc("/learnings/expand", s.expandLearningQuery).Methods(http.MethodGet)
	r.HandleFunc("/learnings", s.createLearning).Methods(http.MethodPost)
	r.HandleFunc("/learnings/{id}", s.getLearning).Methods(http.MethodGet)
	r.HandleFunc("/learnings/{id}", s.updateLearning).Methods(http.MethodPut)
	r.HandleFunc("/learnings/{id}", s.deleteLearning).Methods(http.MethodDelete)
}

type learningOut struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Category       string     `json:"category"`
	Status         string     `json:"status"`
	Symptoms       string     `json:"symptoms"`
	RootCause      string     `json:"rootCause"`
	HowToConfirm   string     `json:"howToConfirm"`
	Fix            string     `json:"fix"`
	KGNodes        string     `json:"kgNodes"`
	Entities       []string   `json:"entities"`
	Investigations []string   `json:"investigations"`
	TimesSeen      int        `json:"timesSeen"`
	Confirmed      int        `json:"confirmed"`
	Contradicted   int        `json:"contradicted"`
	ReviewNote     string     `json:"reviewNote"`
	CreatedBy      string     `json:"createdBy"`
	ApprovedBy     string     `json:"approvedBy"`
	ApprovedAt     *time.Time `json:"approvedAt"`
	LastSeen       time.Time  `json:"lastSeen"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

func toLearningOut(l *store.Learning) learningOut {
	return learningOut{ID: l.ID, Title: l.Title, Category: l.Category, Status: l.Status, Symptoms: l.Symptoms, RootCause: l.RootCause,
		HowToConfirm: l.HowToConfirm, Fix: l.Fix, KGNodes: l.KGNodes, Entities: l.EntityList(), Investigations: l.InvestigationList(),
		TimesSeen: l.TimesSeen, Confirmed: l.Confirmed, Contradicted: l.Contradicted, ReviewNote: l.ReviewNote, CreatedBy: l.CreatedBy,
		ApprovedBy: l.ApprovedBy, ApprovedAt: l.ApprovedAt, LastSeen: l.LastSeen, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt}
}

func (s *Server) listLearnings(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	status := r.URL.Query().Get("status")
	var list []store.Learning
	var err error
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		statuses := []string{store.LearningDraft, store.LearningApproved, store.LearningRetired}
		if status != "" {
			statuses = []string{status}
		}
		// alt: rewordings from /learnings/expand, searched together with q.
		queries := append([]string{q}, r.URL.Query()["alt"]...)
		var matches []store.LearningMatch
		matches, err = s.st.SearchLearningsAny(queries[:min(len(queries), 6)], "", "", statuses, 200)
		for _, m := range matches {
			list = append(list, m.Learning)
		}
	} else {
		list, err = s.st.ListLearnings(status)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]learningOut, 0, len(list))
	for i := range list {
		out = append(out, toLearningOut(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// expandLearningQuery asks Claude (a small model, with the caller's key) for
// rewordings of a search, for GET /learnings?q=…&alt=….
func (s *Server) expandLearningQuery(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	key, _, err := s.claudeKey(session(r).Email)
	if err != nil || key == "" {
		writeError(w, http.StatusPreconditionFailed, "add your Claude API key in Settings to search by meaning")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	alts, err := s.agent.ExpandQuery(ctx, key, q)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alternatives": alts})
}

func (s *Server) getLearning(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	l, err := s.st.GetLearning(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "learning not found")
		return
	}
	writeJSON(w, http.StatusOK, toLearningOut(l))
}

type learningIn struct {
	Title        *string `json:"title"`
	Category     *string `json:"category"`
	Status       *string `json:"status"`
	Symptoms     *string `json:"symptoms"`
	RootCause    *string `json:"rootCause"`
	HowToConfirm *string `json:"howToConfirm"`
	Fix          *string `json:"fix"`
	KGNodes      *string `json:"kgNodes"`
	// ClearReview drops the contradiction note once an admin has dealt with it.
	ClearReview bool `json:"clearReview"`
}

func (in learningIn) apply(l *store.Learning, by string, now time.Time) error {
	set := func(dst *string, v *string, max int) {
		if v != nil {
			*dst = clipStr(strings.TrimSpace(*v), max)
		}
	}
	set(&l.Title, in.Title, 200)
	set(&l.Category, in.Category, 64)
	set(&l.Symptoms, in.Symptoms, 4000)
	set(&l.RootCause, in.RootCause, 4000)
	set(&l.HowToConfirm, in.HowToConfirm, 4000)
	set(&l.Fix, in.Fix, 4000)
	set(&l.KGNodes, in.KGNodes, 200)
	if in.ClearReview {
		l.ReviewNote = ""
	}
	if in.Status != nil {
		switch *in.Status {
		case store.LearningApproved:
			if l.Status != store.LearningApproved {
				l.ApprovedBy, l.ApprovedAt = by, &now
			}
		case store.LearningDraft, store.LearningRetired:
		default:
			return errors.New("status must be draft, approved or retired")
		}
		l.Status = *in.Status
	}
	if l.Title == "" || l.Symptoms == "" || l.RootCause == "" {
		return errors.New("title, symptoms and root cause are required")
	}
	return nil
}

func (s *Server) createLearning(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in learningIn
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, now := session(r), time.Now()
	l := &store.Learning{ID: "LRN-" + strings.ToUpper(secret.RandomID(4)), Status: store.LearningDraft, CreatedBy: sess.Email,
		CreatedAt: now, LastSeen: now, Entities: "[]", Investigations: "[]"}
	if err := in.apply(l, sess.Email, now); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.SaveLearning(l); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(sess.Email, "create_learning", l.ID, l.Status, l.Title)
	writeJSON(w, http.StatusCreated, toLearningOut(l))
}

func (s *Server) updateLearning(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	l, err := s.st.GetLearning(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "learning not found")
		return
	}
	var in learningIn
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, before := session(r), l.Status
	if err := in.apply(l, sess.Email, time.Now()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.SaveLearning(l); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	decision := "edited"
	if before != l.Status {
		decision = l.Status
	}
	s.st.Audit(sess.Email, "update_learning", l.ID, decision, l.Title)
	writeJSON(w, http.StatusOK, toLearningOut(l))
}

// deleteLearning removes drafts only; approved learnings are retired instead,
// so the record of what the Copilot was told survives.
func (s *Server) deleteLearning(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	l, err := s.st.GetLearning(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "learning not found")
		return
	}
	if l.Status != store.LearningDraft {
		writeError(w, http.StatusConflict, "only drafts can be deleted; retire an approved learning instead")
		return
	}
	if err := s.st.DeleteLearning(l.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(session(r).Email, "delete_learning", l.ID, "deleted", l.Title)
	w.WriteHeader(http.StatusNoContent)
}

func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
