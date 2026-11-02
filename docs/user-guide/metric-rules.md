# Metric rules

**Route:** `/log-rules?view=metrics` (the **Metric rules** tab of Log rules)

"PromQL the metrics parser evaluates through Grafana every minute. Each series a rule returns is an active issue; it
resolves when the rule stops returning it."

The page is **read-only for everyone**: the rules ship in the image (`internal/metricrules/rules.yaml`). How they work
is in [Metric rules](../concepts/metric-rules.md).

## Reading the page

| Column | Shows |
|---|---|
| Rule | ID and title |
| Severity | Error, Warning or Info |
| Entity | What an issue is about: container, pod, service, deployment, pvc, node or cluster |
| Value | How the value is shown: percent, count, seconds, per second, or none |
| Meaning · fix | One sentence each |

Search by ID, title, entity or PromQL, and filter by severity. Click a row to see the PromQL ("evaluated every 60 s;
each returned series is one issue").

A link with `?rule=<id>` opens and scrolls to a rule; issue rows on the [Issues](issues.md) page link here.

## Common tasks

### Understand an issue

1. On the Issues page or a cluster overview, click the issue to expand it.
2. Click the rule ID. The Metric rules tab opens on that rule.
3. Read the PromQL to see exactly what fired, and the meaning and fix.

### Change a rule

Rules cannot be changed from the UI. Edit `internal/metricrules/rules.yaml` and ship a new image; see
[Adding a log rule](../development/adding-a-log-rule.md#metric-rules).
