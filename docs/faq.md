# FAQ

## Why not LangChain, LangGraph or CrewAI?

The Copilot is one tool-use loop over one model. The Anthropic Go SDK does everything that loop needs, and writing the
loop directly (`internal/agent/agent.go`, a few hundred lines) gives:

- **Fewer layers.** Every request the console sends is visible in one function (`Agent.params`). There is no framework
  prompt, memory or retry logic to discover when something behaves oddly.
- **Full control of the tool loop.** The console decides how tools run (concurrently, each with a 2-minute timeout),
  how results are capped (60,000 characters), what a non-admin sees for each step, when to stop (30 iterations), and
  how refusals, `pause_turn` and `max_tokens` are handled. Proposed actions and read-only mode are plain Go, not
  framework callbacks.
- **Prompt caching where it pays.** The base prompt and tool definitions are identical for every user and turn and
  carry a cache breakpoint; the conversation is cached as it grows. The per-user audience prompt sits after the cached
  block. That ordering is easy to get right when you build the request yourself.
- **Streaming straight to the browser.** Text deltas and tool steps are forwarded as server-sent events as they
  arrive.
- **An exact, append-only transcript.** Each message is stored exactly as sent to the API, so thinking blocks stay
  valid across turns, and evaluation replay can swap tool outputs without touching anything else.
- **A single static binary.** The console is Go, built with `CGO_ENABLED=0` into a distroless image with no shell
  and no Python runtime. LangChain, LangGraph and CrewAI are Python-first (LangChain also has a JavaScript port);
  using them would mean a second service and language.

The same reasoning applies to the batch review and log rule drafts: each is one request with a forced tool, which the
SDK expresses directly.

## Why SQLite?

The console is a single replica by design: its monitors and workers run in-process and must not run twice. With one
writer, SQLite on a persistent volume is the simplest database that works:

- no database server to run, patch, back up or grant access to; one disk snapshot backs up everything, files included;
- a pure-Go driver, so the binary stays static;
- WAL mode, `busy_timeout` and immediate transactions make concurrent requests and workers safe in one process.

The data is small (sessions, chats, findings, learnings, rule changes, daily cost rows). If the console ever needs
more than one replica or a managed database, `DATABASE_URL=postgres://…` switches the same gorm models to Postgres.

## Why native tools instead of MCP servers for Grafana, Athena and Kubernetes?

The tools need things an off-the-shelf MCP server does not know about:

- **Per-user access.** Every cluster call first checks the user's control-hub token can see that cluster; every
  Athena call resolves the control plane through control-hub with the user's token. A generic Kubernetes or Grafana
  MCP server runs with one fixed identity.
- **Read-only by construction.** The Copilot's tools only call get, list and logs on clusters; Athena only GETs an
  allow-list of read views; Grafana only queries. A test fails if a write tool reaches the Copilot. The only writes
  are dry-run-checked actions the requester clicks.
- **Redaction and limits** tuned for this fleet: secret-named keys, JWTs and bearer tokens masked, Secret values never
  read, results capped so one call cannot flood the context.
- **Fleet knowledge**: the control-hub cluster → Grafana `cluster` / Loki `k8s_cluster_name` mapping, which clusters
  are metrics-only, which credentials open GKE, EKS and AKS.
- **One process.** Tools are Go functions in the same binary, sharing caches (cluster clients, Grafana mappings) with
  the UI. Nothing extra to deploy, authenticate or keep running.
- **Replay.** Evaluations swap tool outputs for recorded fixtures in-process.

The tools are still available over MCP where that helps: `cmd/control-hub-mcp` serves the same registry to Claude
Code on your machine. See [MCP server](development/mcp.md).

## What data leaves the cluster?

Only what goes to the **Anthropic API**, and only when someone uses a Claude feature:

| Feature | What is sent |
|---|---|
| Copilot chat | The system prompt (including the knowledge graph index), the conversation, and every tool result the Copilot requested during it: control-hub records (secrets redacted), log lines (masked), Kubernetes objects (Secret values never read), metric and log query results, Athena views (secrets redacted, emails masked), learnings, certificate inventory, cost data. |
| Claude key check | A model lookup with the key being saved. |
| Learnings search by meaning | The search text. |
| Log rule drafts | The pattern's template and masked example lines, or the learning's text. |
| Right-sizing review | One recommendation per request: workload and container names, usage, requests, limits, notes. |

The background workers (monitors, log parser, pattern miner, metric rules, cost collector, certificate inventory,
digest) use no AI and send nothing to Anthropic. Slack receives finding titles and details and the weekly digest, when
Slack is configured and not muted, and, with the [Copilot in Slack](user-guide/slack.md), the answers to questions asked
there (non-admin view in channels).

Everything else the console reads stays inside: it reads control-hub, the clusters, Grafana, Athena and cloud APIs,
and stores its results on its own volume.

## Does the Copilot change anything?

Not by itself. Its tools are read-only. The only state-changing operation it can offer is retrying SAP post-deploy,
as a button that runs only when the requester clicks it, with their own control-hub token. On a read-only console it
cannot offer even that. See [Security model](concepts/security.md#what-the-console-can-change).

## Why does the Copilot not see a cluster I can see in Lens?

It sees exactly what control-hub lets you see: the clusters in control-hub's `/api/infrastructures` for your token.
Imported and SAP clusters have no credentials in control-hub, so only their Grafana metrics and logs are available.

## Why are costs different from the cloud bill?

They are estimates of compute only, at on-demand list prices, allocated with OpenCost's model. Discounts, savings
plans, storage, network and managed control-plane fees are not included. See [Cost model](concepts/cost-model.md).

## Can I use my Claude.ai Pro or Max subscription?

No. The console needs an API key from console.anthropic.com. Subscriptions cannot be used by an application like this.

## Where are the docs served?

At `/docs/` on the console itself, to signed-in users only (others are sent to sign in first). The site is built with
MkDocs in the image's `docs` stage and served from `DOCS_DIR`. The sidebar's **Documentation** link opens it. See
[Working on these docs](development/docs.md).
