# Guardrails and audit

**Route:** `/guardrails` · "What Copilot may do on its own, what needs a human, and what it never does. Every action
still runs under the requester's Control Hub permissions."

## Action policy

Everyone can read the policy. Modes: **Auto** · **Ask requester** · **Admin approval** · **Never**.

| Action | Examples | Mode |
|---|---|---|
| Read & diagnose | control planes, execution planes, jobs, control-hub logs, DNS, TLS, HTTPS probes | Auto |
| Retry post-deploy | re-run SAP control plane or execution plane post-deploy | Ask requester (Dry run only on a read-only console) |
| Restart a pod / clean up | delete a pod its controller recreates; delete orphaned go-sandboxes | Ask requester (Dry run only on a read-only console) |
| Reversible infra fix | tag subnets, re-issue a certificate | Not available yet |
| IAM / role changes | grant AcrPull, workload identity bindings | Not available yet |
| Scale & upgrade | node pool size, Prophecy version, size config | Not available yet |
| Create cluster | new control plane, execution plane or infrastructure | Not available yet |
| Delete anything | control planes, execution planes, infrastructure, DNS, certificates | Never |

"Ask requester" means the Copilot proposes the action, with a [dry run](../concepts/actions.md), as a button, and it runs
only when the person who asked clicks it, with their own access.

## Audit log (admins)

The last 100 entries, newest first: **Time**, **Who**, **Action**, **Target**, **Decision**, **Detail**.

Entries include:

| Action | Recorded when |
|---|---|
| `sign_in` | Someone signs in. |
| `set_claude_key`, `remove_claude_key` | A user stores or removes their Claude key. |
| `record_investigation`, `record_learning` | The Copilot records an investigation or updates learnings (actor "Copilot for &lt;email&gt;"). |
| `retry_cp_postdeploy`, `retry_ep_postdeploy`, `restart_pod`, `delete_orphaned_sandboxes` | An action is proposed, refused by its dry run, run (succeeded / failed) or dismissed. |
| `slack_copilot` | The Copilot answered in Slack (target: the channel; detail: the chat). |
| `acknowledge_finding` | An admin acknowledges a finding. |
| `create_learning`, `update_learning`, `delete_learning` | An admin changes learnings. |
| `create_log_rule`, `edit_log_rule`, `enable_log_rule`, `disable_log_rule`, `reset_log_rule`, `draft_log_rule`, `log_pattern_<status>` | An admin changes rules or patterns, or drafts a rule. |
| `add_grafana_instance`, `remove_grafana_instance`, `map_grafana_cluster` | An admin changes Grafana settings. |
| `certificate_repair_plan`, `certificate_repair` | An admin checks or applies a certificate repair. |
| `rightsizing_review` | An admin submits a batch review. |
| `send_digest` | An admin sends the digest to Slack. |
