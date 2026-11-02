# Log rules and patterns

The log parser matches log lines against a library of known messages (built-in rules mined from the prophecy repo,
plus admin changes). It never calls the Claude API. See [Log rules and error patterns](../concepts/log-rules.md).

## Types

```ts
type LogRule = {
  id: string; title: string
  services: string[]          // pod-name prefixes; [] = every pod
  pattern: string             // Go RE2 regexp
  severity: "error" | "warning" | "info"
  meaning: string; fix: string
  source: string              // file:line in the prophecy repo where the message is logged, or ""
  examples: string[]; not_examples: string[]   // always arrays
  builtin: boolean            // ships with the console (may be edited or disabled by an admin)
  enabled: boolean
  edited: boolean             // a built-in whose content an admin changed (DELETE resets it)
  hits24h: number             // matches recorded by scans in the last 24 h
}
type Hit = {
  ruleId: string; title: string; severity: "error" | "warning" | "info"
  meaning: string; fix: string
  count: number
  firstLine: number; lastLine: number      // indexes into the scanned lines
  sample: string
}
type ParserResult = { hits: Hit[]; matches: { line: number; rules: string[] }[] }
// Background parser results. counts cover the last 60 min; a rule not seen for 10 min is resolved (active=false).
type PodHit = { ruleId: string; title: string; severity: "error" | "warning" | "info"; meaning: string; fix: string
  count: number; total: number; firstSeen: string; lastSeen: string; active: boolean; sample: string }
type PodScan = { pod: string; errors: number; warnings: number; top: string; hits: PodHit[]; error?: string }
type NamespaceScan = { namespace: string; window: string; scannedAt: string; pending: boolean; error?: string; pods: PodScan[] }
```

## Rules

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/log-rules` | — | `LogRule[]` sorted by id |
| POST | `/api/log-rules` | `LogRule` fields (no builtin/enabled/hits24h) | `LogRule`. Admins only. `400` with the validation message if the pattern does not compile, an example does not match, etc.; `409` if the id exists. |
| PUT | `/api/log-rules/{id}` | same | `LogRule`. Admins only; editing a built-in stores an override. |
| POST | `/api/log-rules/{id}/enable`, `/api/log-rules/{id}/disable` | — | `LogRule`. Admins only. |
| DELETE | `/api/log-rules/{id}` | — | `{}`. Admins only; removes a custom rule, or resets an edited or disabled built-in to its default. |
| POST | `/api/log-rules/test` | `{line, pod?}` | `LogRule[]` that match (all rules when `pod` is empty, else only rules scoped to that pod) |

## Stored scan results

| Method | Path | Response |
|---|---|---|
| GET | `/api/clusters/{id}/log-scan?ns=` | `NamespaceScan`: **stored results only, instant.** `pending` is true until the worker's first cycle for the cluster finished; `scannedAt` is that cycle's time. Viewing never starts a scan. |

The pod logs route (`GET /api/clusters/{id}/pods/{ns}/{name}/logs`) returns `Logs & { parser: ParserResult }`: the
parser's result over the same lines.

## New error patterns and drafted rules (admins)

Error-level lines no rule matches are grouped into templates (a small Drain: variable tokens become `<*>`, lines whose
other tokens mostly agree share a template), counted per cluster and hour. Clusters read through Loki get a capped
error sample every 30 minutes. Secrets and email addresses are masked before anything is stored. Patterns nobody acted
on are dropped 14 days after they stop.

| Method | Path | Notes |
|---|---|---|
| GET | `/api/log-patterns?status=new|ignored|ruled` | `[{id, service, template, example, count, clusters, firstSeen, lastSeen, last6h, last24h, isNew, status, ruleId}]`, new and active first |
| PUT | `/api/log-patterns/{id}` | `{status, ruleId?}` |
| POST | `/api/log-patterns/{id}/draft-rule` | Claude drafts a rule with the caller's key → `{applicable, reason?, rule}`; nothing is saved |
| POST | `/api/learnings/{id}/draft-rule` | The same from an approved learning's symptoms |

A draft is validated before it is returned: the pattern must compile as RE2, match every example and the real line it
came from, match no not-example, and not be a bare "error". One retry gets the failures. The admin saves it through
`POST /api/log-rules`.
