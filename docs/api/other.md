# Readiness, digest and guardrails

## Upgrade readiness

| Method | Path | Returns |
|---|---|---|
| GET | `/api/readiness?cp=<control plane ID or name>` | `{controlPlane, verdict: go|caution|no-go|unknown, summary, checks[{id, name, status: pass|warn|fail|unknown, detail, hint}], at}` |

Rule-based checks, run with the caller's access: control-plane state, open findings, certificates (under 7 days fail,
under 21 warn), nodes (not ready, pressure, 90% or more requested), Prophecy pods, Helm releases not deployed,
Postgres (version; on 15+ "permission denied for schema public" in 24 h of logs fails), the fullest volume (80% or more
warn, 90% or more fail), Athena `prophecy_status` / `update_info`, OOM kills in 30 days, Helm changes in 24 h, and
orphaned go-sandboxes. Any fail → `no-go`, any warn → `caution`, more than half unknown → `unknown`. See
[Upgrade readiness checks](../concepts/readiness.md).

The Copilot's `upgrade_readiness(control_plane)` runs the same checks.

## Weekly digest (admins)

| Method | Path | Notes |
|---|---|---|
| GET | `/api/digest` | `{digest{from, to, headline, sections[{title, lines, link}]}, slack, lastSent, schedule}` for the last 7 days. `slack` is the Slack text. |
| POST | `/api/digest/send` | Posts it to Slack now → `{sent, message}`; `sent: false` when Slack is muted or unset; `502` when Slack fails. |

Built from the console's data, no AI. See [Weekly digest](../concepts/digest.md).

## Guardrails and audit

```ts
type PolicyRow = { action: string; examples: string; mode: "Auto" | "Ask requester" | "Admin approval" | "Never" | "Not available yet" | "Dry run only" }
type AuditEntry = { at: string; actor: string; action: string; target: string; decision: string; detail: string }
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/policy` | `PolicyRow[]` |
| GET | `/api/audit?limit=100` | `AuditEntry[]`, newest first. Admins only. |

## Athena (Copilot tool)

`athena_read(control_plane, view, config_type?, metrics_scope?)` reads a control plane's Athena at
`<controlPlaneUrl>/athena/api/v1` with `ATHENA_AUTH_TOKEN` (the credential control-hub uses). The control plane is
resolved through control-hub's control-plane lists **with the caller's token** (by ID, exact customer, tenant or
cluster name or hostname, then a unique partial match; deleted planes are skipped), so users only reach Athena for
planes they can see. `internal/athena` sends GETs only, and only to these views: `cluster`, `update_info`,
`available_versions`, `maintenance`, `insights`, `prophecy_status`, `service_status`, `platform_report`, `alerts`,
`tasks`, `sandboxes`, `features`, `feature_config`, `metering`, `credits`. Athena's restore, backup, sandbox-delete and
feature-update endpoints are unreachable. Results are capped at 40 KiB, secret-named keys are redacted and email
addresses masked (`j***@acme.com`). Unset `ATHENA_AUTH_TOKEN` disables the tool. It is also served by `control-hub-mcp`
when the variable is set.

## Slack events

| Method | Path | Notes |
|---|---|---|
| POST | `/api/slack/events` | The Events API endpoint of the [Copilot in Slack](../user-guide/slack.md). No session: every request must carry a valid `X-Slack-Signature` for `SLACK_SIGNING_SECRET`, at most 5 minutes old (`401` otherwise). `404` when `SLACK_BOT_TOKEN` or `SLACK_SIGNING_SECRET` is unset. Answers `url_verification` with `{challenge}`; other events get `200` at once and are handled in the background. |

Handled events: `app_mention`, and `message` in a direct message (`channel_type: im`). Bot messages, message subtypes
and retries of an event already handled are ignored. The answer is posted in the event's thread with `chat.postMessage`
and `chat.update`. See [Slack app setup](../operations/slack.md).
