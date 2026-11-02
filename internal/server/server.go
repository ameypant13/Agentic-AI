// Package server is the AI console's HTTP API (see API.md) and serves the
// built web app.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/certs"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/config"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/metricrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/parser"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

const cookieName = "aic_session"

type Server struct {
	cfg   *config.Config
	st    *store.Store
	ch    *controlhub.Client
	box   *secret.Box
	agent *agent.Agent
	kube  *kube.Registry
	lib   *parser.Library
	// worker parses cluster logs in the background; pages read its summaries.
	worker  *parser.Worker
	hub     *grafana.Hub
	metrics *metricrules.Worker
	certs   *certs.Worker
	costs   *cost.Worker
	recs    *recCache
	// clusters caches cluster summaries and per-session cluster access checks.
	clusters *clusterCache
	apiRes   *apiResourceCache
	// cardCompute replaces computeCard in tests.
	cardCompute func(ctx context.Context, in kube.Infra) (*kube.Summary, string)
	// verifyKey checks a Claude API key before it is stored. A field so tests can stub it.
	verifyKey func(ctx context.Context, apiKey string) error
	// slack is the Copilot-in-Slack bot's state; slackBaseURL overrides the
	// Slack Web API address in tests.
	slackBot     *slackBot
	slackBaseURL string
}

// Deps are the server's collaborators.
type Deps struct {
	Store   *store.Store
	CH      *controlhub.Client
	Box     *secret.Box
	Agent   *agent.Agent
	Kube    *kube.Registry
	Rules   *parser.Library
	LogWork *parser.Worker
	Grafana *grafana.Hub
	Metrics *metricrules.Worker
	Certs   *certs.Worker
	Costs   *cost.Worker
}

func New(cfg *config.Config, d Deps) *Server {
	st, ch, box, ag, kr, lib, worker := d.Store, d.CH, d.Box, d.Agent, d.Kube, d.Rules, d.LogWork
	s := &Server{cfg: cfg, st: st, ch: ch, box: box, agent: ag, kube: kr, lib: lib, worker: worker, hub: d.Grafana, metrics: d.Metrics, certs: d.Certs, costs: d.Costs, recs: &recCache{m: map[string]recEntry{}, changes: map[string]changeEntry{}},
		clusters: newClusterCache(), apiRes: &apiResourceCache{byID: map[string]cachedAPIResources{}}, verifyKey: func(ctx context.Context, k string) error { return verifyClaudeKey(ctx, k, cfg.Model) },
		slackBot: &slackBot{seen: map[string]time.Time{}, busy: map[string]bool{}}}
	if ag != nil {
		ag.WithReadiness(s.ReadinessJSON)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()

	api.HandleFunc("/auth/login", s.login).Methods(http.MethodPost)
	api.HandleFunc("/auth/token", s.loginWithToken).Methods(http.MethodPost)
	api.HandleFunc("/auth/logout", s.logout).Methods(http.MethodPost)
	// Signed by Slack, not by a session cookie.
	api.HandleFunc("/slack/events", s.slackEvents).Methods(http.MethodPost)
	api.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })

	authed := api.NewRoute().Subrouter()
	authed.Use(s.requireSession)
	authed.HandleFunc("/me", s.me).Methods(http.MethodGet)
	authed.HandleFunc("/me/claude-key", s.putClaudeKey).Methods(http.MethodPut)
	authed.HandleFunc("/me/claude-key", s.deleteClaudeKey).Methods(http.MethodDelete)

	authed.HandleFunc("/fleet", s.fleet).Methods(http.MethodGet)
	authed.HandleFunc("/findings", s.listFindings).Methods(http.MethodGet)
	authed.HandleFunc("/findings/{id}/investigate", s.investigateFinding).Methods(http.MethodPost)
	authed.HandleFunc("/findings/{id}/ack", s.ackFinding).Methods(http.MethodPost)
	authed.HandleFunc("/monitors", s.monitors).Methods(http.MethodGet)

	authed.HandleFunc("/chats", s.listChats).Methods(http.MethodGet)
	authed.HandleFunc("/chats", s.createChat).Methods(http.MethodPost)
	authed.HandleFunc("/chats/{id}", s.getChat).Methods(http.MethodGet)
	authed.HandleFunc("/chats/{id}/messages", s.sendMessage).Methods(http.MethodPost)

	authed.HandleFunc("/actions/{id}/execute", s.executeAction).Methods(http.MethodPost)
	authed.HandleFunc("/actions/{id}/dismiss", s.dismissAction).Methods(http.MethodPost)

	authed.HandleFunc("/investigations", s.listInvestigations).Methods(http.MethodGet)
	authed.HandleFunc("/investigations/{id}", s.getInvestigation).Methods(http.MethodGet)

	s.registerClusterRoutes(authed)
	s.registerLogRuleRoutes(authed)
	s.registerResourceRoutes(authed)
	s.registerGrafanaRoutes(authed)
	s.registerCertRoutes(authed)
	s.registerCostRoutes(authed)
	s.registerLearningRoutes(authed)
	s.registerPatternRoutes(authed)
	authed.HandleFunc("/readiness", s.readinessHandler).Methods(http.MethodGet)
	authed.HandleFunc("/digest", s.getDigest).Methods(http.MethodGet)
	authed.HandleFunc("/digest/send", s.sendDigest).Methods(http.MethodPost)

	authed.HandleFunc("/policy", s.policy).Methods(http.MethodGet)
	authed.HandleFunc("/audit", s.audit).Methods(http.MethodGet)
	authed.HandleFunc("/audit/changes", s.auditChanges).Methods(http.MethodGet)

	api.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "no such API route")
	})
	r.Handle("/docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
	r.PathPrefix("/docs/").Handler(s.docsHandler())
	r.PathPrefix("/").Handler(spaHandler(s.cfg.StaticDir))
	return securityHeaders(r)
}

// --- helpers ---

type ctxKey struct{}

// session returns the signed-in session; requireSession guarantees it.
func session(r *http.Request) *store.Session { return r.Context().Value(ctxKey{}).(*store.Session) }

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		sess, err := s.st.GetSession(c.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "session expired; sign in again")
			return
		}
		// The stored flag is control-hub's; CONSOLE_ADMIN_EMAILS is checked on
		// every request so adding or removing an email takes effect at once.
		sess.IsAdmin = sess.IsAdmin || s.cfg.IsConsoleAdmin(sess.Email)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess)))
	})
}

// user returns the agent identity for the session, decrypting its token.
func (s *Server) user(sess *store.Session) (agent.User, error) {
	tok, err := s.box.Open(sess.TokenSealed)
	if err != nil {
		return agent.User{}, err
	}
	return agent.User{Email: sess.Email, IsAdmin: sess.IsAdmin, Token: tok}, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, into any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(into); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

func adminOnly(w http.ResponseWriter, r *http.Request) bool {
	if !session(r).IsAdmin {
		writeError(w, http.StatusForbidden, "admins only")
		return false
	}
	return true
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// docsHandler serves the built documentation (MkDocs) to signed-in users;
// others are sent to the login page and back.
func (s *Server) docsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err == nil {
			_, err = s.st.GetSession(c.Value)
		}
		if err != nil {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		// No directory listings: a directory is served only through its index.html.
		p := filepath.Join(s.cfg.DocsDir, filepath.Clean("/"+strings.TrimPrefix(r.URL.Path, "/docs/")))
		if st, err := os.Stat(p); err != nil || (st.IsDir() && !fileExists(filepath.Join(p, "index.html"))) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.StripPrefix("/docs/", http.FileServer(http.Dir(s.cfg.DocsDir))).ServeHTTP(w, r)
	})
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// spaHandler serves built assets and falls back to index.html for client routes.
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fs.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func timePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t
}
