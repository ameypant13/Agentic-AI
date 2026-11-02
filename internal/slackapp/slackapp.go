// Package slackapp is the small part of Slack the Copilot bot needs: request
// signature verification for the Events API, three Web API calls
// (users.info, chat.postMessage, chat.update) and Markdown to Slack mrkdwn.
package slackapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxSkew is how old a signed request may be (Slack's recommendation).
const MaxSkew = 5 * time.Minute

// Verify checks a request's X-Slack-Signature against the signing secret.
func Verify(secret, timestamp, signature string, body []byte, now time.Time) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("bad timestamp")
	}
	if d := now.Sub(time.Unix(ts, 0)); d > MaxSkew || d < -MaxSkew {
		return errors.New("stale request")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "v0:%s:", timestamp)
	mac.Write(body)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return errors.New("bad signature")
	}
	return nil
}

// Client calls the Slack Web API with a bot token.
type Client struct {
	Token string
	// BaseURL is https://slack.com/api unless a test overrides it.
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) call(ctx context.Context, method string, body any, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://slack.com/api"
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	raw := new(bytes.Buffer)
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		return err
	}
	if err := json.Unmarshal(raw.Bytes(), &env); err != nil {
		return fmt.Errorf("slack %s: HTTP %d", method, resp.StatusCode)
	}
	if !env.OK {
		return fmt.Errorf("slack %s: %s", method, env.Error)
	}
	if out != nil {
		return json.Unmarshal(raw.Bytes(), out)
	}
	return nil
}

// UserEmail returns a Slack user's email (needs the users:read.email scope).
func (c *Client) UserEmail(ctx context.Context, userID string) (string, error) {
	var out struct {
		User struct {
			Deleted bool `json:"deleted"`
			IsBot   bool `json:"is_bot"`
			Profile struct {
				Email string `json:"email"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err := c.call(ctx, "users.info", map[string]string{"user": userID}, &out); err != nil {
		return "", err
	}
	if out.User.Deleted || out.User.IsBot || out.User.Profile.Email == "" {
		return "", errors.New("no email for this Slack user")
	}
	return strings.ToLower(out.User.Profile.Email), nil
}

// Post sends a message (in a thread when threadTS is set) and returns its ts.
func (c *Client) Post(ctx context.Context, channel, threadTS, text string) (string, error) {
	body := map[string]any{"channel": channel, "text": text, "unfurl_links": false, "unfurl_media": false}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}
	var out struct {
		TS string `json:"ts"`
	}
	err := c.call(ctx, "chat.postMessage", body, &out)
	return out.TS, err
}

// Update replaces a message's text.
func (c *Client) Update(ctx context.Context, channel, ts, text string) error {
	return c.call(ctx, "chat.update", map[string]any{"channel": channel, "ts": ts, "text": text}, nil)
}

// MaxMessage is where long replies are split; Slack truncates very long text.
const MaxMessage = 3500

// Split cuts text into messages at paragraph or line breaks, keeping code
// fences balanced.
func Split(text string) []string {
	var out []string
	for len(text) > MaxMessage {
		cut := strings.LastIndex(text[:MaxMessage], "\n\n")
		if cut < MaxMessage/2 {
			cut = strings.LastIndex(text[:MaxMessage], "\n")
		}
		if cut < MaxMessage/2 {
			cut = MaxMessage
		}
		part := text[:cut]
		text = strings.TrimLeft(text[cut:], "\n")
		if strings.Count(part, "```")%2 == 1 {
			part += "\n```"
			text = "```\n" + text
		}
		out = append(out, part)
	}
	return append(out, text)
}

var (
	mdBold    = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	mdHeading = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	mdLink    = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	mdBullet  = regexp.MustCompile(`(?m)^(\s*)[-*] `)
	mdStrike  = regexp.MustCompile(`~~([^~\n]+)~~`)
)

// Mrkdwn converts the Copilot's Markdown to Slack mrkdwn. Code blocks and
// inline code are left alone.
func Mrkdwn(md string) string {
	parts := strings.Split(md, "```")
	for i := range parts {
		if i%2 == 1 {
			continue // inside a code fence
		}
		segs := strings.Split(parts[i], "`")
		for j := range segs {
			if j%2 == 1 {
				continue // inline code
			}
			s := segs[j]
			s = mdHeading.ReplaceAllString(s, "*$1*")
			s = mdBold.ReplaceAllString(s, "*$1*")
			s = mdStrike.ReplaceAllString(s, "~$1~")
			s = mdLink.ReplaceAllString(s, "<$2|$1>")
			s = mdBullet.ReplaceAllString(s, "$1• ")
			segs[j] = s
		}
		parts[i] = strings.Join(segs, "`")
	}
	return strings.Join(parts, "```")
}
