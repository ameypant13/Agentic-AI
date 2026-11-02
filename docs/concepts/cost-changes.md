# Cost changes

Cost changes flag clusters whose daily cost moved well away from its usual level, and say what moved it. It is plain
arithmetic over the stored cost days (`internal/cost/changes.go`). No AI is involved until someone clicks
**Ask Copilot why**.

## The rule

For every cluster:

- **Recent window**: the last **3** complete days (today is partial, so the windows end yesterday).
- **Baseline window**: the **14** days before them.
- Compare the average cost per day in each window.
- Flag the cluster when the change is **at least ±25%** and **at least ±$20 a day**.

The API returns the rule text with the results (`GET /api/costs/changes` → `rule`), and the Costs page shows it next
to the **Cost changes** card.

## Drivers

Each flagged change lists what moved it, in dollars a day:

| Driver kind | What it measures |
|---|---|
| `nodes` | The change in the average node count and in instance types. |
| `idle` | The change in idle capacity (node cost no workload requested or used). |
| `workload` | The five workloads whose cost moved most, including new and removed workloads, with pod counts. |

Each change also has a one-line `summary`, the two windows' date ranges, the baseline and recent cost per day, the
difference and the percentage.

## Finding the cause

The arithmetic says *what* changed, not *why*. **Ask Copilot why** sends the change to the Copilot with instructions to
use `query_costs` (grouped by `changes` and `workload`) and `change_timeline` for the cluster, and to say whether the
change is expected. `change_timeline` lines the cost change up with Helm release revisions, Deployment rollouts, new
nodes and the console's own findings and investigations.

## Where it shows up

- The **Cost changes** card on the [Costs](../user-guide/costs.md) page (up to six clusters), and "Cost changed
  recently" for a single cluster.
- The [weekly digest](digest.md)'s cost section.
- The Copilot's `query_costs` with `group_by: changes`.
