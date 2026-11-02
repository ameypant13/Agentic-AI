# Project layout

The console is its own Go module (`github.com/SimpleDataLabsInc/control-hub/ai-console`) inside the control-hub
repository. It shares no code with control-hub and never touches its database.

```text
ai-console/
├── cmd/
│   ├── ai-console/         the web server, API, workers (main.go wires everything)
│   ├── control-hub-mcp/    the read-only tools as an MCP server for Claude Code (+ two certificate write tools)
│   └── copilot-eval/       runs the Copilot on recorded cases and writes a report
├── internal/
│   ├── agent/              Claude tool loop, prompts, console tools (record_investigation, propose_action,
│   │                       learnings), action execution, log rule drafts, right-sizing batch review
│   ├── tools/              read-only tools shared by the Copilot and the MCP server
│   ├── server/             HTTP API, sessions, SPA and docs serving, Slack bot, digest, review poller
│   ├── store/              the console's own tables (gorm; SQLite or Postgres), learnings search
│   ├── config/             environment variables
│   ├── controlhub/         control-hub HTTP client, read-only tokens, JSON redaction
│   ├── monitor/            scheduled checks → findings → Slack
│   ├── kube/               GKE/EKS/AKS with control-hub's credentials; reads, go-sandbox report, DeletePod for actions
│   ├── parser/             background log parser (API server and Loki), hit files and summaries
│   ├── logrules/           the rule library: rules.yaml (embedded), FORMAT.md, prefilter, validation
│   ├── patterns/           Drain miner for error lines no rule matches
│   ├── metricrules/        PromQL rules (rules.yaml) evaluated through Grafana
│   ├── grafana/            Grafana client, instance hub, cluster mappings, AIA certificate fetching
│   ├── certs/              certificate inventory: sources, scan, describe
│   ├── cost/               cost collection, OpenCost allocation, pricing, right-sizing, cost changes
│   ├── readiness/          upgrade readiness checks
│   ├── athena/             read-only Athena client (allow-listed views)
│   ├── eval/               evaluation cases, replay and grading
│   ├── redact/             secret masking for text and log lines
│   ├── secret/             AES-256-GCM sealing, random IDs
│   ├── slackapp/           Slack request verification, users.info / chat.postMessage / chat.update, Markdown → mrkdwn
│   └── testutil/           fakes for tests
├── web/                    React + Vite + Tailwind UI
│   └── src/
│       ├── App.tsx         routes
│       ├── api/            client.ts (fetch + SSE), types.ts
│       ├── components/     Layout, ClusterShell, Issues, Metrics, Chart, ActionButtons, GrafanaSettings, …
│       │   └── shell/      nav.ts (sidebar), CommandPalette, CopilotDock, LiveIndicator, theme
│       ├── lib/            useApi, me (current user), pageContext, format
│       └── pages/          one file per page
├── evals/
│   ├── cases/              recorded evaluation cases (YAML)
│   └── README.md
├── deploy/
│   ├── k8s/                ai-console.yaml (template manifest)
│   └── slack-app-manifest.yaml   the Slack app for the Copilot in Slack
├── docs/                   this site (MkDocs); `make docs-build` writes ./site (git-ignored)
├── mkdocs.yml
├── knowledgegraph/         copied from control-hub's .knowledgegraph by `make kg` (git-ignored)
├── Dockerfile
├── Makefile
├── API.md                  the HTTP API between backend and web
└── README.md
```

## How a request flows

1. `internal/server/server.go` registers routes on a gorilla/mux router under `/api`. Everything except login,
   logout and `/api/healthz` goes through `requireSession`, which loads the session from the `aic_session` cookie and
   applies `CONSOLE_ADMIN_EMAILS`.
2. Handlers call control-hub through `internal/controlhub` with the user's decrypted token, the clusters through
   `internal/kube` after checking the user's infrastructure list, or the console's own store.
3. `/docs/` is served by `docsHandler` (signed-in users only), and `POST /api/slack/events` by the Slack bot, which
   verifies Slack's signature instead of a session.
4. Anything else falls through to the SPA handler, which serves files from `STATIC_DIR` and `index.html` for client
   routes.

## Where to change things

| To change | Look in |
|---|---|
| What the Copilot is told | `internal/agent/prompt.go` |
| A Copilot tool | `internal/tools/*.go` (read-only) or `internal/agent/console_tools.go`, `learnings.go` |
| Which actions exist | `internal/agent/console_tools.go`, `actions.go`, and the policy rows in `internal/server/handlers.go` |
| A monitor | `internal/monitor/monitor.go`, `logs.go` |
| Log rules | `internal/logrules/rules.yaml` |
| Metric rules | `internal/metricrules/rules.yaml` |
| Readiness checks | `internal/readiness/readiness.go` |
| Cost model | `internal/cost/allocate.go`, `rightsize.go`, `changes.go` |
| Configuration | `internal/config/config.go` |
| A page | `web/src/pages/*.tsx`, routes in `web/src/App.tsx`, sidebar in `web/src/components/shell/nav.ts` |
| The API contract | `API.md` and `web/src/api/types.ts` |
