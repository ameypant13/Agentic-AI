# Costs

**Route:** `/costs` · "Estimated compute cost per cluster and workload at on-demand list prices, with right-sizing from
actual use."

The Costs page shows what each cluster and workload costs, where capacity sits idle, which workloads could ask for
less (or need more), and which clusters' costs changed recently. How the numbers are computed is in
[Cost model](../concepts/cost-model.md).

!!! note
    These are estimates: compute only, at on-demand list prices. A new install fills in today first, then the past
    days one day at a time. "No cost data yet" means the cost worker has not stored anything; check back in an hour.

## Layout

- **Range:** **7 days**, **30 days** (default) or **90 days** at the top right.
- **Cluster rail** on the left: **All clusters** with the fleet total, then each cluster with its cost, tenant, idle
  share, and either "save $X" or "N need more". A warning icon means the last cost run for that cluster failed. Filter
  clusters by name.
- **Review with Claude** (admins) at the top right; see [below](#review-recommendations-with-claude-admins).

### All clusters

| Tile | Shows |
|---|---|
| Compute, N days | Total node cost for the range |
| Idle | Share and cost no workload used |
| Could save | Monthly saving from right-sizing requests |
| Need more | Containers that were OOM-killed, throttled or short of their request |

Below the tiles:

- **Cost changes**: clusters whose recent daily cost moved by at least 25% and $20 a day, with the drivers and an
  **Ask Copilot why** button. Hidden when nothing changed. See [Cost changes](../concepts/cost-changes.md).
- **Daily cost**: stacked bars of cost used by workloads and idle cost. Faded bars are days still in progress or
  still filling in.
- **Clusters** table: cost, idle, "Could save / mo" and "Need more" per cluster. Click a row to open that cluster.

### One cluster

The cluster's name, tenant, Grafana instance, node count and instance types; a banner if the last run failed, or if
some nodes are priced at OpenCost's default rates; the same tiles, "Cost changed recently", the daily chart, and the
**Workloads** table.

## The Workloads table

| Column | Shows |
|---|---|
| Workload | Name, namespace, kind, number of containers; pills for **OOM**, **throttled** and Claude's worst verdict |
| Cost | Cost over the range |
| CPU request | Current → recommended |
| Memory request | Current → recommended |
| Saves / mo | Monthly saving, "needs +$X", or "—" |

Filter by **namespace**, show **All**, **Savings** or **Needs more**, and sort by cost or saving. The filters are kept in
the URL. At most 200 rows are shown.

Click a workload to expand it:

- usage over the range (core-hours and GiB-hours requested and used, CPU and memory cost);
- one card per container with the confidence (high, medium, low), OOM days, throttling, days and replicas;
- **Used** (CPU p95 and max, memory peak), **Now** (request / limit) and **Recommended**; changed values are bold and
  "no limit" means no CPU limit;
- notes (for example "Peak memory is within 15% of the limit: this is often a JVM heap…");
- Claude's review, if one ran: **safe**, **adjust** (with the values it suggests) or **risky**, and why;
- **Chart values**: the Helm `resources:` snippet.

Bare pods and one-off jobs have no recommendation, and new workloads need a day of data.

## Common tasks

### Find the cheapest savings

1. Stay on **All clusters**, range **30 days**.
2. Look at **Could save** and the **Clusters** table; click the cluster with the largest "Could save / mo".
3. In **Workloads**, choose **Savings** and sort by **saving**.
4. Expand a workload. Check the confidence is high or medium, and read the notes.
5. Click **Ask Copilot** on the container for a second opinion (it checks JVM heap settings, spikes and restarts).
6. Click **Copy values** and set them in the control plane's Helm chart values.

!!! warning "Apply through Helm"
    Prophecy services are installed with Helm: set these in the control plane's chart values. Editing the Deployment
    directly restarts its pods and is reverted on the next upgrade.

### Fix under-sized workloads first

1. Choose **Needs more**. These containers were OOM-killed, throttled or short of their request.
2. Expand each one. After an OOM kill, memory is never lowered; the recommendation raises the limit.
3. Apply the new values through Helm before the next upgrade (the "Memory history" readiness check warns about OOM
   kills).

### Explain a cost change

1. In **Cost changes**, read the drivers (node count and types, idle, the workloads that moved most).
2. Click **Ask Copilot why**. The Copilot uses `query_costs` and `change_timeline` to find what caused it and says
   whether it is expected.

### Review recommendations with Claude (admins)

1. Click **Review with Claude**. Claude reviews the 50 most valuable recommendations (and every shortfall) through the
   Batch API, at half price, with your Claude key.
2. The button shows progress ("Claude reviewing: n/N"). Results usually arrive within minutes; the page checks every
   minute.
3. Verdicts appear on each container card and as a pill on the workload row.

See [Right-sizing batch review](../concepts/batch-review.md).
