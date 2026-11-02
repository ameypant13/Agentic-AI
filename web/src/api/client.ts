import type {
  RightsizingReview,
  ReviewBatch,
  DigestResponse,
  CostChange,
  ReadinessReport,
  LogPattern,
  RuleDraft,
  Recommendation,
  Learning,
  LearningInput,
  LearningStatus,
  APIResource,
  CertInventory,
  CostSummary,
  CostWorkload,
  CertRepairResult,
  WildcardResult,
  Action,
  AuditEntry,
  Chat,
  ChatSummary,
  ClaudeKey,
  Cluster,
  ClusterDetail,
  ClusterIssues,
  ClusterMetrics,
  ClusterNode,
  Deployment,
  FleetIssue,
  GrafanaInstanceInput,
  GrafanaMapping,
  GrafanaMappingInput,
  GrafanaStatus,
  MetricRule,
  MetricsQuery,
  Finding,
  FindingStateFilter,
  Fleet,
  HelmRelease,
  InvestigateResponse,
  Investigation,
  InvestigationSummary,
  KEvent,
  LogRule,
  LogRuleInput,
  LogsQuery,
  NamespaceScan,
  Me,
  Monitor,
  NodeDetail,
  PolicyRow,
  Pod,
  PodDetail,
  PodLogs,
  ResourceDetail,
  ResourceTable,
  Service,
  StreamEvent,
} from "./types";

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

/** Paths that must not trigger the 401 → /login redirect (they are how you get a session). */
const NO_REDIRECT = ["/api/auth/login", "/api/auth/token", "/api/me"];

function redirectToLogin() {
  if (window.location.pathname !== "/login") {
    const next = window.location.pathname + window.location.search;
    window.location.assign(`/login?next=${encodeURIComponent(next)}`);
  }
}

async function errorFrom(res: Response): Promise<ApiError> {
  let message = `${res.status} ${res.statusText}`.trim();
  try {
    const body = (await res.json()) as { error?: string };
    if (body && typeof body.error === "string" && body.error) message = body.error;
  } catch {
    // non-JSON error body; keep the status line
  }
  return new ApiError(message, res.status);
}

async function send(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<Response> {
  const res = await fetch(path, {
    method,
    credentials: "include",
    headers: body === undefined ? { Accept: "application/json" } : { Accept: "application/json", "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  if (res.status === 401 && !NO_REDIRECT.includes(path)) {
    redirectToLogin();
  }
  if (!res.ok) throw await errorFrom(res);
  return res;
}

export async function request<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  const res = await send(path, method, body);
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

const enc = encodeURIComponent;

/** `?ns=` query for the namespace-scoped cluster lists; "" or undefined = all namespaces. */
const nsQuery = (ns?: string) => (ns ? `?ns=${enc(ns)}` : "");
const clusterPath = (id: string) => `/api/clusters/${enc(id)}`;
const podPath = (id: string, ns: string, name: string) => `${clusterPath(id)}/pods/${enc(ns)}/${enc(name)}`;
/** Resource key "<group|core>/<version>/<resource>" → path segments, each encoded. */
const keyPath = (key: string) => key.split("/").map(enc).join("/");

export const api = {
  // auth
  login: (email: string, password: string) => request<Me>("/api/auth/login", "POST", { email, password }),
  loginWithToken: (token: string) => request<Me>("/api/auth/token", "POST", { token }),
  logout: () => request<Record<string, never>>("/api/auth/logout", "POST"),
  me: () => request<Me>("/api/me"),
  setClaudeKey: (apiKey: string) => request<ClaudeKey>("/api/me/claude-key", "PUT", { apiKey }),
  deleteClaudeKey: () => request<ClaudeKey>("/api/me/claude-key", "DELETE"),

  // certificates
  certificates: () => request<CertInventory>("/api/certificates"),
  scanCertificates: () => request<{ queued: boolean }>("/api/certificates/scan", "POST"),
  repairCertificate: (key: string, dryRun: boolean) => request<CertRepairResult>("/api/certificates/repair", "POST", { key, dryRun }),
  renewWildcard: (key: string, dryRun: boolean, force = false) =>
    request<WildcardResult>("/api/certificates/repair", "POST", { key, dryRun, force }),

  // costs
  costs: (days: number, cluster = "") => request<CostSummary>(`/api/costs?days=${days}${cluster ? `&cluster=${enc(cluster)}` : ""}`),
  costReviews: () => request<{ batch: ReviewBatch | null; reviews: RightsizingReview[] }>("/api/costs/review"),
  submitCostReview: (limit = 50) => request<ReviewBatch>("/api/costs/review", "POST", { limit }),
  costChanges: (cluster = "") =>
    request<{ changes: CostChange[]; rule: string }>(`/api/costs/changes${cluster ? `?cluster=${enc(cluster)}` : ""}`),
  costRecommendationTotals: (days: number) =>
    request<{ from: string; to: string; clusters: Record<string, { monthlySaving: number; needsMore: number; count: number }> }>(
      `/api/costs/recommendations?days=${days}&summary=1`,
    ),
  costRecommendations: (days: number, cluster: string) =>
    request<{ from: string; to: string; policy: string; recommendations: Recommendation[] }>(
      `/api/costs/recommendations?days=${days}&cluster=${enc(cluster)}`,
    ),
  costWorkloads: (days: number, cluster: string, namespace = "") =>
    request<{ workloads: CostWorkload[] }>(
      `/api/costs/workloads?days=${days}&cluster=${enc(cluster)}${namespace ? `&namespace=${enc(namespace)}` : ""}`,
    ),

  // fleet & findings
  fleet: () => request<Fleet>("/api/fleet"),
  findings: (state: FindingStateFilter = "open") => request<Finding[]>(`/api/findings?state=${enc(state)}`),
  investigateFinding: (id: string) => request<InvestigateResponse>(`/api/findings/${enc(id)}/investigate`, "POST"),
  ackFinding: (id: string) => request<Finding>(`/api/findings/${enc(id)}/ack`, "POST"),
  monitors: () => request<Monitor[]>("/api/monitors"),

  // chats
  chats: () => request<ChatSummary[]>("/api/chats"),
  createChat: (title?: string) => request<ChatSummary>("/api/chats", "POST", title ? { title } : {}),
  chat: (id: string) => request<Chat>(`/api/chats/${enc(id)}`),

  // actions
  executeAction: (id: string) => request<Action>(`/api/actions/${enc(id)}/execute`, "POST"),
  dismissAction: (id: string) => request<Action>(`/api/actions/${enc(id)}/dismiss`, "POST"),

  // investigations
  investigations: () => request<InvestigationSummary[]>("/api/investigations"),
  investigation: (id: string) => request<Investigation>(`/api/investigations/${enc(id)}`),

  // learnings (admins)
  learnings: (status?: LearningStatus, q?: string, alt: string[] = []) => {
    const p = new URLSearchParams();
    if (status) p.set("status", status);
    if (q) p.set("q", q);
    for (const a of alt) p.append("alt", a);
    const qs = p.toString();
    return request<Learning[]>(`/api/learnings${qs ? `?${qs}` : ""}`);
  },
  expandLearningQuery: (q: string) =>
    request<{ alternatives: string[] }>(`/api/learnings/expand?q=${enc(q)}`),
  createLearning: (body: LearningInput) => request<Learning>("/api/learnings", "POST", body),
  updateLearning: (id: string, body: LearningInput) => request<Learning>(`/api/learnings/${enc(id)}`, "PUT", body),
  deleteLearning: (id: string) => request<void>(`/api/learnings/${enc(id)}`, "DELETE"),

  // guardrails
  policy: () => request<PolicyRow[]>("/api/policy"),
  audit: (limit = 100) => request<AuditEntry[]>(`/api/audit?limit=${limit}`),

  // clusters (read-only Kubernetes explorer)
  clusters: () => request<Cluster[]>("/api/clusters"),
  cluster: (id: string) => request<ClusterDetail>(clusterPath(id)),
  clusterNodes: (id: string) => request<ClusterNode[]>(`${clusterPath(id)}/nodes`),
  clusterPods: (id: string, ns?: string) => request<Pod[]>(`${clusterPath(id)}/pods${nsQuery(ns)}`),
  clusterWorkloads: (id: string, ns?: string) => request<Deployment[]>(`${clusterPath(id)}/workloads${nsQuery(ns)}`),
  clusterEvents: (id: string, ns?: string) => request<KEvent[]>(`${clusterPath(id)}/events${nsQuery(ns)}`),
  clusterServices: (id: string, ns?: string) => request<Service[]>(`${clusterPath(id)}/services${nsQuery(ns)}`),
  pod: (id: string, ns: string, name: string) => request<PodDetail>(podPath(id, ns, name)),
  podLogs: (id: string, ns: string, name: string, q: LogsQuery = {}) => {
    const params = new URLSearchParams();
    if (q.container) params.set("container", q.container);
    if (q.sinceMinutes !== undefined) params.set("sinceMinutes", String(q.sinceMinutes));
    if (q.tail !== undefined) params.set("tail", String(q.tail));
    if (q.previous) params.set("previous", "true");
    const qs = params.toString();
    return request<PodLogs>(`${podPath(id, ns, name)}/logs${qs ? `?${qs}` : ""}`);
  },
  /** Stored background-parser results for `ns` (required). Instant; never starts a scan. */
  clusterLogScan: (id: string, ns: string) => request<NamespaceScan>(`${clusterPath(id)}/log-scan?ns=${enc(ns)}`),

  // full cluster explorer (Lens-style, read-only)
  apiResources: (id: string) => request<APIResource[]>(`${clusterPath(id)}/api-resources`),
  /** The API server's table output for one resource type; `ns` "" = all namespaces. */
  resources: (id: string, key: string, ns?: string) => request<ResourceTable>(`${clusterPath(id)}/resources/${keyPath(key)}${nsQuery(ns)}`),
  /** One object; `ns` is required for namespaced kinds. */
  resource: (id: string, key: string, name: string, ns?: string) =>
    request<ResourceDetail>(`${clusterPath(id)}/resources/${keyPath(key)}/${enc(name)}${nsQuery(ns)}`),
  node: (id: string, name: string) => request<NodeDetail>(`${clusterPath(id)}/nodes/${enc(name)}`),
  helmReleases: (id: string, ns?: string) => request<HelmRelease[]>(`${clusterPath(id)}/helm-releases${nsQuery(ns)}`),

  // log parser rules
  logRules: () => request<LogRule[]>("/api/log-rules"),
  createLogRule: (rule: LogRuleInput) => request<LogRule>("/api/log-rules", "POST", rule),
  updateLogRule: (id: string, rule: LogRuleInput) => request<LogRule>(`/api/log-rules/${enc(id)}`, "PUT", rule),
  enableLogRule: (id: string) => request<LogRule>(`/api/log-rules/${enc(id)}/enable`, "POST"),
  disableLogRule: (id: string) => request<LogRule>(`/api/log-rules/${enc(id)}/disable`, "POST"),
  /** Removes a custom rule, or resets an edited / disabled built-in to its default. */
  deleteLogRule: (id: string) => request<Record<string, never>>(`/api/log-rules/${enc(id)}`, "DELETE"),
  /** Rules matching `line`; all rules when `pod` is empty, else only rules scoped to that pod. */
  digest: () => request<DigestResponse>("/api/digest"),
  sendDigest: () => request<{ sent: boolean; message: string }>("/api/digest/send", "POST"),
  readiness: (cp: string) => request<ReadinessReport>(`/api/readiness?cp=${enc(cp)}`),
  logPatterns: (status = "") => request<LogPattern[]>(`/api/log-patterns${status ? `?status=${enc(status)}` : ""}`),
  setPatternStatus: (id: string, status: LogPattern["status"], ruleId = "") =>
    request<void>(`/api/log-patterns/${enc(id)}`, "PUT", { status, ruleId }),
  draftRuleFromPattern: (id: string) => request<RuleDraft>(`/api/log-patterns/${enc(id)}/draft-rule`, "POST"),
  draftRuleFromLearning: (id: string) => request<RuleDraft>(`/api/learnings/${enc(id)}/draft-rule`, "POST"),
  testLogLine: (line: string, pod?: string) => request<LogRule[]>("/api/log-rules/test", "POST", pod ? { line, pod } : { line }),

  // Grafana: metrics parser, issues and charts
  /** Active metric issues + active log-parser hits on every cluster the caller can see. */
  issues: () => request<FleetIssue[]>("/api/issues"),
  /** Metric issues of one cluster: active first, then resolved in the last 24 h. */
  clusterIssues: (id: string) => request<ClusterIssues>(`${clusterPath(id)}/issues`),
  clusterMetrics: (id: string, q: MetricsQuery) => {
    const params = new URLSearchParams({ panel: q.panel, range: q.range });
    if (q.ns) params.set("ns", q.ns);
    if (q.pod) params.set("pod", q.pod);
    if (q.node) params.set("node", q.node);
    return request<ClusterMetrics>(`${clusterPath(id)}/metrics?${params.toString()}`);
  },
  metricRules: () => request<MetricRule[]>("/api/metric-rules"),
  grafana: () => request<GrafanaStatus>("/api/grafana"),
  /** Reloads every instance's cluster names, then answers like GET /grafana. */
  refreshGrafana: () => request<GrafanaStatus>("/api/grafana/refresh", "POST"),
  addGrafanaInstance: (body: GrafanaInstanceInput) => request<Record<string, never>>("/api/grafana/instances", "POST", body),
  deleteGrafanaInstance: (name: string) => request<Record<string, never>>(`/api/grafana/instances/${enc(name)}`, "DELETE"),
  /** Admin override for a control-hub cluster name. */
  setGrafanaMapping: (cluster: string, body: GrafanaMappingInput) => request<GrafanaMapping>(`/api/grafana/mappings/${enc(cluster)}`, "PUT", body),
  /** Removes the override; answers the automatic mapping (null when none). */
  resetGrafanaMapping: (cluster: string) => request<GrafanaMapping | null>(`/api/grafana/mappings/${enc(cluster)}`, "DELETE"),
};

/**
 * POST a chat message and consume the Server-Sent Events response.
 * EventSource cannot POST, so the stream is parsed by hand: events are separated by a
 * blank line, each with `event:` and `data:` lines. Resolves when the stream ends.
 */
export async function streamMessage(
  chatId: string,
  text: string,
  onEvent: (ev: StreamEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await send(`/api/chats/${enc(chatId)}/messages`, "POST", { text }, signal);
  if (!res.body) throw new Error("The server returned an empty stream.");

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  const dispatch = (block: string) => {
    let name = "message";
    const dataLines: string[] = [];
    for (const raw of block.split("\n")) {
      const line = raw.endsWith("\r") ? raw.slice(0, -1) : raw;
      if (!line || line.startsWith(":")) continue;
      const colon = line.indexOf(":");
      const field = colon === -1 ? line : line.slice(0, colon);
      let value = colon === -1 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") name = value;
      else if (field === "data") dataLines.push(value);
    }
    if (dataLines.length === 0) return;
    let data: unknown;
    try {
      data = JSON.parse(dataLines.join("\n"));
    } catch {
      return; // ignore malformed frames
    }
    onEvent({ event: name, data } as StreamEvent);
  };

  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer = (buffer + decoder.decode(value, { stream: true })).replace(/\r\n/g, "\n");
    let sep: number;
    while ((sep = buffer.indexOf("\n\n")) !== -1) {
      dispatch(buffer.slice(0, sep));
      buffer = buffer.slice(sep + 2);
    }
  }
  buffer += decoder.decode();
  if (buffer.trim()) dispatch(buffer);
}
