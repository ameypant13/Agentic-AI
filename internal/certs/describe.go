package certs

import (
	"encoding/json"
	"strings"
	"time"
)

// Describe renders the stored inventory for the Copilot: certificates whose
// host, tenant or source contains query (all when empty), soonest expiry first.
func (w *Worker) Describe(query string, problemsOnly bool) (string, error) {
	inv := w.Inventory()
	if inv == nil {
		return `{"note":"The first certificate scan has not finished yet; try again in a few minutes."}`, nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	type row struct {
		Kind      string    `json:"kind"`
		Tenant    string    `json:"tenant,omitempty"`
		Host      string    `json:"host"`
		Source    string    `json:"source"`
		SourceRef string    `json:"sourceRef,omitempty"`
		AutoRenew string    `json:"autoRenew"`
		Issuer    string    `json:"issuer,omitempty"`
		Expires   string    `json:"expires,omitempty"`
		DaysLeft  *int      `json:"daysLeft,omitempty"`
		Status    string    `json:"status"`
		Problems  []Problem `json:"problems,omitempty"`
		Checks    []Check   `json:"checks,omitempty"`
		UsedBy    []string  `json:"usedBy,omitempty"`
		// Repair is what an admin can ask control-hub to fix (Certificates page, or repair_certificate over MCP).
		Repair *RepairTarget `json:"repair,omitempty"`
	}
	var rows []row
	total := 0
	for _, it := range inv.Items {
		if q != "" && !strings.Contains(strings.ToLower(it.Host+" "+it.Tenant+" "+it.SourceRef+" "+strings.Join(it.UsedBy, " ")), q) {
			continue
		}
		if problemsOnly && it.Status == StatusOK && !hasRealProblem(it) {
			continue
		}
		total++
		if len(rows) >= 40 {
			continue
		}
		r := row{Kind: it.Kind, Tenant: it.Tenant, Host: it.Host, Source: it.Source, SourceRef: it.SourceRef, AutoRenew: it.AutoRenew,
			Issuer: it.Issuer, DaysLeft: it.DaysLeft, Status: it.Status, Problems: it.Problems, UsedBy: it.UsedBy, Repair: it.Repair}
		if it.NotAfter != nil {
			r.Expires = it.NotAfter.Format(time.RFC3339)
		}
		if q != "" {
			r.Checks = it.Checks // details only for a narrowed question
		}
		rows = append(rows, r)
	}
	raw, err := json.Marshal(map[string]any{"scannedAt": inv.ScannedAt.Format(time.RFC3339), "matching": total, "shown": len(rows),
		"sourceErrors": inv.Errors, "certificates": rows})
	return string(raw), err
}

func hasRealProblem(it Item) bool {
	for _, p := range it.Problems {
		if p.Severity != SevInfo {
			return true
		}
	}
	return false
}
