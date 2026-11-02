// Package agent runs the Copilot: a Claude tool-use loop over the read-only
// tools in internal/tools plus two console tools (record_investigation,
// propose_action). It streams progress to the browser and persists the
// transcript append-only.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/athena"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/audit"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

const (
	maxIterations   = 30
	maxToolResult   = 60_000 // characters of one tool result sent to the model
	maxStepSummary  = 600    // characters of a tool result kept as evidence
	maxOutputTokens = 32_000
)

type Agent struct {
	store  *store.Store
	ch     *controlhub.Client
	kg     *tools.KnowledgeGraph
	logs   *tools.LogReader
	probe  tools.ProbePolicy
	model  string
	effort string
	base   string // cached system prompt
	// clientOpts are extra Claude client options; tests point the client at a fake API.
	clientOpts []option.RequestOption
	// readOnly removes propose_action: nothing the Copilot offers can change state.
	readOnly bool
	grafana  *grafana.Hub
	issues   func(ctx context.Context, cluster string) (string, error)
	certs    func(ctx context.Context, query string, problemsOnly bool) (string, error)
	costs    func(ctx context.Context, cluster, namespace, groupBy string, days int) (string, error)
	kube     tools.KubeAccess
	athena   *athena.Client
	// audit answers "who changed what"; offered to admins only.
	audit     *audit.Searcher
	readiness func(ctx context.Context, token, controlPlane string) (string, error)
	// replay answers tool calls from recorded outputs (evaluations); nil runs tools.
	replay Replay
}

// Replay returns a recorded output for a tool call. Returning ErrNotReplayed
// runs the real tool instead (the console's own tools, which only touch the
// console's database).
type Replay func(tool string, input json.RawMessage) (string, error)

// ErrNotReplayed tells the agent to run the real tool.
var ErrNotReplayed = errors.New("not replayed")

// WithReplay makes tool calls return recorded outputs instead of reaching
// control-hub, clusters or Grafana, so an evaluation is repeatable.
func (a *Agent) WithReplay(r Replay) *Agent {
	a.replay = r
	return a
}

// WithReadiness gives the Copilot the upgrade readiness check.
func (a *Agent) WithReadiness(f func(ctx context.Context, token, controlPlane string) (string, error)) *Agent {
	a.readiness = f
	return a
}

// WithLogs gives the Copilot control-hub's log search.
func (a *Agent) WithLogs(l *tools.LogReader) *Agent {
	a.logs = l
	return a
}

// WithMonitoring gives the Copilot Grafana metric/log queries and the current issues.
func (a *Agent) WithMonitoring(hub *grafana.Hub, issues func(ctx context.Context, cluster string) (string, error)) *Agent {
	a.grafana, a.issues = hub, issues
	return a
}

// WithCertificates gives the Copilot the certificate inventory.
func (a *Agent) WithCertificates(f func(ctx context.Context, query string, problemsOnly bool) (string, error)) *Agent {
	a.certs = f
	return a
}

// WithCosts gives the Copilot the stored workload costs.
func (a *Agent) WithCosts(f func(ctx context.Context, cluster, namespace, groupBy string, days int) (string, error)) *Agent {
	a.costs = f
	return a
}

// WithKube gives the Copilot read-only access to the clusters the user may see.
func (a *Agent) WithKube(f tools.KubeAccess) *Agent {
	a.kube = f
	return a
}

// WithAthena gives the Copilot read-only access to control planes' Athena.
func (a *Agent) WithAthena(c *athena.Client) *Agent {
	a.athena = c
	return a
}

// WithAudit gives admins the audit_search tool.
func (a *Agent) WithAudit(s *audit.Searcher) *Agent {
	a.audit = s
	return a
}

// Audit returns the audit searcher, nil when audit is off.
func (a *Agent) Audit() *audit.Searcher { return a.audit }

// WithReadOnly removes the action tool from the Copilot.
func (a *Agent) WithReadOnly(ro bool) *Agent {
	a.readOnly = ro
	return a
}

func New(st *store.Store, ch *controlhub.Client, kg *tools.KnowledgeGraph, logs *tools.LogReader, model, effort string) *Agent {
	index := ""
	if kg != nil {
		index = kg.Index()
	}
	return &Agent{store: st, ch: ch, kg: kg, logs: logs, probe: tools.DefaultProbePolicy(),
		model: model, effort: effort, base: basePrompt(index)}
}

// WithClientOptions adds Claude client options (tests use it to target a fake API).
func (a *Agent) WithClientOptions(opts ...option.RequestOption) *Agent {
	a.clientOpts = append(a.clientOpts, opts...)
	return a
}

// User is the signed-in person the turn runs for.
type User struct {
	Email   string
	IsAdmin bool
	Token   string // control-hub token
}

// Emitter sends one server-sent event to the browser.
type Emitter func(event string, data any)

type TurnRequest struct {
	Chat      *store.Chat
	User      User
	APIKey    string
	KeySource string // "personal" or "shared"
	Text      string
}

type turn struct {
	a     *Agent
	chat  *store.Chat
	user  User
	item  *store.ChatItem
	emitM sync.Mutex
	out   Emitter
}

func (t *turn) emit(event string, data any) {
	t.emitM.Lock()
	defer t.emitM.Unlock()
	t.out(event, data)
}

// Run executes one user turn: it appends the user's message, loops through
// Claude and the tools until Claude replies, and streams events via out.
func (a *Agent) Run(ctx context.Context, req TurnRequest, out Emitter) error {
	now := time.Now()
	userItem := &store.ChatItem{ID: secret.RandomID(9), ChatID: req.Chat.ID, Role: "user", Text: req.Text, CreatedAt: now}
	if err := a.store.SaveChatItem(userItem); err != nil {
		return err
	}
	item := &store.ChatItem{ID: secret.RandomID(9), ChatID: req.Chat.ID, Role: "assistant", KeySource: req.KeySource,
		CreatedAt: now.Add(time.Millisecond)}
	if err := a.store.SaveChatItem(item); err != nil {
		return err
	}
	t := &turn{a: a, chat: req.Chat, user: req.User, item: item, out: out}

	messages, err := a.loadTranscript(req.Chat.ID)
	if err != nil {
		return err
	}
	userMsg := anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(a.actionNotes(req.Chat.ID) + req.Text))
	messages = append(messages, userMsg)
	if err := a.store.AppendTranscript(req.Chat.ID, userMsg); err != nil {
		return err
	}

	env := &tools.Env{CH: a.ch, Token: req.User.Token, KG: a.kg, Logs: a.logs, Probe: a.probe, Grafana: a.grafana, Issues: a.issues, Certs: a.certs, Costs: a.costs, Kube: a.kube, Athena: a.athena, Console: a.consoleEvents, Readiness: a.readiness}
	toolset := append(tools.Registry(env), t.consoleTools()...)
	byName := map[string]tools.Tool{}
	for _, tl := range toolset {
		byName[tl.Name] = tl
	}

	client := anthropic.NewClient(append([]option.RequestOption{option.WithAPIKey(req.APIKey)}, a.clientOpts...)...)
	var text strings.Builder
	var usageIn, usageOut int64

	finish := func(runErr error) error {
		item.Text = text.String()
		item.InputTokens, item.OutputTokens = usageIn, usageOut
		if err := a.store.SaveChatItem(item); err != nil {
			log.Printf("save chat item: %v", err)
		}
		_ = a.store.TouchChat(req.Chat.ID, "")
		if runErr != nil {
			t.emit("error", map[string]string{"message": runErr.Error()})
			return runErr
		}
		t.emit("done", map[string]any{"itemId": item.ID, "keySource": req.KeySource,
			"usage": map[string]int64{"inputTokens": usageIn, "outputTokens": usageOut}})
		return nil
	}

	for i := 0; i < maxIterations; i++ {
		params := a.params(req.User.IsAdmin, toolset, messages)
		stream := client.Beta.Messages.NewStreaming(ctx, params)
		msg := anthropic.BetaMessage{}
		for stream.Next() {
			ev := stream.Current()
			if err := msg.Accumulate(ev); err != nil {
				return finish(fmt.Errorf("reading the Claude stream: %w", err))
			}
			if d, ok := ev.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
				if td, ok := d.Delta.AsAny().(anthropic.BetaTextDelta); ok {
					text.WriteString(td.Text)
					t.emit("text", map[string]string{"delta": td.Text})
				}
			}
		}
		if err := stream.Err(); err != nil {
			return finish(describeAPIError(err))
		}
		usageIn += msg.Usage.InputTokens + msg.Usage.CacheReadInputTokens + msg.Usage.CacheCreationInputTokens
		usageOut += msg.Usage.OutputTokens

		switch msg.StopReason {
		case anthropic.BetaStopReasonRefusal:
			// Not appended: the declined turn should not shape later turns.
			return finish(errors.New("Claude declined this request. Try rephrasing it."))
		case anthropic.BetaStopReasonToolUse, anthropic.BetaStopReasonPauseTurn:
			// handled below
		case anthropic.BetaStopReasonMaxTokens:
			_ = a.appendAssistant(req.Chat.ID, &messages, msg)
			return finish(errors.New("the reply hit the length limit; ask a narrower question"))
		default:
			if err := a.appendAssistant(req.Chat.ID, &messages, msg); err != nil {
				return finish(err)
			}
			return finish(nil)
		}

		if err := a.appendAssistant(req.Chat.ID, &messages, msg); err != nil {
			return finish(err)
		}
		if msg.StopReason == anthropic.BetaStopReasonPauseTurn {
			continue
		}
		results := t.runTools(ctx, env, byName, msg)
		resultMsg := anthropic.NewBetaUserMessage(results...)
		messages = append(messages, resultMsg)
		if err := a.store.AppendTranscript(req.Chat.ID, resultMsg); err != nil {
			return finish(err)
		}
		if text.Len() > 0 && !strings.HasSuffix(text.String(), "\n\n") {
			text.WriteString("\n\n")
			t.emit("text", map[string]string{"delta": "\n\n"})
		}
	}
	return finish(errors.New("stopped after too many steps without a final answer"))
}

func (a *Agent) params(isAdmin bool, toolset []tools.Tool, messages []anthropic.BetaMessageParam) anthropic.BetaMessageNewParams {
	tls := make([]anthropic.BetaToolUnionParam, 0, len(toolset))
	for _, tl := range toolset {
		tls = append(tls, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name:        tl.Name,
			Description: param.NewOpt(tl.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: tl.Properties, Required: tl.Required},
		}})
	}
	return anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: maxOutputTokens,
		System: []anthropic.BetaTextBlockParam{
			{Text: a.base, CacheControl: anthropic.NewBetaCacheControlEphemeralParam()},
			{Text: audiencePrompt(isAdmin, time.Now())},
		},
		Tools:    tls,
		Messages: messages,
		// Cache the conversation so far, so each tool-loop step re-reads it cheaply.
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(a.effort)},
		// On a safety-classifier refusal, let the API retry on its recommended fallback model.
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	}
}

func (a *Agent) appendAssistant(chatID string, messages *[]anthropic.BetaMessageParam, msg anthropic.BetaMessage) error {
	p := msg.ToParam()
	*messages = append(*messages, p)
	return a.store.AppendTranscript(chatID, p)
}

func (a *Agent) loadTranscript(chatID string) ([]anthropic.BetaMessageParam, error) {
	entries, err := a.store.Transcript(chatID)
	if err != nil {
		return nil, err
	}
	out := make([]anthropic.BetaMessageParam, 0, len(entries))
	for _, e := range entries {
		var p anthropic.BetaMessageParam
		if err := json.Unmarshal([]byte(e.Param), &p); err != nil {
			return nil, fmt.Errorf("decode transcript entry %d: %w", e.ID, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// runTools executes every tool_use block of msg concurrently and returns the
// tool_result blocks in the same order, for one user message.
func (t *turn) runTools(ctx context.Context, env *tools.Env, byName map[string]tools.Tool, msg anthropic.BetaMessage) []anthropic.BetaContentBlockParamUnion {
	var uses []anthropic.BetaToolUseBlock
	for _, b := range msg.Content {
		if u, ok := b.AsAny().(anthropic.BetaToolUseBlock); ok {
			uses = append(uses, u)
		}
	}
	results := make([]anthropic.BetaContentBlockParamUnion, len(uses))
	var wg sync.WaitGroup
	for i, u := range uses {
		wg.Add(1)
		go func(i int, u anthropic.BetaToolUseBlock) {
			defer wg.Done()
			results[i] = t.runTool(ctx, env, byName, u)
		}(i, u)
	}
	wg.Wait()
	return results
}

func (t *turn) runTool(ctx context.Context, env *tools.Env, byName map[string]tools.Tool, u anthropic.BetaToolUseBlock) anthropic.BetaContentBlockParamUnion {
	input := json.RawMessage(u.JSON.Input.Raw())
	tl, known := byName[u.Name]
	label := tl.Label
	if !known {
		label = "Ran a check"
	}
	tc := &store.ToolCall{ID: u.ID, ChatID: t.chat.ID, ChatItemID: t.item.ID, Tool: u.Name, Label: label,
		Input: string(input), At: time.Now()}
	_ = t.a.store.SaveToolCall(tc)

	step := map[string]any{"id": u.ID, "label": label, "ok": nil}
	if t.user.IsAdmin {
		step["tool"], step["input"] = u.Name, string(input)
	}
	t.emit("step", step)

	var out string
	var err error
	if !known {
		err = fmt.Errorf("unknown tool %q", u.Name)
	} else {
		replayed := false
		if t.a.replay != nil {
			out, err = t.a.replay(u.Name, input)
			replayed = !errors.Is(err, ErrNotReplayed)
		}
		if !replayed {
			toolCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			out, err = tl.Run(toolCtx, env, input)
			cancel()
		}
	}
	ok := err == nil
	if err != nil {
		out = "Error: " + err.Error()
	}
	if len(out) > maxToolResult {
		out = out[:maxToolResult] + "\n…[truncated; narrow the query]"
	}

	tc.OK, tc.Summary = &ok, truncate(out, maxStepSummary)
	_ = t.a.store.SaveToolCall(tc)
	done := map[string]any{"id": u.ID, "ok": ok}
	if t.user.IsAdmin {
		done["summary"] = tc.Summary
	}
	t.emit("step_done", done)
	return anthropic.NewBetaToolResultBlock(u.ID, out, !ok)
}

// actionNotes tells the model about actions the user ran since its last turn,
// so it doesn't propose them again or assume they did not happen.
func (a *Agent) actionNotes(chatID string) string {
	acts, err := a.store.ActionsForChat(chatID)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for i := range acts {
		act := &acts[i]
		if act.ExecutedAt == nil || act.ReportedToModel {
			continue
		}
		fmt.Fprintf(&b, "[Console note: the user ran %s (%s for %s); status %s: %s]\n",
			act.ID, act.Title, act.TargetName, act.Status, truncate(act.Result, 500))
		act.ReportedToModel = true
		_ = a.store.SaveAction(act)
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

// linkFindings attaches an investigation to the open findings of its entity.
func (a *Agent) linkFindings(inv *store.Investigation) {
	if inv.EntityID == "" {
		return
	}
	findings, err := a.store.UnresolvedFindings()
	if err != nil {
		return
	}
	for i := range findings {
		if findings[i].EntityID == inv.EntityID {
			findings[i].InvestigationID = inv.ID
			_ = a.store.SaveFinding(&findings[i])
		}
	}
}

func describeAPIError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return errors.New("the Claude API key was rejected; update it in Settings")
		case 403:
			return errors.New("the Claude API key isn't allowed to use this model")
		case 429:
			return errors.New("the Claude API rate limit was reached; try again in a minute")
		case 529, 503:
			return errors.New("the Claude API is overloaded; try again shortly")
		}
		return fmt.Errorf("Claude API error %d", apiErr.StatusCode)
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("stopped")
	}
	return fmt.Errorf("calling the Claude API: %w", err)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
