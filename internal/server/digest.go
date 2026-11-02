package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/monitor"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// The weekly fleet digest: what happened in the last 7 days, assembled from
// the console's own data (findings, certificates, costs, investigations,
// learnings, error patterns). No AI. Posted to Slack weekly, previewed at
// GET /api/digest, sent on demand by admins.

type DigestSection struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
	Link  string   `json:"link,omitempty"` // console page with the details
}

type Digest struct {
	From     time.Time       `json:"from"`
	To       time.Time       `json:"to"`
	Headline string          `json:"headline"`
	Sections []DigestSection `json:"sections"`
}

const digestKV = "digest_last_sent"

// BuildDigest summarises the 7 days before now.
func (s *Server) BuildDigest(now time.Time) Digest {
	from := now.Add(-7 * 24 * time.Hour)
	d := Digest{From: from.UTC(), To: now.UTC()}
	var heads []string

	// Findings.
	all, _ := s.st.ListFindings("")
	var newCrit, newWarn, resolved, stale []string
	for _, f := range all {
		label := fmt.Sprintf("%s — %s", f.EntityName, f.Title)
		if !f.FirstSeenAt.Before(from) {
			if f.Severity == "critical" {
				newCrit = append(newCrit, label)
			} else {
				newWarn = append(newWarn, label)
			}
		}
		if f.ResolvedAt != nil && !f.ResolvedAt.Before(from) {
			resolved = append(resolved, label)
		}
		if f.State != "resolved" && f.FirstSeenAt.Before(from) && f.Severity == "critical" {
			stale = append(stale, fmt.Sprintf("%s (open %d days)", label, int(now.Sub(f.FirstSeenAt).Hours()/24)))
		}
	}
	sec := DigestSection{Title: "Findings", Link: "/issues"}
	sec.Lines = append(sec.Lines, fmt.Sprintf("%d new critical, %d new warnings, %d resolved", len(newCrit), len(newWarn), len(resolved)))
	for _, l := range top(newCrit, 5) {
		sec.Lines = append(sec.Lines, "New: "+l)
	}
	if len(stale) > 0 {
		sec.Lines = append(sec.Lines, fmt.Sprintf("%d critical open for over a week:", len(stale)))
		for _, l := range top(stale, 5) {
			sec.Lines = append(sec.Lines, "  "+l)
		}
	}
	d.Sections = append(d.Sections, sec)
	heads = append(heads, fmt.Sprintf("%d new critical findings", len(newCrit)))

	// Certificates.
	if s.certs != nil {
		if inv := s.certs.Inventory(); inv != nil {
			var soon []string
			for _, it := range inv.Items {
				if it.DaysLeft != nil && *it.DaysLeft < 21 {
					soon = append(soon, fmt.Sprintf("%s: %d days (%s)", it.Host, *it.DaysLeft, it.AutoRenew))
				}
			}
			sort.Strings(soon)
			sec := DigestSection{Title: "Certificates", Link: "/certificates"}
			if len(soon) == 0 {
				sec.Lines = []string{"Nothing expires in the next 21 days."}
			} else {
				sec.Lines = append([]string{fmt.Sprintf("%d expire within 21 days:", len(soon))}, top(soon, 8)...)
				heads = append(heads, fmt.Sprintf("%d certificates expiring", len(soon)))
			}
			d.Sections = append(d.Sections, sec)
		}
	}

	// Costs.
	if s.costs != nil {
		sec := DigestSection{Title: "Cost", Link: "/costs"}
		day := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
		this, _ := s.st.CostDays(day(now.AddDate(0, 0, -7)), day(now.AddDate(0, 0, -1)), "")
		prev, _ := s.st.CostDays(day(now.AddDate(0, 0, -14)), day(now.AddDate(0, 0, -8)), "")
		sum := func(ds []store.CostClusterDay) (t float64) {
			for _, x := range ds {
				t += x.NodeCost
			}
			return t
		}
		if a, b := sum(this), sum(prev); a > 0 {
			line := fmt.Sprintf("Compute this week %s", money(a))
			if b > 0 {
				line += fmt.Sprintf(" (%+.0f%% on the week before, %s)", 100*(a-b)/b, money(b))
			}
			sec.Lines = append(sec.Lines, line)
		}
		if changes, err := costChanges(s.st, ""); err == nil {
			for _, c := range topChanges(changes, 3) {
				sec.Lines = append(sec.Lines, c)
			}
		}
		to := now.UTC()
		if recs, err := s.cachedRightsizing(to.AddDate(0, 0, -29).Format("2006-01-02"), to.Format("2006-01-02"), "", ""); err == nil && len(recs) > 0 {
			totals := rightsizingByCluster(recs)
			var save float64
			needs := 0
			for _, t := range totals {
				save += t.MonthlySaving
				needs += t.NeedsMore
			}
			sec.Lines = append(sec.Lines, fmt.Sprintf("Right-sizing could save %s a month; %d containers need more (OOM-killed or throttled)", money(save), needs))
			heads = append(heads, fmt.Sprintf("%s/month in right-sizing", money(save)))
		}
		if len(sec.Lines) > 0 {
			d.Sections = append(d.Sections, sec)
		}
	}

	// Investigations, learnings and patterns waiting for review.
	invs, _ := s.st.ListInvestigations("")
	var weekInv []string
	for _, inv := range invs {
		if !inv.CreatedAt.Before(from) {
			weekInv = append(weekInv, fmt.Sprintf("%s: %s (%s)", inv.ID, inv.Title, inv.Category))
		}
	}
	drafts, _ := s.st.ListLearnings(store.LearningDraft)
	patterns, _ := s.st.ListLogPatterns("new")
	newPatterns := 0
	for _, p := range patterns {
		if !p.FirstSeen.Before(from) {
			newPatterns++
		}
	}
	sec = DigestSection{Title: "Investigations and learning", Link: "/investigations"}
	sec.Lines = append(sec.Lines, fmt.Sprintf("%d investigations recorded", len(weekInv)))
	sec.Lines = append(sec.Lines, top(weekInv, 5)...)
	if len(drafts) > 0 {
		sec.Lines = append(sec.Lines, fmt.Sprintf("%d draft learnings wait for review (Learnings)", len(drafts)))
	}
	if newPatterns > 0 {
		sec.Lines = append(sec.Lines, fmt.Sprintf("%d new error patterns no log rule matches (Log rules → New error patterns)", newPatterns))
	}
	d.Sections = append(d.Sections, sec)

	d.Headline = "Week of " + from.UTC().Format("Jan 2") + ": " + strings.Join(heads, " · ")
	return d
}

// Slack renders the digest as Slack mrkdwn.
func (d Digest) Slack(baseURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*Control Hub weekly digest* — %s\n", d.Headline)
	for _, s := range d.Sections {
		fmt.Fprintf(&b, "\n*%s*", s.Title)
		if s.Link != "" && baseURL != "" {
			fmt.Fprintf(&b, "  <%s%s|open>", strings.TrimRight(baseURL, "/"), s.Link)
		}
		b.WriteString("\n")
		for _, l := range s.Lines {
			fmt.Fprintf(&b, "• %s\n", l)
		}
	}
	return b.String()
}

func (s *Server) slack() *monitor.Slack {
	return &monitor.Slack{WebhookURL: s.cfg.SlackWebhookURL, Mute: s.cfg.SlackMute}
}

func (s *Server) getDigest(w http.ResponseWriter, r *http.Request) {
	// Admins only: the digest names every investigation, and non-admins see only their own.
	if !adminOnly(w, r) {
		return
	}
	d := s.BuildDigest(time.Now())
	var last time.Time
	_, _ = s.st.GetKV(digestKV, &last)
	writeJSON(w, http.StatusOK, map[string]any{"digest": d, "slack": d.Slack(s.cfg.PublicURL), "lastSent": nullTime(last),
		"schedule": s.digestSchedule()})
}

func (s *Server) sendDigest(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	d := s.BuildDigest(time.Now())
	err := s.slack().Post(d.Slack(s.cfg.PublicURL))
	switch {
	case errors.Is(err, monitor.ErrMuted):
		writeJSON(w, http.StatusOK, map[string]any{"sent": false, "message": err.Error()})
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "Slack: "+err.Error())
		return
	}
	_ = s.st.PutKV(digestKV, time.Now())
	s.st.Audit(session(r).Email, "send_digest", "slack", "sent", d.Headline)
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "message": "Posted to Slack."})
}

func (s *Server) digestSchedule() string {
	if !s.cfg.DigestEnabled {
		return "off (DIGEST_ENABLED=false)"
	}
	return fmt.Sprintf("%s at %02d:00 UTC", time.Weekday(s.cfg.DigestWeekday), s.cfg.DigestHourUTC)
}

// StartDigest posts the digest once a week at the configured hour. The last
// send is stored, so a restart never sends it twice.
func (s *Server) StartDigest(ctx context.Context) {
	if !s.cfg.DigestEnabled {
		return
	}
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			now := time.Now().UTC()
			if int(now.Weekday()) != s.cfg.DigestWeekday || now.Hour() != s.cfg.DigestHourUTC {
				continue
			}
			var last time.Time
			if ok, _ := s.st.GetKV(digestKV, &last); ok && now.Sub(last) < 24*time.Hour {
				continue
			}
			d := s.BuildDigest(now)
			if err := s.slack().Post(d.Slack(s.cfg.PublicURL)); err != nil && !errors.Is(err, monitor.ErrMuted) {
				log.Printf("digest: %v", err)
				continue
			}
			_ = s.st.PutKV(digestKV, now)
		}
	}()
}

func topChanges(changes []cost.Change, n int) []string {
	var out []string
	for i, c := range changes {
		if i == n {
			break
		}
		out = append(out, c.Summary)
	}
	return out
}

func top(v []string, n int) []string {
	if len(v) > n {
		return append(v[:n:n], fmt.Sprintf("…and %d more", len(v)-n))
	}
	return v
}

func money(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("$%.1fk", v/1000)
	}
	return fmt.Sprintf("$%.0f", v)
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
