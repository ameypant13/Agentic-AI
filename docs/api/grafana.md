# Grafana, metrics and issues

Each Grafana instance (AWS `monitoring.prophecy.io`, Azure `azure.monitoring.prophecy.io`, …) is read with a Viewer
service-account token. Instances come from the deployment secret (`GRAFANA_<NAME>_URL` + `GRAFANA_<NAME>_TOKEN`) or
are added by admins in Settings (token stored encrypted). A control-hub cluster is matched to an instance's `cluster`
label (metrics) and `k8s_cluster_name` label (Loki) automatically; admins can override a mapping.

The **metrics parser** evaluates PromQL rules (`internal/metricrules/rules.yaml`) every 60 s, one query per rule per
instance; each returned series is an issue. Issues open, stay open while the rule keeps returning them, and resolve
when it stops (kept 24 h).

## Types

```ts
type GrafanaMapping = { instance: string; promCluster: string; lokiCluster: string; override: boolean }
// Cluster (from GET /api/clusters and /api/clusters/{id}) has:  grafana: GrafanaMapping | null
type MetricIssue = {
  instance: string; cluster: string; ruleId: string; title: string
  severity: "error" | "warning" | "info"
  entity: "container" | "pod" | "service" | "deployment" | "pvc" | "node" | "cluster"
  entityKey: string; entityName: string; namespace: string; pod: string
  labels: Record<string, string>; value: number; valueText: string   // "95%", "6", "1.2 s", "9.9/s", ""
  meaning: string; fix: string
  firstSeen: string; lastSeen: string; active: boolean; resolvedAt?: string
}
type ClusterIssues = { grafana: GrafanaMapping | null; evaluatedAt: string | null; metrics: MetricIssue[] }
type FleetIssue = {
  source: "metrics" | "logs"; clusterId: string; clusterName: string; ruleId: string; title: string
  severity: "error" | "warning" | "info"; entity: string; entityName: string; namespace: string; pod: string
  valueText: string; meaning: string; fix: string; firstSeen: string; lastSeen: string; active: boolean
}
type MetricRule = { id: string; title: string; severity: string; entity: string; value: string; expr: string; meaning: string; fix: string }
type ChartSeries = { name: string; unit: "cores" | "bytes" | "count"; labels: Record<string, string>; points: [number, number][] } // [unix s, value]
type ClusterMetrics = { available: boolean; instance?: string; cluster?: string; from?: string; to?: string; stepSeconds?: number; series: ChartSeries[] }
type GrafanaInstance = { name: string; url: string; source: "secret" | "settings"; clusters: string[]; logClusters: number
  checkedAt: string; error: string; evaluatedAt: string | null; ruleErrors: string[] }
```

## Routes

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/issues` | — | `FleetIssue[]`: active metric issues and active log parser hits on every cluster the caller can see |
| GET | `/api/clusters/{id}/issues` | — | `ClusterIssues` (active first, then resolved in the last 24 h) |
| GET | `/api/clusters/{id}/metrics?panel=cluster|pod|node&ns=&pod=&node=&range=6h` | — | `ClusterMetrics`. Panels: cluster = CPU used/allocatable, memory used/requested/allocatable; pod = CPU and memory per container, memory limit, restarts, JVM heap used/max; node = CPU, memory, allocatable. `range` is a Go duration (max 30 d). `available: false` when the cluster has no Grafana mapping. |
| GET | `/api/metric-rules` | — | `MetricRule[]` |
| GET | `/api/grafana` | — | `{instances: GrafanaInstance[], overrides: {cluster, instance, promCluster, lokiCluster, updatedBy, updatedAt}[]}` |
| POST | `/api/grafana/instances` | `{name, url, token}` | `{}`. Admins only. `url` must start with `https://`; the token is checked against Grafana first (it needs a Prometheus datasource). `409` if a secret-defined instance has the name. |
| DELETE | `/api/grafana/instances/{name}` | — | `{}`. Admins only; only Settings-added instances. |
| POST | `/api/grafana/refresh` | — | Same as `GET /api/grafana`, after reloading cluster names |
| PUT | `/api/grafana/mappings/{cluster}` | `{instance, promCluster, lokiCluster}` | `GrafanaMapping`. Admins only; override for a control-hub cluster name. |
| DELETE | `/api/grafana/mappings/{cluster}` | — | The automatic mapping again. Admins only. |

## Copilot Grafana tools

Read-only, using the same cluster → Grafana mapping:

| Tool | Does |
|---|---|
| `grafana_query_metrics` | PromQL against the cluster's Thanos (write `cluster="$cluster"` in selectors). |
| `grafana_query_logs` | Loki search narrowed by namespace and pod name prefix. |
| `grafana_list_metrics(cluster, search?)` | Metric names seen in the last hour. |
| `grafana_label_values(cluster, metric?, label?, search?)` | Label names of a metric, or a label's values. |
| `grafana_alerts(cluster, state?, search?)` | Prometheus `ALERTS` deduplicated across replicas plus Grafana-managed rule instances labelled with the cluster; firing first, then by severity. |
| `grafana_dashboards(cluster, search? | uid, panel?)` | Dashboard search, or one dashboard's variables and panel queries. |

They are also served by `control-hub-mcp` when Grafana is configured.
