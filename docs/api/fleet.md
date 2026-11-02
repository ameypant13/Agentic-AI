# Fleet, findings and monitors

## Types

```ts
type Fleet = {
  controlPlanes: { total: number; byStatus: Record<string, number>; byRealm: Record<string, number> }
  openFindings: { critical: number; warning: number }   // open + acknowledged
  investigations7d: number
  lastMonitorRun: string | null
}
type Finding = {
  id: string
  kind: "cp_failed" | "cp_stuck" | "postdeploy_failed" | "ep_failed" | "cert_expiry" | "log_errors"
  severity: "critical" | "warning"
  title: string                 // one line, e.g. "Provisioning failed"
  detail: string                // one or two sentences
  entityType: "control_plane" | "execution_plane" | "workload"
  entityId: string
  entityName: string            // customer / tenant name (cluster name for log_errors)
  realm: string; provider: string; region: string
  state: "open" | "acknowledged" | "resolved"
  firstSeenAt: string; lastSeenAt: string; resolvedAt: string | null
  investigationId: string | null
  acknowledgedBy: string        // email, "" when not acknowledged
  acknowledgedByName: string    // control-hub username
  acknowledgedAt: string | null
}
type Monitor = { key: string; name: string; condition: string; interval: string; notify: string; enabled: boolean }
```

## Routes

| Method | Path | Response |
|---|---|---|
| GET | `/api/fleet` | `Fleet`. Control plane counts come from the monitors' last run. |
| GET | `/api/findings?state=open|resolved|all` (default `open`) | `Finding[]`, critical first, then most recently seen. `open` includes acknowledged findings. At most 500. |
| POST | `/api/findings/{id}/investigate` | `{chatId, prompt}`. Creates a chat; the UI opens it and sends `prompt` as the first message. |
| POST | `/api/findings/{id}/ack` | `Finding`. Admins only. Acknowledges an open finding (no change otherwise) and records who did it. |
| GET | `/api/monitors` | `Monitor[]`. `notify` is "Slack" or "Console only". |

See [Findings and monitors](../concepts/monitors.md) for the lifecycle.
