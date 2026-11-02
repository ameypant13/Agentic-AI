package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

const sessionDays = 7

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.ch.Login(r.Context(), strings.TrimSpace(in.Email), in.Password, sessionDays)
	if err != nil {
		var apiErr *controlhub.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		log.Printf("login: %v", err)
		writeError(w, http.StatusBadGateway, "could not reach control-hub")
		return
	}
	s.startSession(w, r, resp.Email, resp.Username, resp.IsAdmin, resp.Token, time.Now().Add(sessionDays*24*time.Hour))
}

// loginWithToken signs in with an existing control-hub token, for people who
// sign in to control-hub with Google SSO and have no password. The token's
// claims are only trusted after control-hub accepts the token.
func (s *Server) loginWithToken(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tok := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(in.Token), "Bearer "))
	claims, err := parseClaims(tok)
	if err != nil {
		writeError(w, http.StatusBadRequest, "that is not a control-hub token")
		return
	}
	realm := "sap"
	if len(claims.Realms) > 0 {
		realm = claims.Realms[0]
	}
	if _, err := s.ch.Get(r.Context(), tok, "/api/control-planes", url.Values{"realm": {realm}, "limit": {"1"}}); err != nil {
		var apiErr *controlhub.APIError
		// 403 means control-hub accepted the token but the user lacks that permission: still a valid sign-in.
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
			writeError(w, http.StatusUnauthorized, "control-hub rejected the token")
			return
		}
	}
	expires := time.Now().Add(sessionDays * 24 * time.Hour)
	if claims.Exp > 0 && time.Unix(claims.Exp, 0).Before(expires) {
		expires = time.Unix(claims.Exp, 0)
	}
	s.startSession(w, r, claims.Email, claims.Username, claims.IsAdmin, tok, expires)
}

type tokenClaims struct {
	Email    string   `json:"email"`
	Username string   `json:"username"`
	IsAdmin  bool     `json:"isAdmin"`
	Realms   []string `json:"realms"`
	Exp      int64    `json:"exp"`
}

func parseClaims(tok string) (*tokenClaims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c tokenClaims
	if err := json.Unmarshal(raw, &c); err != nil || c.Email == "" {
		return nil, errors.New("no email claim")
	}
	return &c, nil
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, email, username string, isAdmin bool, token string, expires time.Time) {
	sealed, err := s.box.Seal(token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	sess := &store.Session{ID: secret.RandomID(32), Email: email, Username: username, IsAdmin: isAdmin,
		TokenSealed: sealed, ExpiresAt: expires}
	if err := s.st.CreateSession(sess); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	_ = s.st.DeleteExpiredSessions()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: sess.ID, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	s.st.Audit(email, "sign_in", "", "ok", "")
	writeJSON(w, http.StatusOK, s.meView(sess))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.st.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.CookieSecure})
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.meView(session(r)))
}

func (s *Server) meView(sess *store.Session) map[string]any {
	return map[string]any{"email": sess.Email, "username": sess.Username, "isAdmin": sess.IsAdmin,
		"claudeKey": s.claudeKeyView(sess.Email), "readOnly": s.cfg.ReadOnly, "controlHub": s.cfg.ControlHubLabel}
}

func (s *Server) claudeKeyView(email string) map[string]any {
	k, err := s.st.GetUserKey(email)
	if err == nil {
		return map[string]any{"configured": true, "last4": k.Last4, "source": "personal"}
	}
	source := "none"
	if s.cfg.SharedAnthropicKey != "" {
		source = "shared"
	}
	return map[string]any{"configured": false, "last4": "", "source": source}
}

// claudeKey returns the API key a chat turn should use and where it came from.
func (s *Server) claudeKey(email string) (key, source string, err error) {
	if k, err := s.st.GetUserKey(email); err == nil {
		plain, err := s.box.Open(k.KeySealed)
		return plain, "personal", err
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", "", err
	}
	if s.cfg.SharedAnthropicKey != "" {
		return s.cfg.SharedAnthropicKey, "shared", nil
	}
	return "", "none", nil
}

func (s *Server) putClaudeKey(w http.ResponseWriter, r *http.Request) {
	var in struct{ APIKey string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key := strings.TrimSpace(in.APIKey)
	if !strings.HasPrefix(key, "sk-ant-") {
		writeError(w, http.StatusBadRequest, "that doesn't look like a Claude API key (they start with sk-ant-)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.verifyKey(ctx, key); err != nil {
		writeError(w, http.StatusBadRequest, "the Claude API rejected this key")
		return
	}
	sealed, err := s.box.Seal(key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store the key")
		return
	}
	email := session(r).Email
	if err := s.st.PutUserKey(&store.UserKey{Email: email, KeySealed: sealed, Last4: key[len(key)-4:], UpdatedAt: time.Now()}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store the key")
		return
	}
	s.st.Audit(email, "set_claude_key", "", "ok", "")
	writeJSON(w, http.StatusOK, s.claudeKeyView(email))
}

func (s *Server) deleteClaudeKey(w http.ResponseWriter, r *http.Request) {
	email := session(r).Email
	if err := s.st.DeleteUserKey(email); err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove the key")
		return
	}
	s.st.Audit(email, "remove_claude_key", "", "ok", "")
	writeJSON(w, http.StatusOK, s.claudeKeyView(email))
}

// verifyClaudeKey makes a free, authenticated call (model lookup) with the key.
func verifyClaudeKey(ctx context.Context, apiKey, model string) error {
	client := anthropic.NewClient(option.WithAPIKey(apiKey), option.WithMaxRetries(0))
	_, err := client.Models.Get(ctx, model, anthropic.ModelGetParams{})
	return err
}
