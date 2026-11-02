# Issues

**Route:** `/issues` · "Active problems the metrics parser (Grafana) and the log parser see on every cluster you can
access. Rules, not AI."

The Issues page lists every **active** issue on the clusters control-hub lets you see:

- **metrics** issues: a [metric rule](../concepts/metric-rules.md) returned a series for the entity;
- **logs** issues: the [log parser](../concepts/log-rules.md) matched a rule in a pod's recent lines.

Issues are not [findings](../concepts/monitors.md): they have no acknowledge step and disappear when the rule stops
matching. The page refreshes every 30 seconds (the sidebar badge uses the same data); **Refresh** reloads now.

## Reading the page

Issues are grouped into one card per cluster, clusters with the most errors first. Click a cluster's name to collapse
or expand it; **Cluster overview** opens the cluster.

| Column | Shows |
|---|---|
| Severity | error, warning or info |
| Source | metrics or logs |
| Issue | The rule's title and `namespace / entity` |
| Value | The series value ("95%", "6", "1.2 s") for metric issues |
| Since | How long it has been active |
| *(button)* | **Open** goes to the pod, node or cluster |

Click a row to see the rule's **Meaning** and **Fix**, the rule ID (a link to [Log rules](log-rules.md) or
[Metric rules](metric-rules.md)) and when it was last seen.

## Filtering

All filters are kept in the URL, so you can share a filtered view.

| Filter | Options |
|---|---|
| Search | Title, entity, namespace, rule, cluster, meaning or value |
| Severity | All, Error, Warning |
| Source | All, Metrics, Logs |
| Cluster | All clusters, or one |

## Common tasks

### Find out why a pod keeps failing

1. Filter **Severity: Error** and search for the pod or service name.
2. Click the row to read the meaning and fix.
3. Click **Open** to go to the pod, then open **Logs** with **Only parser hits** on, or **Previous container** for the
   crashed run.
4. If the cause is still unclear, ask the Copilot from the command palette with the pod and cluster names.

### Check one cluster

Pick it in **Cluster**, or open the cluster's overview, which lists the same issues plus resolved ones from the last
24 hours.

!!! note "Empty page"
    "No active issues" means neither parser sees a problem on any cluster you can access. Metric issues need the
    cluster to be mapped to a Grafana instance (see [Settings](settings.md#grafana)).
