# Environment variables

The console is configured only through environment variables. Most are read in `internal/config/config.go`; cloud,
Grafana and a few others are read where they are used (noted below). All are read **once at start-up**: restart the
pod after changing one.

**Parsing rules**

- An empty value counts as unset and gets the default, except where noted.
- Booleans accept `1`, `t`, `true`, `TRUE`, `0`, `f`, `false`, … (Go's `strconv.ParseBool`). An invalid boolean or
  integer silently falls back to the default.
- Durations use Go syntax (`90s`, `5m`, `6h`). An invalid duration stops the console at start-up.
- Comma-separated lists marked "trimmed" ignore spaces and empty entries; the others are split on commas as is.

## Core

| Variable | Default | Meaning |
|---|---|---|
| `ENCRYPTION_KEY` | **required** | Base64 of exactly 32 random bytes (`openssl rand -base64 32`). Encrypts session tokens, personal Claude keys and Grafana tokens saved in Settings with AES-256-GCM. Changing it makes all of them unreadable. |
| `DATABASE_URL` | `file:ai-console.db` | `postgres://…` or `postgresql://…` opens Postgres; anything else is a SQLite file. The current deployment uses `file:/data/ai-console.db`. |
| `DATA_DIR` | `./data` | Root for the workers' files (log hits and state, metric state, certificate inventory, price cache). Put it on the persistent volume. |
| `LISTEN_ADDR` | `:8090` | HTTP listen address. |
| `STATIC_DIR` | `./web/dist` | The built UI. The image sets `/app/web`. |
| `DOCS_DIR` | `./site` | The built documentation site, served at `/docs/` to signed-in users. The image sets `/app/docs`. See [Working on these docs](../development/docs.md). |
| `KG_DIR` | `./knowledgegraph` | The control-hub knowledge graph (needs `INDEX.md`). The image sets `/app/knowledgegraph`. Without it the Copilot runs without `read_knowledge_node`. |
| `COOKIE_SECURE` | `true` | Sets `Secure` on the session cookie. `false` only for local http. |
| `PUBLIC_URL` | `https://controlhub.app.prophecy.ai` | The console's address, for links in Slack. |

## control-hub

| Variable | Default | Meaning |
|---|---|---|
| `CONTROLHUB_URL` | `http://control-hub:8003` | control-hub's base URL, without `/api`. A trailing slash is removed. |
| `CONTROLHUB_LABEL` | `control-hub` | Names the connected control-hub in the UI header and read-only banner, e.g. `control-hub-prod`. |
| `READ_ONLY` | `false` | Turns off every write the console can make (Copilot actions). **Required** when `CONTROLHUB_URL` is a production control-hub. Certificate repair (`CERT_REPAIR_ENABLED`) is still allowed. |
| `CONTROLHUB_SERVICE_TOKEN` | — | control-hub admin token the monitors, certificate inventory and cost tenant mapping use to list every realm. **Required when `MONITOR_ENABLED=true`.** Read-only: the client refuses any non-GET request made with it. |
| `CONTROLHUB_DEV_SERVICE_TOKEN` | — | The same for control-hub dev; also read-only. |
| `REALMS` | `sap,dedicated-saas,public-saas` | Realms the monitors and certificate inventory list. Not trimmed. |
| `CONSOLE_ADMIN_EMAILS` | — | Console admins in addition to control-hub admins. Comma-separated, trimmed, case-insensitive; checked on every request. |

## control-hub log search

| Variable | Default | Meaning |
|---|---|---|
| `POD_NAMESPACE` | — | The console's own namespace, normally from the downward API. Default for `CONTROLHUB_LOGS_NAMESPACE`. |
| `CONTROLHUB_LOGS_NAMESPACE` | value of `POD_NAMESPACE` | Where control-hub's pods run, for `search_controlhub_logs`. **Set but empty** turns log search off (for example when `CONTROLHUB_URL` is a control-hub in a namespace the console cannot read). Needs pods and `pods/log` read access there. |
| `CONTROLHUB_POD_SELECTOR` | `app=control-hub` | Label selector for control-hub's pods. |

Log search also needs the pod to run in a cluster (it uses `KUBERNETES_SERVICE_HOST` and `KUBERNETES_SERVICE_PORT`,
which Kubernetes sets).

## Claude

| Variable | Default | Meaning |
|---|---|---|
| `ANTHROPIC_API_KEY` | — | Shared team key for people without a personal key. Empty = personal keys only. |
| `CLAUDE_MODEL` | `claude-opus-5-5` | Model for Copilot chats, log rule drafts and right-sizing reviews. |
| `CLAUDE_EFFORT` | `high` | Reasoning effort for Copilot chats. |

## Monitors and Slack

| Variable | Default | Meaning |
|---|---|---|
| `MONITOR_ENABLED` | `true` | Run the monitors. |
| `MONITOR_INTERVAL` | `5m` | How often they run. |
| `STUCK_AFTER` | `45m` | How long a control plane may stay `CREATING`, `DELETING` or `UPDATING` before it is "stuck". |
| `CERT_WARN_DAYS` | `5` | The `cert_expiry` monitor opens a finding when a served certificate expires in fewer days than this. |
| `SLACK_WEBHOOK_URL` | — | Slack incoming webhook for new findings and the digest. |
| `SLACK_MUTE` | `false` | `true` posts nothing to Slack through the webhook (findings, digest). Set it on every non-team environment. It does not affect the Copilot in Slack. |

## Copilot in Slack

| Variable | Default | Meaning |
|---|---|---|
| `SLACK_BOT_TOKEN` | — | The Slack app's Bot User OAuth Token (`xoxb-…`). |
| `SLACK_SIGNING_SECRET` | — | The Slack app's Signing Secret, used to verify every request to `POST /api/slack/events`. |

Both are needed; with either missing the Copilot in Slack is off and the endpoint returns `404`. See
[Slack app setup](../operations/slack.md).

## Weekly digest

| Variable | Default | Meaning |
|---|---|---|
| `DIGEST_ENABLED` | `true` | Post the digest weekly. |
| `DIGEST_WEEKDAY` | `1` | Day of the week, 0 = Sunday. |
| `DIGEST_HOUR_UTC` | `4` | Hour in UTC (04:00 UTC is 09:30 IST). |

## Log parser

| Variable | Default | Meaning |
|---|---|---|
| `LOG_SCAN_INTERVAL` | `60s` | How often the log parser scans every tracked cluster. Also the metric rules' evaluation interval. |
| `LOG_SCAN_NAMESPACES` | `prophecy` | Namespaces read through the API server, and the namespaces (with every `prophecy*` one) whose errors feed the pattern miner. Not trimmed. |
| `HIT_RETENTION_DAYS` | `30` | Days of `DATA_DIR/hits/` files kept. |
| `PATTERN_IGNORE_SERVICES` | `sla-tests,e2e-tests` | Services (pod names without their generated suffix) whose errors are not mined into error patterns; test runners log expected failures as ERROR. Trimmed. |

## Certificates

| Variable | Default | Meaning |
|---|---|---|
| `CERT_SCAN_INTERVAL` | `6h` | Certificate inventory rescan interval. Needs `CONTROLHUB_SERVICE_TOKEN` for the tenant list. |
| `CERT_SECRET_NAMESPACES` | `control-hub-prod` | Namespaces whose `kubernetes.io/tls` secrets are tracked as wildcards (only `tls.crt` is parsed). Trimmed. Needs read access to secrets there. |
| `CERT_ACM_REGIONS` | `us-east-1,us-east-2,us-west-1,us-west-2` | ACM regions read with `AWS_PROD_*`. Trimmed. control-hub's AWS user may only read ACM in the US regions. |
| `AUDIT_GCP_PROJECTS` | `prophecy-cloud` | Projects whose Cloud Audit Logs `audit_search` reads (GKE Kubernetes audit and GCP resources). Empty turns it off. Needs `roles/logging.viewer`. See [Audit](../concepts/audit.md). |
| `CERT_GCP_PROJECT_FILTER` | `id:sap-*` | Resource Manager filter for projects whose Certificate Manager certificates are read. **Set but empty** disables GCP certificates. |
| `CERT_REPAIR_ENABLED` | `false` | Lets admins ask control-hub to recreate a certificate's renewal DNS records or renew the wildcard. The one write allowed with `READ_ONLY`; runs with the admin's own token. Needs control-hub with `POST /api/certificates/repair`. |

## Cost

| Variable | Default | Meaning |
|---|---|---|
| `COST_ENABLED` | `true` | Run the cost collector and serve cost data and `query_costs`. |
| `COST_BACKFILL_DAYS` | `30` | Past days to backfill. 0 or less means 30. |
| `COST_RETENTION_DAYS` | `400` | Days of cost rows kept. 0 or less means 400. |

## Athena

| Variable | Default | Meaning |
|---|---|---|
| `ATHENA_AUTH_TOKEN` | — | The `Authorization` value for control planes' Athena API (as control-hub's `ATHENA_AUTH_TOKEN`). Enables `athena_read` and the readiness check's Athena reads. GETs only, to an allow-list of read views. |

## Cloud credentials

Read in `internal/kube/connect.go`, `internal/certs` and `internal/cost`. Use the same values as control-hub.

| Variable | Used for |
|---|---|
| `AWS_PROD_ACCESS_KEY_ID`, `AWS_PROD_SECRET_ACCESS_KEY` | EKS access (presigned STS token), ACM certificates, AWS list prices (needs `pricing:GetProducts`). |
| `AZURE_PROD_TENANT_ID`, `AZURE_PROD_CLIENT_ID`, `AZURE_PROD_CLIENT_SECRET`, `AZURE_PROD_SUBSCRIPTION_ID` | AKS access (the admin kubeconfig is fetched and kept in memory only). |

GKE and GCP Certificate Manager use Google Application Default Credentials: the pod's Workload Identity in the cluster,
or `gcloud auth application-default login` locally. There is no variable.

## Grafana

Read in `internal/grafana/hub.go`.

| Variable | Meaning |
|---|---|
| `GRAFANA_<NAME>_URL` | URL of a Grafana instance named `<name>` (lower-cased). One pair per instance, e.g. `GRAFANA_AWS_URL`, `GRAFANA_AZURE_URL`. |
| `GRAFANA_<NAME>_TOKEN` | Viewer service-account token for that instance. An instance with a URL and no token is skipped. |
| `GRAFANA_MAPPINGS` | Overrides for clusters whose control-hub name differs from their Grafana labels: `<control-hub cluster>=<instance>:<metrics cluster>[:<loki cluster>]`, comma-separated. Mappings saved in Settings win. |

The instance name also tells the cost collector which cloud the instance's clusters run on: instances are named after
their cloud (`aws`, `azure`, …).

Example:

```bash
GRAFANA_MAPPINGS='my-cluster-aks=azure:prophecy-my-metrics:prophecy-my-metrics-aks'
```

## MCP server (`control-hub-mcp`)

| Variable | Default | Meaning |
|---|---|---|
| `CONTROLHUB_TOKEN` | **required** | Your control-hub token; every call runs with it. |
| `CONTROLHUB_URL` | `http://localhost:8083` | control-hub's base URL. |
| `KG_DIR` | — | Knowledge graph directory; enables `read_knowledge_node`. |
| `ATHENA_AUTH_TOKEN` | — | Enables `athena_read`. |
| `GRAFANA_<NAME>_URL`, `GRAFANA_<NAME>_TOKEN` | — | Enable the Grafana tools. |

## Evaluation runner (`copilot-eval`)

| Variable | Meaning |
|---|---|
| `ANTHROPIC_API_KEY` | **Required.** Every run calls Claude. |

The runner's other settings are flags; see [Tests and evals](../development/testing.md#copilot-evaluations).

## Set in the current deployment

For reference, the current deployment sets these in the Deployment (values that are not secrets):
`CONTROLHUB_URL`, `CONTROLHUB_LABEL`, `READ_ONLY=true`, `CONTROLHUB_LOGS_NAMESPACE` (empty: log search off),
`POD_NAMESPACE`, `DATABASE_URL=file:/data/ai-console.db`, `DATA_DIR=/data`, `LOG_SCAN_INTERVAL=60s`,
`LOG_SCAN_NAMESPACES=prophecy`, `HIT_RETENTION_DAYS=30`, `MONITOR_ENABLED=true`, `SLACK_MUTE=true`,
`COOKIE_SECURE=true`, `GRAFANA_MAPPINGS` and `CONSOLE_ADMIN_EMAILS`. Everything else comes from `ai-console-secrets`
or the defaults.
