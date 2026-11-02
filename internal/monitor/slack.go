package monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Slack posts new findings to an incoming webhook. With Mute set, or no
// webhook, it only logs.
type Slack struct {
	WebhookURL string
	Mute       bool
}

func (s *Slack) NotifyFinding(f *store.Finding) {
	text := fmt.Sprintf("*%s* · %s — %s\n%s\n_%s · %s · %s_", f.Severity, f.EntityName, f.Title, f.Detail,
		f.Provider, f.Realm, f.Region)
	if err := s.Post(text); err != nil {
		log.Printf("slack notify failed: %v", err)
	}
}

// ErrMuted means the message was only logged (SLACK_MUTE or no webhook).
var ErrMuted = errors.New("Slack is muted or not configured; the message was only logged")

// Post sends one message. Muted or unconfigured, it logs it and returns ErrMuted.
func (s *Slack) Post(text string) error {
	if s.WebhookURL == "" || s.Mute {
		log.Printf("slack (not sent): %s", text)
		return ErrMuted
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(s.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
