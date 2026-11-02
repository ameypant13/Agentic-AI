# Cloud Platform AI Console

A web console for the cloud and SRE teams. It sits next to the cloud platform, finds problems before
anyone reports them, and has a Copilot (Claude) that investigates them through the cloud platform API.

It is its own Go module with its own image. It never touches the cloud platform database: every
read and every action goes through the cloud platform REST API **with the signed-in user's
cloud platform token**, so the platform's permission checks apply unchanged.

## What phase 1 does

- **Fleet overview:** open findings and control plane counts.
- **Monitors** (every 5 min). Each one opens a finding and posts to Slack:
  - failed provisioning (`*_FAILED`)
  - provisioning stuck in `CREATING`/`DELETING`/`UPDATING` longer than `STUCK_AFTER`
  - failed post-deploy (control and execution planes)
  - a failed SAP execution plane
  - a TLS certificate served by an operational control plane that expires within `CERT_WARN_DAYS`

  Findings resolve themselves when the condition clears. The first run after a fresh deploy
  records what is already broken without posting to Slack.
- **Copilot**, using these read-only tools:
  - find/list/get control planes, execution planes, infrastructure and jobs
  - search the cloud platform's own pod logs
  - DNS lookup, TLS certificate check and HTTPS probe (Prophecy hostnames only)
  - read the cloud platform knowledge graph, which is shipped in the image
- **Retry is the only write.** The Copilot can *propose* re-running SAP control plane or
  execution plane post-deploy. It runs only when the requester clicks the button, with their token.
- **Investigations:** the Copilot records each diagnosis. Admins see the root cause, the
  recommended fix and every tool call as evidence. Non-admins see a plain summary and a reference
  ID, never the internal cause.
- **Claude keys:** each person can store their own Claude API key in Settings (encrypted at rest)
  so their chats bill to them. Without one, chats use the shared `ANTHROPIC_API_KEY`, if set. Keys
  must come from console.anthropic.com; claude.ai Pro/Max subscriptions can't be used by an app
  like this.
- **Clusters (phase 2, step 1):** a read-only, Lens-style explorer over every cluster the cloud platform
  manages: cluster list with capacity, nodes, pods (kubectl-style status and an issue per pod:
  OOMKilled, ImagePullBackOff, unschedulable, restarts), workloads, events, services, and a pod page
  with containers, logs, manifest (secret-looking env values masked) and events.
  The cluster list comes from the cloud platform with the caller's token; each cluster is then read with
  the platform's own cloud credentials: GKE via the pod's Google identity (the console runs as
  the platform's service account), EKS via a presigned STS token from `AWS_PROD_*`, AKS via the
  admin kubeconfig fetched with `AZURE_PROD_*`. Only get, list and logs are called.
- **Log parser (phase 2, step 2):** rules, not AI. `internal/logrules/rules.yaml` ships 93 rules
  mined from the prophecy repo (pattern, severity, meaning, fix, the source line that logs it, and
  examples that `go test` checks); the format is in `internal/logrules/FORMAT.md`. Admins add, edit,
  disable or reset rules on the Log rules page (stored in the console DB over the defaults).
  A single background worker parses every known cluster's *new* log lines every 60 s
  (`LOG_SCAN_INTERVAL`, namespaces in `LOG_SCAN_NAMESPACES`), also reading the crashed run of
  containers that restarted. Matches go to append-only files `DATA_DIR/hits/<cluster>/<day>.jsonl`
  (kept `HIT_RETENTION_DAYS`), and a per-cluster summary (`DATA_DIR/state/<cluster>.json`) is what
  pages read, so viewing never starts a scan. The pod page tags matching lines. A literal prefilter keeps scans cheap
  (~16 ms per 3,000 lines). With monitors on, workloads logging error-level hits become findings.
- **Audit log** of sign-ins, key changes, investigations, and proposed and executed actions.

Not in phase 1: version-drift detection (needs data from inside the tenant cluster), creating
clusters, any fix other than retrying post-deploy, Google SSO redirect (SSO users paste their
cloud platform token), and automatic investigation of new findings.

## Layout

```
cmd/ai-console        the web server + monitors
cmd/control-hub-mcp   the same read-only tools as an MCP server for Claude Code, plus repair_certificate and renew_wildcard_certificate (admin token; dry run first)
internal/agent        Claude tool loop, prompts, record_investigation / propose_action, action execution
internal/tools        read-only tools shared by the Copilot and the MCP server
internal/monitor      scheduled checks → findings → Slack
internal/controlhub   cloud platform HTTP client + secret redaction
internal/server       HTTP API (see API.md), sessions, SPA serving
internal/store        the console's own tables (gorm on SQLite; /data/ai-console.db on the PVC in cluster)
web/                  React + Vite + Tailwind UI
deploy/k8s            Deployment, Service, Ingress, RBAC
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `ENCRYPTION_KEY` | — **required** | `openssl rand -base64 32`. Encrypts stored Claude keys and session tokens. |
| `DATABASE_URL` | `file:ai-console.db` | SQLite path; `file:/data/ai-console.db` in cluster (a `postgres://…` URL also works). |
| `CONTROLHUB_URL` | `http://control-hub:8003` | |
| `CONTROLHUB_SERVICE_TOKEN` | — required when monitors are on | Cloud platform admin token the monitors use to list every realm. Read-only: the client refuses any non-GET request made with it. |
| `CONTROLHUB_DEV_SERVICE_TOKEN` | — | Same for cloud platform dev; also read-only. |
| `ANTHROPIC_API_KEY` | — | Shared team key for people without a personal key. Empty = personal keys only. |
| `CLAUDE_MODEL` / `CLAUDE_EFFORT` | `claude-opus-5-5` / `high` | |
| `MONITOR_ENABLED` / `MONITOR_INTERVAL` | `true` / `5m` | |
| `STUCK_AFTER` / `CERT_WARN_DAYS` | `45m` / `5` | |
| `SLACK_WEBHOOK_URL` / `SLACK_MUTE` | — / `false` | Set `SLACK_MUTE=true` on dev. |
| `SLACK_BOT_TOKEN` / `SLACK_SIGNING_SECRET` | — | Turn on the Copilot in Slack (`POST /api/slack/events`); create the app from `deploy/slack-app-manifest.yaml`. |
| `AWS_PROD_ACCESS_KEY_ID` / `AWS_PROD_SECRET_ACCESS_KEY` | — | EKS access; same values as the cloud platform. |
| `AZURE_PROD_TENANT_ID` / `_CLIENT_ID` / `_CLIENT_SECRET` / `_SUBSCRIPTION_ID` | — | AKS access; same values as the cloud platform. |
| `POD_NAMESPACE` / `CONTROLHUB_POD_SELECTOR` | — / `app=control-hub` | Enables log search; set from the downward API in cluster. |
| `KG_DIR` / `STATIC_DIR` / `LISTEN_ADDR` | image paths / `:8090` | |
| `COOKIE_SECURE` | `true` | `false` only for local http. |
| `PATTERN_IGNORE_SERVICES` | `sla-tests,e2e-tests` | Workloads left out of error-pattern mining (their errors are expected). Patterns are only mined from `prophecy*` namespaces and `LOG_SCAN_NAMESPACES`. |
| `DATA_DIR` / `LOG_SCAN_INTERVAL` / `LOG_SCAN_NAMESPACES` / `HIT_RETENTION_DAYS` | `./data` / `60s` / `prophecy` / `30` | Background log parser (put `DATA_DIR` on the PVC). |
| `CERT_SCAN_INTERVAL` | `6h` | Certificate inventory rescan interval. Needs `CONTROLHUB_SERVICE_TOKEN` for the tenant list. |
| `CERT_SECRET_NAMESPACES` | `control-hub-prod` | Namespaces whose `kubernetes.io/tls` secrets are tracked as wildcards (only `tls.crt` is parsed). Needs read access to secrets there. |
| `CERT_ACM_REGIONS` | US regions | ACM regions read with `AWS_PROD_*`. |
| `COST_ENABLED` / `COST_BACKFILL_DAYS` / `COST_RETENTION_DAYS` | `true` / `30` / `400` | Estimated workload cost from Grafana metrics (see API.md → Costs). AWS list prices need `pricing:GetProducts` on the cloud platform AWS user; without it nodes use OpenCost's default rates. |
| `GRAFANA_MAPPINGS` | — | Cluster → Grafana overrides for clusters whose cloud platform name differs from their Grafana labels: `<cluster>=<instance>:<metrics cluster>[:<loki cluster>]`, comma-separated. Mappings saved in Settings win. |
| `CERT_REPAIR_ENABLED` | `false` | Lets admins ask the cloud platform to recreate a certificate's renewal DNS records (the one write allowed with `READ_ONLY`; runs with the admin's own token). Needs the platform with `POST /api/certificates/repair`. |
| `AUDIT_GCP_PROJECTS` | `prophecy-cloud` | Projects whose Cloud Audit Logs the admin-only `audit_search` reads; empty disables. |
| `CERT_GCP_PROJECT_FILTER` | `id:sap-*` | Resource Manager filter for projects whose Certificate Manager certificates are read; empty disables. |
| `READ_ONLY` / `CONTROLHUB_LABEL` / `CONTROLHUB_LOGS_NAMESPACE` | `false` / `control-hub` / `POD_NAMESPACE` | Read-only turns off all writes; required against a prod cloud platform. |

## Run locally

```bash
# terminal 1: control-hub (dev) on localhost:8003
kubectl -n control-hub-dev port-forward svc/control-hub 8003:8003

# terminal 2: the console API on :8090 (SQLite, monitors off, no log search)
ENCRYPTION_KEY=$(openssl rand -base64 32) CONTROLHUB_URL=http://localhost:8003 \
MONITOR_ENABLED=false ANTHROPIC_API_KEY=... make run

# terminal 3: the UI with hot reload on :5173 (proxies /api to :8090)
cd web && npm install && npm run dev
```

Or build the UI once (`make web`) and open http://localhost:8090.

## Test

```bash
make build test            # Go: vet + unit and integration tests (fake cloud platform and Claude API)
cd web && npm run build    # UI: type-check + bundle (tsc --noEmit alone is not a check here)
```

## Deploy

Dev runs at **https://example.com** in its own namespace, `ai-console`, on the
management cluster (configure ingress, TLS and DNS for your environment). It reads **prod** cloud platform read-only
(`READ_ONLY=true`, `CONTROLHUB_URL=http://control-hub.control-hub-prod.svc.cluster.local:8003`).
Data (SQLite DB, parser and rule state) lives on PVC `ai-console-data` at `/data`.

1. Create the namespace and service account. The service account needs Workload Identity
   (`iam.gke.io/gcp-service-account: control-hub-admin@prophecy-cloud.iam.gserviceaccount.com`,
   plus `roles/iam.workloadIdentityUser` for `prophecy-cloud.svc.id.goog[ai-console/ai-console]`)
   and the `ai-console-cluster-read` ClusterRoleBinding.
2. Create the secret `ai-console-secrets` in `ai-console` (`ENCRYPTION_KEY`, `CONTROLHUB_SERVICE_TOKEN`,
   cloud and Grafana credentials, optional `ANTHROPIC_API_KEY`, `SLACK_WEBHOOK_URL`, `ATHENA_AUTH_TOKEN`).
   Keep `ENCRYPTION_KEY` stable: changing it makes stored keys and sessions unreadable.
3. Copy `wildcard-app-prophecy-ai-tls-secret` into `ai-console`. The copy does not renew with the
   original: copy it again whenever the wildcard is renewed (current one expires 2026-12-24).
4. Build and push: `make push TAG=<tag>` (copies the knowledge graph into the image first), then
   `kubectl -n ai-console rollout restart deploy/ai-console`.

## Use the tools from Claude Code (MCP)

```bash
make mcp
claude mcp add control-hub -- env CONTROLHUB_URL=http://localhost:8003 \
  CONTROLHUB_TOKEN=<your cloud platform token> KG_DIR=<repo>/.knowledgegraph \
  $(pwd)/bin/control-hub-mcp
```

The MCP server is read-only and has no log search (that needs the in-cluster service account).
