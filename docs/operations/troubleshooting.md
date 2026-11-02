# Troubleshooting

Start with the pod's log. The console logs what it turned off at start-up and every background error.

```bash
kubectl -n ai-console logs deploy/ai-console --tail=200
kubectl -n ai-console describe pod -l app=ai-console
```

## The pod does not start

| Log line | Cause and fix |
|---|---|
| `config: ENCRYPTION_KEY is required …` | Add `ENCRYPTION_KEY` to `ai-console-secrets`. |
| `config: ENCRYPTION_KEY must be base64 of exactly 32 bytes` | Generate it with `openssl rand -base64 32`. Keep the old value if you had one. |
| `config: CONTROLHUB_SERVICE_TOKEN is required when MONITOR_ENABLED=true` | Add the token, or set `MONITOR_ENABLED=false`. |
| `config: invalid MONITOR_INTERVAL …` (or `STUCK_AFTER`, `LOG_SCAN_INTERVAL`, `CERT_SCAN_INTERVAL`) | Use a Go duration such as `5m`, `45m`, `60s`, `6h`. |
| `store: open database …` / `migrate: …` | The database path is not writable or the volume is not mounted. Check `DATABASE_URL`, the volume mount at `/data` and `fsGroup: 65532`. |
| `log worker: …` / `metric rules: …` / `certificates: …` | The worker could not create its directories under `DATA_DIR`. `DATA_DIR` must be on the writable volume; the root filesystem is read-only. |

## Google credentials (gcloud auth)

GKE clusters and GCP Certificate Manager are read with Google Application Default Credentials.

**In the cluster** the credentials come from Workload Identity. Symptoms of a broken setup: GKE clusters show
"Unreachable" with `google credentials: …` or `get GKE cluster: … 403`, and the certificate inventory lists GCP
errors.

1. Check the service account annotation:

    ```bash
    kubectl -n ai-console get sa ai-console -o jsonpath='{.metadata.annotations}'
    ```

    It must name `control-hub-admin@prophecy-cloud.iam.gserviceaccount.com`.

2. Check the IAM binding includes `serviceAccount:prophecy-cloud.svc.id.goog[ai-console/ai-console]` with
   `roles/iam.workloadIdentityUser`:

    ```bash
    gcloud iam service-accounts get-iam-policy \
      control-hub-admin@prophecy-cloud.iam.gserviceaccount.com --project prophecy-cloud
    ```

3. After moving namespaces, the member must name the **new** namespace.

**Locally** run:

```bash
gcloud auth application-default login
```

so the console can read GKE clusters with your own Google account.

**For kubectl** against GKE from your machine you need the `gke-gcloud-auth-plugin`, which ships in the Google Cloud
SDK's `bin` directory. If kubectl says it cannot find the plugin, put that directory on your `PATH`:

```bash
export PATH="$(dirname "$(command -v gcloud)"):$PATH"
```

## Other cloud credentials

| Symptom | Cause |
|---|---|
| EKS clusters "Unreachable: control-hub's AWS credentials were refused for this cluster." | `AWS_PROD_*` missing or wrong, or the AWS user is not mapped in the cluster. |
| AKS clusters unreachable with an authorization error | `AZURE_PROD_*` missing or wrong. |
| "Cluster not found in AWS/GCP/Azure. It was probably deleted outside control-hub." | The cluster no longer exists; it moves to **Gone from the cloud**. |
| "The cluster's API endpoint no longer resolves." | Same: deleted outside control-hub. |
| "timed out talking to the cluster" | Network path or a slow API server; retry later. |
| Costs: many nodes "priced at OpenCost's default rates" | The AWS user lacks `pricing:GetProducts`, or the instance type was not found. Failed lookups are retried after a day. |

## SQLite "database is locked"

The console sets `busy_timeout=10000`, `journal_mode=WAL` and `_txlock=immediate`, so in normal operation writers wait
for each other instead of failing. If you still see `database is locked` or `SQLITE_BUSY`:

1. **Two processes are using the file.** Check there is exactly one pod: `kubectl -n ai-console get pods`. Keep
   `replicas: 1` and `strategy: Recreate`. Never run a second console (or a debug pod with `sqlite3`) against the same
   file while the console is running.
2. **A custom `DATABASE_URL` overrides the pragmas.** The console only adds a pragma if the URL does not already
   mention it. Use `file:/data/ai-console.db` without parameters.
3. **The volume is slow or full.** Check free space on `/data` (the Grafana volume panel or `kubectl describe pvc`).

`disk I/O error (6410)` means SQLite could not find a temp directory; the console sets `temp_store=memory` to avoid
it. If you see it, a custom `DATABASE_URL` probably removed that pragma.

## Slow start-up or high CPU

Right after a deploy the console does a lot at once:

- the **cost collector** recomputes today and then backfills `COST_BACKFILL_DAYS` (30) past days, one day per cluster
  every 2 minutes;
- the **log parser** reads 500 lines from every container the first time it sees it (or 10 minutes of Loki);
- the **certificate inventory** scans at once unless the stored inventory is still fresh;
- the **Grafana hub** loads every instance's cluster names, and the **metric rules** run their first queries.

This settles within the first cycles. What helps:

- Give the pod enough CPU (the current deployment requests 1 CPU and limits memory to 2Gi; the repository manifest's
  `100m` is too little for a fleet).
- Lower `COST_BACKFILL_DAYS` for a fresh install, or set `COST_ENABLED=false` while investigating.
- Raise `LOG_SCAN_INTERVAL` (it also sets the metric rules interval) or narrow `LOG_SCAN_NAMESPACES`.
- The readiness probe only needs `/api/healthz`, which does not wait for the workers.

## Grafana errors

Grafana problems show on **Settings → Grafana** (the **Errors** column) and in the log.

| Error | Cause and fix |
|---|---|
| `grafana <name> /api/datasources: 401 …` | The token is wrong or expired. Create a new Viewer service-account token. |
| `no Prometheus datasource in <name>` | The service account cannot see a Prometheus datasource, or the instance has none. Check the service account's role. |
| `no Loki datasource in <name>` | Loki-backed log parsing and `grafana_query_logs` need a Loki datasource. Without one, the parser reads pods through the API server. |
| `promql: …` | A query failed. For a metric rule, the rule keeps its previous issues and the error is listed under the instance. |
| `grafana <name>: … x509: certificate signed by unknown authority` | The console fetches missing intermediates from the certificate's AIA URL, as browsers do. If this still fails, the chain does not reach a trusted root. |
| `grafana <name>: GRAFANA_<NAME>_URL set but no token; skipped` | Add `GRAFANA_<NAME>_TOKEN`. |
| `grafana: ignoring GRAFANA_MAPPINGS entry "…"` | The entry does not follow `<cluster>=<instance>:<metrics cluster>[:<loki cluster>]`. |
| A cluster shows "Not connected to Grafana" | No instance reports a matching `cluster` label. Override the mapping in Settings, or add it to `GRAFANA_MAPPINGS`. |

## Monitors

| Symptom | Cause |
|---|---|
| `monitor run failed: could not list control planes in any realm; check CONTROLHUB_SERVICE_TOKEN` | The token is wrong or expired, or `CONTROLHUB_URL` is unreachable. |
| "Last monitor run: Never" | `MONITOR_ENABLED=false`, or every run failed (see the log). |
| No Slack messages | `SLACK_MUTE=true`, `SLACK_WEBHOOK_URL` empty, or the monitors have not baselined yet (the first run never posts). |

## Copilot

| Symptom | Cause |
|---|---|
| Replies stop after about a minute, or arrive all at once | The ingress buffers or times out the event stream. Set `proxy-buffering: "off"` and the 900 s read and send timeouts. |
| "add your Claude API key in Settings to use the Copilot" | No personal key and no `ANTHROPIC_API_KEY`. |
| No `search_controlhub_logs` | `control-hub log search is off` in the log: `CONTROLHUB_LOGS_NAMESPACE` is empty, the pod is not in a cluster, or RBAC does not allow reading pods and logs there. |
| No `read_knowledge_node` | `knowledge graph not found in …`: build with `make image` so the knowledge graph is copied in. |
| No `athena_read` | `athena: ATHENA_AUTH_TOKEN not set; athena_read is off`. |

## Sign-in

| Symptom | Cause |
|---|---|
| "could not reach control-hub" | `CONTROLHUB_URL` is wrong or control-hub is down. |
| Everyone is signed out after a deploy | `ENCRYPTION_KEY` changed, or the database was replaced. |
| Sign-in works but the session is lost at once on plain http | `COOKIE_SECURE=true` (the default) needs HTTPS. Set `COOKIE_SECURE=false` only for local http. |
| The browser shows a certificate error for the console | The copied wildcard secret expired; copy it again. |
