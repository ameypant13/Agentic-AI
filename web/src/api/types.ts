// Types mirror ai-console/API.md exactly. Times are RFC 3339 strings, IDs are strings.

// ---- Auth ----
export type KeySource = "personal" | "shared" | "none";

export type ClaudeKey = {
  /** the user stored a personal key */
  configured: boolean;
  /** "" when not configured */
  last4: string;
  /** which key the next chat will use */
  source: KeySource;
};

export type Me = {
  email: string;
  username: string;
  isAdmin: boolean;
  claudeKey: ClaudeKey;
  /** the console can change nothing (Copilot actions off, execute returns 403) */
  readOnly: boolean;
  /** which control-hub this console talks to, e.g. "control-hub-prod" */
  controlHub: string;
};

// ---- Fleet & findings ----
export type Fleet = {
  controlPlanes: {
    total: number;
    byStatus: Record<string, number>;
    byRealm: Record<string, number>;
  };
  openFindings: { critical: number; warning: number };
  investigations7d: number;
  lastMonitorRun: string | null;
};

export type FindingKind = "cp_failed" | "cp_stuck" | "postdeploy_failed" | "ep_failed" | "cert_expiry";
export type Severity = "critical" | "warning";
export type FindingState = "open" | "acknowledged" | "resolved";
export type FindingStateFilter = "open" | "resolved" | "all";

export type Finding = {
  id: string;
  kind: FindingKind;
  severity: Severity;
  /** one line, e.g. "Provisioning failed" */
  title: string;
  /** one or two sentences */
  detail: string;
  entityType: "control_plane" | "execution_plane";
  entityId: string;
  /** customer / tenant name */
  entityName: string;
  realm: string;
  provider: string;
  region: string;
  state: FindingState;
  firstSeenAt: string;
  lastSeenAt: string;
  resolvedAt: string | null;
  investigationId: string | null;
  /** email of whoever acknowledged it; empty when open or unknown */
  acknowledgedBy: string;
  acknowledgedByName: string;
  acknowledgedAt: string | null;
};

export type InvestigateResponse = { chatId: string; prompt: string };

export type Monitor = {
  key: string;
  name: string;
  condition: string;
  interval: string;
  notify: string;
  enabled: boolean;
};

// ---- Copilot chats ----
export type ChatSummary = { id: string; title: string; createdAt: string; updatedAt: string };

/** tool / input / summary are present only for admins */
export type Step = {
  id: string;
  label: string;
  ok: boolean | null;
  tool?: string;
  input?: string;
  summary?: string;
};

export type ActionKind = "retry_cp_postdeploy" | "retry_ep_postdeploy" | "restart_pod" | "delete_orphaned_sandboxes";
export type ActionStatus = "proposed" | "running" | "succeeded" | "failed" | "dismissed";

export type Action = {
  id: string;
  kind: ActionKind;
  /** button text, e.g. "Retry post-deploy" */
  title: string;
  /** why, in one sentence */
  description: string;
  targetName: string;
  status: ActionStatus;
  /** "" until executed */
  result: string;
  /** the dry run computed when it was proposed; null for actions older than dry runs */
  plan: ActionPlan | null;
};

export type ActionPlan = {
  summary: string;
  /** exact operations, as commands or API calls */
  steps: string[];
  /** what the dry run read and found */
  checks: string[];
  risks?: string[];
  targets: string[];
};

export type ChatItem = {
  id: string;
  role: "user" | "assistant";
  /** markdown */
  text: string;
  /** tool calls made while producing this assistant turn */
  steps: Step[];
  actions: Action[];
  investigationId: string | null;
  createdAt: string;
  /** which Claude key produced an assistant reply; "" for user messages */
  keySource?: "personal" | "shared" | "";
};

export type Chat = { id: string; title: string; items: ChatItem[] };

// ---- Stream events (POST /api/chats/{id}/messages) ----
export type StreamUsage = { inputTokens: number; outputTokens: number };

export type StreamEvent =
  | { event: "step"; data: Step }
  | { event: "step_done"; data: { id: string; ok: boolean; summary?: string } }
  | { event: "text"; data: { delta: string } }
  | { event: "action"; data: Action }
  | { event: "investigation"; data: { id: string; title: string } }
  | {
      event: "done";
      data: { itemId: string; keySource: "personal" | "shared"; usage: StreamUsage };
    }
  | { event: "error"; data: { message: string } };

// ---- Investigations ----
export type Confidence = "high" | "medium" | "low";

export type InvestigationSummary = {
  id: string;
  title: string;
  entityName: string;
  category: string;
  requester: string;
  createdAt: string;
  chatId: string;
};

export type Evidence = { at: string; tool: string; input: string; summary: string; ok: boolean };

export type Investigation = InvestigationSummary & {
  entityId: string;
  summaryForRequester: string;
  confidence: Confidence;
  /** admins only */
  rootCause?: string;
  /** admins only */
  recommendedFix?: string;
  /** admins only */
  evidence?: Evidence[];
  actions: Action[];
};

// ---- Guardrails & audit ----
export type PolicyMode = "Auto" | "Ask requester" | "Dry run only" | "Admin approval" | "Never" | "Not available yet";

export type PolicyRow = { action: string; examples: string; mode: PolicyMode };

export type AuditEntry = {
  at: string;
  actor: string;
  action: string;
  target: string;
  decision: string;
  detail: string;
};

// ---- Clusters (read-only Kubernetes explorer) ----
export type ClusterSummary = {
  nodes: number;
  nodesReady: number;
  pods: number;
  podsNotReady: number;
  /** requests ÷ allocatable, 0–100 */
  cpuRequestedPct: number;
  /** requests ÷ allocatable, 0–100 */
  memRequestedPct: number;
  /** "14.9 / 24 cores" */
  cpuDetail: string;
  /** "77 / 96 GiB" */
  memDetail: string;
  /** metrics-server answered */
  metricsAvailable: boolean;
  source?: "thanos" | "kubernetes";
};

export type Cluster = {
  /** control-hub infrastructure id */
  id: string;
  /** cluster name */
  name: string;
  provider: "aws" | "gcp" | "azure" | (string & {});
  region: string;
  kubernetesVersion: string;
  /** control-hub infrastructure status */
  status: string;
  /** customer names of control planes on this cluster */
  tenants: string[];
  /** null when the cluster could not be reached */
  summary: ClusterSummary | null;
  /** "" or why the cluster could not be reached */
  error: string;
  /** Grafana instance / clusters this cluster's metrics and logs come from; null when not mapped */
  grafana?: GrafanaMapping | null;
  /** The card is still being computed; the page asks again shortly. */
  pending?: boolean;
  /** Where the cluster comes from in control-hub. */
  kind?: "infrastructure" | "imported" | "sap-control-plane" | "sap-execution-plane";
  /** "full": explorer; "metrics": control-hub keeps no credentials, Grafana only. */
  access?: "full" | "metrics";
};

export const CLUSTER_KIND_LABEL: Record<NonNullable<Cluster["kind"]>, string> = {
  infrastructure: "Managed",
  imported: "Imported",
  "sap-control-plane": "SAP control plane",
  "sap-execution-plane": "SAP execution plane",
};

/** Metrics-only cluster ids (no credentials in control-hub). */
export const isMetricsOnlyId = (id: string) => /^(cp|sapcp|sapep)\./.test(id);

export type ClusterDetail = Cluster & { namespaces: string[] };

/** API.md calls this `Node`; renamed so it doesn't shadow the DOM's global `Node` type. */
export type ClusterNode = {
  name: string;
  ready: boolean;
  /** "Ready" | "NotReady" | "SchedulingDisabled" */
  status: string;
  pool: string;
  instanceType: string;
  version: string;
  createdAt: string;
  cpuRequestedPct: number;
  memRequestedPct: number;
  pods: number;
  /** "" when metrics-server is unavailable */
  cpuUsage: string;
  /** "" when metrics-server is unavailable */
  memUsage: string;
  /** ["MemoryPressure", …] */
  pressure: string[];
};

export type PodIssueSeverity = "critical" | "warning" | "";

export type Pod = {
  name: string;
  namespace: string;
  /** as kubectl shows it: Running, Pending, CrashLoopBackOff, ImagePullBackOff, OOMKilled, Completed… */
  status: string;
  /** "1/1" */
  ready: string;
  restarts: number;
  /** current usage from metrics-server, else "" */
  cpu: string;
  /** current usage from metrics-server, else "" */
  memory: string;
  cpuRequest: string;
  memRequest: string;
  memLimit: string;
  node: string;
  createdAt: string;
  /** "Deployment/metagraph", "StatefulSet/postgres", "" */
  owner: string;
  images: string[];
  /** short reason worth a pill: "OOMKilled 1h ago", "ImagePullBackOff", "" */
  issue: string;
  issueSeverity: PodIssueSeverity;
};

export type Deployment = {
  name: string;
  namespace: string;
  ready: string;
  upToDate: number;
  available: number;
  images: string[];
  createdAt: string;
  updatedAt: string;
  kind: "Deployment" | "StatefulSet" | "DaemonSet";
};

export type KEvent = {
  type: "Normal" | "Warning";
  reason: string;
  object: string;
  message: string;
  count: number;
  lastSeen: string;
  namespace: string;
};

export type Service = {
  name: string;
  namespace: string;
  type: string;
  clusterIP: string;
  external: string;
  ports: string;
};

export type Container = {
  name: string;
  image: string;
  ready: boolean;
  restarts: number;
  /** "Running since …", "Waiting: CrashLoopBackOff", "Terminated: Completed" */
  state: string;
  /** "OOMKilled (exit 137) 1h ago" or "" */
  lastTerminated: string;
  cpuRequest: string;
  memRequest: string;
  cpuLimit: string;
  memLimit: string;
};

export type PodCondition = { type: string; status: string; reason: string; message: string };

export type PodDetail = Pod & {
  labels: Record<string, string>;
  conditions: PodCondition[];
  containers: Container[];
  events: KEvent[];
  /** YAML; managedFields removed; env values with secret-looking names masked */
  manifest: string;
};

export type Logs = { container: string; lines: string[]; truncated: boolean };

export type LogsQuery = { container?: string; sinceMinutes?: number; tail?: number; previous?: boolean };

// ---- Log parser (rules, not AI) ----
export type LogSeverity = "error" | "warning" | "info";

export type LogRule = {
  id: string;
  title: string;
  /** pod-name prefixes; [] = every pod (the server may send null for []) */
  services: string[] | null;
  /** Go RE2 regexp */
  pattern: string;
  severity: LogSeverity;
  meaning: string;
  fix: string;
  /** file:line in the prophecy repo where the message is logged, or "" */
  source: string;
  examples: string[] | null;
  not_examples?: string[] | null;
  /** ships with the console (may be edited or disabled by an admin) */
  builtin: boolean;
  enabled: boolean;
  /** matches recorded by scans in the last 24 h */
  hits24h: number;
  /** true when an admin changed a built-in rule's content (Reset restores the default) */
  edited: boolean;
};

/** Body of POST /log-rules and PUT /log-rules/{id}. */
export type LogRuleInput = Pick<LogRule, "id" | "title" | "pattern" | "severity" | "meaning" | "fix" | "source"> & {
  services: string[];
  examples: string[];
  not_examples: string[];
};

export type Hit = {
  ruleId: string;
  title: string;
  severity: LogSeverity;
  meaning: string;
  fix: string;
  count: number;
  /** indexes into the scanned lines */
  firstLine: number;
  lastLine: number;
  sample: string;
};

export type LineMatch = { line: number; rules: string[] };

export type ParserResult = { hits: Hit[] | null; matches: LineMatch[] | null };

/**
 * One rule's stored hits for one pod, from the background parser. `count` covers the last 60 min;
 * a rule not seen for 10 min is resolved (`active` false).
 */
export type PodHit = {
  ruleId: string;
  title: string;
  severity: LogSeverity;
  meaning: string;
  fix: string;
  /** matches in the last 60 min */
  count: number;
  /** matches since the parser first saw this rule on the pod */
  total: number;
  firstSeen: string;
  lastSeen: string;
  active: boolean;
  sample: string;
};

export type PodScan = {
  pod: string;
  /** 60-min counts */
  errors: number;
  warnings: number;
  /** title of the most important hit */
  top: string;
  hits: PodHit[] | null;
  error?: string;
};

/** Stored results of the background log parser (instant; viewing never starts a scan). */
export type NamespaceScan = {
  namespace: string;
  /** Go duration, e.g. "1h0m0s" */
  window: string;
  /** time of the parser's last cycle for the cluster */
  scannedAt: string;
  /** true until the first cycle for the cluster finished */
  pending: boolean;
  error?: string;
  pods: PodScan[] | null;
};

/** The pod logs response carries the parser's result over the same lines. */
export type PodLogs = Logs & { parser?: ParserResult };

// ---- Full cluster explorer (Lens-style, read-only) ----
export type APIResourceCategory =
  | "Cluster"
  | "Workloads"
  | "Config"
  | "Network"
  | "Storage"
  | "Access Control"
  | "Custom Resources"
  | "Other";

export type APIResource = {
  /** "<group|core>/<version>/<resource>", e.g. "apps/v1/deployments" */
  key: string;
  /** "" for the core group */
  group: string;
  version: string;
  resource: string;
  kind: string;
  namespaced: boolean;
  category: APIResourceCategory | (string & {});
};

/** priority 0 = default column; > 0 = "wide" only */
export type ResourceColumn = { name: string; type: string; priority: number };

/** `cells` align with `ResourceTable.columns` */
export type ResourceRow = { name: string; namespace: string; createdAt: string; cells: string[] };

export type ResourceTable = { columns: ResourceColumn[]; rows: ResourceRow[] };

/** key "" when the owner is not linkable */
export type OwnerRef = { kind: string; name: string; key: string };

export type ResourceDetail = {
  kind: string;
  apiVersion: string;
  name: string;
  namespace: string;
  createdAt: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  owners: OwnerRef[];
  /** label selector for workloads/services, "" otherwise */
  selector: string;
  /** pods the workload/service selects ([] otherwise) */
  pods: Pod[];
  events: KEvent[];
  /** masked */
  yaml: string;
};

/**
 * The server's struct embeds Node, but its own `pods` (the pod list) shadows Node's pod count,
 * so `pods` here is the list; the count is `pods.length`.
 */
export type NodeDetail = Omit<ClusterNode, "pods"> & {
  labels: Record<string, string>;
  taints: string[];
  addresses: string[];
  conditions: PodCondition[];
  capacity: Record<string, string>;
  allocatable: Record<string, string>;
  os: string;
  kernel: string;
  runtime: string;
  providerId: string;
  unschedulable: boolean;
  pods: Pod[];
  events: KEvent[];
};

export type HelmRelease = {
  name: string;
  namespace: string;
  chart: string;
  chartVersion: string;
  appVersion: string;
  revision: number;
  status: string;
  updated: string;
  description: string;
};


// ---- Grafana: metrics parser, issues and charts ----
export type GrafanaMapping = {
  instance: string;
  /** the metrics `cluster` label */
  promCluster: string;
  /** Loki `k8s_cluster_name` label */
  lokiCluster: string;
  /** an admin set this mapping (false = automatic) */
  override: boolean;
};

export type IssueSeverity = "error" | "warning" | "info";
export type MetricEntity = "container" | "pod" | "service" | "deployment" | "pvc" | "node" | "cluster";

export type MetricIssue = {
  instance: string;
  cluster: string;
  ruleId: string;
  title: string;
  severity: IssueSeverity;
  entity: MetricEntity | (string & {});
  entityKey: string;
  entityName: string;
  namespace: string;
  pod: string;
  labels: Record<string, string> | null;
  value: number;
  /** "95%", "6", "1.2 s", "9.9/s", "" */
  valueText: string;
  meaning: string;
  fix: string;
  firstSeen: string;
  lastSeen: string;
  active: boolean;
  resolvedAt?: string | null;
};

/** Active first, then resolved in the last 24 h. */
export type ClusterIssues = { grafana: GrafanaMapping | null; evaluatedAt: string | null; metrics: MetricIssue[] | null };

export type IssueSource = "metrics" | "logs";

export type FleetIssue = {
  source: IssueSource;
  clusterId: string;
  clusterName: string;
  ruleId: string;
  title: string;
  severity: IssueSeverity;
  entity: string;
  entityName: string;
  namespace: string;
  pod: string;
  valueText: string;
  meaning: string;
  fix: string;
  firstSeen: string;
  lastSeen: string;
  active: boolean;
};

export type MetricRule = {
  id: string;
  title: string;
  severity: string;
  entity: string;
  /** how the value is shown: percent | count | seconds | rate | none */
  value: string;
  /** PromQL */
  expr: string;
  meaning: string;
  fix: string;
};

export type ChartUnit = "cores" | "bytes" | "count" | (string & {});

export type ChartSeries = {
  name: string;
  unit: ChartUnit;
  labels: Record<string, string> | null;
  /** [unix seconds, value] */
  points: [number, number][] | null;
};

export type MetricsPanel = "cluster" | "pod" | "node";

export type MetricsQuery = { panel: MetricsPanel; range: string; ns?: string; pod?: string; node?: string };

export type ClusterMetrics = {
  /** false when the cluster has no Grafana mapping */
  available: boolean;
  instance?: string;
  cluster?: string;
  from?: string;
  to?: string;
  stepSeconds?: number;
  series: ChartSeries[] | null;
};

export type GrafanaInstance = {
  name: string;
  url: string;
  source: "secret" | "settings";
  clusters: string[] | null;
  logClusters: number;
  checkedAt: string;
  error: string;
  evaluatedAt: string | null;
  ruleErrors: string[] | null;
};

export type GrafanaOverride = {
  cluster: string;
  instance: string;
  promCluster: string;
  lokiCluster: string;
  updatedBy: string;
  updatedAt: string;
};

export type GrafanaStatus = { instances: GrafanaInstance[] | null; overrides: GrafanaOverride[] | null };

export type GrafanaInstanceInput = { name: string; url: string; token: string };

export type GrafanaMappingInput = { instance: string; promCluster: string; lokiCluster: string };

// --- certificates ---

export type CertKind = "tenant" | "wildcard" | "acm" | "gcp";
export type CertStatus = "ok" | "unknown" | "warning" | "critical" | "expired";

export interface CertProblem {
  severity: "info" | "warning" | "critical";
  code: string;
  message: string;
}

export interface CertCheck {
  name: string;
  ok: boolean;
  detail: string;
}

export interface CertItem {
  key: string;
  kind: CertKind;
  tenant?: string;
  tenantId?: string;
  realm?: string;
  provider?: string;
  region?: string;
  host: string;
  source: "acm" | "gcp-certificate-manager" | "wildcard-secret" | "unknown";
  sourceRef?: string;
  autoRenew: string;
  subject?: string;
  issuer?: string;
  sans?: string[];
  notAfter?: string;
  daysLeft?: number;
  status: CertStatus;
  problems: CertProblem[];
  checks: CertCheck[];
  usedBy?: string[];
  repair?: {
    kind: "acm-validation" | "gcp-dns-authorization" | "wildcard-renew";
    certificateArn?: string;
    certificate?: string;
    secretName?: string;
    broken: boolean;
  };
}

export interface CertRepairChange {
  name: string;
  type: string;
  current: string;
  desired: string;
  action: "create" | "update" | "none";
  applied: boolean;
  error?: string;
}

export interface CertRepairResult {
  kind: string;
  target: string;
  dryRun: boolean;
  changes: CertRepairChange[];
  next: string;
}

export interface CertInventory {
  scannedAt: string | null;
  durationMs: number;
  running: boolean;
  intervalMinutes: number;
  items: CertItem[];
  errors: string[];
  counts: Record<CertStatus, number>;
  repairEnabled: boolean;
}

// --- costs ---

export interface CostClusterDay {
  day: string;
  cluster: string;
  instance: string;
  tenant: string;
  nodeCost: number;
  allocatedCost: number;
  idleCost: number;
  nodeHours: number;
  nodes: number;
  estimatedNodes: number;
  nodeTypes: string;
  hours: number;
  computedAt: string;
}

export interface CostClusterTotal {
  cluster: string;
  instance: string;
  tenant: string;
  nodeCost: number;
  allocatedCost: number;
  idleCost: number;
  days: number;
  estimatedNodes: number;
  nodes: number;
  nodeTypes: string;
}

export interface CostSummary {
  from: string;
  to: string;
  total: number;
  idle: number;
  clusters: CostClusterTotal[];
  days: CostClusterDay[];
  errors: Record<string, string>;
  priceErrors: Record<string, string>;
  lastRun: string | null;
  note: string;
}

export interface CostWorkload {
  cluster: string;
  namespace: string;
  kind: string;
  name: string;
  cpuCost: number;
  memCost: number;
  cpuRequestCoreHours: number;
  cpuUsageCoreHours: number;
  memRequestGiBHours: number;
  memUsageGiBHours: number;
  pods: number;
}

export interface WildcardChange {
  target: string;
  secret: string;
  currentExpiry?: string;
  newExpiry?: string;
  action: "update" | "current" | "skip";
  reason?: string;
  applied: boolean;
  error?: string;
}

export interface WildcardResult {
  secret: string;
  domain: string;
  dryRun: boolean;
  currentExpiry: string;
  source?: { arn: string; region: string; domain: string; notAfter: string; renewalStatus?: string };
  step: "renew-in-acm" | "sync" | "up-to-date" | "manual";
  changes: WildcardChange[];
  next: string;
}


// --- learnings ---

export type LearningStatus = "draft" | "approved" | "retired";

export const LEARNING_CATEGORIES = [
  "networking-dns",
  "certificates",
  "cloud-quota",
  "cloud-permissions",
  "cloud-capacity",
  "helm-kubernetes",
  "athena-postdeploy",
  "control-hub-bug",
  "configuration",
  "transient",
  "unknown",
] as const;

export type Learning = {
  id: string;
  title: string;
  category: string;
  status: LearningStatus;
  symptoms: string;
  rootCause: string;
  howToConfirm: string;
  fix: string;
  kgNodes: string;
  entities: string[];
  investigations: string[];
  timesSeen: number;
  confirmed: number;
  contradicted: number;
  reviewNote: string;
  createdBy: string;
  approvedBy: string;
  approvedAt: string | null;
  lastSeen: string;
  createdAt: string;
  updatedAt: string;
};

export type LearningInput = Partial<Pick<Learning, "title" | "category" | "status" | "symptoms" | "rootCause" | "howToConfirm" | "fix" | "kgNodes">> & {
  clearReview?: boolean;
};

// --- right-sizing ---

export type Resources = { cpuRequest: number; cpuLimit: number; memRequest: number; memLimit: number };

export type Recommendation = {
  cluster: string;
  namespace: string;
  kind: string;
  name: string;
  container: string;
  days: number;
  replicas: number;
  current: Resources;
  recommended: Resources;
  cpuP95: number;
  cpuMax: number;
  memMax: number;
  oomDays: number;
  throttledPct: number;
  confidence: "high" | "medium" | "low";
  notes: string[];
  monthlySaving: number;
};

// --- error patterns and drafted rules ---

export type LogPattern = {
  id: string;
  service: string;
  /** the message with variable parts as <*> */
  template: string;
  example: string;
  count: number;
  clusters: Record<string, number>;
  firstSeen: string;
  lastSeen: string;
  last6h: number;
  last24h: number;
  isNew: boolean;
  status: "new" | "ignored" | "ruled";
  ruleId?: string;
};

export type RuleDraft = { applicable: boolean; reason?: string; rule: LogRule };

// --- upgrade readiness ---

export type ReadinessStatus = "pass" | "warn" | "fail" | "unknown";

export type ReadinessReport = {
  controlPlane: {
    ID: string;
    Customer: string;
    URL: string;
    Provider: string;
    Region: string;
    Status: string;
    ProphecyVersion: string;
    ClusterName: string;
  };
  verdict: "go" | "caution" | "no-go" | "unknown";
  summary: string;
  checks: { id: string; name: string; status: ReadinessStatus; detail: string; hint?: string }[];
  at: string;
};

// --- cost changes ---

export type CostChange = {
  cluster: string;
  tenant?: string;
  baselineFrom: string;
  baselineTo: string;
  recentFrom: string;
  recentTo: string;
  baselinePerDay: number;
  recentPerDay: number;
  deltaPerDay: number;
  pct: number;
  drivers: { kind: "nodes" | "idle" | "workload"; what: string; perDay: number }[];
  summary: string;
};

// --- weekly digest ---

export type DigestResponse = {
  digest: { from: string; to: string; headline: string; sections: { title: string; lines: string[]; link?: string }[] };
  slack: string;
  lastSent: string | null;
  schedule: string;
};

// --- Claude review of right-sizing ---

export type RightsizingReview = {
  key: string; // cluster|namespace|kind|name|container
  status: "pending" | "done" | "failed";
  verdict: "" | "safe" | "adjust" | "risky";
  reason: string;
  cpuRequest: number;
  memRequest: number;
  memLimit: number;
  reviewedAt: string | null;
};

export type ReviewBatch = {
  ID: string;
  SubmittedBy: string;
  SubmittedAt: string;
  Status: "in_progress" | "ended" | "failed";
  Requests: number;
  Succeeded: number;
  Errored: number;
  EndedAt: string | null;
  Error: string;
};
