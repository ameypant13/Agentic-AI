# Monitors

**Route:** `/monitors` · "Findings raised by scheduled checks, and the checks themselves."

The Monitors page shows every finding the monitors raised (in any state) and the monitor definitions. How monitors and
findings work is in [Findings and monitors](../concepts/monitors.md).

## Findings

Choose **Open**, **Resolved** or **All** in **State**.

| Column | Shows |
|---|---|
| Severity | critical or warning |
| Tenant | The entity, with provider · realm · region |
| Finding | Title and detail |
| State | open, acknowledged or resolved (with the resolved time) |
| First seen, Last seen | When the monitor first and last saw it |
| *(link)* | **View RCA** when an investigation is linked |

"Nothing needs attention" means there are no open findings right now. To investigate or acknowledge a finding, use
the [Fleet overview](fleet.md).

## Monitor definitions

"Scheduled checks. A hit opens a finding on Fleet overview."

| Column | Shows |
|---|---|
| Monitor | Name and key (`cp_failed`, `cp_stuck`, `postdeploy_failed`, `ep_failed`, `log_errors`, `cert_expiry`) |
| Condition | What the monitor looks for, with the configured thresholds (`STUCK_AFTER`, `CERT_WARN_DAYS`) |
| Every | `MONITOR_INTERVAL` |
| Notify | "Slack", or "Console only" when Slack is not configured or muted |
| Status | Enabled or Disabled (`MONITOR_ENABLED`) |
