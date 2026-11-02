# Data model

The console keeps two kinds of state: **tables** in its own database, and **files** under `DATA_DIR`. Both live on
the same persistent volume in the current deployment. Control-hub's data is never copied into either; it is always
read live through control-hub's API.

## The database

`DATABASE_URL` picks the database (`internal/store/store.go`):

- a URL starting with `postgres://` or `postgresql://` opens Postgres;
- anything else is a SQLite file path (default `file:ai-console.db`). The current deployment uses
  `file:/data/ai-console.db` on the PVC.

Tables are created and migrated at start-up by gorm `AutoMigrate`; there are no migration files.

For SQLite the console adds these pragmas unless the URL already sets them:

| Setting | Why |
|---|---|
| `temp_store=memory` | The image has no `/tmp`; without it, large sorts and transactions fail with `disk I/O error (6410)`. |
| `busy_timeout=10000` | Wait up to 10 s for a lock instead of failing at once. |
| `journal_mode=WAL` | Readers do not wait for the writer. |
| `_txlock=immediate` | Transactions take the write lock when they begin, so a `COMMIT` never fails with `SQLITE_BUSY`. |

The pool is limited to 4 open connections.

## Tables

The models are in `internal/store/models.go` and `internal/store/learnings.go`.

### Users and sessions

| Table | One row is | Key fields |
|---|---|---|
| `Session` | A signed-in browser | `ID` (the cookie value), `Email`, `Username`, `IsAdmin` (control-hub's flag), `TokenSealed` (the encrypted control-hub token), `ExpiresAt`. Expired sessions are deleted at the next sign-in. |
| `UserKey` | A user's personal Claude API key | `Email` (primary key), `KeySealed` (encrypted), `Last4`. |

### Copilot

| Table | One row is | Key fields |
|---|---|---|
| `Chat` | A conversation | `ID`, `Owner` (email; only the owner can read it), `Title`. |
| `TranscriptEntry` | One message exactly as sent to the Claude API (a `BetaMessageParam` as JSON) | `ChatID`, `Param`. Append-only: entries are never edited, so thinking blocks stay valid across turns. |
| `ChatItem` | What the UI renders: one user message or one assistant reply | `Role`, `Text` (markdown), `InvestigationID`, `KeySource` (`personal`/`shared`), `InputTokens`, `OutputTokens`. Kept apart from the transcript so tool results never reach a non-admin's browser. |
| `ToolCall` | One tool execution: the evidence trail of an investigation | `Tool`, `Label` (shown to non-admins), `Input`, `Summary` (first 600 characters of the result), `OK`. |
| `Action` | A state-changing operation the Copilot proposed | `Kind` (`retry_cp_postdeploy`, `retry_ep_postdeploy`, `restart_pod`, `delete_orphaned_sandboxes`), `TargetID`, `TargetName`, `Params`, `Plan` (the [dry run](actions.md) as JSON), `Status` (`proposed`, `running`, `succeeded`, `failed`, `dismissed`), `Result`, `ProposedFor`, `ExecutedBy`, `ExecutedAt`, `ReportedToModel` (set once the Copilot has been told the outcome). |
| `Investigation` | The Copilot's write-up of a diagnosis | `ID` (`INC-…`), `Requester`, `EntityID`, `EntityName`, `Category`, `Confidence`, `SummaryForRequester`, `RootCause` and `RecommendedFix` (admins only). |
| `Learning` | A reusable failure pattern | `ID` (`LRN-…`), `Status` (`draft`, `approved`, `retired`), `Symptoms`, `RootCause`, `HowToConfirm`, `Fix`, `KGNodes`, `Entities` and `Investigations` (JSON lists), `TimesSeen`, `Confirmed`, `Contradicted`, `ReviewNote`, `CreatedBy`, `ApprovedBy`. See [Learnings](learnings.md). |

### Monitors

| Table | One row is | Key fields |
|---|---|---|
| `Finding` | A problem a monitor detected | `ID` (`F-…`), `Key` (unique: kind + entity, so each run updates the same row), `Kind`, `Severity`, `State` (`open`, `acknowledged`, `resolved`), `FirstSeenAt`, `LastSeenAt`, `ResolvedAt`, `InvestigationID`, `AcknowledgedBy`, `AcknowledgedByName`, `AcknowledgedAt`. See [Findings and monitors](monitors.md). |
| `KV` | Small persistent state | Monitor state (`monitor:fleet` snapshot, `monitor:first-seen` times, `monitor:baselined`), the digest's last send time, the error-pattern grouping version (`log_patterns_version`), and which chat each Slack thread maps to (`slack_chat:<channel>:<thread>:<email>`). |
| `AuditEntry` | One audited event | `At`, `Actor`, `Action`, `Target`, `Decision`, `Detail`. |

### Log rules and patterns

| Table | One row is | Key fields |
|---|---|---|
| `LogRule` | An admin change to the rule library: a custom rule, or an override of a built-in one (same ID) | `RuleJSON` (the full rule), `Enabled` (a disabled built-in is a row with `Enabled=false`), `UpdatedBy`. |
| `LogRuleHit` | One rule's matches in one pod in one scan window | `RuleID`, `ClusterID`, `Namespace`, `Pod`, `Count`, `SeenAt`. Feeds "Hits 24 h". |
| `LogPattern` | An error message template no rule matched | `Service`, `Template`, `Example`, `Count`, `Clusters` and `Hourly` (JSON counters), `FirstSeen`, `LastSeen`, `Status` (`new`, `ignored`, `ruled`), `RuleID`. |

### Grafana

| Table | One row is | Key fields |
|---|---|---|
| `GrafanaInstance` | A Grafana added in Settings (instances from the secret are not stored) | `Name`, `URL`, `TokenSealed` (encrypted), `Last4`. |
| `GrafanaMapping` | An admin override of where a control-hub cluster's metrics and logs live | `Cluster` (control-hub name), `Instance`, `PromCluster`, `LokiCluster`. |

### Cost

| Table | One row is | Key fields |
|---|---|---|
| `CostClusterDay` | One cluster's compute cost for one UTC day (today's row is recomputed every hour) | `Day`, `Cluster` (Grafana cluster name), `Instance`, `Tenant`, `NodeCost`, `AllocatedCost`, `IdleCost`, `NodeHours`, `Nodes`, `EstimatedNodes`, `NodeTypes`, `Hours`, `ComputedAt`, `Rightsizing`. |
| `CostWorkloadDay` | One workload's cost for one day | `Namespace`, `Kind`, `Name`, `CPUCost`, `MemCost`, requested/used core-hours and GiB-hours, `Pods`. |
| `CostContainerDay` | One workload container's usage for a day, per replica (the largest replica) | `CPUP95`, `CPUMax`, `MemMax`, requests and limits, `OOMKilled`, `ThrottledPct`, `PodHours`, `Pods`. The input of right-sizing. |
| `ReviewBatch` | One Claude Message Batch reviewing recommendations | `SubmittedBy` (whose key the poller uses), `Status` (`in_progress`, `ended`, `failed`), `Requests`, `Succeeded`, `Errored`. |
| `RightsizingReview` | Claude's latest verdict on one recommendation | `Key` (cluster, namespace, kind, name and container joined by `|`), `Status` (`pending`, `done`, `failed`), `Verdict` (`safe`, `adjust`, `risky`), `Reason`, suggested values. |

Cost rows are kept for `COST_RETENTION_DAYS` (400 by default).

## Files under DATA_DIR

| Path | Written by | Contents |
|---|---|---|
| `hits/<cluster>/<YYYY-MM-DD>.jsonl` | Log parser | Append-only log rule hits; kept `HIT_RETENTION_DAYS` (30). |
| `state/<cluster>.json` | Log parser | The per-cluster summary pages read. |
| `state/clusters.json` | Log parser | The list of tracked clusters, restored after a restart. |
| `metrics/state.json` | Metric rules | Current metric issues. |
| `metrics/events/<YYYY-MM-DD>.jsonl` | Metric rules | Append-only log of issues opened and resolved. |
| `certs/inventory.json` | Certificate inventory | The latest scan. |
| `cost/prices.json` | Cost collector | Cached list prices: a found price for a week, a failed lookup for a day. |

!!! tip
    Put `DATA_DIR` and the SQLite file on the same persistent volume, so one disk snapshot captures all of the
    console's state. See [Backups](../operations/backups.md).
