# Dry-run actions

The Copilot cannot change anything by itself. When a fix is an operation the console supports, it **proposes an
action**. Every proposal is first **dry-run** against the live state with the requester's access, and the plan is what
the requester sees: exactly what would be called or deleted, what was checked, and what could go wrong. Nothing runs
unless the requester clicks. Code: `internal/agent/dryrun.go`, `actions.go`, `console_tools.go`.

## Action kinds

| Kind | Card title | Inputs | What it does |
|---|---|---|---|
| `retry_cp_postdeploy` | Retry post-deploy | `control_plane_id` | `POST /api/control-planes/{id}/postdeploy?realm=sap` with the user's control-hub token. control-hub re-runs the post-deploy steps; the deployment itself is not touched. |
| `retry_ep_postdeploy` | Retry execution plane post-deploy | `control_plane_id`, `execution_plane_id` | `POST /api/control-planes/{id}/execution-planes/postdeploy?realm=sap` with `{"executionPlaneID": …}`. |
| `restart_pod` | Restart pod | `cluster`, `namespace`, `pod` | Deletes the pod so its controller recreates it. |
| `delete_orphaned_sandboxes` | Delete orphaned go-sandboxes | `cluster` | Deletes the [orphaned go-sandboxes](go-sandboxes.md) on the cluster. |

`cluster` is resolved like the `k8s_*` tools: only clusters the user's control-hub token can see.

## The plan

```ts
type Plan = {
  summary: string     // what happens, in one sentence
  steps: string[]     // the exact operations, as commands or API calls
  checks: string[]    // what the dry run read and found
  risks?: string[]
  blocked?: string    // why the action must not run as proposed
  targets: string[]   // the objects the steps touch, compared at execution time
}
```

| Kind | Checks | Steps | Risks |
|---|---|---|---|
| Post-deploy retries | Current status, post-deploy status and Prophecy version, read from control-hub | The `POST` above | Post-deploy already completed: re-running repeats steps that succeeded (idempotent, but takes several minutes). |
| `restart_pod` | Pod status, ready, restarts, node and owner | `kubectl --context <cluster> -n <ns> delete pod <pod>` | StatefulSet pods restart one at a time with the same name and volume (down until ready); a Postgres pod takes every service using it down; a single replica means requests fail until the new pod is ready. |
| `delete_orphaned_sandboxes` | go-sandbox count and how many are orphaned | One `kubectl … delete pod` per orphan | — (summary states the cores and GiB freed) |

The steps are written as commands so an admin can apply them by hand; the card has a copy button.

## Refusals

If the dry run sets `blocked`, **nothing is proposed**. The Copilot is told the reason and explains it instead, and the
refusal is audited (decision `refused`).

| Kind | Refused when |
|---|---|
| Post-deploy retries | Post-deploy is still running, in progress or pending. |
| `restart_pod` | The pod has no controller (a bare pod: nothing would recreate it). The pod belongs to a Job or CronJob (deleting it can fail or re-run the job instead of restarting a service). |
| `delete_orphaned_sandboxes` | The orchestrator log could not be read (orphans cannot be told apart from sandboxes in use). There is no orphaned go-sandbox on the cluster. |

A dry run that cannot read the state at all (control-hub or the cluster fails) is an error, and nothing is proposed.

## Executing

When the requester clicks the action's button (`POST /api/actions/{id}/execute`):

1. **Read-only consoles refuse**: `403` "the console is read-only; actions are turned off".
2. **The plan is recomputed.** If the new dry run is blocked, or its targets differ from the plan the user saw, the
   action does not run: `409` "the state changed since the dry run; ask the Copilot to propose it again".
3. The action is claimed once (a second click gets `409` "this action is no longer pending").
4. It runs with the clicking user's access: their control-hub token for post-deploy retries; for pod deletions, the
   cluster opened through their control-hub cluster list. Pods are deleted one by one in plan order; a failure stops
   there and the result lists what was deleted.
5. The result and status (`succeeded`, `failed`) are stored, with who ran it, and audited.

`DeletePod` is the only Kubernetes write in the console, used only by executed actions.

## Read-only consoles

With `READ_ONLY=true` the Copilot **keeps `propose_action`**, but as a dry run only:

- its tool description says the action cannot be executed and that an admin can apply the steps by hand;
- the card shows the plan and "Dry run only: the console is read-only, so this can't be executed here. An admin can
  apply the steps by hand." There is no button;
- the execute endpoint returns `403`.

The plan is the deliverable: an exact, checked list of commands for someone with access to run.

## In Slack

The [Copilot in Slack](../user-guide/slack.md) describes proposed actions ("*Prepared:* … Review the dry run in the
console.") but has no buttons; open the conversation in the console to review or run them.
