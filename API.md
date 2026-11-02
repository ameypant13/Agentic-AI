# AI Console — HTTP API (backend ↔ web)

All routes are under `/api`, JSON in and out, and authenticated by the httpOnly cookie
`aic_session` (set by login). An unauthenticated call returns `401 {"error": "..."}`.
Errors are always `{"error": "<message>"}` with a 4xx/5xx status.

Times are RFC 3339 strings. IDs are strings.

## Auth

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/api/auth/login` | `{email, password}` | `Me` — logs in through control-hub's `/api/auth/login` |
| POST | `/api/auth/token` | `{token}` | `Me` — sign in with an existing control-hub token (SSO users) |
| POST | `/api/auth/logout` | — | `{}` |
| GET | `/api/me` | — | `Me` |
| PUT | `/api/me/claude-key` | `{apiKey}` | `ClaudeKey` — validated against the Claude API before it is stored |
| DELETE | `/api/me/claude-key` | — | `ClaudeKey` |

```ts
type Me = { email: string; username: string; isAdmin: boolean; claudeKey: ClaudeKey }
type ClaudeKey = {
  configured: boolean          // the user stored a personal key
  last4: string                // "" when not configured
  source: "personal" | "shared" | "none"   // which key the next chat will use
}
```

## Fleet & findings

```ts
type Fleet = {
  controlPlanes: { total: number; byStatus: Record<string, number>; byRealm: Record<string, number> }
  openFindings: { critical: number; warning: number }
  investigations7d: number
  lastMonitorRun: string | null
}
type Finding = {
  id: string
  kind: "cp_failed" | "cp_stuck" | "postdeploy_failed" | "ep_failed" | "cert_expiry"
  severity: "critical" | "warning"
  title: string                 // one line, e.g. "Provisioning failed"
  detail: string                // one or two sentences
  entityType: "control_plane" | "execution_plane"
  entityId: string
  entityName: string            // customer / tenant name
  realm: string; provider: string; region: string
  state: "open" | "acknowledged" | "resolved"
  firstSeenAt: string; lastSeenAt: string; resolvedAt: string | null
  investigationId: string | null
}
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/fleet` | `Fleet` |
| GET | `/api/findings?state=open\|resolved\|all` (default `open`) | `Finding[]`, critical first, then newest |
| POST | `/api/findings/{id}/investigate` | `{chatId, prompt}` — creates a chat; the UI opens it and sends `prompt` as the first message |
| POST | `/api/findings/{id}/ack` | `Finding` (admin only) |
| GET | `/api/monitors` | `Monitor[]` |

```ts
type Monitor = { key: string; name: string; condition: string; interval: string; notify: string; enabled: boolean }
```

## Certificates

A background worker (`CERT_SCAN_INTERVAL`, default 6 h; result in `DATA_DIR/certs/inventory.json`) inventories:
the certificate each control plane host serves (`kind: tenant`), control-hub's wildcard TLS secrets (`wildcard`),
and ACM / GCP Certificate Manager certificates not already shown through a tenant (`acm`, `gcp`). Read-only.

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/api/certificates?kind=` | — | `{scannedAt, durationMs, running, intervalMinutes, counts, errors[], items: CertItem[]}`, soonest expiry first (unknown expiry last) |
| POST | `/api/certificates/scan` | — | `202 {queued}` — admins; rescans now |
| POST | `/api/certificates/repair` | `{key, dryRun, force?}` | control-hub's `RepairResult {kind, target, dryRun, changes[{name, type, current, desired, action, applied, error}], next}` — admins, needs `CERT_REPAIR_ENABLED`. The target comes from the inventory item's `repair` field; control-hub (`POST /api/certificates/repair`, admin token) recreates ACM validation / Certificate Manager DNS-authorization CNAMEs. For a wildcard row (`repair.kind = wildcard-renew`) it calls control-hub's `POST /api/certificates/wildcards/renew` instead: renew in ACM, then export and sync to control-hub's secret and every tenant copy; returns `WildcardResult {secret, domain, step: renew-in-acm|sync|up-to-date|manual, source, changes[{target, secret, currentExpiry, newExpiry, action, reason, applied, error}], next}`. Audited. |

`CertItem`: `key, kind, tenant, tenantId, realm, provider, region, host, source (acm | gcp-certificate-manager |
wildcard-secret | unknown), sourceRef, autoRenew, subject, issuer, sans, notAfter, daysLeft, status (ok | unknown |
warning | critical | expired), problems[{severity, code, message}], checks[{name, ok, detail}], usedBy[], repair?{kind,
certificateArn | certificate}`. The list response also has `repairEnabled`.

Status: expired < 0 days, critical ≤ 7, warning ≤ 30, raised by any warning/critical problem; `unknown` when the host
could not be reached. Problem codes: `dns_missing`, `tls_unreachable` (info), `hostname_mismatch`, `untrusted_chain`,
`validation_cname_missing`, `validation_cname_mismatch`, `renewal_failed`, `renewal_pending_validation`, `acm_status`,
`not_in_use` (info), `gcp_state`, `gcp_provisioning_issue`, `gcp_authorization_failed`, `stale_copy` (a cluster serves an
older copy of a wildcard than a secret, an exportable ACM cert or another host has), `stale_secret`, `manual_renewal` (info).

## Costs (estimated workload cost)

The cost worker (`internal/cost`) recomputes today for every Grafana cluster each hour and backfills
`COST_BACKFILL_DAYS` (30) past days one day per cluster every 2 minutes. Per window it runs about 12 aggregated
PromQL queries per cluster (Thanos does the aggregation) and applies OpenCost's model: node list price split into
per-core and per-GiB rates, each pod charged for max(request, usage), the rest of node cost is idle. Instance types
come from node-exporter's DMI info (AWS) or node labels (Azure); prices from the AWS Pricing API (needs
`pricing:GetProducts`) and Azure Retail Prices, otherwise OpenCost's default rates (`estimatedNodes`).

| Method | Path | Returns |
|---|---|---|
| GET | `/api/costs?days=30` (or `from`/`to`, `cluster`) | `{from, to, total, idle, clusters[{cluster, instance, tenant, nodeCost, allocatedCost, idleCost, days, estimatedNodes, nodes, nodeTypes}], days[CostClusterDay], errors, priceErrors, lastRun, note}` |
| GET | `/api/costs/workloads?days=30&cluster=&namespace=&limit=200` | `{from, to, workloads[{cluster, namespace, kind, name, cpuCost, memCost, cpuRequestCoreHours, cpuUsageCoreHours, memRequestGiBHours, memUsageGiBHours, pods}]}` by cost |

| GET | `/api/costs/recommendations?days=30&cluster=&namespace=` | `{from, to, policy, recommendations[Recommendation]}`, largest monthly saving first |

Right-sizing (read-only). With each day's cost the worker stores, per workload container (largest replica), the day's
95th-percentile CPU, CPU and memory peaks, the current requests and limits, OOM kills and CFS throttling
(`CostContainerDay`). Recommendations follow Robusta KRR's simple policy: CPU request = the highest daily p95, no CPU
limit; memory request = limit = peak working set + 15% (raised to limit × 1.25 after an OOM kill, never lowered);
floors 10m / 100Mi. Confidence: high ≥ 14 days and ≥ 7 replica-days, medium ≥ 7 days, else low. `monthlySaving` =
request change × replica-hours × the workload's own cost per core-hour / GiB-hour, scaled to 30 days (negative = needs
more). Nothing is applied: Prophecy services are Helm-managed, so the UI offers the chart values to copy and a Copilot review.

```ts
type Resources = { cpuRequest: number; cpuLimit: number; memRequest: number; memLimit: number } // cores, bytes; 0 = unset
type Recommendation = {
  cluster: string; namespace: string; kind: string; name: string; container: string
  days: number; replicas: number; current: Resources; recommended: Resources
  cpuP95: number; cpuMax: number; memMax: number; oomDays: number; throttledPct: number
  confidence: "high" | "medium" | "low"; notes: string[]; monthlySaving: number
}
```

| GET | `/api/costs/changes?cluster=` | `{changes[{cluster, baselinePerDay, recentPerDay, deltaPerDay, pct, drivers[{kind, what, perDay}], summary, windows}], rule}` |

Cost changes: the last 3 complete days against the 14 before them, per cluster; flagged at ±25% and ±$20/day. Drivers
are the change in average node count and instance types, idle capacity, and the five workloads whose cost moved most
(new and removed included, with pod counts). Arithmetic only; "Ask Copilot why" sends it to the Copilot with
`change_timeline` for the cause.

| GET | `/api/costs/review` | `{batch: ReviewBatch \| null, reviews[{key, status, verdict, reason, cpuRequest, memRequest, memLimit, reviewedAt}]}` |
| POST | `/api/costs/review` | admins; `{limit?}` (default 50, max 200) → 202 with the batch; 409 while one runs; 412 without a Claude key |

Claude review of right-sizing: the fleet's recommendations with 7+ days of data that need more or save $10+/month,
shortfalls first, are sent as one Message Batch (half price), one request per container with a forced
`review_recommendation` tool: safe, adjust (with the values Claude would use) or risky, and why (JVM heaps, bursty work,
short history). A poller checks running batches every 2 minutes with the submitting admin's key (read sealed from the
database) and stores the verdicts; the previous verdict stays visible until the new one lands. Keys are
`cluster|namespace|kind|name|container`.

Copilot tool `query_costs(cluster?, namespace?, group_by: cluster|day|namespace|workload|rightsizing|changes, days)`.

## Copilot chats

```ts
type ChatSummary = { id: string; title: string; createdAt: string; updatedAt: string }
type Step = { id: string; label: string; ok: boolean | null; tool?: string; input?: string; summary?: string }
// tool / input / summary are present only for admins
type Action = {
  id: string
  kind: "retry_cp_postdeploy" | "retry_ep_postdeploy"
  title: string                 // button text, e.g. "Retry post-deploy"
  description: string           // why, in one sentence
  targetName: string
  status: "proposed" | "running" | "succeeded" | "failed" | "dismissed"
  result: string                // "" until executed
}
type ChatItem = {
  id: string
  role: "user" | "assistant"
  text: string                  // markdown
  steps: Step[]                 // tool calls made while producing this assistant turn
  actions: Action[]
  investigationId: string | null
  createdAt: string
  keySource: "personal" | "shared" | ""   // which Claude key produced an assistant reply
}
type Chat = { id: string; title: string; items: ChatItem[] }
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/chats` | — | `ChatSummary[]` (the caller's own, newest first) |
| POST | `/api/chats` | `{title?}` | `ChatSummary` |
| GET | `/api/chats/{id}` | — | `Chat` |
| POST | `/api/chats/{id}/messages` | `{text}` | **Server-Sent Events** stream (below) |

### Stream events (`POST /api/chats/{id}/messages`)

`Content-Type: text/event-stream`. Each event is `event: <name>\ndata: <json>\n\n`.
Use `fetch` + a `ReadableStream` reader (not `EventSource`, which cannot POST).

| event | data |
|---|---|
| `step` | `Step` with `ok: null` — a tool call started |
| `step_done` | `{id, ok, summary?}` |
| `text` | `{delta}` — append to the current assistant text |
| `action` | `Action` — render as a button under the reply |
| `investigation` | `{id, title}` — link to the investigation |
| `done` | `{itemId, keySource: "personal" \| "shared", usage: {inputTokens, outputTokens}}` |
| `error` | `{message}` — the turn ended; show inline |

## Actions

| Method | Path | Response |
|---|---|---|
| POST | `/api/actions/{id}/execute` | `Action` (status `succeeded` / `failed`, `result` filled) |
| POST | `/api/actions/{id}/dismiss` | `Action` |

## Investigations

```ts
type InvestigationSummary = {
  id: string; title: string; entityName: string; category: string
  requester: string; createdAt: string; chatId: string
}
type Evidence = { at: string; tool: string; input: string; summary: string; ok: boolean }
type Investigation = InvestigationSummary & {
  entityId: string
  summaryForRequester: string
  confidence: "high" | "medium" | "low"
  rootCause?: string            // admins only
  recommendedFix?: string       // admins only
  evidence?: Evidence[]         // admins only
  actions: Action[]
}
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/investigations` | `InvestigationSummary[]` — admins see all, others their own |
| GET | `/api/investigations/{id}` | `Investigation` |

## Learnings (admins)

Reusable failure patterns distilled from investigations. The Copilot calls `search_learnings(query, category?, entity?)`
early in an investigation (BM25 over title, symptoms, cause, checks and fix; **approved learnings only**) and, after
`record_investigation`, `record_learning`: either `learning_id + outcome` (`confirmed` adds a sighting; `contradicted`
leaves the text alone and sets `reviewNote` for an admin) or a new **draft** (secrets masked). Drafts are never searchable,
because the Copilot writes them from logs and pages that may hold planted text; an admin approves, edits or deletes them.

```ts
type Learning = {
  id: string; title: string; category: string; status: "draft" | "approved" | "retired"
  symptoms: string; rootCause: string; howToConfirm: string; fix: string; kgNodes: string
  entities: string[]; investigations: string[]; timesSeen: number; confirmed: number; contradicted: number
  reviewNote: string; createdBy: string; approvedBy: string; approvedAt: string | null
  lastSeen: string; createdAt: string; updatedAt: string
}
```

| Method | Path | Notes |
|---|---|---|
| GET | `/api/learnings?status=&q=` | admins; `q` ranks by relevance |
| POST | `/api/learnings` | admins; body: title, category, symptoms, rootCause, howToConfirm, fix, kgNodes, status? |
| GET/PUT | `/api/learnings/{id}` | admins; PUT edits fields, `status` approves/retires/restores, `clearReview` drops the note |
| DELETE | `/api/learnings/{id}` | admins; drafts only (409 otherwise — retire instead) |

Console admins are control-hub admins plus the emails in `CONSOLE_ADMIN_EMAILS` (comma separated).

## Guardrails & audit

```ts
type PolicyRow = { action: string; examples: string; mode: "Auto" | "Ask requester" | "Admin approval" | "Never" | "Not available yet" }
type AuditEntry = { at: string; actor: string; action: string; target: string; decision: string; detail: string }
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/policy` | `PolicyRow[]` |
| GET | `/api/audit?limit=100` | `AuditEntry[]` (admin only) |

## Weekly digest (admins)

| Method | Path | Notes |
|---|---|---|
| GET | `/api/digest` | `{digest{from, to, headline, sections[{title, lines, link}]}, slack, lastSent, schedule}` for the last 7 days |
| POST | `/api/digest/send` | posts it to Slack now → `{sent, message}`; `sent: false` when Slack is muted or unset |

Sections: findings (new critical/warnings, resolved, critical open over a week), certificates expiring within 21 days,
cost (week on week, the biggest cost changes, right-sizing potential and containers needing more), investigations,
draft learnings and new error patterns waiting for review. Built from the console's data, no AI. Posted weekly at
`DIGEST_WEEKDAY` (0 = Sunday, default 1) `DIGEST_HOUR_UTC` (default 4) unless `DIGEST_ENABLED=false`; the last send is
stored so restarts never repeat it. Links use `PUBLIC_URL`. Admin-only because it names every investigation.

## Upgrade readiness

`GET /api/readiness?cp=<control plane ID or name>` → `{controlPlane, verdict: go|caution|no-go|unknown, summary, checks[{id, name,
status: pass|warn|fail|unknown, detail, hint}], at}`. Twelve rule-based checks, run with the caller's access: control-plane
state, open findings, certificates (<7 days fail, <21 warn), nodes (not ready, pressure, ≥90% requested), Prophecy pods,
Helm releases not deployed, Postgres (version; 15+ "permission denied for schema public" in 24 h of logs fails), the fullest
volume (≥80% warn, ≥90% fail), Athena prophecy_status/update_info, OOM kills in 30 days, Helm changes in 24 h, and orphaned go-sandboxes (pods the orchestrator logged as
"failed to start: Unschedulable" that still exist). Any fail →
no-go, any warn → caution, more than half unknown → unknown. Also the Copilot's `upgrade_readiness(control_plane)`.

## Clusters (read-only Kubernetes explorer)

Clusters are control-hub's infrastructures. The list is fetched from control-hub **with the
caller's token**, so control-hub decides which clusters a user may see; every per-cluster route
first checks that the caller can read that infrastructure in control-hub. The console then reads
the cluster itself with control-hub's cloud credentials. Only get/list/logs are ever called.
Times are RFC 3339; the UI renders ages ("3d", "4m"). `ns` empty or omitted = all namespaces.

The Copilot reads the same clusters through read-only tools, with the same per-user check (the
caller's token lists `/api/infrastructures`; a cluster is matched by ID, control-hub name or Grafana
cluster name): `k8s_list_nodes`, `k8s_list_pods(namespace?, filter?, problems_only?, sort: name|cpu|memory|restarts)`,
`k8s_describe_pod(namespace, pod, include_manifest?)`, `k8s_pod_logs(namespace, pod, container?, previous?, tail?, since_minutes?, grep?)`,
`k8s_events(namespace?, warnings_only?, object?)`, `k8s_sandboxes()` (go-sandboxes and the orphans the orchestrator left after "Unschedulable") and `k8s_get(kind, namespace?, name?)` for any other kind.
`change_timeline(target, hours?)` (tenant, control plane, or cluster) merges, oldest first, the cluster's Helm
release revisions, Deployment rollouts (image diffs; rollout restarts labelled), StatefulSet/DaemonSet revisions and
new nodes with the console's own findings (appeared, resolved) and investigations for that tenant.
Results are capped (150 rows, 500 log lines, 24 KiB of YAML, 60 KiB in all); logs and event messages are
masked, Secret values never leave the cluster. Imported and SAP clusters return a "metrics only" error.

```ts
type ClusterSummary = {
  nodes: number; nodesReady: number
  pods: number; podsNotReady: number
  cpuRequestedPct: number; memRequestedPct: number      // requests ÷ allocatable, 0–100
  cpuDetail: string; memDetail: string                  // "14.9 / 24 cores", "77 / 96 GiB"
  metricsAvailable: boolean                             // metrics-server answered
}
type Cluster = {
  id: string                 // control-hub infrastructure id
  name: string               // cluster name
  provider: "aws" | "gcp" | "azure" | string
  region: string
  kubernetesVersion: string
  status: string             // control-hub infrastructure status
  tenants: string[]          // customer names of control planes on this cluster
  summary: ClusterSummary | null   // null when the cluster could not be reached
  error: string              // "" or why the cluster could not be reached
}
type Node = {
  name: string; ready: boolean; status: string          // "Ready" | "NotReady" | "SchedulingDisabled"
  pool: string; instanceType: string; version: string; createdAt: string
  cpuRequestedPct: number; memRequestedPct: number; pods: number
  cpuUsage: string; memUsage: string                    // "" when metrics-server is unavailable
  pressure: string[]                                    // ["MemoryPressure", …]
}
type Pod = {
  name: string; namespace: string
  status: string             // as kubectl shows it: Running, Pending, CrashLoopBackOff, ImagePullBackOff, OOMKilled, Completed…
  ready: string              // "1/1"
  restarts: number
  cpu: string; memory: string        // current usage from metrics-server, else ""
  cpuRequest: string; memRequest: string; memLimit: string
  node: string; createdAt: string
  owner: string              // "Deployment/metagraph", "StatefulSet/postgres", ""
  images: string[]
  issue: string              // short reason worth a pill: "OOMKilled 1h ago", "ImagePullBackOff", "Unschedulable: Insufficient memory", ""
  issueSeverity: "critical" | "warning" | ""
}
type Deployment = { name: string; namespace: string; ready: string; upToDate: number; available: number; images: string[]; createdAt: string; updatedAt: string; kind: "Deployment" | "StatefulSet" | "DaemonSet" }
type KEvent = { type: "Normal" | "Warning"; reason: string; object: string; message: string; count: number; lastSeen: string; namespace: string }
type Service = { name: string; namespace: string; type: string; clusterIP: string; external: string; ports: string }
type Container = {
  name: string; image: string; ready: boolean; restarts: number
  state: string              // "Running since …", "Waiting: CrashLoopBackOff", "Terminated: Completed"
  lastTerminated: string     // "OOMKilled (exit 137) 1h ago" or ""
  cpuRequest: string; memRequest: string; cpuLimit: string; memLimit: string
}
type PodDetail = Pod & {
  labels: Record<string, string>
  conditions: { type: string; status: string; reason: string; message: string }[]
  containers: Container[]
  events: KEvent[]
  manifest: string           // YAML; managedFields removed; env values with secret-looking names masked
}
type Logs = { container: string; lines: string[]; truncated: boolean }
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/clusters` | `Cluster[]` (summaries computed live, cached ~60 s) |
| GET | `/api/clusters/{id}` | `Cluster & { namespaces: string[] }` |
| GET | `/api/clusters/{id}/nodes` | `Node[]` |
| GET | `/api/clusters/{id}/pods?ns=` | `Pod[]` |
| GET | `/api/clusters/{id}/workloads?ns=` | `Deployment[]` (deployments, statefulsets, daemonsets) |
| GET | `/api/clusters/{id}/events?ns=` | `KEvent[]`, newest first, max 300 |
| GET | `/api/clusters/{id}/services?ns=` | `Service[]` |
| GET | `/api/clusters/{id}/pods/{ns}/{name}` | `PodDetail` |
| GET | `/api/clusters/{id}/pods/{ns}/{name}/logs?container=&sinceMinutes=360&tail=2000&previous=false` | `Logs` |

## Log parser (rules, not AI)

The parser matches log lines against a library of known messages (built-in rules mined from the
prophecy repo, plus admin changes). It never calls the Claude API.

```ts
type LogRule = {
  id: string; title: string
  services: string[]          // pod-name prefixes; [] = every pod
  pattern: string             // Go RE2 regexp
  severity: "error" | "warning" | "info"
  meaning: string; fix: string
  source: string              // file:line in the prophecy repo where the message is logged, or ""
  examples: string[]; not_examples: string[]   // always arrays
  builtin: boolean            // ships with the console (may be edited or disabled by an admin)
  enabled: boolean
  edited: boolean             // a built-in whose content an admin changed (DELETE resets it)
  hits24h: number             // matches recorded by scans in the last 24 h
}
type Hit = {
  ruleId: string; title: string; severity: "error" | "warning" | "info"
  meaning: string; fix: string
  count: number
  firstLine: number; lastLine: number      // indexes into the scanned lines
  sample: string
}
type ParserResult = { hits: Hit[]; matches: { line: number; rules: string[] }[] }
// Background parser results. counts cover the last 60 min; a rule not seen for 10 min is resolved (active=false).
type PodHit = { ruleId: string; title: string; severity: "error" | "warning" | "info"; meaning: string; fix: string
  count: number; total: number; firstSeen: string; lastSeen: string; active: boolean; sample: string }
type PodScan = { pod: string; errors: number; warnings: number; top: string; hits: PodHit[]; error?: string }
type NamespaceScan = { namespace: string; window: string; scannedAt: string; pending: boolean; error?: string; pods: PodScan[] }
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/log-rules` | — | `LogRule[]` sorted by id |
| POST | `/api/log-rules` | `LogRule` fields (no builtin/enabled/hits24h) | `LogRule` — admin only; 400 with the validation message if the pattern does not compile, an example does not match, etc.; 409 if the id exists |
| PUT | `/api/log-rules/{id}` | same | `LogRule` — admin only; editing a built-in stores an override |
| POST | `/api/log-rules/{id}/enable` · `/disable` | — | `LogRule` — admin only |
| DELETE | `/api/log-rules/{id}` | — | `{}` — admin only; removes a custom rule, or resets an edited/disabled built-in to its default |
| POST | `/api/log-rules/test` | `{line, pod?}` | `LogRule[]` that match (all rules when `pod` is empty, else only rules scoped to that pod) |
| GET | `/api/clusters/{id}/log-scan?ns=` | — | `NamespaceScan` — **stored results only, instant.** A single background worker parses every known cluster's new log lines every 60 s (namespaces from `LOG_SCAN_NAMESPACES`, default `prophecy`); `pending` is true until its first cycle for the cluster finished; `scannedAt` is that cycle's time. Viewing a page never starts a scan. |

The pod logs response gains the parser's result over the same lines:
`GET /api/clusters/{id}/pods/{ns}/{name}/logs` → `Logs & { parser: ParserResult }`.


### New error patterns and drafted rules (admins)

Error-level lines no rule matches are grouped into templates (`internal/patterns`, a small Drain: variable tokens
become `<*>`, lines whose other tokens mostly agree share a template), counted per cluster and hour. Clusters read
through Loki get a capped error sample every 30 minutes (the rule query only returns rule literals). Secrets and email
addresses are masked before anything is stored. Patterns nobody acted on are dropped 14 days after they stop.

| Method | Path | Notes |
|---|---|---|
| GET | `/api/log-patterns?status=new\|ignored\|ruled` | `[{id, service, template, example, count, clusters, firstSeen, lastSeen, last6h, last24h, isNew, status, ruleId}]`, new and active first |
| PUT | `/api/log-patterns/{id}` | `{status, ruleId?}` |
| POST | `/api/log-patterns/{id}/draft-rule` | Claude drafts a rule → `{applicable, reason?, rule}`; nothing is saved |
| POST | `/api/learnings/{id}/draft-rule` | the same from an approved learning's symptoms |

A draft is validated before it is returned: the pattern must compile as RE2, match every example and the real line it
came from, match no not-example, and not be a bare "error"; one retry gets the failures. The admin saves it through
`POST /api/log-rules`.

## Full cluster explorer (Lens-style, read-only)

Every resource type the cluster serves is browsable through two generic routes that use the
API server's own table output (the same columns `kubectl get` prints, custom resources included).
A resource is addressed by its **key** `<group|core>/<version>/<resource>` (e.g. `core/v1/pods`,
`apps/v1/deployments`, `cert-manager.io/v1/certificates`).

Never shown: Secret values (keys are listed with their size), ConfigMap values under
secret-looking keys or containing credentials, env values with secret-looking names, managedFields,
the last-applied annotation, Helm release values. There is no exec, port-forward, edit or delete.

```ts
type APIResource = {
  key: string; group: string; version: string; resource: string; kind: string
  namespaced: boolean
  category: "Cluster" | "Workloads" | "Config" | "Network" | "Storage" | "Access Control" | "Custom Resources" | "Other"
}
type ResourceTable = {
  columns: { name: string; type: string; priority: number }[]   // priority 0 = default; > 0 = "wide" only
  rows: { name: string; namespace: string; createdAt: string; cells: string[] }[]  // cells align with columns
}
type ResourceDetail = {
  kind: string; apiVersion: string; name: string; namespace: string; createdAt: string
  labels: Record<string, string>; annotations: Record<string, string>
  owners: { kind: string; name: string; key: string }[]   // key "" when not linkable
  selector: string            // label selector for workloads/services, "" otherwise
  pods: Pod[]                 // pods the workload/service selects ([] otherwise)
  events: KEvent[]
  yaml: string                // masked
}
// Note: `pods` here is the list of pods on the node (it replaces ClusterNode's pod count; use pods.length).
type NodeDetail = Omit<ClusterNode, "pods"> & {
  labels: Record<string, string>; taints: string[]; addresses: string[]
  conditions: { type: string; status: string; reason: string; message: string }[]
  capacity: Record<string, string>; allocatable: Record<string, string>
  os: string; kernel: string; runtime: string; providerId: string; unschedulable: boolean
  pods: Pod[]; events: KEvent[]
}
type HelmRelease = { name: string; namespace: string; chart: string; chartVersion: string; appVersion: string
  revision: number; status: string; updated: string; description: string }
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/clusters/{id}/api-resources` | `APIResource[]` (cached 10 min) |
| GET | `/api/clusters/{id}/resources/{group}/{version}/{resource}?ns=` | `ResourceTable` (max 2000 rows) |
| GET | `/api/clusters/{id}/resources/{group}/{version}/{resource}/{name}?ns=` | `ResourceDetail` (`ns` required for namespaced kinds) |
| GET | `/api/clusters/{id}/nodes/{name}` | `NodeDetail` |
| GET | `/api/clusters/{id}/helm-releases?ns=` | `HelmRelease[]` (latest revision of each) |

`GET /api/me` also returns `readOnly: boolean` and `controlHub: string` (e.g. "control-hub-prod");
when `readOnly` is true, Copilot actions are off and `POST /api/actions/{id}/execute` returns 403.

## Grafana: metrics parser, issues and charts

Each Grafana instance (AWS `monitoring.prophecy.io`, Azure `azure.monitoring.prophecy.io`, …) is
read with a Viewer service-account token. Instances come from the deployment secret
(`GRAFANA_<NAME>_URL` + `GRAFANA_<NAME>_TOKEN`) or are added by admins in Settings (token stored
encrypted). A control-hub cluster is matched to an instance's `cluster` label (metrics) and
`k8s_cluster_name` label (Loki) automatically; admins can override a mapping.

The **metrics parser** evaluates PromQL rules (`internal/metricrules/rules.yaml`) every 60 s, one
query per rule per instance; each returned series is an issue. Issues open, stay open while the
rule keeps returning them, and resolve when it stops (kept 24 h).

```ts
type GrafanaMapping = { instance: string; promCluster: string; lokiCluster: string; override: boolean }
// Cluster (from GET /api/clusters and /api/clusters/{id}) gains:  grafana: GrafanaMapping | null
type MetricIssue = {
  instance: string; cluster: string; ruleId: string; title: string
  severity: "error" | "warning" | "info"
  entity: "container" | "pod" | "service" | "deployment" | "pvc" | "node" | "cluster"
  entityKey: string; entityName: string; namespace: string; pod: string
  labels: Record<string, string>; value: number; valueText: string   // "95%", "6", "1.2 s", "9.9/s", ""
  meaning: string; fix: string
  firstSeen: string; lastSeen: string; active: boolean; resolvedAt?: string
}
type ClusterIssues = { grafana: GrafanaMapping | null; evaluatedAt: string | null; metrics: MetricIssue[] }
type FleetIssue = {
  source: "metrics" | "logs"; clusterId: string; clusterName: string; ruleId: string; title: string
  severity: "error" | "warning" | "info"; entity: string; entityName: string; namespace: string; pod: string
  valueText: string; meaning: string; fix: string; firstSeen: string; lastSeen: string; active: boolean
}
type MetricRule = { id: string; title: string; severity: string; entity: string; value: string; expr: string; meaning: string; fix: string }
type ChartSeries = { name: string; unit: "cores" | "bytes" | "count"; labels: Record<string, string>; points: [number, number][] } // [unix s, value]
type ClusterMetrics = { available: boolean; instance?: string; cluster?: string; from?: string; to?: string; stepSeconds?: number; series: ChartSeries[] }
type GrafanaInstance = { name: string; url: string; source: "secret" | "settings"; clusters: string[]; logClusters: number
  checkedAt: string; error: string; evaluatedAt: string | null; ruleErrors: string[] }
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/issues` | — | `FleetIssue[]` — active metric issues + active log-parser hits on every cluster the caller can see |
| GET | `/api/clusters/{id}/issues` | — | `ClusterIssues` (active first, then resolved in the last 24 h) |
| GET | `/api/clusters/{id}/metrics?panel=cluster\|pod\|node&ns=&pod=&node=&range=6h` | — | `ClusterMetrics`. Panels: cluster = CPU used/allocatable, memory used/requested/allocatable; pod = CPU and memory per container, memory limit, restarts, JVM heap used/max; node = CPU, memory, allocatable. `range` is a Go duration (max 30 d). `available:false` when the cluster has no Grafana mapping |
| GET | `/api/metric-rules` | — | `MetricRule[]` |
| GET | `/api/grafana` | — | `{instances: GrafanaInstance[], overrides: {cluster, instance, promCluster, lokiCluster, updatedBy, updatedAt}[]}` |
| POST | `/api/grafana/instances` | `{name, url, token}` | `{}` — admin; the token is checked against Grafana first |
| DELETE | `/api/grafana/instances/{name}` | — | `{}` — admin; only Settings-added instances |
| POST | `/api/grafana/refresh` | — | same as GET `/api/grafana`, after reloading cluster names |
| PUT | `/api/grafana/mappings/{cluster}` | `{instance, promCluster, lokiCluster}` | `GrafanaMapping` — admin override for a control-hub cluster name |
| DELETE | `/api/grafana/mappings/{cluster}` | — | the automatic mapping again |


Copilot discovery tools (read-only, same cluster → Grafana mapping as `grafana_query_metrics`):
`grafana_list_metrics(cluster, search?)` (metric names seen in the last hour), `grafana_label_values(cluster, metric?, label?, search?)`
(label names of a metric, or a label's values), `grafana_alerts(cluster, state?, search?)` (Prometheus `ALERTS` deduplicated
across replicas plus Grafana-managed rule instances labelled with the cluster; firing first, then by severity) and
`grafana_dashboards(cluster, search? | uid, panel?)` (dashboard search, or one dashboard's variables and panel queries).
They are also served by `control-hub-mcp` when Grafana is configured.

## Athena (Copilot tool)

`athena_read(control_plane, view, config_type?, metrics_scope?)` reads a control plane's Athena at
`<controlPlaneUrl>/athena/api/v1` with `ATHENA_AUTH_TOKEN` (the credential control-hub uses). The control plane is
resolved through control-hub's control-plane lists **with the caller's token** (by ID, exact customer/tenant/cluster
name or hostname, then a unique partial match; deleted planes are skipped), so users only reach Athena for planes they
can see. `internal/athena` sends GETs only, and only to these views: cluster, update_info, available_versions,
maintenance, insights, prophecy_status, service_status, platform_report, alerts, tasks, sandboxes, features,
feature_config, metering, credits. Athena's restore, backup, sandbox-delete and feature-update endpoints are
unreachable. Results are capped at 40 KiB, secret-named keys are redacted and email addresses masked (`j***@acme.com`).
Unset `ATHENA_AUTH_TOKEN` disables the tool. Also served by `control-hub-mcp` when the variable is set.

## Copilot in Slack

`POST /api/slack/events` — Slack Events API endpoint, authenticated by the request signature
(`SLACK_SIGNING_SECRET`), not a session. Handles `url_verification`, `app_mention` and direct messages
(`message.im`). The asker is matched by Slack email to a console user with an unexpired session; the turn
runs with that session's control-hub token and the person's Claude key. Channel answers use the non-admin
view; DMs use the person's own. Each Slack thread and person is one Copilot chat (linked at the end of the
reply). Proposed actions are described, not executable, in Slack. Off unless `SLACK_BOT_TOKEN` and
`SLACK_SIGNING_SECRET` are set; the app manifest is `deploy/slack-app-manifest.yaml`.

## Learnings search by meaning

`GET /api/learnings/expand?q=` (admins) → `{alternatives: [...]}`: up to 5 rewordings from Claude Haiku with
the caller's key. Pass them back as repeated `alt` parameters to `GET /api/learnings?q=…&alt=…&alt=…`; the
rankings of all queries are merged (reciprocal rank fusion, the original query weighted double). The Copilot's
`search_learnings` takes `alternatives` itself, at no extra cost.

## Dry-run actions

`propose_action` kinds: `retry_cp_postdeploy`, `retry_ep_postdeploy`, `restart_pod` (cluster, namespace, pod;
refused without a controller or for Jobs), `delete_orphaned_sandboxes` (cluster). Each is dry-run against the
live state when proposed; the plan (`summary`, `steps`, `checks`, `risks`, `targets`) is returned on the action
as `plan`. A dry run that finds the action unsafe proposes nothing. `POST /api/actions/{id}/execute` refuses
with 403 while `READ_ONLY=true`, and with 409 when the dry run computed at click time names different targets.

## Audit (admins)

`GET /api/audit/changes?scope=&cluster=&kind=&namespace=&name=&actor=&since=&until=&service=&include_system=` — who
changed what. GKE clusters in `AUDIT_GCP_PROJECTS` and GCP resources come from Cloud Audit Logs (Admin Activity);
other clusters return `fieldOwners` (managedFields: tool and time, not who). Same as the Copilot's admin-only
`audit_search`.
