package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/logrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/parser"
)

type ruleView struct {
	logrules.Rule
	Hits24h int `json:"hits24h"`
}

func (s *Server) registerLogRuleRoutes(rt *mux.Router) {
	rt.HandleFunc("/log-rules", s.listLogRules).Methods(http.MethodGet)
	rt.HandleFunc("/log-rules", s.createLogRule).Methods(http.MethodPost)
	rt.HandleFunc("/log-rules/test", s.testLogLine).Methods(http.MethodPost)
	rt.HandleFunc("/log-rules/{id}", s.updateLogRule).Methods(http.MethodPut)
	rt.HandleFunc("/log-rules/{id}", s.resetLogRule).Methods(http.MethodDelete)
	rt.HandleFunc("/log-rules/{id}/enable", s.toggleLogRule(true)).Methods(http.MethodPost)
	rt.HandleFunc("/log-rules/{id}/disable", s.toggleLogRule(false)).Methods(http.MethodPost)
	// Stored results only: the background worker parses logs; this never scans.
	rt.HandleFunc("/clusters/{id}/log-scan", s.clusterRead(func(_ context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return s.worker.Summary(cl.Infra.ID, r.URL.Query().Get("ns")), nil
	})).Methods(http.MethodGet)
}

func (s *Server) listLogRules(w http.ResponseWriter, _ *http.Request) {
	totals, err := s.st.LogHitTotals(time.Now().Add(-24 * time.Hour))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rules := s.lib.Rules()
	out := make([]ruleView, 0, len(rules))
	for _, r := range rules {
		out = append(out, ruleView{Rule: r, Hits24h: totals[r.ID]})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createLogRule(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in logrules.Rule
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.ID = strings.TrimSpace(in.ID)
	for _, existing := range s.lib.Rules() {
		if existing.ID == in.ID {
			writeError(w, http.StatusConflict, "a rule with this id already exists; edit it instead")
			return
		}
	}
	s.saveLogRule(w, r, in, "create_log_rule")
}

func (s *Server) updateLogRule(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in logrules.Rule
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.ID = mux.Vars(r)["id"]
	s.saveLogRule(w, r, in, "edit_log_rule")
}

func (s *Server) saveLogRule(w http.ResponseWriter, r *http.Request, in logrules.Rule, action string) {
	if err := s.lib.Save(in, session(r).Email); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.Audit(session(r).Email, action, in.ID, "ok", in.Pattern)
	writeJSON(w, http.StatusOK, s.ruleByID(in.ID))
}

func (s *Server) toggleLogRule(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminOnly(w, r) {
			return
		}
		id := mux.Vars(r)["id"]
		if err := s.lib.SetEnabled(id, enabled, session(r).Email); err != nil {
			if errors.Is(err, parser.ErrNotFound) {
				writeError(w, http.StatusNotFound, "rule not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		action := "disable_log_rule"
		if enabled {
			action = "enable_log_rule"
		}
		s.st.Audit(session(r).Email, action, id, "ok", "")
		writeJSON(w, http.StatusOK, s.ruleByID(id))
	}
}

func (s *Server) resetLogRule(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	id := mux.Vars(r)["id"]
	if err := s.lib.Reset(id); err != nil {
		if errors.Is(err, parser.ErrNotFound) {
			writeError(w, http.StatusNotFound, "this rule has no changes to reset")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(session(r).Email, "reset_log_rule", id, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) ruleByID(id string) any {
	for _, r := range s.lib.Rules() {
		if r.ID == id {
			return ruleView{Rule: r}
		}
	}
	return map[string]any{}
}

func (s *Server) testLogLine(w http.ResponseWriter, r *http.Request) {
	var in struct{ Line, Pod string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	matches := s.lib.Test(strings.TrimSpace(in.Pod), in.Line)
	if matches == nil {
		matches = []logrules.Rule{}
	}
	writeJSON(w, http.StatusOK, matches)
}

// logsWithParser is the logs response plus the parser's result over the same lines.
type logsWithParser struct {
	*kube.Logs
	Parser logrules.Result `json:"parser"`
}

func (s *Server) podLogsWithParser(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
	v, q := mux.Vars(r), r.URL.Query()
	since := clampInt(q.Get("sinceMinutes"), 360, 7*24*60)
	tail := clampInt(q.Get("tail"), 2000, 10000)
	logs, err := cl.PodLogs(ctx, v["ns"], v["name"], q.Get("container"), time.Duration(since)*time.Minute, int64(tail), q.Get("previous") == "true")
	if err != nil {
		return nil, err
	}
	return logsWithParser{Logs: logs, Parser: s.lib.Set().Scan(v["name"], logs.Lines)}, nil
}
