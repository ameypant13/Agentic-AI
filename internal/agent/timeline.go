package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

// consoleEvents is change_timeline's view of what the console recorded:
// findings appearing and resolving, and investigations, for any of names.
func (a *Agent) consoleEvents(_ context.Context, names []string, since time.Time) []tools.Event {
	about := func(fields ...string) bool {
		for _, f := range fields {
			lf := strings.ToLower(f)
			for _, n := range names {
				if n = strings.ToLower(n); n != "" && (lf == n || strings.Contains(lf, n)) {
					return true
				}
			}
		}
		return false
	}
	var out []tools.Event
	if fs, err := a.store.FindingsSince(since); err == nil {
		for _, f := range fs {
			if !about(f.EntityName, f.EntityID, f.Title) {
				continue
			}
			if !f.FirstSeenAt.Before(since) {
				out = append(out, tools.Event{At: f.FirstSeenAt, Source: "finding", Object: f.EntityName,
					What: fmt.Sprintf("%s appeared: %s", f.Severity, f.Title)})
			}
			if f.ResolvedAt != nil && !f.ResolvedAt.Before(since) {
				out = append(out, tools.Event{At: *f.ResolvedAt, Source: "finding", Object: f.EntityName, What: "resolved: " + f.Title})
			}
		}
	}
	if invs, err := a.store.ListInvestigations(""); err == nil {
		for _, inv := range invs {
			if inv.CreatedAt.Before(since) || !about(inv.EntityName, inv.EntityID) {
				continue
			}
			out = append(out, tools.Event{At: inv.CreatedAt, Source: "investigation", Object: inv.EntityName,
				What: fmt.Sprintf("%s: %s (%s)", inv.ID, inv.Title, inv.Category)})
		}
	}
	return out
}
