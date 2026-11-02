# Running locally

You can run the whole console on your machine against a port-forwarded control-hub. Locally it uses SQLite in the
working directory, monitors are usually off, and control-hub log search is off (it needs the in-cluster service
account).

## Three terminals

**1. control-hub (dev) on localhost:8003**

```bash
kubectl -n control-hub-dev port-forward svc/control-hub 8003:8003
```

**2. The console API on :8090**

```bash
cd ai-console
ENCRYPTION_KEY=$(openssl rand -base64 32) \
CONTROLHUB_URL=http://localhost:8003 \
MONITOR_ENABLED=false \
ANTHROPIC_API_KEY=<your key> \
make run
```

`make run` copies the knowledge graph (`make kg`) and runs `go run ./cmd/ai-console` with
`KG_DIR=./knowledgegraph STATIC_DIR=./web/dist COOKIE_SECURE=false`. The database is `ai-console.db` and the workers'
files go to `./data`.

**3. The UI with hot reload on :5173**

```bash
cd ai-console/web
npm install
npm run dev
```

Vite proxies `/api` to `localhost:8090`. Open http://localhost:5173 and sign in with your control-hub dev account.

Or build the UI once (`make web`) and open http://localhost:8090 directly.

To serve this manual too, run `make docs-build` once (it writes `./site`, the default `DOCS_DIR`) and open
http://localhost:8090/docs/ after signing in. For live editing use `make docs`; see
[Working on these docs](docs.md).

!!! tip "Keep the same ENCRYPTION_KEY"
    `$(openssl rand -base64 32)` makes a new key on every start, so your session and stored Claude key stop working
    after a restart. Export a fixed key in your shell for longer sessions.

## What is real when you run locally

The console reads whatever its credentials reach. Locally that means:

| Feature | Locally |
|---|---|
| control-hub | The port-forwarded control-hub, with your own token. |
| GKE clusters, Certificate Manager | Read with your Application Default Credentials (`gcloud auth application-default login`), if set. |
| EKS, AKS, ACM, AWS prices | Only if you export `AWS_PROD_*` / `AZURE_PROD_*`. Leave them unset unless you mean to read those clouds. |
| Grafana | Only if you export `GRAFANA_<NAME>_URL` and `_TOKEN`, or add an instance in Settings. |
| Monitors | Off with `MONITOR_ENABLED=false`. Turning them on needs `CONTROLHUB_SERVICE_TOKEN`. |
| Slack | Off unless `SLACK_WEBHOOK_URL` is set. Set `SLACK_MUTE=true` if you set it. |
| Claude | Real API calls, billed to the key you use. |
| control-hub log search | Off (not in a cluster). |

## Useful variations

```bash
# Read-only, as against prod
READ_ONLY=true CONTROLHUB_LABEL=control-hub-dev make run

# No cost collection while you work on something else
COST_ENABLED=false make run

# Postgres instead of SQLite
DATABASE_URL=postgres://user:pass@localhost:5432/ai_console make run
```
