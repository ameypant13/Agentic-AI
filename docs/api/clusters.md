# Clusters and explorer

Clusters are control-hub's infrastructures. The list is fetched from control-hub **with the caller's token**, so
control-hub decides which clusters a user may see; every per-cluster route first checks that the caller can read that
infrastructure. The console then reads the cluster itself with control-hub's cloud credentials. These routes only call
get, list and logs. (Pods are deleted only by executed [actions](copilot.md#actions).)

Times are RFC 3339; the UI renders ages ("3d", "4m"). `ns` empty or omitted means all namespaces.

## Types

```ts
type ClusterSummary = {
  nodes: number; nodesReady: number
  pods: number; podsNotReady: number
  cpuRequestedPct: number; memRequestedPct: number      // requests ÷ allocatable, 0–100
  cpuDetail: string; memDetail: string                  // "14.9 / 24 cores", "77 / 96 GiB"
  metricsAvailable: boolean                             // metrics-server answered
}
type Cluster = {
  id: string                 // control-hub infrastructure id
  name: string               // cluster name
  provider: "aws" | "gcp" | "azure" | string
  region: string
  kubernetesVersion: string
  status: string             // control-hub infrastructure status
  tenants: string[]          // customer names of control planes on this cluster
  summary: ClusterSummary | null   // null when the cluster could not be reached
  error: string              // "" or why the cluster could not be reached
  grafana: GrafanaMapping | null   // see Grafana, metrics and issues
}
type Node = {
  name: string; ready: boolean; status: string          // "Ready" | "NotReady" | "SchedulingDisabled"
  pool: string; instanceType: string; version: string; createdAt: string
  cpuRequestedPct: number; memRequestedPct: number; pods: number
  cpuUsage: string; memUsage: string                    // "" when metrics-server is unavailable
  pressure: string[]                                    // ["MemoryPressure", …]
}
type Pod = {
  name: string; namespace: string
  status: string             // as kubectl shows it: Running, Pending, CrashLoopBackOff, ImagePullBackOff, OOMKilled, Completed…
  ready: string              // "1/1"
  restarts: number
  cpu: string; memory: string        // current usage from metrics-server, else ""
  cpuRequest: string; memRequest: string; memLimit: string
  node: string; createdAt: string
  owner: string              // "Deployment/metagraph", "StatefulSet/postgres", ""
  images: string[]
  issue: string              // short reason worth a pill: "OOMKilled 1h ago", "ImagePullBackOff", "Unschedulable: Insufficient memory", ""
  issueSeverity: "critical" | "warning" | ""
}
type Deployment = { name: string; namespace: string; ready: string; upToDate: number; available: number; images: string[]; createdAt: string; updatedAt: string; kind: "Deployment" | "StatefulSet" | "DaemonSet" }
type KEvent = { type: "Normal" | "Warning"; reason: string; object: string; message: string; count: number; lastSeen: string; namespace: string }
type Service = { name: string; namespace: string; type: string; clusterIP: string; external: string; ports: string }
type Container = {
  name: string; image: string; ready: boolean; restarts: number
  state: string              // "Running since …", "Waiting: CrashLoopBackOff", "Terminated: Completed"
  lastTerminated: string     // "OOMKilled (exit 137) 1h ago" or ""
  cpuRequest: string; memRequest: string; cpuLimit: string; memLimit: string
}
type PodDetail = Pod & {
  labels: Record<string, string>
  conditions: { type: string; status: string; reason: string; message: string }[]
  containers: Container[]
  events: KEvent[]
  manifest: string           // YAML; managedFields removed; env values with secret-looking names masked
}
type Logs = { container: string; lines: string[]; truncated: boolean; parser: ParserResult }
```

`ParserResult` is described in [Log rules and patterns](log-rules.md#types).

## Cluster routes

| Method | Path | Response |
|---|---|---|
| GET | `/api/clusters` | `Cluster[]` (summaries computed live, cached about 60 s) |
| GET | `/api/clusters/{id}` | `Cluster & { namespaces: string[] }` |
| GET | `/api/clusters/{id}/nodes` | `Node[]` |
| GET | `/api/clusters/{id}/pods?ns=` | `Pod[]` |
| GET | `/api/clusters/{id}/workloads?ns=` | `Deployment[]` (deployments, statefulsets, daemonsets) |
| GET | `/api/clusters/{id}/events?ns=` | `KEvent[]`, newest first, max 300 |
| GET | `/api/clusters/{id}/services?ns=` | `Service[]` |
| GET | `/api/clusters/{id}/pods/{ns}/{name}` | `PodDetail` |
| GET | `/api/clusters/{id}/pods/{ns}/{name}/logs?container=&sinceMinutes=360&tail=2000&previous=false` | `Logs`, with the log parser's result over the same lines |

control-hub errors are passed on: `401` "control-hub rejected your session; sign in again", `403` "control-hub says you
can't read infrastructures", `404` "cluster not found", `502` "could not reach control-hub". Imported and SAP clusters
return a "metrics only" error for the Kubernetes routes.

## Full explorer (any resource kind)

Every resource type the cluster serves is browsable through two generic routes that use the API server's own table
output (the same columns `kubectl get` prints, custom resources included). A resource is addressed by its **key**
`<group|core>/<version>/<resource>` (for example `core/v1/pods`, `apps/v1/deployments`,
`cert-manager.io/v1/certificates`).

Never shown: Secret values (keys are listed with their size), ConfigMap values under secret-looking keys or containing
credentials, env values with secret-looking names, `managedFields`, the last-applied annotation, Helm release values.
There is no exec, port-forward, edit or delete.

```ts
type APIResource = {
  key: string; group: string; version: string; resource: string; kind: string
  namespaced: boolean
  category: "Cluster" | "Workloads" | "Config" | "Network" | "Storage" | "Access Control" | "Custom Resources" | "Other"
}
type ResourceTable = {
  columns: { name: string; type: string; priority: number }[]   // priority 0 = default; > 0 = "wide" only
  rows: { name: string; namespace: string; createdAt: string; cells: string[] }[]  // cells align with columns
}
type ResourceDetail = {
  kind: string; apiVersion: string; name: string; namespace: string; createdAt: string
  labels: Record<string, string>; annotations: Record<string, string>
  owners: { kind: string; name: string; key: string }[]   // key "" when not linkable
  selector: string            // label selector for workloads/services, "" otherwise
  pods: Pod[]                 // pods the workload/service selects ([] otherwise)
  events: KEvent[]
  yaml: string                // masked
}
// `pods` here is the list of pods on the node (it replaces Node's pod count; use pods.length).
type NodeDetail = Omit<Node, "pods"> & {
  labels: Record<string, string>; taints: string[]; addresses: string[]
  conditions: { type: string; status: string; reason: string; message: string }[]
  capacity: Record<string, string>; allocatable: Record<string, string>
  os: string; kernel: string; runtime: string; providerId: string; unschedulable: boolean
  pods: Pod[]; events: KEvent[]
}
type HelmRelease = { name: string; namespace: string; chart: string; chartVersion: string; appVersion: string
  revision: number; status: string; updated: string; description: string }
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/clusters/{id}/api-resources` | `APIResource[]` (cached 10 min) |
| GET | `/api/clusters/{id}/resources/{group}/{version}/{resource}?ns=` | `ResourceTable` (max 2000 rows) |
| GET | `/api/clusters/{id}/resources/{group}/{version}/{resource}/{name}?ns=` | `ResourceDetail` (`ns` required for namespaced kinds) |
| GET | `/api/clusters/{id}/nodes/{name}` | `NodeDetail` |
| GET | `/api/clusters/{id}/helm-releases?ns=` | `HelmRelease[]` (latest revision of each) |

## Copilot cluster tools

The Copilot reads the same clusters through read-only tools, with the same per-user check (the caller's token lists
`/api/infrastructures`; a cluster is matched by ID, control-hub name or Grafana cluster name):

- `k8s_list_nodes`
- `k8s_list_pods(namespace?, filter?, problems_only?, sort: name|cpu|memory|restarts)`
- `k8s_describe_pod(namespace, pod, include_manifest?)`
- `k8s_pod_logs(namespace, pod, container?, previous?, tail?, since_minutes?, grep?)`
- `k8s_events(namespace?, warnings_only?, object?)`
- `k8s_get(kind, namespace?, name?)` for any other kind
- `change_timeline(target, hours?)` (tenant, control plane or cluster): merges, oldest first, the cluster's Helm release
  revisions, Deployment rollouts (image diffs; rollout restarts labelled), StatefulSet and DaemonSet revisions and new
  nodes with the console's own findings (appeared, resolved) and investigations for that tenant.

Results are capped (150 rows, 500 log lines, 24 KiB of YAML, 60 KiB in all); logs and event messages are masked, and
Secret values never leave the cluster. Imported and SAP clusters return a "metrics only" error.
