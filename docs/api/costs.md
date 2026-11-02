# Costs

The cost worker (`internal/cost`) recomputes today for every Grafana cluster each hour and backfills
`COST_BACKFILL_DAYS` (30) past days one day per cluster every 2 minutes. Per window it runs about 12 aggregated PromQL
queries per cluster (Thanos does the aggregation) and applies OpenCost's model. See
[Cost model](../concepts/cost-model.md).

Every cost route returns `503` "workload cost is turned off (COST_ENABLED)" when cost is disabled.

**Ranges.** Routes take `?days=` (default 30, at most 400) or `?from=&to=` (`YYYY-MM-DD`, UTC). The range includes
today.

## Summary and workloads

| Method | Path | Returns |
|---|---|---|
| GET | `/api/costs?days=30` (or `from`/`to`, `cluster`) | `{from, to, total, idle, clusters[{cluster, instance, tenant, nodeCost, allocatedCost, idleCost, days, estimatedNodes, nodes, nodeTypes}], days[CostClusterDay], errors, priceErrors, lastRun, note}` |
| GET | `/api/costs/workloads?days=30&cluster=&namespace=&limit=200` | `{from, to, workloads[{cluster, namespace, kind, name, cpuCost, memCost, cpuRequestCoreHours, cpuUsageCoreHours, memRequestGiBHours, memUsageGiBHours, pods}]}` by cost |

`CostClusterDay` is the stored row: `day, cluster, instance, tenant, nodeCost, allocatedCost, idleCost, nodeHours, nodes,
estimatedNodes, nodeTypes, hours, computedAt`.

## Right-sizing

| Method | Path | Returns |
|---|---|---|
| GET | `/api/costs/recommendations?days=30&cluster=&namespace=` | `{from, to, policy, recommendations[Recommendation]}`, largest monthly saving first |
| GET | `/api/costs/recommendations?days=30&summary=1` | `{from, to, clusters}`: per-cluster totals `{monthlySaving, needsMore, count}` (positive savings only) |

```ts
type Resources = { cpuRequest: number; cpuLimit: number; memRequest: number; memLimit: number } // cores, bytes; 0 = unset
type Recommendation = {
  cluster: string; namespace: string; kind: string; name: string; container: string
  days: number; replicas: number; current: Resources; recommended: Resources
  cpuP95: number; cpuMax: number; memMax: number; oomDays: number; throttledPct: number
  confidence: "high" | "medium" | "low"; notes: string[]; monthlySaving: number
}
```

Recommendations follow Robusta KRR's simple policy: CPU request = the highest daily p95, no CPU limit; memory request
= limit = peak working set + 15% (raised to the limit × 1.25 after an OOM kill, never lowered); floors 10m / 100Mi.
Confidence: high at 14 or more days and at least 7 replica-days, medium at 7 or more days, else low. `monthlySaving` =
request change × replica-hours × the workload's own cost per core-hour and GiB-hour, scaled to 30 days (negative =
needs more). Nothing is applied.

## Cost changes

| Method | Path | Returns |
|---|---|---|
| GET | `/api/costs/changes?cluster=` | `{changes[{cluster, tenant, baselineFrom, baselineTo, recentFrom, recentTo, baselinePerDay, recentPerDay, deltaPerDay, pct, drivers[{kind, what, perDay}], summary}], rule}` |

The last 3 complete days against the 14 before them, per cluster; flagged at ±25% and ±$20/day. Drivers (`kind`:
`nodes`, `idle`, `workload`) are the change in average node count and instance types, idle capacity, and the five
workloads whose cost moved most. See [Cost changes](../concepts/cost-changes.md).

## Claude review

| Method | Path | Notes |
|---|---|---|
| GET | `/api/costs/review` | `{batch: ReviewBatch | null, reviews[{key, status, verdict, reason, cpuRequest, memRequest, memLimit, reviewedAt}]}` |
| POST | `/api/costs/review` | Admins only. `{limit?}` (default 50, max 200) → `202` with the batch; `409` while one runs or when nothing qualifies; `412` without a Claude key |

Review keys are the recommendation's cluster, namespace, kind, name and container joined by `|`. See
[Right-sizing batch review](../concepts/batch-review.md).

## Copilot

`query_costs(cluster?, namespace?, group_by: cluster|day|namespace|workload|rightsizing|changes, days)`; `days`
defaults to 7.
