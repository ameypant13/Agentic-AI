# Orphaned go-sandboxes

Since **orchestrator 4.2.12.1**, a pipeline run whose new go-sandbox pod is `Unschedulable` fails at once (about
200 ms after the pod is created) instead of waiting for the autoscaler to add a node. This happens even on autoscaling
clusters (GKE Autopilot, Karpenter); on 4.2.11.x runs waited. The run fails, but the pod stays behind, starts when a
node frees up, and no run ever uses it. It holds its CPU and memory requests until someone deletes it.

The console detects this in three places: a log rule, the Copilot tool `k8s_sandboxes`, and a readiness check. It can
also propose deleting the orphans as a [dry-run action](actions.md).

## How orphans are detected

Nothing about the pod itself tells an orphan apart from a sandbox in use. The orchestrator, however, logs the name of
every pod it gave up on:

```text
Pod go-operator-sandbox-<id> failed to start: Unschedulable: …
```

`Cluster.Sandboxes` (`internal/kube/sandboxes.go`):

1. Lists every pod named `go-operator-sandbox-*` in any namespace that has not finished.
2. Finds the running orchestrator pods (pod name contains `orchestrator`).
3. Reads each orchestrator's log, with timestamps, from just before the oldest sandbox still running (at most 7 days
   back; at most 200,000 lines and 64 MiB).
4. Marks a sandbox **orphaned** when that log names it in a "failed to start: Unschedulable" line, and records when.
5. Sums the CPU and memory the orphans request.

If no orchestrator log could be read (`logsRead: false`, for example no running orchestrator pod), orphans are
**unknown**, not absent.

The report:

| Field | Meaning |
|---|---|
| `total`, `orphaned` | go-sandbox pods, and how many are orphaned |
| `orphanCpu`, `orphanMem` | What the orphans request (cores, bytes) |
| `sandboxes[]` | `name, namespace, phase, node, createdAt, ageHours, cpuRequest, memRequest, orphaned, abandonedAt`; orphans first, then oldest |
| `logsRead`, `logErrors` | Whether an orchestrator log was read, and why not |

## Where it shows up

| Where | What |
|---|---|
| Log rule `orchestrator-sandbox-unschedulable` (error, service `orchestrator`) | Matches `Pod go-operator-sandbox-\S+ failed to start: Unschedulable`. Its fix: check the orchestrator version and node headroom in `prophecy-execution`, delete orphaned sandboxes, rerun the pipeline; fix capacity first if nodes are genuinely full. |
| Copilot tool `k8s_sandboxes(cluster)` | The report above. For runs failing with Unschedulable sandboxes, or capacity questions on execution planes. |
| Readiness check `sandboxes` ("Orphaned go-sandboxes") | **pass** with no go-sandboxes or none orphaned; **warn** listing the orphans and what they hold; **unknown** when the orchestrator log is unreadable. The hint gives the `kubectl delete pod` to run. |
| Action `delete_orphaned_sandboxes` | The Copilot can propose deleting exactly the orphans; see [Dry-run actions](actions.md). |
