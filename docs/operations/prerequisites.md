# Prerequisites

## To build

| Tool | Version | Used for |
|---|---|---|
| Go | 1.26 (the Dockerfile builds with `golang:1.26-alpine`) | The server, the MCP server, the eval runner |
| Node.js and npm | 22 (the Dockerfile builds with `node:22-alpine`) | The React UI |
| Docker | with `--platform linux/amd64` support | The image |
| git | | `make kg` finds the knowledge graph through the repository |
| The control-hub knowledge graph | `.knowledgegraph/` at the control-hub repository root, or in the main checkout when you build from a worktree | Shipped in the image for the Copilot |

## To deploy

| Requirement | Why |
|---|---|
| A Kubernetes cluster on GKE with Workload Identity | The console runs as control-hub's Google service account (`control-hub-admin@prophecy-cloud.iam.gserviceaccount.com`) to read GKE clusters and Certificate Manager. The current deployment runs on the management cluster (`prophecy-cloud-mgmt`, us-west1). |
| Network access from the pod to control-hub | `CONTROLHUB_URL`, for example `http://control-hub.control-hub-prod.svc.cluster.local:8003`. |
| An nginx ingress controller | The manifest uses the `prophecy-nginx` ingress class. |
| A TLS certificate for the console's host | The current deployment uses a copy of the `*.app.prophecy.ai` wildcard secret. ACM certificates cannot be used by a GKE ingress. |
| A DNS record for the host | Pointing at the ingress controller's external IP. |
| A persistent volume (ReadWriteOnce) | For the SQLite database and `DATA_DIR`. The current deployment uses a 10Gi `standard-rwo` PVC. |
| Permission to push images | To `gcr.io/prophecy-cloud/control-hub-ai-console`. |

## Credentials

Collect these before installing. None of them belongs in the repository or in these docs.

| Credential | Required | Notes |
|---|---|---|
| `ENCRYPTION_KEY` | yes | `openssl rand -base64 32`. Generate once and keep it stable. |
| `CONTROLHUB_SERVICE_TOKEN` | when monitors are on | A control-hub admin token that can read every realm. The console only ever reads with it. |
| `CONTROLHUB_DEV_SERVICE_TOKEN` | no | The same for control-hub dev. |
| `ANTHROPIC_API_KEY` | no | A shared team key for people without a personal key. |
| `SLACK_WEBHOOK_URL` | no | For findings and the weekly digest. |
| `SLACK_BOT_TOKEN`, `SLACK_SIGNING_SECRET` | no | Both, for the [Copilot in Slack](slack.md). |
| `ATHENA_AUTH_TOKEN` | no | The value control-hub uses for Athena; enables `athena_read` and the Athena readiness check. |
| `AWS_PROD_ACCESS_KEY_ID`, `AWS_PROD_SECRET_ACCESS_KEY` | for EKS, ACM, AWS prices | The same values as control-hub. AWS list prices also need `pricing:GetProducts`. |
| `AZURE_PROD_TENANT_ID`, `AZURE_PROD_CLIENT_ID`, `AZURE_PROD_CLIENT_SECRET`, `AZURE_PROD_SUBSCRIPTION_ID` | for AKS | The same values as control-hub. |
| `GRAFANA_<NAME>_URL`, `GRAFANA_<NAME>_TOKEN` | no | One pair per Grafana instance, with a Viewer service-account token. Instances can also be added later in Settings. |

## Who signs in

Anyone with a control-hub account can sign in. Decide who should be a console admin beyond control-hub's own admins,
and list their emails in `CONSOLE_ADMIN_EMAILS`.
