# Metric rules

The **metrics parser** (`internal/metricrules`) evaluates PromQL rules against every Grafana instance, through
Grafana's Prometheus (Thanos) datasource. Like the log parser it is rules, not AI.

## How it works

- Rules live in `internal/metricrules/rules.yaml`, embedded in the image. They are read-only in the UI.
- Every interval (the worker uses `LOG_SCAN_INTERVAL`, default 60 s) the worker runs **one query per rule per Grafana
  instance**. The query covers every cluster the instance monitors.
- An expression returns **only the series that are a problem**. Each returned series is one active **issue**.
- An issue opens when a series first appears, stays open while the rule keeps returning it, and resolves when it
  stops. Resolved issues are kept for 24 hours.
- A rule whose query fails keeps its previous issues rather than resolving them, and the failure is shown as a rule
  error on the Grafana settings page.
- State is saved to `DATA_DIR/metrics/state.json`; every open and resolve is appended to
  `DATA_DIR/metrics/events/<day>.jsonl`.

Issues are matched to control-hub clusters through the [Grafana mapping](../user-guide/settings.md#grafana): the
instance's `cluster` label for metrics.

## Rule format

```yaml
version: 1
rules:
  - id: container-memory-near-limit
    title: Memory near its limit
    severity: warning          # error | warning | info
    entity: container          # container | pod | service | deployment | pvc | node | cluster
    value: percent             # percent | count | seconds | rate | none
    expr: |
      max by (cluster, namespace, pod, container) (container_memory_working_set_bytes{container!="", container!="POD"})
        / on (cluster, namespace, pod, container)
      max by (cluster, namespace, pod, container) (kube_pod_container_resource_limits{resource="memory"}) > 0.9
    meaning: The container uses over 90% of its memory limit and will be OOM-killed if it keeps growing.
    fix: Check for a leak or a heavy job in its logs; raise the memory limit if the load is legitimate.
```

Every expression must keep the `cluster` label, plus whatever identifies the entity (namespace, pod, container, node,
`persistentvolumeclaim`, deployment, job). `value` says how to show the series value ("95%", "6", "1.2 s", "9.9/s").

## The shipped rules

| ID | Title | Severity |
|---|---|---|
| `container-memory-near-limit` | Memory near its limit | warning |
| `container-oomkilled` | Container OOM-killed | error |
| `container-restarting` | Restarting repeatedly | warning |
| `pod-crashloop` | CrashLoopBackOff | error |
| `image-pull-failing` | Image pull failing | error |
| `pod-not-running` | Pod stuck Pending | warning |
| `deployment-replicas-unavailable` | Deployment missing replicas | warning |
| `jvm-heap-high` | JVM heap above 90% | warning |
| `jvm-gc-time-high` | Spending >20% of time in GC | warning |
| `http-5xx-rate-high` | HTTP 5xx error rate above 5% | error |
| `http-p95-slow` | Slow requests (p95 above 5 s) | warning |
| `akka-dead-letters` | Akka dead letters piling up | warning |
| `pvc-almost-full` | Volume almost full | warning |
| `node-not-ready` | Node not ready | error |
| `node-pressure` | Node under pressure | error |
| `cluster-memory-requests-high` | Cluster memory almost fully requested | warning |

The [Metric rules](../user-guide/metric-rules.md) page shows each rule's PromQL, meaning and fix.

## Where issues show up

- **Issues** page and the sidebar badge: active metric issues plus active log parser hits on every cluster the user
  can see (`GET /api/issues`).
- **Fleet overview**: the top five.
- **Cluster overview** and **pod overview**: the cluster's or pod's issues, active first, then those resolved in the
  last 24 hours.
- The Copilot's `current_issues` tool.

To add or change a metric rule, edit `rules.yaml` and ship a new image; see
[Development](../development/adding-a-log-rule.md#metric-rules).
