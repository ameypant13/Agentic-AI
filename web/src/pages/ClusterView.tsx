import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import type { Cluster, ClusterIssues, Deployment, KEvent, NamespaceScan, Pod, PodScan } from "../api/types";
import type { ApiState } from "../lib/useApi";
import {
  Dash,
  EventsTable,
  HIGH_PCT,
  IssuePill,
  PctBar,
  PodStatus,
  Resource,
  TableCard,
  Toolbar,
  clusterUrl,
  issueTone,
  nodeUrl,
  podUrl,
  resourceUrl,
  useAutoRefresh,
  useNow,
} from "../components/Cluster";
import { useCluster, useClusterSection, useNsQuery } from "../components/ClusterShell";
import { RefreshIcon, Spinner } from "../components/Icons";
import { ISSUES_POLL_MS, MetricIssueList, sortIssues } from "../components/Issues";
import { ClusterCharts, NotConnected } from "../components/Metrics";
import { PodScanPill, ScanHitStatus, logSevTone, sortHits } from "../components/LogParser";
import { Pill } from "../components/Pill";
import { ErrorCard, Loading } from "../components/States";
import { age, formatDateTime } from "../lib/format";
import { useApi } from "../lib/useApi";

const TABS = ["overview", "nodes", "events", "pods", "workloads", "services"] as const;
type Tab = (typeof TABS)[number];

const TAB_LABEL: Record<Tab, string> = {
  overview: "Overview",
  nodes: "Nodes",
  events: "Events",
  pods: "Pods",
  workloads: "Workloads",
  services: "Services",
};

/** Tabs whose list is namespace-scoped and gets the namespace + filter toolbar. */
const NS_TABS: Tab[] = ["pods", "workloads", "events", "services"];

const POD_REFRESH_MS = 15_000;
/** The log-scan endpoint returns stored results (the background worker parses every 60 s). */
const SCAN_POLL_MS = 30_000;

function includes(q: string, ...fields: (string | undefined)[]): boolean {
  if (!q) return true;
  const needle = q.toLowerCase();
  return fields.some((f) => f && f.toLowerCase().includes(needle));
}

// ---------------------------------------------------------------- frame

function Tile({ label, value, sub, valueClass = "" }: { label: string; value: ReactNode; sub?: ReactNode; valueClass?: string }) {
  return (
    <div className="card px-3.5 py-3">
      <div className="text-xs text-muted">{label}</div>
      <div className={`text-xl font-semibold tabular-nums ${valueClass}`}>{value}</div>
      {sub && <div className="text-xs text-muted">{sub}</div>}
    </div>
  );
}

function SummaryTiles({ cluster, loading }: { cluster: Cluster | undefined; loading: boolean }) {
  const s = cluster?.summary;
  const dash = loading && !cluster ? "…" : "—";
  return (
    <div className="grid grid-cols-[repeat(auto-fit,minmax(min(170px,100%),1fr))] gap-3">
      <Tile label="CPU requested" value={s ? `${Math.round(s.cpuRequestedPct)}%` : dash} sub={s?.cpuDetail} valueClass={s && s.cpuRequestedPct > HIGH_PCT ? "text-warn-fg" : ""} />
      <Tile label="Memory requested" value={s ? `${Math.round(s.memRequestedPct)}%` : dash} sub={s?.memDetail} valueClass={s && s.memRequestedPct > HIGH_PCT ? "text-warn-fg" : ""} />
      <Tile
        label="Pods"
        value={s ? s.pods : dash}
        sub={s ? (s.podsNotReady > 0 ? <span className="font-medium text-warn-fg">{s.podsNotReady} not ready</span> : "all ready") : undefined}
      />
      <Tile
        label="Nodes ready"
        value={s ? `${s.nodesReady}/${s.nodes}` : dash}
        valueClass={s && s.nodesReady < s.nodes ? "text-warn-fg" : ""}
        sub={s ? (s.source === "thanos" ? "counts from Grafana" : s.metricsAvailable ? "metrics-server available" : "no metrics-server — usage hidden") : undefined}
      />
    </div>
  );
}

// ---------------------------------------------------------------- tabs

/** Metric issues of the cluster (Grafana metrics parser): active, then resolved in the last 24 h (collapsed). */
function IssuesCard({ id, issues }: { id: string; issues: ApiState<ClusterIssues> }) {
  const now = useNow(15_000);
  const data = issues.data;
  const all = data?.metrics ?? [];
  const active = sortIssues(all.filter((i) => i.active));
  const resolved = all
    .filter((i) => !i.active)
    .sort((a, b) => Date.parse(b.resolvedAt ?? b.lastSeen) - Date.parse(a.resolvedAt ?? a.lastSeen));
  const g = data?.grafana;
  return (
    <section className="card" aria-labelledby="cv-issues">
      <div className="card-head">
        <h2 id="cv-issues" className="h2">
          Issues{data && g && ` · ${active.length} active`}
        </h2>
        <span className="flex flex-wrap items-center gap-2 text-xs text-muted">
          {issues.loading && data && <Spinner />}
          {g && (
            <>
              <span title={data?.evaluatedAt ? formatDateTime(data.evaluatedAt) : undefined}>
                {data?.evaluatedAt ? `evaluated ${age(data.evaluatedAt, now)} ago` : "not evaluated yet"}
              </span>
              <span className="font-mono" title="Grafana instance / metrics cluster">
                {g.instance} / {g.promCluster}
              </span>
              {g.override && <Pill tone="violet">override</Pill>}
            </>
          )}
        </span>
      </div>
      <div className="px-4 py-2">
        {issues.error && !data ? (
          <div className="py-2">
            <ErrorCard title="Couldn't load metric issues" error={issues.error} onRetry={issues.reload} />
          </div>
        ) : !data ? (
          <Loading label="Loading metric issues…" />
        ) : !g ? (
          <div className="py-1.5">
            <NotConnected />
          </div>
        ) : (
          <>
            {active.length === 0 ? (
              <div className="py-1.5 text-[13px] text-muted">No active metric issues. Rules run every minute.</div>
            ) : (
              <MetricIssueList clusterId={id} issues={active} now={now} />
            )}
            {resolved.length > 0 && (
              <details className="border-t border-line py-2">
                <summary className="cursor-pointer text-[13px] font-medium text-muted">Resolved in the last 24 h ({resolved.length})</summary>
                <div className="mt-1">
                  <MetricIssueList clusterId={id} issues={resolved} now={now} resolved />
                </div>
              </details>
            )}
          </>
        )}
        {issues.error && data && <div className="pb-2 text-xs text-crit-fg">Refresh failed — showing the last data: {issues.error.message}</div>}
      </div>
    </section>
  );
}

/** A cluster control-hub has no credentials for: everything from Grafana. */
export function MetricsOnlyOverview({ id, cluster, loading }: { id: string; cluster: Cluster | undefined; loading: boolean }) {
  const issues = useApi(() => api.clusterIssues(id), [id]);
  useAutoRefresh(issues.reload, ISSUES_POLL_MS);
  return (
    <>
      <div className="card bg-info-bg p-3 text-[13px] text-info-fg" role="note">
        <span className="font-medium">Metrics only.</span> control-hub keeps no credentials for this cluster, so nodes, pods, logs and events
        can't be browsed here. The counts, issues and charts below come from Grafana.
      </div>
      <SummaryTiles cluster={cluster} loading={loading} />
      <IssuesCard id={id} issues={issues} />
      {issues.data?.grafana && <ClusterCharts id={id} />}
    </>
  );
}

function Overview({ id, scanNs }: { id: string; scanNs: string | null | undefined }) {
  const issues = useApi(() => api.clusterIssues(id), [id]);
  useAutoRefresh(issues.reload, ISSUES_POLL_MS);
  return (
    <>
      <IssuesCard id={id} issues={issues} />
      {issues.data?.grafana && <ClusterCharts id={id} />}
      <WarningsCard id={id} scanNs={scanNs} />
    </>
  );
}

/** Namespace whose log-scan hits the overview shows. */
const OVERVIEW_SCAN_NS = "prophecy";
const OVERVIEW_PARSER_HITS = 6;

/**
 * `scanNs`: namespace to show log-parser hits for; null = the cluster has none to scan,
 * undefined = namespaces are still loading.
 */
function WarningsCard({ id, scanNs }: { id: string; scanNs: string | null | undefined }) {
  const events = useApi(() => api.clusterEvents(id), [id]);
  const pods = useApi(() => api.clusterPods(id), [id]);
  const scan = useApi<NamespaceScan | null>(() => (scanNs ? api.clusterLogScan(id, scanNs) : Promise.resolve(null)), [id, scanNs]);
  const now = useNow(30_000);
  useAutoRefresh(scan.reload, SCAN_POLL_MS);

  // Only hits the background parser still sees (a rule unseen for 10 min is resolved).
  const parserHits = useMemo(
    () =>
      sortHits(
        (scan.data?.pods ?? []).flatMap((p) => (p.hits ?? []).filter((h) => h.active).map((h) => ({ ...h, pod: p.pod }))),
      ).slice(0, OVERVIEW_PARSER_HITS),
    [scan.data],
  );

  const podIssues = useMemo(
    () =>
      (pods.data ?? [])
        .filter((p) => p.issue)
        .sort((a, b) => (a.issueSeverity === "critical" ? 0 : 1) - (b.issueSeverity === "critical" ? 0 : 1) || b.restarts - a.restarts)
        .slice(0, 10),
    [pods.data],
  );
  const warnings = useMemo(() => (events.data ?? []).filter((e) => e.type === "Warning").slice(0, 8), [events.data]);
  const scanning = scanNs === undefined || (!!scanNs && scan.loading && !scan.data);
  const done = !pods.loading && !events.loading && !scanning && !scan.data?.pending;

  return (
    <section className="card" aria-labelledby="cv-warnings">
      <div className="card-head">
        <h2 id="cv-warnings" className="h2">
          Warnings
        </h2>
        <span className="text-xs text-muted">pods with an issue, log parser hits in {OVERVIEW_SCAN_NS}, then the latest Warning events</span>
      </div>
      <div className="flex flex-col gap-3 p-4">
        {pods.error ? (
          <ErrorCard title="Couldn't load pods" error={pods.error} onRetry={pods.reload} />
        ) : pods.loading && !pods.data ? (
          <Loading label="Checking pods…" />
        ) : (
          podIssues.length > 0 && (
            <ul className="m-0 flex list-none flex-col gap-2 p-0 text-[13px]">
              {podIssues.map((p) => (
                <li key={`${p.namespace}/${p.name}`} className="flex flex-wrap items-baseline gap-2">
                  <Pill tone={issueTone(p.issueSeverity)}>{p.issue}</Pill>
                  <Link to={podUrl(id, p.namespace, p.name)} className="font-mono text-xs">
                    {p.name}
                  </Link>
                  <span className="text-xs text-muted">
                    {p.namespace} · {p.status}
                    {p.restarts > 0 && ` · ${p.restarts} restarts`}
                  </span>
                </li>
              ))}
            </ul>
          )
        )}
        {scanNs &&
          (scan.error ? (
            <ErrorCard title={`Couldn't load the ${scanNs} log parser results`} error={scan.error} onRetry={scan.reload} />
          ) : scan.loading && !scan.data ? (
            <Loading label="Loading log parser results…" />
          ) : scan.data?.pending ? (
            <div className="text-[13px] text-muted">Log parser: first scan of {scanNs} in progress…</div>
          ) : scan.data?.error ? (
            <div className="text-[13px] text-crit-fg">Log parser: {scan.data.error}</div>
          ) : (
            parserHits.length > 0 && (
              <ul className="m-0 flex list-none flex-col gap-2 p-0 text-[13px]">
                {parserHits.map((h) => (
                  <li key={`${h.pod}/${h.ruleId}`} className="flex flex-wrap items-baseline gap-2">
                    <Pill tone={logSevTone(h.severity)}>Log parser</Pill>
                    <span className="font-medium">{h.title}</span>
                    <Link to={`${podUrl(id, scanNs, h.pod, "logs")}?only=hits`} className="font-mono text-xs">
                      {h.pod}
                    </Link>
                    <span className="text-xs text-muted" title={`first seen ${formatDateTime(h.firstSeen)} · ${h.total} in total`}>
                      ×{h.count} in the last {scanWindowLabel(scan.data?.window)}
                    </span>
                    <ScanHitStatus hit={h} now={now} />
                  </li>
                ))}
              </ul>
            )
          ))}
        {events.error ? (
          <ErrorCard title="Couldn't load events" error={events.error} onRetry={events.reload} />
        ) : events.loading && !events.data ? (
          <Loading label="Loading events…" />
        ) : (
          warnings.length > 0 && (
            <ul className="m-0 flex list-none flex-col gap-2 p-0 text-[13px]">
              {warnings.map((e, i) => (
                <li key={`${e.object}/${e.reason}/${i}`} className="flex items-baseline gap-2">
                  <Pill tone="warning">Event</Pill>
                  <span className="min-w-0">
                    <span className="font-medium">{e.reason}</span> · <span className="font-mono text-xs">{e.object}</span>
                    <span className="text-muted">
                      {" "}
                      — {e.message} {e.count > 1 && `(×${e.count})`} · {age(e.lastSeen, now)} ago
                    </span>
                  </span>
                </li>
              ))}
            </ul>
          )
        )}
        {done && !pods.error && !events.error && !scan.error && podIssues.length === 0 && parserHits.length === 0 && warnings.length === 0 && (
          <div className="text-[13px] text-muted">
            No pod issues, {scanNs ? "no known log errors" : "no log scan (no prophecy namespace)"} and no Warning events. Nothing to look at here.
          </div>
        )}
      </div>
    </section>
  );
}

function NodesTab({ id }: { id: string }) {
  const nodes = useApi(() => api.clusterNodes(id), [id]);
  const now = useNow(60_000);
  const hasUsage = (nodes.data ?? []).some((n) => n.cpuUsage || n.memUsage);
  return (
    <TableCard label="Nodes" loading={nodes.loading} error={nodes.error} onRetry={nodes.reload} rows={nodes.data} emptyTitle="No nodes">
      {(rows) => (
        <table className="table min-w-[980px]">
          <thead>
            <tr>
              <th scope="col">Node</th>
              <th scope="col">Status</th>
              <th scope="col">Pool</th>
              <th scope="col">Instance type</th>
              <th scope="col">CPU requested</th>
              <th scope="col">Memory requested</th>
              {hasUsage && <th scope="col">CPU usage</th>}
              {hasUsage && <th scope="col">Memory usage</th>}
              <th scope="col">Pods</th>
              <th scope="col">Pressure</th>
              <th scope="col">Version</th>
              <th scope="col">Age</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((n) => (
              <tr key={n.name}>
                <td className="whitespace-nowrap font-mono text-xs font-medium">
                  <Link to={nodeUrl(id, n.name)}>{n.name}</Link>
                </td>
                <td>
                  <Pill tone={n.status === "Ready" ? "success" : n.status === "SchedulingDisabled" ? "warning" : "critical"}>{n.status}</Pill>
                </td>
                <td className="whitespace-nowrap">{n.pool || <Dash />}</td>
                <td className="whitespace-nowrap font-mono text-xs">{n.instanceType || <Dash />}</td>
                <td>
                  <PctBar pct={n.cpuRequestedPct} label="CPU requested" />
                </td>
                <td>
                  <PctBar pct={n.memRequestedPct} label="Memory requested" />
                </td>
                {hasUsage && <td className="whitespace-nowrap font-mono text-xs">{n.cpuUsage || <Dash />}</td>}
                {hasUsage && <td className="whitespace-nowrap font-mono text-xs">{n.memUsage || <Dash />}</td>}
                <td className="tabular-nums">{n.pods}</td>
                <td>
                  {n.pressure.length === 0 ? (
                    <Dash />
                  ) : (
                    <div className="flex flex-wrap gap-1">
                      {n.pressure.map((p) => (
                        <Pill key={p} tone="warning">
                          {p}
                        </Pill>
                      ))}
                    </div>
                  )}
                </td>
                <td className="whitespace-nowrap font-mono text-xs">{n.version}</td>
                <td className="whitespace-nowrap text-muted" title={n.createdAt}>
                  {age(n.createdAt, now)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </TableCard>
  );
}

/** "1h0m0s" → "1 h", "30m0s" → "30 min"; anything unexpected is shown as-is. */
function scanWindowLabel(w: string | undefined): string {
  if (!w) return "60 min";
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:0s)?$/.exec(w);
  if (!m || (!m[1] && !m[2])) return w;
  const mins = Number(m[1] ?? 0) * 60 + Number(m[2] ?? 0);
  return mins % 60 === 0 ? `${mins / 60} h` : `${mins} min`;
}

function PodsTab({ id, ns, q, toolbar }: { id: string; ns: string; q: string; toolbar: (right: ReactNode) => ReactNode }) {
  const pods = useApi(() => api.clusterPods(id, ns), [id, ns]);
  // Stored background-parser results: instant, so fetched once per namespace and then polled.
  const scan = useApi<NamespaceScan | null>(() => (ns ? api.clusterLogScan(id, ns) : Promise.resolve(null)), [id, ns]);
  const scanByPod = useMemo(() => new Map((scan.data?.pods ?? []).map((p) => [p.pod, p])), [scan.data]);
  const [refreshedAt, setRefreshedAt] = useState<number | null>(null);
  const now = useNow(1000);
  const { reload } = pods;

  useEffect(() => {
    if (pods.data) setRefreshedAt(Date.now());
  }, [pods.data]);

  // Auto-refresh while the browser tab is visible.
  useAutoRefresh(reload, POD_REFRESH_MS);
  useAutoRefresh(scan.reload, SCAN_POLL_MS);

  const rows = useMemo(
    () => (pods.data ?? []).filter((p) => includes(q, p.name, p.status, p.node, p.issue, p.namespace, p.owner, scanByPod.get(p.name)?.top)),
    [pods.data, q, scanByPod],
  );
  const ago = refreshedAt === null ? null : Math.max(0, Math.round((now - refreshedAt) / 1000));

  return (
    <div className="flex flex-col gap-3">
      {toolbar(
        <>
          {pods.loading && pods.data ? <Spinner /> : <span aria-hidden="true" className="inline-block h-2 w-2 rounded-full bg-[#059669]" />}
          <span aria-live="off">{ago === null ? "Live" : `Live · refreshed ${ago}s ago`}</span>
          <button type="button" className="btn min-h-[30px] px-2" onClick={reload} aria-label="Refresh pods" disabled={pods.loading}>
            <RefreshIcon size={14} />
          </button>
        </>,
      )}
      {scan.error && !scan.data && <ErrorCard title={`Couldn't load the ${ns} log parser results`} error={scan.error} onRetry={scan.reload} />}
      <TableCard
        label="Pods"
        loading={pods.loading}
        error={pods.error}
        onRetry={pods.reload}
        rows={pods.data ? rows : undefined}
        emptyTitle={(pods.data ?? []).length === 0 ? "No pods" : "No pods match"}
        emptyText={(pods.data ?? []).length === 0 ? `There are no pods in ${ns || "any namespace"}.` : "Try a different filter."}
      >
        {(list) => <PodsTable id={id} pods={list} showNs={!ns} now={now} logs={{ enabled: ns !== "", scan: scan.data ?? undefined, byPod: scanByPod }} />}
      </TableCard>
    </div>
  );
}

type PodLogsInfo = {
  /** false for "All namespaces": the parser results are per namespace */
  enabled: boolean;
  /** undefined until the first response */
  scan?: NamespaceScan;
  byPod: Map<string, PodScan>;
};

function LogIssuesHeader({ logs, now }: { logs: PodLogsInfo; now: number }) {
  let sub: ReactNode = null;
  const scan = logs.scan;
  if (!logs.enabled) sub = "Pick a namespace";
  else if (!scan) sub = null;
  else if (scan.pending) sub = "first scan in progress…";
  else if (scan.error)
    sub = (
      <span className="text-crit-fg" title={scan.error}>
        {scan.error.length > 60 ? `${scan.error.slice(0, 60)}…` : scan.error}
      </span>
    );
  else if (scan.scannedAt)
    sub = (
      <span title={`last ${scanWindowLabel(scan.window)} · parsed ${formatDateTime(scan.scannedAt)}`}>parsed {age(scan.scannedAt, now)} ago</span>
    );
  return (
    <th scope="col">
      Log issues
      {sub && <div className="font-normal">{sub}</div>}
    </th>
  );
}

function LogIssuesCell({ logs, pod, now }: { logs: PodLogsInfo; pod: string; now: number }) {
  if (!logs.enabled) return <Dash />;
  const s = logs.byPod.get(pod);
  if (!s) return <Dash />;
  if (!s.error && s.errors === 0 && s.warnings === 0) {
    const resolved = (s.hits ?? []).filter((h) => !h.active);
    if (resolved.length > 0)
      return (
        <span
          className="text-xs text-muted"
          title={`Resolved:\n${resolved.map((h) => `${h.title} (${h.ruleId}) — last seen ${age(h.lastSeen, now)} ago`).join("\n")}`}
        >
          resolved
        </span>
      );
    return (
      <span className="text-muted" title="No known error or warning patterns">
        —
      </span>
    );
  }
  return <PodScanPill scan={s} />;
}

function PodsTable({ id, pods, showNs, now, logs }: { id: string; pods: Pod[]; showNs: boolean; now: number; logs: PodLogsInfo }) {
  return (
    <table className="table min-w-[1100px]">
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Status</th>
          <th scope="col">Ready</th>
          <th scope="col">Restarts</th>
          <th scope="col">K8s issue</th>
          <LogIssuesHeader logs={logs} now={now} />
          <th scope="col">CPU</th>
          <th scope="col">Memory</th>
          <th scope="col">Node</th>
          <th scope="col">Age</th>
        </tr>
      </thead>
      <tbody>
        {pods.map((p) => (
          <tr key={`${p.namespace}/${p.name}`}>
            <td>
              <Link to={podUrl(id, p.namespace, p.name)} className="whitespace-nowrap font-mono text-xs font-medium">
                {p.name}
              </Link>
              {showNs && <div className="text-xs text-muted">{p.namespace}</div>}
            </td>
            <td>
              <PodStatus status={p.status} />
            </td>
            <td className="tabular-nums">{p.ready}</td>
            <td className={`tabular-nums ${p.restarts > 3 ? "font-bold text-crit-fg" : ""}`}>{p.restarts}</td>
            <td>
              <IssuePill issue={p.issue} severity={p.issueSeverity} />
            </td>
            <td>
              <LogIssuesCell logs={logs} pod={p.name} now={now} />
            </td>
            <td className="whitespace-nowrap">
              <Resource usage={p.cpu} request={p.cpuRequest} />
            </td>
            <td className="whitespace-nowrap">
              <Resource usage={p.memory} request={p.memRequest} />
            </td>
            <td className="whitespace-nowrap font-mono text-xs">
              {p.node ? <Link to={nodeUrl(id, p.node)}>{p.node}</Link> : <span className="font-sans text-muted">not scheduled</span>}
            </td>
            <td className="whitespace-nowrap text-muted" title={p.createdAt}>
              {age(p.createdAt, now)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function WorkloadsTab({ id, ns, q, toolbar }: { id: string; ns: string; q: string; toolbar: (right: ReactNode) => ReactNode }) {
  const wl = useApi(() => api.clusterWorkloads(id, ns), [id, ns]);
  const now = useNow(60_000);
  const rows = useMemo(() => (wl.data ?? []).filter((d) => includes(q, d.name, d.namespace, d.kind, ...d.images)), [wl.data, q]);
  const behind = (d: Deployment) => {
    const [a, b] = d.ready.split("/");
    return a !== undefined && b !== undefined && a !== b;
  };
  return (
    <div className="flex flex-col gap-3">
      {toolbar(null)}
      <TableCard
        label="Workloads"
        loading={wl.loading}
        error={wl.error}
        onRetry={wl.reload}
        rows={wl.data ? rows : undefined}
        emptyTitle={(wl.data ?? []).length === 0 ? "No workloads" : "No workloads match"}
        emptyText={(wl.data ?? []).length === 0 ? `No deployments, statefulsets or daemonsets in ${ns || "any namespace"}.` : "Try a different filter."}
      >
        {(list) => (
          <table className="table min-w-[900px]">
            <thead>
              <tr>
                <th scope="col">Kind</th>
                <th scope="col">Name</th>
                <th scope="col">Namespace</th>
                <th scope="col">Ready</th>
                <th scope="col">Up-to-date</th>
                <th scope="col">Images</th>
                <th scope="col">Updated</th>
              </tr>
            </thead>
            <tbody>
              {list.map((d) => (
                <tr key={`${d.kind}/${d.namespace}/${d.name}`}>
                  <td>
                    <Pill>{d.kind}</Pill>
                  </td>
                  <td>
                    <Link to={clusterUrl(id, "pods", { ns: d.namespace, q: d.name })} className="whitespace-nowrap font-mono text-xs font-medium">
                      {d.name}
                    </Link>
                  </td>
                  <td className="whitespace-nowrap">{d.namespace}</td>
                  <td className={`tabular-nums ${behind(d) ? "font-semibold text-warn-fg" : ""}`}>{d.ready}</td>
                  <td className="tabular-nums">{d.upToDate}</td>
                  <td className="font-mono text-xs">
                    {d.images.length === 0 ? (
                      <Dash />
                    ) : (
                      d.images.map((img) => (
                        <div key={img} className="whitespace-nowrap">
                          {img}
                        </div>
                      ))
                    )}
                  </td>
                  <td className="whitespace-nowrap text-muted" title={d.updatedAt}>
                    {age(d.updatedAt || d.createdAt, now)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </TableCard>
    </div>
  );
}

function EventsTab({ id, ns, q, toolbar }: { id: string; ns: string; q: string; toolbar: (right: ReactNode) => ReactNode }) {
  const events = useApi(() => api.clusterEvents(id, ns), [id, ns]);
  const now = useNow(30_000);
  const rows = useMemo(() => (events.data ?? []).filter((e: KEvent) => includes(q, e.type, e.reason, e.object, e.message, e.namespace)), [events.data, q]);
  return (
    <div className="flex flex-col gap-3">
      {toolbar(<span>newest first · max 300</span>)}
      <TableCard
        label="Events"
        loading={events.loading}
        error={events.error}
        onRetry={events.reload}
        rows={events.data ? rows : undefined}
        emptyTitle={(events.data ?? []).length === 0 ? "No events" : "No events match"}
        emptyText={(events.data ?? []).length === 0 ? "Kubernetes keeps events for about an hour." : "Try a different filter."}
      >
        {(list) => <EventsTable events={list} now={now} />}
      </TableCard>
    </div>
  );
}

function ServicesTab({ id, ns, q, toolbar }: { id: string; ns: string; q: string; toolbar: (right: ReactNode) => ReactNode }) {
  const svc = useApi(() => api.clusterServices(id, ns), [id, ns]);
  const rows = useMemo(() => (svc.data ?? []).filter((s) => includes(q, s.name, s.namespace, s.type, s.clusterIP, s.external, s.ports)), [svc.data, q]);
  return (
    <div className="flex flex-col gap-3">
      {toolbar(null)}
      <TableCard
        label="Services"
        loading={svc.loading}
        error={svc.error}
        onRetry={svc.reload}
        rows={svc.data ? rows : undefined}
        emptyTitle={(svc.data ?? []).length === 0 ? "No services" : "No services match"}
        emptyText={(svc.data ?? []).length === 0 ? `No services in ${ns || "any namespace"}.` : "Try a different filter."}
      >
        {(list) => (
          <table className="table min-w-[820px]">
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Namespace</th>
                <th scope="col">Type</th>
                <th scope="col">Cluster IP</th>
                <th scope="col">External address</th>
                <th scope="col">Ports</th>
              </tr>
            </thead>
            <tbody>
              {list.map((s) => (
                <tr key={`${s.namespace}/${s.name}`}>
                  <td className="whitespace-nowrap font-mono text-xs font-medium">
                    <Link to={resourceUrl(id, "core/v1/services", s.name, s.namespace)}>{s.name}</Link>
                  </td>
                  <td className="whitespace-nowrap">{s.namespace}</td>
                  <td className="whitespace-nowrap">{s.type}</td>
                  <td className="whitespace-nowrap font-mono text-xs">{s.clusterIP || <Dash />}</td>
                  <td>
                    {s.type === "LoadBalancer" && (!s.external || s.external === "<pending>") ? (
                      <Pill tone="critical">&lt;pending&gt;</Pill>
                    ) : s.external ? (
                      <span className="font-mono text-xs">{s.external}</span>
                    ) : (
                      <Dash />
                    )}
                  </td>
                  <td className="font-mono text-xs">{s.ports || <Dash />}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </TableCard>
    </div>
  );
}

// ---------------------------------------------------------------- page

export default function ClusterViewPage() {
  const { tab: tabParam } = useParams();
  const tab: Tab = (TABS as readonly string[]).includes(tabParam ?? "") ? (tabParam as Tab) : "overview";
  const { id, cluster, detail, namespaces } = useCluster();
  const { ns, q, setQuery } = useNsQuery();
  useClusterSection(TAB_LABEL[tab]);

  const toolbar = (filterLabel: string) => (right: ReactNode) => (
    <Toolbar
      namespaces={namespaces ?? []}
      ns={ns ?? ""}
      q={q}
      onNs={(v) => setQuery({ ns: v })}
      onQ={(v) => setQuery({ q: v })}
      filterLabel={filterLabel}
      right={right}
    />
  );

  let content: ReactNode;
  if (NS_TABS.includes(tab) && ns === undefined) {
    content = <Loading label="Loading namespaces…" />;
  } else {
    const key = `${id}|${ns ?? ""}`;
    switch (tab) {
      case "overview":
        content = (
          <Overview
            key={id}
            id={id}
            scanNs={namespaces ? (namespaces.includes(OVERVIEW_SCAN_NS) ? OVERVIEW_SCAN_NS : null) : detail.error ? null : undefined}
          />
        );
        break;
      case "nodes":
        content = <NodesTab key={id} id={id} />;
        break;
      case "pods":
        content = <PodsTab key={key} id={id} ns={ns!} q={q} toolbar={toolbar("Filter pods")} />;
        break;
      case "workloads":
        content = <WorkloadsTab key={key} id={id} ns={ns!} q={q} toolbar={toolbar("Filter workloads")} />;
        break;
      case "events":
        content = <EventsTab key={key} id={id} ns={ns!} q={q} toolbar={toolbar("Filter events")} />;
        break;
      case "services":
        content = <ServicesTab key={key} id={id} ns={ns!} q={q} toolbar={toolbar("Filter services")} />;
        break;
    }
  }

  return (
    <>
      <SummaryTiles cluster={cluster} loading={detail.loading} />
      {content}
    </>
  );
}
