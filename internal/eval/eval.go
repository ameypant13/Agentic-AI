// Package eval measures the Copilot on recorded cases. Each case is a
// question plus the tool outputs a real investigation would have seen; the
// agent runs against Claude with those outputs replayed instead of reaching
// control-hub, clusters or Grafana, so a run is repeatable and tests only the
// Copilot's reasoning. A case passes when the recorded investigation (or the
// reply) names the expected cause and none of the known wrong ones.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
)

// Case is one evaluation scenario.
type Case struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Question string `json:"question"`
	// Learnings are approved learnings present when the case runs (a case can
	// plant a misleading one to check the Copilot verifies before trusting it).
	Learnings []Learning `json:"learnings"`
	Fixtures  []Fixture  `json:"fixtures"`
	Expect    Expect     `json:"expect"`
}

type Learning struct {
	Title, Category, Symptoms, RootCause, HowToConfirm, Fix string
}

// Fixture answers calls to Tool whose input contains every Match value
// (per input field, case-insensitive). The first matching fixture wins.
type Fixture struct {
	Tool   string            `json:"tool"`
	Match  map[string]string `json:"match"`
	Output any               `json:"output"`
	Error  string            `json:"error"`
}

type Expect struct {
	// Categories accepted for the recorded investigation; empty = any.
	Categories []string `json:"categories"`
	// RootCause: every group needs one of its terms in the diagnosis.
	RootCause [][]string `json:"rootCause"`
	// MustNot: none of these may appear in the diagnosis.
	MustNot []string `json:"mustNot"`
	// NoInvestigation: nothing is wrong, so nothing should be recorded.
	NoInvestigation bool `json:"noInvestigation"`
	// MaxToolCalls fails a case that needed more calls than this (0 = no cap).
	MaxToolCalls int `json:"maxToolCalls"`
}

// Load reads every *.yaml case in dir, sorted by ID.
func Load(dir string) ([]Case, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	var out []Case
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var c Case
		if err := yaml.UnmarshalStrict(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if c.ID == "" || c.Question == "" {
			return nil, fmt.Errorf("%s: id and question are required", f)
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// consoleTools only touch the console's own (scratch) database, so they run for real.
var consoleTools = map[string]bool{"record_investigation": true, "record_learning": true, "search_learnings": true, "propose_action": true}

// noData is what an unrecorded call returns: the case has nothing for it.
const noData = `{"note":"no data recorded for this call in the evaluation case"}`

type replayer struct {
	c          *Case
	mu         sync.Mutex
	unrecorded []string
}

func (r *replayer) replay(tool string, input json.RawMessage) (string, error) {
	if consoleTools[tool] {
		return "", agent.ErrNotReplayed
	}
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	for _, f := range r.c.Fixtures {
		if f.Tool != tool || !matches(f.Match, in) {
			continue
		}
		if f.Error != "" {
			return "", fmt.Errorf("%s", f.Error)
		}
		if s, ok := f.Output.(string); ok {
			return s, nil
		}
		b, err := json.Marshal(f.Output)
		return string(b), err
	}
	r.mu.Lock()
	r.unrecorded = append(r.unrecorded, tool+" "+string(input))
	r.mu.Unlock()
	return noData, nil
}

func matches(want map[string]string, in map[string]any) bool {
	for k, v := range want {
		got, ok := in[k]
		if !ok || !strings.Contains(strings.ToLower(fmt.Sprint(got)), strings.ToLower(v)) {
			return false
		}
	}
	return true
}

// Result is one case's outcome.
type Result struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Pass         bool          `json:"pass"`
	Failures     []string      `json:"failures"`
	Category     string        `json:"category"`
	Diagnosis    string        `json:"diagnosis"`
	ToolCalls    []string      `json:"toolCalls"`
	Unrecorded   []string      `json:"unrecorded"`
	InputTokens  int64         `json:"inputTokens"`
	OutputTokens int64         `json:"outputTokens"`
	Duration     time.Duration `json:"duration"`
	Error        string        `json:"error,omitempty"`
}

// NewAgent builds the agent for one case on its scratch store; Run sets the replay.
type NewAgent func(st *store.Store) *agent.Agent

// Run executes one case.
func Run(ctx context.Context, c Case, newAgent NewAgent, apiKey string) Result {
	res := Result{ID: c.ID, Title: c.Title}
	start := time.Now()
	dir, err := os.MkdirTemp("", "copilot-eval-")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer os.RemoveAll(dir)
	st, err := store.Open("file:" + filepath.Join(dir, "eval.db"))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	for i, l := range c.Learnings {
		now := time.Now()
		if err := st.SaveLearning(&store.Learning{ID: fmt.Sprintf("LRN-EVAL%d", i), Status: store.LearningApproved, Title: l.Title,
			Category: l.Category, Symptoms: l.Symptoms, RootCause: l.RootCause, HowToConfirm: l.HowToConfirm, Fix: l.Fix,
			Entities: "[]", Investigations: "[]", LastSeen: now, CreatedAt: now}); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	chat := &store.Chat{ID: "eval-" + c.ID, Owner: "eval@prophecy.io", Title: c.Title, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := st.CreateChat(chat); err != nil {
		res.Error = err.Error()
		return res
	}
	rp := &replayer{c: &c}
	a := newAgent(st).WithReplay(rp.replay)
	var text strings.Builder
	runErr := a.Run(ctx, agent.TurnRequest{Chat: chat, User: agent.User{Email: "eval@prophecy.io", IsAdmin: true},
		APIKey: apiKey, KeySource: "eval", Text: c.Question}, func(event string, data any) {
		m, _ := data.(map[string]string)
		if event == "text" {
			text.WriteString(m["delta"])
		}
	})
	res.Duration = time.Since(start).Round(time.Second)
	if runErr != nil {
		res.Error = runErr.Error()
	}
	if items, err := st.ChatItems(chat.ID); err == nil {
		for _, it := range items {
			res.InputTokens += it.InputTokens
			res.OutputTokens += it.OutputTokens
		}
	}
	if calls, err := st.ToolCallsForChat(chat.ID); err == nil {
		for _, tc := range calls {
			res.ToolCalls = append(res.ToolCalls, tc.Tool)
		}
	}
	res.Unrecorded = rp.unrecorded
	invs, _ := st.ListInvestigations("")
	var inv *store.Investigation
	if len(invs) > 0 {
		inv = &invs[0]
		res.Category = inv.Category
	}
	res.Diagnosis, res.Failures = Grade(c, inv, text.String(), len(res.ToolCalls))
	res.Pass = len(res.Failures) == 0 && res.Error == ""
	return res
}

// Grade checks a case's diagnosis: the recorded root cause and fix plus the reply.
func Grade(c Case, inv *store.Investigation, reply string, toolCalls int) (diagnosis string, failures []string) {
	e := c.Expect
	diagnosis = reply
	if inv != nil {
		diagnosis = inv.Title + "\n" + inv.RootCause + "\n" + inv.RecommendedFix + "\n" + reply
	}
	lower := strings.ToLower(diagnosis)
	switch {
	case e.NoInvestigation && inv != nil:
		failures = append(failures, fmt.Sprintf("recorded an investigation (%s) when nothing was wrong", inv.Title))
	case !e.NoInvestigation && inv == nil:
		failures = append(failures, "no investigation recorded")
	}
	if inv != nil && len(e.Categories) > 0 {
		ok := false
		for _, cat := range e.Categories {
			ok = ok || cat == inv.Category
		}
		if !ok {
			failures = append(failures, fmt.Sprintf("category %q, want one of %v", inv.Category, e.Categories))
		}
	}
	for _, group := range e.RootCause {
		found := false
		for _, term := range group {
			found = found || strings.Contains(lower, strings.ToLower(term))
		}
		if !found {
			failures = append(failures, fmt.Sprintf("diagnosis names none of %v", group))
		}
	}
	for _, term := range e.MustNot {
		if strings.Contains(lower, strings.ToLower(term)) {
			failures = append(failures, fmt.Sprintf("diagnosis mentions %q", term))
		}
	}
	if e.MaxToolCalls > 0 && toolCalls > e.MaxToolCalls {
		failures = append(failures, fmt.Sprintf("%d tool calls, budget %d", toolCalls, e.MaxToolCalls))
	}
	return diagnosis, failures
}

// Report renders results as Markdown.
func Report(results []Result, model, effort string) string {
	var b strings.Builder
	pass := 0
	var in, out int64
	var calls int
	for _, r := range results {
		if r.Pass {
			pass++
		}
		in += r.InputTokens
		out += r.OutputTokens
		calls += len(r.ToolCalls)
	}
	fmt.Fprintf(&b, "# Copilot evaluation\n\n%s, effort %s · %s\n\n", model, effort, time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "**%d / %d passed** · %d tool calls · %d input / %d output tokens\n\n", pass, len(results), calls, in, out)
	b.WriteString("| Case | Result | Category | Tool calls | Unrecorded | Tokens in / out | Time |\n|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		status := "pass"
		if !r.Pass {
			status = "**fail**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %d / %d | %s |\n", r.ID, status, r.Category, len(r.ToolCalls), len(r.Unrecorded),
			r.InputTokens, r.OutputTokens, r.Duration)
	}
	for _, r := range results {
		if r.Pass {
			continue
		}
		fmt.Fprintf(&b, "\n## %s — %s\n\n", r.ID, r.Title)
		if r.Error != "" {
			fmt.Fprintf(&b, "Error: %s\n\n", r.Error)
		}
		for _, f := range r.Failures {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		fmt.Fprintf(&b, "\nTools: %s\n", strings.Join(r.ToolCalls, ", "))
		if len(r.Unrecorded) > 0 {
			fmt.Fprintf(&b, "\nUnrecorded calls (add fixtures if they matter):\n")
			for _, u := range r.Unrecorded {
				fmt.Fprintf(&b, "- `%s`\n", truncate(u, 200))
			}
		}
		fmt.Fprintf(&b, "\n<details><summary>Diagnosis</summary>\n\n%s\n\n</details>\n", r.Diagnosis)
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
