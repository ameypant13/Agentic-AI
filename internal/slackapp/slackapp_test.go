package slackapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sign(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerify(t *testing.T) {
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"event_callback"}`)
	if err := Verify("s3cret", ts, sign("s3cret", ts, body), body, now); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if Verify("s3cret", ts, sign("other", ts, body), body, now) == nil {
		t.Error("wrong secret accepted")
	}
	if Verify("s3cret", ts, sign("s3cret", ts, body), []byte(`{}`), now) == nil {
		t.Error("changed body accepted")
	}
	old := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	if Verify("s3cret", old, sign("s3cret", old, body), body, now) == nil {
		t.Error("stale request accepted")
	}
}

func TestMrkdwn(t *testing.T) {
	in := "## Cause\n**capgroup** post-deploy failed. See [runbook](https://x.io/r).\n- one\n- `**not bold**`\n```\n**code**\n```"
	want := "*Cause*\n*capgroup* post-deploy failed. See <https://x.io/r|runbook>.\n• one\n• `**not bold**`\n```\n**code**\n```"
	if got := Mrkdwn(in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSplitKeepsFencesBalanced(t *testing.T) {
	long := "intro\n\n```\n" + strings.Repeat("line of code\n", 400) + "```\n\nend"
	parts := Split(long)
	if len(parts) < 2 {
		t.Fatalf("not split: %d", len(parts))
	}
	for i, p := range parts {
		if len(p) > MaxMessage+10 || strings.Count(p, "```")%2 != 0 {
			t.Errorf("part %d: len %d, fences %d", i, len(p), strings.Count(p, "```"))
		}
	}
}
