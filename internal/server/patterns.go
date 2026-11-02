package server

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/patterns"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Error patterns no log rule matches, and Claude-drafted rules for them or
// for approved learnings. Drafting only proposes: the admin saves the rule
// through the normal log-rule endpoint.

func (s *Server) registerPatternRoutes(r *mux.Router) {
	r.HandleFunc("/log-patterns", s.listPatterns).Methods(http.MethodGet)
	r.HandleFunc("/log-patterns/{id}", s.setPatternStatus).Methods(http.MethodPut)
	r.HandleFunc("/log-patterns/{id}/draft-rule", s.draftRuleFromPattern).Methods(http.MethodPost)
	r.HandleFunc("/learnings/{id}/draft-rule", s.draftRuleFromLearning).Methods(http.MethodPost)
}

type patternView struct {
	ID        string         `json:"id"`
	Service   string         `json:"service"`
	Template  string         `json:"template"`
	Example   string         `json:"example"`
	Count     int            `json:"count"`
	Clusters  map[string]int `json:"clusters"`
	FirstSeen time.Time      `json:"firstSeen"`
	LastSeen  time.Time      `json:"lastSeen"`
	Last6h    int            `json:"last6h"`
	Last24h   int            `json:"last24h"`
	IsNew     bool           `json:"isNew"`
	Status    string         `json:"status"`
	RuleID    string         `json:"ruleId,omitempty"`
}

func (s *Server) listPatterns(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	rows, err := s.st.ListLogPatterns(r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	out := make([]patternView, 0, len(rows))
	for _, row := range rows {
		t := patterns.FromRow(row)
		l6, l24, isNew := t.Trend(now)
		out = append(out, patternView{ID: row.ID, Service: row.Service, Template: row.Template, Example: row.Example, Count: row.Count,
			Clusters: t.Clusters, FirstSeen: row.FirstSeen, LastSeen: row.LastSeen, Last6h: l6, Last24h: l24, IsNew: isNew,
			Status: row.Status, RuleID: row.RuleID})
	}
	// New and active first: what to look at now.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsNew != out[j].IsNew {
			return out[i].IsNew
		}
		if out[i].Last24h != out[j].Last24h {
			return out[i].Last24h > out[j].Last24h
		}
		return out[i].Count > out[j].Count
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setPatternStatus(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in struct {
		Status string `json:"status"`
		RuleID string `json:"ruleId"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch in.Status {
	case "new", "ignored", "ruled":
	default:
		writeError(w, http.StatusBadRequest, "status must be new, ignored or ruled")
		return
	}
	id := mux.Vars(r)["id"]
	if _, err := s.st.GetLogPattern(id); err != nil {
		writeError(w, http.StatusNotFound, "pattern not found")
		return
	}
	if err := s.st.SetLogPatternStatus(id, in.Status, in.RuleID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(session(r).Email, "log_pattern_"+in.Status, id, "ok", in.RuleID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) draftRuleFromPattern(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	p, err := s.st.GetLogPattern(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "pattern not found")
		return
	}
	s.draftRule(w, r, agent.RuleSource{Kind: "pattern", Service: serviceName(p), Template: p.Template, Examples: []string{p.Example}})
}

func (s *Server) draftRuleFromLearning(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	l, err := s.st.GetLearning(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "learning not found")
		return
	}
	if l.Status != store.LearningApproved {
		writeError(w, http.StatusConflict, "approve the learning before turning it into a rule")
		return
	}
	s.draftRule(w, r, agent.RuleSource{Kind: "learning", Title: l.Title, Symptoms: l.Symptoms, RootCause: l.RootCause, Fix: l.Fix})
}

func (s *Server) draftRule(w http.ResponseWriter, r *http.Request, src agent.RuleSource) {
	key, _, err := s.claudeKey(session(r).Email)
	if err != nil || key == "" {
		writeError(w, http.StatusPreconditionFailed, "add your Claude API key in Settings to draft rules")
		return
	}
	for _, rule := range s.lib.Rules() {
		src.ExistingIDs = append(src.ExistingIDs, rule.ID)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	draft, err := s.agent.DraftLogRule(ctx, key, src)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.st.Audit(session(r).Email, "draft_log_rule", src.Kind, "drafted", draft.Rule.ID+draft.Reason)
	writeJSON(w, http.StatusOK, draft)
}

// serviceName is the rule's service: the workload without the "-prophecy" suffix.
func serviceName(p *store.LogPattern) string {
	const suffix = "-prophecy"
	if len(p.Service) > len(suffix) && p.Service[len(p.Service)-len(suffix):] == suffix {
		return p.Service[:len(p.Service)-len(suffix)]
	}
	return p.Service
}
