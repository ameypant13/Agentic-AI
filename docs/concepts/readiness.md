# Upgrade readiness checks

Upgrade readiness (`internal/readiness`) runs twelve checks to answer one question before a tenant is upgraded: is it safe right now? Each
check reads one source and returns **pass**, **warn**, **fail** or **unknown**. No AI is involved; the Copilot can
explain a report, but the checks are plain rules.

The checks run concurrently with the **caller's access** (their control-hub token decides which control plane and
cluster they can read), with a 60-second timeout. The cluster is opened once and shared by the cluster checks.

## The checks

| ID | Name | Fails or warns when |
|---|---|---|
| `control-plane` | Control plane state | **fail**: a `*FAILED` status (resolve the failed operation first). **warn**: another operation is in progress, or post-deploy has not completed. |
| `findings` | Open findings | **fail**: open critical findings about the tenant (an upgrade restarts every service). **warn**: open warnings. |
| `certificates` | TLS certificates | The tenant's certificate expires in under 7 days (**fail**) or under 21 days (**warn**), or has renewal problems (**warn**). |
| `nodes` | Nodes and headroom | **fail**: nodes not ready. **warn**: nodes under pressure, or CPU or memory 90% or more requested (a rolling upgrade needs headroom). |
| `pods` | Prophecy services | **fail**: pods in the Prophecy namespace are unhealthy (an upgrade will not fix a crash-looping service). **warn**: pods pending. |
| `helm` | Helm releases | **fail**: a release is pending or failed (Helm refuses a new upgrade then). |
| `postgres` | Postgres | Reports the in-cluster Postgres version. On 15 or later, "permission denied for schema public" in the last 24 h of logs **fails** (grant CREATE on schema public to the application role first). Unknown with an external database. |
| `disk` | Volume headroom | The fullest volume is 80% or more (**warn**) or 90% or more (**fail**) full (from Grafana). |
| `athena` | Athena status | **fail**: Athena reports Prophecy down. **warn**: a service down, or an upgrade status in `update_info` other than completed or none. |
| `oom` | Memory history | Containers were OOM-killed in the last 30 days (**warn**: new versions often use more memory; raise these limits first). |
| `changes` | Recent changes | Helm ran in the last 24 h (**warn**: let the last change settle first). |
| `sandboxes` | Orphaned go-sandboxes | go-sandbox pods the orchestrator gave up on (orchestrator 4.2.12.1 regression) still hold CPU and memory (**warn**, with the pods to delete). **Unknown** when the orchestrator log cannot be read. See [Orphaned go-sandboxes](go-sandboxes.md). |

A check whose source is not available (no cluster access, no Grafana mapping, no `ATHENA_AUTH_TOKEN`, no usage history)
reports **unknown** with the reason.

## The verdict

| Verdict | UI label | Rule |
|---|---|---|
| `no-go` | No-go | Any check failed. Summary: "Fix before upgrading: …". |
| `caution` | Go with care | Any check warned. Summary: "Safe to upgrade with care; review: …". |
| `unknown` | Can't tell | More than half the checks are unknown. Never say go without having looked. |
| `go` | Go | Every check that ran passed. |

The summary also lists the checks that could not run.

## Where it is used

- The [Upgrade readiness](../user-guide/readiness.md) page (`GET /api/readiness?cp=`).
- The Copilot's `upgrade_readiness(control_plane)` tool. The system prompt tells it to call this before answering
  whether a tenant can be upgraded, or why an upgrade failed, and to explain the failing checks in order.
