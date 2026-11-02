# Adding a Copilot tool

Copilot tools live in `internal/tools`. The same registry serves the Copilot and the MCP server, so a tool you add
is available in both. Tools there must be **read-only**: anything that changes state is a proposed action instead (see
[The Copilot agent loop](../concepts/copilot-agent.md#proposing-actions)).

## The Tool type

```go
type Tool struct {
	Name        string
	Description string
	Properties  map[string]any   // JSON Schema properties of the input
	Required    []string
	// Label is the step text shown to non-admins, e.g. "Checked DNS". It must
	// not contain resource IDs or internal details.
	Label string
	// Writes marks a tool that changes state (MCP only, never the Copilot).
	Writes bool
	Run    func(ctx context.Context, env *Env, input json.RawMessage) (string, error)
}
```

`Env` carries what a tool may use: the control-hub client and **the calling user's token** (`env.CH`, `env.Token`),
and optional dependencies (knowledge graph, log reader, Grafana hub, issues, certificates, costs, Athena, readiness,
cluster access, console events). A nil dependency disables the tools that need it.

## Steps

1. **Write the tool** in a file in `internal/tools`, following an existing one. A minimal example, modelled on
   `upgrade_readiness`:

    ```go
    func exampleTool() Tool {
        return Tool{
            Name: "example_lookup",
            Description: "What it returns, when to use it, and its limits. The model reads this; " +
                "be specific about inputs and what the output contains.",
            Properties: map[string]any{"control_plane": str("Control plane ID or customer / tenant name.")},
            Required:   []string{"control_plane"},
            Label:      "Checked the example",
            Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
                in, err := decode[struct {
                    ControlPlane string `json:"control_plane"`
                }](input)
                if err != nil {
                    return "", err
                }
                if err := requireID("control_plane", in.ControlPlane); err != nil {
                    return "", err
                }
                raw, err := env.CH.Get(ctx, env.Token, "/api/…", nil)
                if err != nil {
                    return "", err
                }
                return string(controlhub.Redact(raw)), nil
            },
        }
    }
    ```

    Helpers in `tools.go`: `str`, `integer`, `enum` for schema properties, `realmProp` for the realm, `decode[T]` for
    the input, `requireID` to reject empty IDs and IDs with `/`, `?` or `#`.

2. **Register it** in `Registry` in `internal/tools/tools.go`. If it needs a new dependency, add a field to `Env`,
   gate the tool on it (`if env.X != nil { all = append(all, …) }`), and set it in `internal/agent/agent.go` (an
   `Agent.WithX` method and the `tools.Env` built in `Run`) and in `cmd/ai-console/main.go`. Add it to
   `cmd/control-hub-mcp/main.go` too if the MCP server should have it.

3. **Keep secrets out**:
    - pass control-hub JSON through `controlhub.Redact`;
    - pass log lines and free text through `redact.LogLine` / `redact.Text`;
    - never return Secret values; cap output size (the agent cuts results at 60,000 characters, but a smaller,
      focused result is better).

4. **Use the user's access.** Call control-hub with `env.Token`, never a service token. For clusters, go through
   `env.Kube`, which checks the user's infrastructure list on every call.

5. **Write a Label** that is safe for non-admins: no IDs, hostnames or component names.

6. **Tell the Copilot when to use it.** If the tool answers a class of questions, add a sentence to the base prompt in
   `internal/agent/prompt.go`. Keep the prompt identical for every user and turn (it is cached).

7. **Test it.**
    - Unit-test the tool in `internal/tools/*_test.go` with the fakes in `internal/testutil`.
    - `TestRegistryIsReadOnly` must still pass.
    - Add or update an [eval case](testing.md#copilot-evaluations) with fixtures for the new tool, and run `make eval`
      on the cases it affects.

8. **Document it** in the tool tables of [The Copilot agent loop](../concepts/copilot-agent.md#tools) and the
   [Copilot user guide](../user-guide/copilot.md#what-the-copilot-can-use).

## Console tools

Tools that need the turn's context (the chat, the user, the event stream), such as `record_investigation`,
`propose_action`, `search_learnings` and `record_learning`, are built per turn in `internal/agent`
(`console_tools.go`, `learnings.go`), not in the shared registry. A new state-changing action needs:

- a new kind constant and its entry in the `propose_action` schema and description (`console_tools.go`);
- its execution in `ExecuteAction` (`actions.go`), with the clicking user's token;
- a policy row in `internal/server/handlers.go` (`policy`);
- a dry-run planner in `DryRun` (`dryrun.go`) that returns the steps, checks, risks, targets and any reason to block it;
- a title in `actionTitles`;
- on read-only consoles it is proposed as a dry run only, and `ExecuteAction` refuses it.
