# Copilot

**Route:** `/copilot` and `/copilot/<chat id>`

The Copilot is Claude with read-only tools over control-hub, the clusters, Grafana, Athena and the console's own data.
"Copilot investigates control-plane provisioning failures using Control Hub's API. It can read what you can see; any
change it proposes shows up as a button for you to approve."

You can use it on the Copilot page, or in the Copilot panel docked on every other page (see
[Signing in and finding your way](getting-started.md#the-copilot-panel)).

## Before you start

You need a Claude API key: your own (add it in [Settings](settings.md)) or the team's shared key. If neither is
available, the page shows "No Claude API key available" and **Add your key**.

## Ask a question

1. Open **Copilot** and click **New chat** (or start from a suggestion: "Which control planes failed to provision this
   week?", "Why did &lt;tenant&gt; fail?", "Which certificates expire in the next 14 days?").
2. Type your question and press <kbd>Enter</kbd> (<kbd>Shift</kbd>+<kbd>Enter</kbd> for a new line).
3. Watch the steps: "Checking… N things so far". Each step is one tool call with ✓ or ✗. Admins also see the tool
   name, its input and a summary of its output.
4. Read the answer. It leads with the cause and the next step.
5. If the Copilot diagnosed a specific control plane or execution plane, a link "Investigation: …" appears under the
   reply.
6. If it proposed an action, a button appears under the reply. Nothing runs until you click it.
7. Under each reply, "Used your Claude key" or "Used the shared team key" says which key paid for it.

Your chats are listed under **Your chats**, newest first. Only you can see them.

## Asking good questions

The Copilot works from evidence and keeps going until it can name the cause or has ruled out what its tools can check.
You get better answers faster when you:

- **Name the entity.** A tenant or customer name, a control plane ID, a hostname or a cluster name. "Why did
  capgroup's upgrade fail?" beats "Why did the upgrade fail?".
- **Say what you saw.** Paste the error message, the failed step, the job ID or the alert name. Exact strings let it
  search learnings and logs directly.
- **Give a time window.** "since this morning", "in the last 6 hours". Metric and log queries are bounded.
- **Ask one thing at a time.** A narrow question finishes in fewer steps; a reply can hit the length limit on a very
  broad one.
- **Follow up in the same chat.** The Copilot keeps the whole conversation, including what it already checked and any
  action you ran.

Examples:

```text
capgroup has been stuck on "waiting for Prophecy" for an hour after an upgrade to Postgres 16. Why?
Which pods in the prophecy namespace on <cluster> restarted in the last 6 hours, and why?
Is <tenant> safe to upgrade? What should I fix first?
Why did compute cost on <cluster> go up this week?
What does Athena say about <tenant>'s services right now?
```

## What the Copilot can use

The Copilot gets every tool whose data source is configured. All of them are read-only, and every control-hub and
cluster read uses **your** control-hub token.

**control-hub**

| Tool | What it does |
|---|---|
| `find_control_plane` | Find control planes by customer, cluster, tenant name or URL. |
| `list_control_planes` | List control planes in a realm, optionally by status. |
| `get_control_plane` | Full record of one control plane, secrets redacted. |
| `get_control_plane_env_status` | Runtime status Athena reports from inside the control plane cluster. |
| `get_post_deploy_info` | Post-deploy details of a SAP control plane, per execution plane. |
| `list_execution_planes` | Execution planes of a control plane, with status and failure reason. |
| `get_execution_plane` | Full record of one execution plane. |
| `get_infrastructure` | The cluster record behind a control plane. |
| `get_job` | An async job: status, progress, the failed step and the raw error. |
| `get_default_prophecy_version` | The default Prophecy version for new control planes. |
| `search_controlhub_logs` | Search control-hub's own logs (when log search is configured). |
| `read_knowledge_node` | Read a node of the control-hub knowledge graph. |

**Network** (Prophecy hostnames only)

| Tool | What it does |
|---|---|
| `resolve_dns` | CNAME and A/AAAA records, resolved from inside the cluster. |
| `check_tls_certificate` | The certificate a host serves on 443. |
| `probe_https` | Status code, latency and the first 500 bytes of a URL. |

**Clusters** (managed clusters you can see)

| Tool | What it does |
|---|---|
| `k8s_list_nodes` | Nodes with readiness, pool, instance type and live usage. |
| `k8s_list_pods` | Pods with status, restarts and usage; only problems, or sorted by CPU, memory or restarts. |
| `k8s_describe_pod` | One pod in detail, like `kubectl describe`. |
| `k8s_pod_logs` | Recent log lines of a container, masked; the previous (crashed) run on request. |
| `k8s_events` | Recent events, optionally warnings only. |
| `k8s_get` | Any other resource kind, like `kubectl get`. |
| `k8s_sandboxes` | go-sandbox pods and which ones the orchestrator abandoned (orphans holding CPU and memory). |
| `audit_search` | Admins only: who changed, created or deleted something and what changed (see [Audit](../concepts/audit.md)). |
| `change_timeline` | What changed around a tenant or cluster: Helm revisions, rollouts, new nodes, findings, investigations. |

**Monitoring** (clusters mapped to Grafana)

| Tool | What it does |
|---|---|
| `current_issues` | What the metrics and log parsers currently see. |
| `grafana_query_metrics` | Read-only PromQL, up to 7 days of history. |
| `grafana_query_logs` | Loki log search, up to 24 hours. |
| `grafana_list_metrics` | Metric names a cluster sent in the last hour. |
| `grafana_label_values` | A metric's labels, or a label's values. |
| `grafana_alerts` | Alerts firing or pending on a cluster. |
| `grafana_dashboards` | The team's dashboards and their panel queries. |

**Other data**

| Tool | What it does |
|---|---|
| `athena_read` | One read view of a control plane's Athena (status, update info, services, sandboxes, metering…). |
| `list_certificates` | The certificate inventory, with renewal checks. |
| `query_costs` | Estimated cost per cluster, day, namespace or workload; right-sizing; cost changes. |
| `upgrade_readiness` | The upgrade readiness checks for a control plane. |
| `search_learnings` | Approved learnings from past investigations, searched with the symptoms and rewordings of them. |

**Console tools**

| Tool | What it does |
|---|---|
| `record_investigation` | Record the diagnosis (root cause for admins, a plain summary for the requester). |
| `record_learning` | Confirm or contradict a learning, or draft a new one for admin review. |
| `propose_action` | Propose an action after a dry run: retry SAP post-deploy (control plane or execution plane), restart a pod, or delete orphaned go-sandboxes. On a read-only console the dry run is shown without a button. |

## Running a proposed action

The Copilot can propose four operations, and only when the evidence says they should help:

| Action | When |
|---|---|
| **Retry post-deploy** / **Retry execution plane post-deploy** | A SAP post-deploy failed on a transient error and the control plane is now healthy. |
| **Restart pod** | A stuck or leaking process (not a config error a restart cannot fix). Refused for pods without a controller and for Job pods. |
| **Delete orphaned go-sandboxes** | Unschedulable sandboxes the orchestrator abandoned are holding capacity. |

Every proposal is first a **dry run** against the live state, with your access. If the dry run finds it unsafe (for
example post-deploy is still running, or there are no orphans), nothing is proposed and the Copilot explains why.
Otherwise the card shows:

- **Dry run**: what happens, in one sentence;
- the checks it made (current status, pod owner, restarts…);
- the exact steps, as commands or API calls, with a copy button;
- any **Risk** lines (for example a single replica, or a StatefulSet pod).

To run it:

1. Read the dry run and the risks.
2. Click the button. The dry run is repeated first; if the state changed since you saw it, the action is refused
   ("the state changed since the dry run; ask the Copilot to propose it again").
3. It runs with **your** access, so control-hub and your cluster list decide whether you may do it.
4. The card shows "Running…", then succeeded or failed with the result.
5. Your next message tells the Copilot what happened, so it will not propose it again.

Or click **Dismiss**. Every proposal, refusal, run and dismissal is in the audit log.

On a **read-only** console the card says "Dry run only: the console is read-only, so this can't be executed here. An
admin can apply the steps by hand." and has no button: copy the steps for someone with access. Details are in
[Dry-run actions](../concepts/actions.md).

## Admins and non-admins

| | Admins | Non-admins |
|---|---|---|
| Reply detail | Full technical detail: root cause, evidence, resource names, raw errors. | The outcome in plain terms, the next step and a reference ID. No internal root cause, raw errors, resource names, internal hostnames or component names. |
| Steps | Tool name, input and output summary. | A neutral label per step. |
| Investigations | Root cause, fix and evidence. | The summary. |

## When something goes wrong

| Message | What to do |
|---|---|
| the Claude API key was rejected; update it in Settings | Replace your key in [Settings](settings.md). |
| the Claude API key isn't allowed to use this model | Use a key from an organisation with access to the configured model. |
| the Claude API rate limit was reached; try again in a minute | Wait and resend. |
| the Claude API is overloaded; try again shortly | Wait and resend. |
| the reply hit the length limit; ask a narrower question | Split the question. |
| stopped after too many steps without a final answer | Narrow the question or name the entity. |
| Claude declined this request. Try rephrasing it. | Rephrase. |
