package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

// Learnings: past diagnoses distilled into reusable patterns. The Copilot
// searches approved ones before investigating and drafts new ones after.
// Drafts stay out of search until an admin approves them, because a draft is
// written from logs and pages that may contain planted text.

const maxLearningField = 2000

func (t *turn) learningTools() []tools.Tool {
	return []tools.Tool{
		{
			Name: "search_learnings",
			Description: "Search the team's approved learnings from past investigations: known failure patterns with their " +
				"symptoms, root cause, how to confirm and fix. Call it early in any investigation with the symptoms you have " +
				"(error text, failed step, alert or status names). A match is a hypothesis, not a conclusion: run its " +
				"how-to-confirm checks before relying on it.",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "Symptoms in your words plus exact error strings, statuses or alert names."},
				"alternatives": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 5,
					"description": "2-5 rewordings of the same problem as someone else might have written it: synonyms, the " +
						"underlying cause, related error strings or component names (e.g. for \"pod Unschedulable\": " +
						"\"Insufficient memory node capacity\", \"autoscaler did not add node\"). Searched together with query."},
				"category": map[string]any{"type": "string", "enum": categories, "description": "Optional category to favour."},
				"entity":   map[string]any{"type": "string", "description": "Optional customer or tenant name to favour patterns seen there."},
			},
			Required: []string{"query"},
			Label:    "Checked past learnings",
			Run:      t.searchLearnings,
		},
		{
			Name: "record_learning",
			Description: "After record_investigation, feed the result back into the learnings. Either: (a) learning_id + " +
				"outcome for a learning from search_learnings you tested (confirmed: the evidence matched; contradicted: it " +
				"looked similar but the cause was different), or (b) a new draft when no learning covered this problem. " +
				"Write a draft as a general pattern for any tenant: no customer names, IDs, hostnames, emails or secrets; " +
				"symptoms as the exact error strings and statuses someone would search for; how_to_confirm as the specific " +
				"tool calls or queries that proved it. Skip it for transient one-offs. Drafts need admin approval before use.",
			Properties: map[string]any{
				"learning_id":    map[string]any{"type": "string", "description": "An existing learning you tested."},
				"outcome":        map[string]any{"type": "string", "enum": []string{"confirmed", "contradicted"}},
				"note":           map[string]any{"type": "string", "description": "With contradicted: what was different."},
				"title":          map[string]any{"type": "string", "description": "New draft: the pattern in one line, e.g. \"Metadata DB never created on Postgres 15+\"."},
				"category":       map[string]any{"type": "string", "enum": categories},
				"symptoms":       map[string]any{"type": "string"},
				"root_cause":     map[string]any{"type": "string"},
				"how_to_confirm": map[string]any{"type": "string"},
				"fix":            map[string]any{"type": "string"},
				"kg_nodes":       map[string]any{"type": "string", "description": "Optional related knowledge-graph nodes, e.g. 04-controlplane."},
			},
			Label: "Updated the team's learnings",
			Run:   t.recordLearning,
		},
	}
}

type learningView struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Category     string   `json:"category"`
	Symptoms     string   `json:"symptoms"`
	RootCause    string   `json:"rootCause"`
	HowToConfirm string   `json:"howToConfirm"`
	Fix          string   `json:"fix"`
	KGNodes      string   `json:"kgNodes,omitempty"`
	TimesSeen    int      `json:"timesSeen"`
	Confirmed    int      `json:"confirmed"`
	Contradicted int      `json:"contradicted"`
	SeenOn       []string `json:"seenOn"`
	LastSeen     string   `json:"lastSeen"`
}

func (t *turn) searchLearnings(_ context.Context, _ *tools.Env, input json.RawMessage) (string, error) {
	var in struct {
		Query        string   `json:"query"`
		Alternatives []string `json:"alternatives"`
		Category     string   `json:"category"`
		Entity       string   `json:"entity"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	queries := append([]string{in.Query}, in.Alternatives[:min(len(in.Alternatives), 5)]...)
	matches, err := t.a.store.SearchLearningsAny(queries, in.Category, in.Entity, []string{store.LearningApproved}, 5)
	if err != nil {
		return "", err
	}
	out := make([]learningView, 0, len(matches))
	for _, m := range matches {
		l := m.Learning
		out = append(out, learningView{ID: l.ID, Title: l.Title, Category: l.Category, Symptoms: l.Symptoms, RootCause: l.RootCause,
			HowToConfirm: l.HowToConfirm, Fix: l.Fix, KGNodes: l.KGNodes, TimesSeen: l.TimesSeen, Confirmed: l.Confirmed,
			Contradicted: l.Contradicted, SeenOn: l.EntityList(), LastSeen: l.LastSeen.UTC().Format("2006-01-02")})
	}
	if len(out) == 0 {
		return `{"matches":[],"note":"No approved learning matches; investigate from scratch and draft one afterwards with record_learning."}`, nil
	}
	b, err := json.Marshal(map[string]any{"matches": out})
	return string(b), err
}

func (t *turn) recordLearning(_ context.Context, _ *tools.Env, input json.RawMessage) (string, error) {
	var in struct {
		LearningID   string `json:"learning_id"`
		Outcome      string `json:"outcome"`
		Note         string `json:"note"`
		Title        string `json:"title"`
		Category     string `json:"category"`
		Symptoms     string `json:"symptoms"`
		RootCause    string `json:"root_cause"`
		HowToConfirm string `json:"how_to_confirm"`
		Fix          string `json:"fix"`
		KGNodes      string `json:"kg_nodes"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	invID := t.item.InvestigationID
	if invID == "" {
		return "", fmt.Errorf("call record_investigation first; a learning must come from a recorded investigation")
	}
	inv, err := t.a.store.GetInvestigation(invID)
	if err != nil {
		return "", err
	}
	now := time.Now()

	if in.LearningID != "" {
		l, err := t.a.store.GetLearning(in.LearningID)
		if err != nil {
			return "", fmt.Errorf("no learning %s", in.LearningID)
		}
		switch in.Outcome {
		case "confirmed":
			l.Confirmed++
			l.AddSighting(inv.ID, inv.EntityName, now)
		case "contradicted":
			// The approved text is left alone; an admin decides what changes.
			l.Contradicted++
			l.ReviewNote = clip(strings.TrimSpace(l.ReviewNote+"\n"+inv.ID+": "+redact.Text(in.Note)), maxLearningField)
		default:
			return "", fmt.Errorf("outcome must be confirmed or contradicted")
		}
		if err := t.a.store.SaveLearning(l); err != nil {
			return "", err
		}
		t.a.store.Audit("Copilot for "+t.user.Email, "record_learning", l.ID, in.Outcome, inv.ID)
		return fmt.Sprintf("Recorded %s as %s for %s.", in.Outcome, l.ID, inv.ID), nil
	}

	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Symptoms) == "" || strings.TrimSpace(in.RootCause) == "" {
		return "", fmt.Errorf("a new draft needs title, symptoms and root_cause")
	}
	// A draft with the same title gains a sighting instead of a duplicate.
	drafts, _ := t.a.store.ListLearnings(store.LearningDraft)
	for i := range drafts {
		if strings.EqualFold(strings.TrimSpace(drafts[i].Title), strings.TrimSpace(in.Title)) {
			drafts[i].AddSighting(inv.ID, inv.EntityName, now)
			if err := t.a.store.SaveLearning(&drafts[i]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Added %s to the existing draft %s.", inv.ID, drafts[i].ID), nil
		}
	}
	cat := in.Category
	if cat == "" {
		cat = inv.Category
	}
	l := &store.Learning{
		ID:           "LRN-" + strings.ToUpper(secret.RandomID(4)),
		Title:        clip(redact.Text(in.Title), 200),
		Category:     cat,
		Status:       store.LearningDraft,
		Symptoms:     clip(redact.Text(in.Symptoms), maxLearningField),
		RootCause:    clip(redact.Text(in.RootCause), maxLearningField),
		HowToConfirm: clip(redact.Text(in.HowToConfirm), maxLearningField),
		Fix:          clip(redact.Text(in.Fix), maxLearningField),
		KGNodes:      clip(in.KGNodes, 200),
		CreatedBy:    "Copilot for " + t.user.Email,
		CreatedAt:    now,
	}
	l.AddSighting(inv.ID, inv.EntityName, now)
	if err := t.a.store.SaveLearning(l); err != nil {
		return "", err
	}
	t.a.store.Audit("Copilot for "+t.user.Email, "record_learning", l.ID, "drafted", inv.ID+": "+l.Title)
	return fmt.Sprintf("Drafted %s; an admin reviews it on the Learnings page before it is used.", l.ID), nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
