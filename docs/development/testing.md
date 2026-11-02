# Tests and evals

## Unit and integration tests

```bash
cd ai-console
make build test
```

`make build` runs `go build ./...` and `go vet ./...`; `make test` runs `go test ./...`. The tests use a fake
control-hub, a fake Claude API and fake Kubernetes clientsets (`internal/testutil`), so they need no credentials and
make no network calls.

A few tests guard the safety properties. Keep them passing:

| Test | Guards |
|---|---|
| `internal/tools/writes_test.go` `TestRegistryIsReadOnly` | No write tool (`Writes: true`, `repair_certificate`, `renew_wildcard_certificate`) reaches the Copilot's tool registry. |
| `internal/controlhub/readonly_test.go` `TestReadOnlyTokenBlocksWrites` | Service tokens can only make GET requests. |
| `internal/agent/readonly_test.go` `TestReadOnlyKeepsDryRunActions` | `READ_ONLY` keeps `propose_action` (proposals are dry runs and execution refuses them). |
| `internal/agent/dryrun_test.go` | Dry-run plans and refusals, and `ExecuteAction`'s guards. |
| `internal/slackapp/slackapp_test.go`, `internal/server/slackbot_test.go` | Slack signature verification, Markdown conversion and splitting, and the Slack Copilot flow. |
| `internal/logrules/logrules_test.go` `TestDefaultRulesAreValid` | Every built-in log rule compiles, matches its examples and none of its not-examples. |
| `internal/controlhub/redact_test.go` | Secret-looking keys are redacted. |
| `internal/tools/tools_test.go` | Log line redaction, the probe host allow-list, and that the knowledge-graph tool only serves its own files. |

Two tests talk to a real Grafana and are skipped unless their environment is set: `internal/cost/live_test.go`
(`COST_LIVE_URL`, `COST_LIVE_TOKEN`, `COST_LIVE_CLUSTER`) and `internal/grafana/explore_live_test.go`
(`GRAFANA_LIVE_URL`, `GRAFANA_LIVE_TOKEN`, `GRAFANA_LIVE_CLUSTER`).

The log parser has a benchmark:

```bash
go test ./internal/logrules -bench . -run '^$'
```

## UI

```bash
cd ai-console/web
npm run build
```

`npm run build` runs `tsc -b` and `vite build`. `tsc --noEmit` alone is not a check here: it always passes.

## Copilot evaluations

Unit tests check the plumbing; **evals** check the Copilot's reasoning. Each case is a question plus the tool outputs a
real investigation would see. `make eval` runs the Copilot on every case against Claude with those outputs replayed
(nothing reaches control-hub, clusters or Grafana) and writes `evals/report.md`: pass or fail per case, tool calls,
calls with no recorded data, and tokens.

```bash
cd ai-console
ANTHROPIC_API_KEY=<key> make eval                                    # all cases
ANTHROPIC_API_KEY=<key> make eval EVAL_ARGS="-only pg16-public-schema,healthy-tenant"
```

!!! warning "Costs tokens"
    Every run costs tokens (about ten Opus conversations). Run it when the prompt, a tool or the model changes, and
    compare reports before shipping the change.

### Runner flags

`make eval` runs `go run ./cmd/copilot-eval -cases evals/cases -kg knowledgegraph -out evals/report.md $(EVAL_ARGS)`.

| Flag | Default | Meaning |
|---|---|---|
| `-cases` | `evals/cases` | Directory of case YAML files. |
| `-only` | — | Comma-separated case IDs to run. |
| `-model` | `claude-opus-5-5` | Claude model. |
| `-effort` | `high` | Reasoning effort. |
| `-kg` | `knowledgegraph` | Knowledge graph directory (`make kg`). |
| `-parallel` | `3` | Cases run at once. |
| `-out` | `evals/report.md` | Markdown report path; a `.json` is written next to it. |

### How a case runs

1. A scratch database is created, with the case's `learnings` inserted as approved learnings.
2. The Copilot gets the case's `question` as the first message.
3. Every tool call is answered from `fixtures`: the first fixture for that tool whose `match` values are substrings of
   the call's input fields (case-insensitive) wins. A call with no fixture returns
   `{"note":"no data recorded for this call in the evaluation case"}` and is listed in the report.
4. The console's own tools (`record_investigation`, `record_learning`, `search_learnings`, `propose_action`) run for
   real against the scratch database.
5. The case is graded on the recorded investigation (title, root cause, fix) plus the reply.

### Case format

```yaml
id: short-id
title: one line
question: what the user asks
learnings: []            # optional approved learnings present during the run
fixtures:                # first match wins; unmatched calls return "no data recorded"
  - tool: get_job
    match: {job_id: job-1}   # substrings of input fields, case-insensitive; omit to match any call
    output: {...}            # or a string, or `error: message`
expect:
  categories: [networking-dns]       # accepted investigation categories
  rootCause: [[subnet], [tag, kubernetes.io/role/elb]]   # every group needs one term
  mustNot: [certificate expired]     # known wrong answers
  noInvestigation: false             # true when nothing is wrong
  maxToolCalls: 18
```

| Expectation | Fails when |
|---|---|
| `noInvestigation: false` (default) | No investigation was recorded. |
| `noInvestigation: true` | An investigation was recorded although nothing is wrong. |
| `categories` | The investigation's category is not in the list (empty = any). |
| `rootCause` | Some group has none of its terms in the diagnosis. |
| `mustNot` | Any term appears in the diagnosis. |
| `maxToolCalls` | The Copilot needed more calls (0 = no cap). |

### The shipped cases

| Case | Title |
|---|---|
| `acm-validation-cname-missing` | ACM certificate cannot renew because its DNS validation record was deleted |
| `azure-imds-blocked` | Azure pods cannot get cloud credentials after IMDS access was restricted |
| `dns-elb-subnet-tags` | DNS step times out because the ingress load balancer is never created |
| `eks-unauthorized` | The console's cluster access fails because the EKS access entry is missing |
| `gcp-cpu-quota` | Execution plane creation fails on the regional CPU quota |
| `healthy-tenant` | Nothing is wrong; the Copilot should say so and not invent a cause |
| `loki-rate-limited` | Logs missing in Grafana because Loki is rate-limiting ingestion |
| `metagraph-oom-heap` | metagraph OOM-killed because its JVM heap is sized at the container limit |
| `misleading-learning-wrong-zone` | A known learning matches the symptom, but the evidence points to the wrong hosted zone |
| `pg16-public-schema` | Prophecy never comes up on Postgres 16 because the public schema grant is missing |

Add a case for every real investigation the Copilot got wrong, or one worth keeping right. The report lists the
unrecorded calls of failed cases: add fixtures for the ones the right diagnosis depends on.
