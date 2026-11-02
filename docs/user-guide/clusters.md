# Clusters

**Route:** `/clusters` · "Every Kubernetes cluster control-hub knows about, read-only. Managed clusters open in the
explorer; the rest show Grafana's metrics."

The Clusters section is a read-only, Lens-style explorer over every cluster control-hub manages. The cluster list
comes from control-hub **with your token**, so you see exactly the clusters control-hub lets you read. The console
then reads each cluster with control-hub's cloud credentials using only get, list and logs: the explorer has no exec,
port-forward, edit or delete. (A Copilot [action](../concepts/actions.md) you click can restart a pod or delete
orphaned go-sandboxes.)

## The cluster list

| Column | Shows |
|---|---|
| Cluster | Name (opens the cluster) |
| Tenant | Tenants (control planes) on the cluster |
| Cloud · region | AWS, GCP or Azure, and the region |
| Nodes | Ready / total |
| CPU req., Mem req. | Requests ÷ allocatable; amber above 80% |
| Pods | Total, with "(n!)" for pods not ready |
| Status | control-hub's status ("ready" for OPERATIONAL), "Unreachable" (hover for the error), "Gone" or "loading…" |

Clusters are grouped:

| Section | What is in it |
|---|---|
| Managed | Clusters control-hub has credentials for: the full explorer. |
| Imported | No cluster credentials in control-hub: metrics from Grafana only. |
| SAP (GKE) | One row per SAP tenant ("N CPs · M EPs"), expanding to its control plane and execution plane clusters; metrics from Grafana. |
| Gone from the cloud | control-hub still lists these, but the cloud says they no longer exist. Collapsed by default. |

The list is sorted with unreachable clusters first, then clusters with nodes not ready, then pods not ready, then the
highest CPU. Filter by text (cluster, tenant, region) or by cloud. Summaries are computed live and cached for about a
minute; clusters still loading are polled for a few seconds.

## Inside a cluster

Opening a managed cluster shows:

- a **left rail** of cluster initials to switch clusters (a red dot marks an unreachable one); switching keeps you on
  the same list;
- a **resource tree** with sections Cluster (Overview, Nodes, Namespaces, Events), Workloads (Overview, Pods,
  Deployments, DaemonSets, StatefulSets, ReplicaSets, Jobs, CronJobs), Config, Network, Storage, Access Control, Helm
  (Releases), Custom Resources (Definitions and one entry per API group) and Other. Filter kinds with <kbd>/</kbd>;
- a **header** with the cluster's status, Kubernetes version and tenant.

Namespace-scoped pages open on the `prophecy` namespace when the cluster has one, otherwise all namespaces. Pick
another in the **Namespace** dropdown.

**Metrics-only clusters** (imported and SAP) show: "**Metrics only.** control-hub keeps no credentials for this
cluster, so nodes, pods, logs and events can't be browsed here." followed by the summary tiles, issues and charts from
Grafana.

### Overview

- **Tiles:** CPU requested, Memory requested, Pods (not ready / all ready), Nodes ready.
- **Issues · N active:** the cluster's metric issues (refreshed every 30 s), with those resolved in the last 24 hours
  below. Without a Grafana mapping: "Not connected to Grafana"; admins get a link to map it.
- **Charts:** CPU used vs allocatable, memory used / requested / allocatable, over 1 h, 6 h, 24 h or 7 d.
- **Warnings:** pods with an issue (OOMKilled, ImagePullBackOff, unschedulable, restarts), log parser hits in
  `prophecy` in the last hour, then the latest Warning events.

### Nodes, Pods, Workloads, Events, Services

| Tab | Shows |
|---|---|
| Nodes | Status, pool, instance type, CPU and memory requested, live usage (when metrics-server answers), pods, pressure, version, age. |
| Pods | kubectl-style status, ready, restarts, a **K8s issue** pill, **Log issues** from the log parser, CPU, memory, node, age. Refreshed every 15 s. |
| Workloads | Deployments, StatefulSets and DaemonSets with ready, up-to-date, images, last update. |
| Events | Newest first, at most 300. Kubernetes keeps events for about an hour. |
| Services | Type, cluster IP, external address ("&lt;pending&gt;" for a LoadBalancer with none), ports. |

### Any resource kind

Every kind the cluster serves, custom resources included, is browsable through the tree. Lists use the API server's
own table output (the same columns `kubectl get` prints); **Wide** adds the extra columns. Lists show at most 2,000
rows. A resource's page has **Overview** (labels, annotations, owners, selector and its pods), **YAML** (searchable,
with copy) and **Events**.

Never shown: Secret values (keys are listed with their size), ConfigMap values under secret-looking keys or containing
credentials, env values with secret-looking names, `managedFields`, the last-applied annotation and Helm release
values.

### Helm releases and the workloads overview

- **Helm → Releases** lists each release's latest revision: chart, chart version, app version, revision, status and
  last update. Release values are never read.
- **Workloads → Overview** shows healthy/total counts per workload kind and a **Not ready** list of workloads below
  their desired count, failed jobs and pods that are not running and ready.

## The pod page

**Route:** `/clusters/<id>/pods/<namespace>/<name>`

| Tab | Shows |
|---|---|
| Overview | Namespace, node, owner, images, requests and limits; the pod's metric issues; charts (CPU and memory per container, memory limit, restarts, JVM heap); the **Log parser** card (known patterns in the last 6 h, up to 2,000 lines); containers; conditions. |
| Logs | Up to 2,000 lines. Choose the container and **Since** (15 min, 1 h, 6 h, 24 h), toggle **Previous container**, **Only parser hits** and **Wrap lines**, and search. Matched lines are tinted by severity and tagged with rule IDs. |
| Manifest | The pod's YAML with secret values masked, and a copy button. |
| Events | The pod's events. |

## The node page

**Route:** `/clusters/<id>/nodes/<name>`

Requested and live usage, charts, system info (pool, instance type, kubelet, OS, kernel, runtime, provider ID),
conditions, capacity vs allocatable, taints, labels, the pods on the node and its events.

## Common tasks

### Find a crashing pod and its error

1. Open the cluster. In **Overview → Warnings**, look for pods with a red issue pill such as "OOMKilled 1h ago" or
   "CrashLoopBackOff".
2. Click the pod, then **Logs**.
3. Turn on **Previous container** to read the run that crashed.
4. Turn on **Only parser hits**, or click a rule chip in the **Parser** bar, to see only lines a log rule knows.
5. Click a rule ID to read its meaning and fix.

### Check whether a cluster has room

1. Look at the **CPU requested** and **Memory requested** tiles.
2. Open **Nodes** for per-node requests and pressure.
3. Open **Charts** with a 7 d range to see the trend.

### Look at a custom resource

1. Filter the tree with <kbd>/</kbd> and type the kind (for example `certificates`).
2. Pick the kind under **Custom Resources**, then the object.
3. Read its **YAML** and **Events**.
