package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/slackapp"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// The Copilot in Slack: mention the bot in a channel or DM it. It answers in
// the thread as the person who asked: their Slack email must belong to a
// console user with a current session (their control-hub access) and a
// Claude key. Answers in channels use the non-admin view, since everyone in
// the channel reads them; DMs use the person's own view. Each Slack thread is
// one Copilot chat, which the person can open in the console.

const slackTurnTimeout = 10 * time.Minute

type slackEvent struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	User        string `json:"user"`
	BotID       string `json:"bot_id"`
	Text        string `json:"text"`
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
}

// slackBot holds the bot's in-memory state.
type slackBot struct {
	mu   sync.Mutex
	seen map[string]time.Time // event IDs already handled (Slack retries)
	busy map[string]bool      // chats with a turn running
}

var slackMention = regexp.MustCompile(`<@[A-Z0-9]+>`)

func (s *Server) slackEnabled() bool {
	return s.cfg.SlackBotToken != "" && s.cfg.SlackSigningSecret != ""
}

func (s *Server) slackClient() *slackapp.Client {
	return &slackapp.Client{Token: s.cfg.SlackBotToken, BaseURL: s.slackBaseURL}
}

// slackEvents is the Events API endpoint. It must answer within 3 seconds,
// so turns run in the background.
func (s *Server) slackEvents(w http.ResponseWriter, r *http.Request) {
	if !s.slackEnabled() {
		writeError(w, http.StatusNotFound, "the Slack Copilot is not configured")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "unreadable body")
		return
	}
	if err := slackapp.Verify(s.cfg.SlackSigningSecret, r.Header.Get("X-Slack-Request-Timestamp"),
		r.Header.Get("X-Slack-Signature"), body, time.Now()); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid Slack signature")
		return
	}
	var env struct {
		Type      string     `json:"type"`
		Challenge string     `json:"challenge"`
		EventID   string     `json:"event_id"`
		Event     slackEvent `json:"event"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if env.Type == "url_verification" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": env.Challenge})
		return
	}
	w.WriteHeader(http.StatusOK)
	ev := env.Event
	if env.Type != "event_callback" || ev.BotID != "" || ev.Subtype != "" || ev.User == "" {
		return
	}
	if ev.Type != "app_mention" && !(ev.Type == "message" && ev.ChannelType == "im") {
		return
	}
	if !s.slackBot.first(env.EventID) {
		return
	}
	go s.slackTurn(ev)
}

// first records an event ID and reports whether it is new.
func (b *slackBot) first(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for k, t := range b.seen {
		if now.Sub(t) > time.Hour {
			delete(b.seen, k)
		}
	}
	if _, ok := b.seen[id]; ok && id != "" {
		return false
	}
	b.seen[id] = now
	return true
}

func (b *slackBot) claim(chatID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.busy[chatID] {
		return false
	}
	b.busy[chatID] = true
	return true
}

func (b *slackBot) release(chatID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.busy, chatID)
}

func (s *Server) slackTurn(ev slackEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), slackTurnTimeout)
	defer cancel()
	sc := s.slackClient()
	thread := firstNonEmptyStr(ev.ThreadTS, ev.TS)
	reply := func(text string) {
		if _, err := sc.Post(ctx, ev.Channel, thread, text); err != nil {
			log.Printf("slack: post: %v", err)
		}
	}
	text := strings.TrimSpace(slackMention.ReplaceAllString(ev.Text, ""))
	if text == "" {
		reply("Ask me about a tenant, control plane, cluster or error, e.g. _why is capgroup's post-deploy failing?_")
		return
	}

	email, err := sc.UserEmail(ctx, ev.User)
	if err != nil {
		reply("I couldn't read your email from Slack, so I can't tell who you are in the console.")
		return
	}
	sess, err := s.st.LatestSession(email)
	if err != nil {
		reply("I answer with your own control-hub access. Sign in to the console at " + s.cfg.PublicURL + " first, then ask again.")
		return
	}
	user, err := s.user(sess)
	if err != nil {
		reply("Your console session can't be read; sign in again at " + s.cfg.PublicURL + ".")
		return
	}
	user.IsAdmin = (sess.IsAdmin || s.cfg.IsConsoleAdmin(email)) && ev.ChannelType == "im"
	key, source, err := s.claudeKey(email)
	if err != nil || key == "" {
		reply("Add your Claude API key in the console's Settings (" + s.cfg.PublicURL + "/settings) to use the Copilot.")
		return
	}

	chat, err := s.slackChat(ev.Channel, thread, email, text)
	if err != nil {
		reply("Something went wrong starting the conversation: " + err.Error())
		return
	}
	if !s.slackBot.claim(chat.ID) {
		reply("I'm still working on the previous question in this thread.")
		return
	}
	defer s.slackBot.release(chat.ID)

	placeholder, err := sc.Post(ctx, ev.Channel, thread, "_Looking into it…_")
	if err != nil {
		log.Printf("slack: post: %v", err)
		return
	}
	var (
		mu       sync.Mutex
		answer   strings.Builder
		steps    []string
		actions  []string
		failure  string
		lastEdit time.Time
	)
	emit := func(event string, data any) {
		mu.Lock()
		defer mu.Unlock()
		m, _ := data.(map[string]any)
		switch event {
		case "text":
			if d, ok := data.(map[string]string); ok {
				answer.WriteString(d["delta"])
			}
		case "step":
			if l, ok := m["label"].(string); ok {
				steps = append(steps, l)
				if time.Since(lastEdit) > 4*time.Second {
					lastEdit = time.Now()
					_ = sc.Update(ctx, ev.Channel, placeholder, "_Looking into it… "+steps[len(steps)-1]+"_")
				}
			}
		case "action":
			actions = append(actions, slackActionNote(m))
		case "error":
			if d, ok := data.(map[string]string); ok {
				failure = d["message"]
			}
		}
	}
	runErr := s.agent.Run(ctx, agent.TurnRequest{Chat: chat, User: user, APIKey: key, KeySource: source, Text: text}, emit)

	mu.Lock()
	out := strings.TrimSpace(answer.String())
	if out == "" {
		out = "I couldn't finish an answer."
	}
	if runErr != nil && failure != "" {
		out += "\n\n_Stopped: " + failure + "_"
	}
	if len(actions) > 0 {
		out += "\n\n" + strings.Join(actions, "\n")
	}
	out += "\n\n<" + strings.TrimRight(s.cfg.PublicURL, "/") + "/copilot/" + chat.ID + "|Open this conversation in the console>"
	mu.Unlock()

	parts := slackapp.Split(slackapp.Mrkdwn(out))
	if err := sc.Update(ctx, ev.Channel, placeholder, parts[0]); err != nil {
		log.Printf("slack: update: %v", err)
	}
	for _, p := range parts[1:] {
		reply(p)
	}
	s.st.Audit(email, "slack_copilot", ev.Channel, "answered", chat.ID)
}

// slackChat finds or starts the chat for a Slack thread and person.
func (s *Server) slackChat(channel, thread, email, text string) (*store.Chat, error) {
	k := "slack_chat:" + channel + ":" + thread + ":" + email
	var id string
	if ok, _ := s.st.GetKV(k, &id); ok {
		if c, err := s.st.GetChat(id); err == nil && c.Owner == email {
			return c, nil
		}
	}
	c := &store.Chat{ID: secret.RandomID(9), Owner: email, Title: "Slack: " + titleFrom(text)}
	if err := s.st.CreateChat(c); err != nil {
		return nil, err
	}
	return c, s.st.PutKV(k, c.ID)
}

// slackActionNote describes a proposed action: buttons live in the console.
func slackActionNote(m map[string]any) string {
	title, _ := m["title"].(string)
	target, _ := m["targetName"].(string)
	note := "*Prepared:* " + title + " on `" + target + "`"
	if p, ok := m["plan"].(json.RawMessage); ok && len(p) > 0 && string(p) != "null" {
		var plan agent.Plan
		if json.Unmarshal(p, &plan) == nil && plan.Summary != "" {
			note += " (" + plan.Summary + ")"
		}
	}
	return note + ". Review the dry run in the console."
}
