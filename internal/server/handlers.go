package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/monitor"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

// --- fleet & findings ---

func (s *Server) fleet(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{
		"controlPlanes":  map[string]any{"total": 0, "byStatus": map[string]int{}, "byRealm": map[string]int{}},
		"lastMonitorRun": nil,
	}
	if f, err := monitor.LoadFleet(s.st); err == nil && f != nil {
		out["controlPlanes"] = map[string]any{"total": f.Total, "byStatus": f.ByStatus, "byRealm": f.ByRealm}
		out["lastMonitorRun"] = f.At
	}
	open, err := s.st.ListFindings("open")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	counts := map[string]int{"critical": 0, "warning": 0}
	for _, f := range open {
		counts[f.Severity]++
	}
	out["openFindings"] = counts
	n, _ := s.st.CountInvestigationsSince(time.Now().Add(-7 * 24 * time.Hour))
	out["investigations7d"] = n
	writeJSON(w, http.StatusOK, out)
}

func findingView(f *store.Finding) map[string]any {
	var inv any
	if f.InvestigationID != "" {
		inv = f.InvestigationID
	}
	return map[string]any{
		"id": f.ID, "kind": f.Kind, "severity": f.Severity, "title": f.Title, "detail": f.Detail,
		"entityType": f.EntityType, "entityId": f.EntityID, "entityName": f.EntityName,
		"realm": f.Realm, "provider": f.Provider, "region": f.Region, "state": f.State,
		"firstSeenAt": f.FirstSeenAt, "lastSeenAt": f.LastSeenAt, "resolvedAt": timePtr(f.ResolvedAt),
		"investigationId": inv,
		"acknowledgedBy":  f.AcknowledgedBy, "acknowledgedByName": f.AcknowledgedByName, "acknowledgedAt": timePtr(f.AcknowledgedAt),
	}
}

func (s *Server) listFindings(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "open"
	}
	fs, err := s.st.ListFindings(state)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(fs))
	for i := range fs {
		out = append(out, findingView(&fs[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) investigateFinding(w http.ResponseWriter, r *http.Request) {
	f, err := s.st.GetFinding(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "finding not found")
		return
	}
	entity := "control plane"
	if f.EntityType == "execution_plane" {
		entity = "execution plane"
	}
	prompt := fmt.Sprintf("The monitor flagged %s %s (id %s, %s, %s %s): %s — %s\nInvestigate the root cause and tell me what to do.",
		entity, f.EntityName, f.EntityID, f.Realm, f.Provider, f.Region, f.Title, f.Detail)
	chat := &store.Chat{ID: secret.RandomID(9), Owner: session(r).Email, Title: f.EntityName + " — " + f.Title}
	if err := s.st.CreateChat(chat); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"chatId": chat.ID, "prompt": prompt})
}

func (s *Server) ackFinding(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	f, err := s.st.GetFinding(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "finding not found")
		return
	}
	if f.State == "open" {
		sess, now := session(r), time.Now()
		f.State = "acknowledged"
		f.AcknowledgedBy, f.AcknowledgedByName, f.AcknowledgedAt = sess.Email, sess.Username, &now
		if err := s.st.SaveFinding(f); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.st.Audit(session(r).Email, "acknowledge_finding", f.EntityName, "ok", f.ID+": "+f.Title)
	}
	writeJSON(w, http.StatusOK, findingView(f))
}

func (s *Server) monitors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, monitor.Definitions(s.cfg))
}

// --- chats ---

func chatSummary(c *store.Chat) map[string]any {
	return map[string]any{"id": c.ID, "title": c.Title, "createdAt": c.CreatedAt, "updatedAt": c.UpdatedAt}
}

func (s *Server) listChats(w http.ResponseWriter, r *http.Request) {
	chats, err := s.st.ListChats(session(r).Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(chats))
	for i := range chats {
		out = append(out, chatSummary(&chats[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createChat(w http.ResponseWriter, r *http.Request) {
	var in struct{ Title string }
	_ = readJSON(r, &in) // the body is optional
	c := &store.Chat{ID: secret.RandomID(9), Owner: session(r).Email, Title: strings.TrimSpace(in.Title)}
	if err := s.st.CreateChat(c); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chatSummary(c))
}

// ownChat loads a chat and checks the caller owns it.
func (s *Server) ownChat(w http.ResponseWriter, r *http.Request, id string) *store.Chat {
	c, err := s.st.GetChat(id)
	if err != nil || c.Owner != session(r).Email {
		writeError(w, http.StatusNotFound, "chat not found")
		return nil
	}
	return c
}

func (s *Server) getChat(w http.ResponseWriter, r *http.Request) {
	c := s.ownChat(w, r, mux.Vars(r)["id"])
	if c == nil {
		return
	}
	isAdmin := session(r).IsAdmin
	items, err := s.st.ChatItems(c.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	calls, _ := s.st.ToolCallsForChat(c.ID)
	acts, _ := s.st.ActionsForChat(c.ID)

	stepsByItem := map[string][]map[string]any{}
	for i := range calls {
		stepsByItem[calls[i].ChatItemID] = append(stepsByItem[calls[i].ChatItemID], stepView(&calls[i], isAdmin))
	}
	actsByItem := map[string][]map[string]any{}
	for i := range acts {
		actsByItem[acts[i].ChatItemID] = append(actsByItem[acts[i].ChatItemID], agent.ActionView(&acts[i]))
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		var inv any
		if it.InvestigationID != "" {
			inv = it.InvestigationID
		}
		steps, actions := stepsByItem[it.ID], actsByItem[it.ID]
		if steps == nil {
			steps = []map[string]any{}
		}
		if actions == nil {
			actions = []map[string]any{}
		}
		out = append(out, map[string]any{"id": it.ID, "role": it.Role, "text": it.Text, "steps": steps,
			"actions": actions, "investigationId": inv, "createdAt": it.CreatedAt, "keySource": it.KeySource})
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": c.ID, "title": c.Title, "items": out})
}

func stepView(tc *store.ToolCall, isAdmin bool) map[string]any {
	v := map[string]any{"id": tc.ID, "label": tc.Label, "ok": tc.OK}
	if isAdmin {
		v["tool"], v["input"], v["summary"] = tc.Tool, tc.Input, tc.Summary
	}
	return v
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	c := s.ownChat(w, r, mux.Vars(r)["id"])
	if c == nil {
		return
	}
	var in struct{ Text string }
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	sess := session(r)
	user, err := s.user(sess)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session is invalid; sign in again")
		return
	}
	key, source, err := s.claudeKey(sess.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read your Claude key")
		return
	}
	if key == "" {
		writeError(w, http.StatusPreconditionFailed, "add your Claude API key in Settings to use the Copilot")
		return
	}
	if c.Title == "" {
		_ = s.st.TouchChat(c.ID, titleFrom(in.Text))
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // nginx ingress: don't buffer the stream
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	emit := func(event string, data any) {
		raw, err := json.Marshal(data)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		flusher.Flush()
	}
	req := agent.TurnRequest{Chat: c, User: user, APIKey: key, KeySource: source, Text: strings.TrimSpace(in.Text)}
	if err := s.agent.Run(r.Context(), req, emit); err != nil {
		log.Printf("chat %s turn ended with error: %v", c.ID, err)
	}
}

func titleFrom(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	if len(t) > 60 {
		t = t[:60] + "…"
	}
	return t
}

// --- actions ---

// ownAction loads an action from one of the caller's chats.
func (s *Server) ownAction(w http.ResponseWriter, r *http.Request) *store.Action {
	act, err := s.st.GetAction(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "action not found")
		return nil
	}
	if s.ownChat(w, r, act.ChatID) == nil {
		return nil
	}
	return act
}

func (s *Server) executeAction(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ReadOnly {
		writeError(w, http.StatusForbidden, agent.ErrReadOnly.Error())
		return
	}
	act := s.ownAction(w, r)
	if act == nil {
		return
	}
	user, err := s.user(session(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session is invalid; sign in again")
		return
	}
	env := &tools.Env{CH: s.ch, Token: user.Token, Kube: KubeAccess(s.ch, s.kube, s.hub)}
	if err := agent.ExecuteAction(r.Context(), s.st, env, act, user, s.cfg.ReadOnly); err != nil {
		if errors.Is(err, agent.ErrActionNotPending) || errors.Is(err, agent.ErrPlanChanged) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agent.ActionView(act))
}

func (s *Server) dismissAction(w http.ResponseWriter, r *http.Request) {
	act := s.ownAction(w, r)
	if act == nil {
		return
	}
	if act.Status == "proposed" {
		act.Status = "dismissed"
		if err := s.st.SaveAction(act); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.st.Audit(session(r).Email, act.Kind, act.TargetName, "dismissed", act.ID)
	}
	writeJSON(w, http.StatusOK, agent.ActionView(act))
}

// --- investigations ---

func invSummary(inv *store.Investigation) map[string]any {
	return map[string]any{"id": inv.ID, "title": inv.Title, "entityName": inv.EntityName, "category": inv.Category,
		"requester": inv.Requester, "createdAt": inv.CreatedAt, "chatId": inv.ChatID}
}

func (s *Server) listInvestigations(w http.ResponseWriter, r *http.Request) {
	sess := session(r)
	requester := sess.Email
	if sess.IsAdmin {
		requester = ""
	}
	invs, err := s.st.ListInvestigations(requester)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(invs))
	for i := range invs {
		out = append(out, invSummary(&invs[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getInvestigation(w http.ResponseWriter, r *http.Request) {
	sess := session(r)
	inv, err := s.st.GetInvestigation(mux.Vars(r)["id"])
	if err != nil || (!sess.IsAdmin && inv.Requester != sess.Email) {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	out := invSummary(inv)
	out["entityId"] = inv.EntityID
	out["summaryForRequester"] = inv.SummaryForRequester
	out["confidence"] = inv.Confidence

	acts, _ := s.st.ActionsForChat(inv.ChatID)
	actions := make([]map[string]any, 0, len(acts))
	for i := range acts {
		actions = append(actions, agent.ActionView(&acts[i]))
	}
	out["actions"] = actions

	if sess.IsAdmin {
		out["rootCause"] = inv.RootCause
		out["recommendedFix"] = inv.RecommendedFix
		calls, _ := s.st.ToolCallsForChat(inv.ChatID)
		evidence := make([]map[string]any, 0, len(calls))
		for _, tc := range calls {
			if tc.Tool == "record_investigation" || tc.Tool == "propose_action" {
				continue
			}
			ok := tc.OK != nil && *tc.OK
			evidence = append(evidence, map[string]any{"at": tc.At, "tool": tc.Tool, "input": tc.Input,
				"summary": tc.Summary, "ok": ok})
		}
		out["evidence"] = evidence
	}
	writeJSON(w, http.StatusOK, out)
}

// --- guardrails & audit ---

func (s *Server) policy(w http.ResponseWriter, _ *http.Request) {
	retryMode := "Ask requester"
	if s.cfg.ReadOnly {
		retryMode = "Dry run only"
	}
	writeJSON(w, http.StatusOK, []map[string]string{
		{"action": "Read & diagnose", "examples": "control planes, execution planes, jobs, control-hub logs, DNS, TLS, HTTPS probes", "mode": "Auto"},
		{"action": "Retry post-deploy", "examples": "re-run SAP control plane or execution plane post-deploy", "mode": retryMode},
		{"action": "Restart a pod / clean up", "examples": "delete a pod its controller recreates; delete orphaned go-sandboxes", "mode": retryMode},
		{"action": "Reversible infra fix", "examples": "tag subnets, re-issue a certificate", "mode": "Not available yet"},
		{"action": "IAM / role changes", "examples": "grant AcrPull, workload identity bindings", "mode": "Not available yet"},
		{"action": "Scale & upgrade", "examples": "node pool size, Prophecy version, size config", "mode": "Not available yet"},
		{"action": "Create cluster", "examples": "new control plane, execution plane or infrastructure", "mode": "Not available yet"},
		{"action": "Delete anything", "examples": "control planes, execution planes, infrastructure, DNS, certificates", "mode": "Never"},
	})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.st.ListAudit(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{"at": e.At, "actor": e.Actor, "action": e.Action, "target": e.Target,
			"decision": e.Decision, "detail": e.Detail})
	}
	writeJSON(w, http.StatusOK, out)
}

// auditChanges runs the Copilot's audit_search with query parameters (cluster,
// kind, namespace, name, actor, since, scope, service, include_system), for
// admins: who changed what, from Cloud Audit Logs or field owners.
func (s *Server) auditChanges(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	if s.agent == nil || s.agent.Audit() == nil {
		writeError(w, http.StatusServiceUnavailable, "audit search is off (AUDIT_GCP_PROJECTS)")
		return
	}
	user, err := s.user(session(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session is invalid; sign in again")
		return
	}
	q := r.URL.Query()
	in := map[string]any{"include_system": q.Get("include_system") == "true"}
	for _, k := range []string{"scope", "cluster", "kind", "namespace", "name", "actor", "since", "until", "service"} {
		if v := q.Get(k); v != "" {
			in[k] = v
		}
	}
	raw, _ := json.Marshal(in)
	env := &tools.Env{CH: s.ch, Token: user.Token, Kube: KubeAccess(s.ch, s.kube, s.hub)}
	out, err := tools.AuditTool(s.agent.Audit()).Run(r.Context(), env, raw)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(out))
}
