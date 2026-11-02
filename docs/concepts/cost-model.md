# Cost model

The console estimates what each Kubernetes workload costs, from the metrics in Grafana (Thanos) and cloud list
prices, using **OpenCost's allocation model**. It then recommends request and limit changes using **Robusta KRR's**
simple right-sizing policy. Everything is read-only: nothing is ever applied to a cluster.

!!! note "Estimates"
    Costs are compute only, at on-demand list prices. They do not include discounts, savings plans, storage, network
    or managed-service charges. The Copilot is told to say so whenever it quotes them.

## Collection

The cost collector (`internal/cost`) runs when `COST_ENABLED=true` (the default):

- **Today** (UTC midnight to now) is recomputed for every Grafana cluster **every hour**, starting a minute after
  start-up.
- **Past days** are backfilled for `COST_BACKFILL_DAYS` (30): for each cluster, its most recent past day that is
  missing (or was stored before the day ended) is computed, **one day per cluster every 2 minutes**. This keeps a fresh
  install from hitting Thanos with a month of queries at once. When nothing is missing it looks again an hour later.
- Per window it runs about 12 aggregated PromQL queries per cluster; Thanos does the aggregation and the console
  stores one row per workload per day.
- Rows older than `COST_RETENTION_DAYS` (400) are deleted.

Clusters are the Grafana metrics clusters (the `cluster` label). They are mapped back to control-hub cluster names
where known, so the UI can show the tenant.

## Prices

| Cloud | Instance type from | Price from |
|---|---|---|
| AWS | node-exporter's DMI info | AWS Pricing API (on-demand Linux), with `AWS_PROD_*`. Needs `pricing:GetProducts` on control-hub's AWS user. |
| Azure | node labels | Azure Retail Prices API (Linux pay-as-you-go hourly). Public, no credentials. |
| Anything unknown | — | OpenCost's default rates. |

Prices are cached in `DATA_DIR/cost/prices.json`: a found price for a week, a failed lookup for a day. Failed lookups
are listed on the Costs page (`priceErrors`), and nodes priced with default rates are counted as `estimatedNodes`.

OpenCost's default rates: **$0.031611 per vCPU-hour** and **$0.004237 per GiB-hour**.

## Allocation

For each window (a day, or today so far):

1. **Node rates.** A node's hourly list price is split into a per-core and a per-GiB rate in OpenCost's default
   CPU:RAM ratio:

    ```text
    cpuShare = cores × 0.031611 / (cores × 0.031611 + GiB × 0.004237)
    cpu rate = price × cpuShare / cores
    ram rate = price × (1 − cpuShare) / GiB
    ```

    A node with no known price, core count or memory uses the default rates.

2. **Node cost** = (cpu rate × cores + ram rate × GiB) × hours the node existed.
3. **Pod cost.** Each pod is charged for the **larger of what it requested and what it used**, for CPU and memory
   separately, at its node's rates. Pods whose node is unknown pay the cluster's average rates.
4. **Workloads.** Pods are summed into their controlling workload: a ReplicaSet's Deployment, a Job's CronJob,
   otherwise the direct owner; a bare pod is its own workload.
5. **Idle** = node cost − allocated cost (never below zero): capacity no workload requested or used.

## Right-sizing (KRR policy)

With each day's cost the collector stores, per workload container (the largest replica), the day's 95th-percentile CPU,
the CPU and memory peaks, current requests and limits, whether it was OOM-killed, and the share of CFS periods it was
throttled (`CostContainerDay`). Recommendations are computed over the chosen range:

| Resource | Recommendation |
|---|---|
| CPU request | The **highest daily p95** in the range (slightly above a true range-wide p95, which errs towards headroom), at least **10m**. |
| CPU limit | **None.** Limits only throttle; the request is what the scheduler reserves. |
| Memory request and limit | **Peak working set + 15%**, at least **100Mi**, rounded up to 16 MiB. After an OOM kill: at least the current limit × 1.25, **never lowered**. With no memory data: unchanged. |

Notes are added when a CPU limit is removed (and whether it was throttling 10% or more of periods), when the container
was OOM-killed, when peak memory is within 15% of the limit (often a JVM heap filling up: change `-Xmx` together with the
limit), and when there is little data.

**Confidence**

| Confidence | Data |
|---|---|
| high | 14 or more days, and at least 7 replica-days (168 pod-hours) |
| medium | 7 or more days |
| low | fewer than 7 days |

**Monthly saving** = (current request − recommended request) × replica-hours × the workload's own cost per requested
core-hour and GiB-hour, scaled from the days observed to a 30-day month. A negative value means the workload needs
more (it is under-sized).

Prophecy services are installed with Helm, so the console never applies a recommendation. The UI offers the chart
values to copy and a Copilot review; admins can also run a [batch review](batch-review.md).

## Where costs show up

- The [Costs](../user-guide/costs.md) page.
- The Copilot's `query_costs(cluster?, namespace?, group_by, days)` with `group_by` one of `cluster`, `day`,
  `namespace`, `workload`, `rightsizing`, `changes`.
- [Cost changes](cost-changes.md) and the [weekly digest](digest.md).
- The "Memory history" [readiness check](readiness.md), which uses OOM kills from right-sizing data.
