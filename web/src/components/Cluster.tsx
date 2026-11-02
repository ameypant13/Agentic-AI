import { useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { Link } from "react-router-dom";
import type { KEvent, Pod, PodIssueSeverity } from "../api/types";
import { age } from "../lib/format";
import { Pill, type Tone } from "./Pill";
import { Empty, ErrorCard, Loading } from "./States";

/** In-app URLs of the cluster explorer. `query.ns` "" means all namespaces. */
const enc = encodeURIComponent;
export const clusterUrl = (id: string, tab: string, query?: { ns?: string; q?: string }) => {
  const p = new URLSearchParams();
  if (query?.ns !== undefined) p.set("ns", query.ns);
  if (query?.q) p.set("q", query.q);
  const qs = p.toString();
  return `/clusters/${enc(id)}/${tab}${qs ? `?${qs}` : ""}`;
};
export const podUrl = (id: string, ns: string, name: string, tab?: string) =>
  `/clusters/${enc(id)}/pods/${enc(ns)}/${enc(name)}${tab && tab !== "overview" ? `/${tab}` : ""}`;

const withQuery = (path: string, query: Record<string, string | undefined>) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) if (v !== undefined) p.set(k, v);
  const qs = p.toString();
  return `${path}${qs ? `?${qs}` : ""}`;
};
const keyPath = (key: string) => key.split("/").map(enc).join("/");

export const POD_KEY = "core/v1/pods";
export const NODE_KEY = "core/v1/nodes";
export const NAMESPACE_KEY = "core/v1/namespaces";

export const nodeUrl = (id: string, name: string) => `/clusters/${enc(id)}/nodes/${enc(name)}`;
export const helmUrl = (id: string, ns?: string) => withQuery(`/clusters/${enc(id)}/helm`, { ns });
export const workloadsOverviewUrl = (id: string, ns?: string) => withQuery(`/clusters/${enc(id)}/workloads-overview`, { ns });
/** Generic list of one resource type. Pods and nodes go to their richer tabs. `ns` "" = all namespaces. */
export const resourceListUrl = (id: string, key: string, ns?: string) => {
  if (key === POD_KEY) return clusterUrl(id, "pods", ns !== undefined ? { ns } : undefined);
  if (key === NODE_KEY) return clusterUrl(id, "nodes");
  return withQuery(`/clusters/${enc(id)}/r/${keyPath(key)}`, { ns });
};
/** Detail page of one object. Pods and nodes go to their own pages. */
export const resourceUrl = (id: string, key: string, name: string, ns?: string, tab?: string) => {
  if (key === POD_KEY && ns) return podUrl(id, ns, name);
  if (key === NODE_KEY) return nodeUrl(id, name);
  return withQuery(`/clusters/${enc(id)}/r/${keyPath(key)}/${enc(name)}`, { ns: ns || undefined, tab: tab && tab !== "overview" ? tab : undefined });
};

/** Requested share above which a bar / figure turns amber. */
export const HIGH_PCT = 75;

export function Dash() {
  return <span className="text-muted">—</span>;
}

/** Small horizontal bar plus the rounded percentage. Amber above HIGH_PCT. */
export function PctBar({ pct, label }: { pct: number | null | undefined; label: string }) {
  if (pct === null || pct === undefined || Number.isNaN(pct)) return <Dash />;
  const value = Math.round(pct);
  const width = Math.min(100, Math.max(0, value));
  const high = value > HIGH_PCT;
  return (
    <div className="flex items-center gap-2" title={`${label}: ${value}%`}>
      <div aria-hidden="true" className="h-1.5 w-20 flex-none overflow-hidden rounded-full bg-line">
        <div className={`h-full ${high ? "bg-[#B45309]" : "bg-violet"}`} style={{ width: `${width}%` }} />
      </div>
      <span className={`tabular-nums ${high ? "font-semibold text-warn-fg" : ""}`}>{value}%</span>
    </div>
  );
}

/** Pod status → colour of the dot in the pods table. */
export function podStatusColor(status: string): string {
  if (status === "Running" || status === "Completed" || status === "Succeeded") return "#059669";
  if (status === "Pending" || status === "ContainerCreating" || status === "PodInitializing" || status.startsWith("Init:")) return "#B45309";
  return "#B91C1C";
}

export function podStatusTone(status: string): Tone {
  if (status === "Running") return "success";
  if (status === "Completed" || status === "Succeeded") return "neutral";
  if (status === "Pending" || status === "ContainerCreating" || status === "PodInitializing" || status.startsWith("Init:")) return "warning";
  return "critical";
}

export function PodStatus({ status }: { status: string }) {
  return (
    <span className="inline-flex items-center whitespace-nowrap">
      <span aria-hidden="true" className="mr-1.5 inline-block h-2 w-2 flex-none rounded-full" style={{ background: podStatusColor(status) }} />
      {status || "Unknown"}
    </span>
  );
}

export function issueTone(sev: PodIssueSeverity): Tone {
  return sev === "critical" ? "critical" : sev === "warning" ? "warning" : "neutral";
}

export function IssuePill({ issue, severity }: { issue: string; severity: PodIssueSeverity }) {
  if (!issue) return <Dash />;
  return <Pill tone={issueTone(severity)}>{issue}</Pill>;
}

/** control-hub infrastructure status → pill tone. */
export function infraStatusTone(status: string): Tone {
  const s = status.toLowerCase();
  if (/fail|error|degraded/.test(s)) return "critical";
  if (/pend|progress|creat|updat|delet|provision|upgrad/.test(s)) return "warning";
  if (/ready|active|running|healthy|success|complete|available|provisioned/.test(s)) return "success";
  return "neutral";
}

/** Re-renders every `ms` so relative times stay current. */
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(t);
  }, [ms]);
  return now;
}

/** Card that renders loading / error / empty / table consistently. */
export function TableCard<T>({
  label,
  loading,
  error,
  onRetry,
  rows,
  emptyTitle,
  emptyText,
  children,
}: {
  label: string;
  loading: boolean;
  error: Error | undefined;
  onRetry: () => void;
  rows: T[] | undefined;
  emptyTitle: string;
  emptyText?: ReactNode;
  children: (rows: T[]) => ReactNode;
}) {
  return (
    <section className="card" aria-label={label}>
      {error && !rows ? (
        <div className="p-4">
          <ErrorCard title={`Couldn't load ${label.toLowerCase()}`} error={error} onRetry={onRetry} />
        </div>
      ) : !rows ? (
        loading ? <Loading label={`Loading ${label.toLowerCase()}…`} /> : null
      ) : (
        <>
          {error && (
            <div className="border-b border-line p-3">
              <ErrorCard title="Refresh failed — showing the last data" error={error} onRetry={onRetry} />
            </div>
          )}
          {rows.length === 0 ? <Empty title={emptyTitle}>{emptyText}</Empty> : <div className="overflow-x-auto">{children(rows)}</div>}
        </>
      )}
    </section>
  );
}

export function EventTypePill({ type }: { type: string }) {
  return <Pill tone={type === "Warning" ? "warning" : "neutral"}>{type}</Pill>;
}

export function EventsTable({ events, showObject = true, now }: { events: KEvent[]; showObject?: boolean; now?: number }) {
  return (
    <table className="table min-w-[760px]">
      <thead>
        <tr>
          <th scope="col">Type</th>
          <th scope="col">Reason</th>
          {showObject && <th scope="col">Object</th>}
          <th scope="col">Message</th>
          <th scope="col">Count</th>
          <th scope="col">Last seen</th>
        </tr>
      </thead>
      <tbody>
        {events.map((e, i) => (
          <tr key={`${e.namespace}/${e.object}/${e.reason}/${i}`}>
            <td>
              <EventTypePill type={e.type} />
            </td>
            <td className="whitespace-nowrap font-medium">{e.reason}</td>
            {showObject && (
              <td className="whitespace-nowrap font-mono text-xs">
                {e.object}
                {e.namespace && <div className="text-muted">{e.namespace}</div>}
              </td>
            )}
            <td className="min-w-[320px] break-words">{e.message}</td>
            <td className="tabular-nums">{e.count}</td>
            <td className="whitespace-nowrap text-muted" title={e.lastSeen}>
              {age(e.lastSeen, now)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** CPU / memory cell: live usage when metrics-server has it, else the request (marked). */
export function Resource({ usage, request }: { usage: string; request: string }) {
  if (usage) return <span className="font-mono text-xs">{usage}</span>;
  if (request)
    return (
      <span className="font-mono text-xs text-muted" title="requested (no live usage)">
        {request} <span className="font-sans">req</span>
      </span>
    );
  return <Dash />;
}

/**
 * Pods table used on detail pages (workload, service, node): same look as the pods tab
 * without the log-scan column. Names link to the pod page, nodes to the node page.
 */
export function PodsMiniTable({ id, pods, showNs, showNode = true, now }: { id: string; pods: Pod[]; showNs: boolean; showNode?: boolean; now: number }) {
  return (
    <table className="table min-w-[900px]">
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Status</th>
          <th scope="col">Ready</th>
          <th scope="col">Restarts</th>
          <th scope="col">K8s issue</th>
          <th scope="col">CPU</th>
          <th scope="col">Memory</th>
          {showNode && <th scope="col">Node</th>}
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
            <td className="whitespace-nowrap">
              <Resource usage={p.cpu} request={p.cpuRequest} />
            </td>
            <td className="whitespace-nowrap">
              <Resource usage={p.memory} request={p.memRequest} />
            </td>
            {showNode && (
              <td className="whitespace-nowrap font-mono text-xs">
                {p.node ? <Link to={nodeUrl(id, p.node)}>{p.node}</Link> : <span className="font-sans text-muted">not scheduled</span>}
              </td>
            )}
            <td className="whitespace-nowrap text-muted" title={p.createdAt}>
              {age(p.createdAt, now)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/**
 * Calls `reload` every `ms` while the browser tab is visible, and catches up immediately when it
 * becomes visible again after being hidden for longer than `ms`.
 */
export function useAutoRefresh(reload: () => void, ms: number) {
  useEffect(() => {
    let last = Date.now();
    const tick = () => {
      if (document.visibilityState !== "visible") return;
      last = Date.now();
      reload();
    };
    const timer = window.setInterval(tick, ms);
    const onVis = () => {
      if (document.visibilityState === "visible" && Date.now() - last >= ms) tick();
    };
    document.addEventListener("visibilitychange", onVis);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [reload, ms]);
}

/** Epoch ms of the last time `data` changed (null until the first load). */
export function useRefreshedAt(data: unknown): number | null {
  const [at, setAt] = useState<number | null>(null);
  useEffect(() => {
    if (data !== undefined) setAt(Date.now());
  }, [data]);
  return at;
}

function isTyping(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || el.isContentEditable;
}

/**
 * `/` focuses the page's filter (an input marked `data-slash-filter="page"`), else the resource
 * tree's filter (`data-slash-filter="tree"`). Ignored while typing or with a modifier key.
 * Mounted once by the cluster explorer, so it is local to those pages.
 */
export function useSlashToFocus(scope: RefObject<HTMLElement>) {
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented || isTyping(e.target)) return;
      const root = scopeRef.current.current;
      if (!root) return;
      const target =
        root.querySelector<HTMLInputElement>('[data-slash-filter="page"]') ?? root.querySelector<HTMLInputElement>('[data-slash-filter="tree"]');
      if (!target) return;
      e.preventDefault();
      target.focus();
      target.select();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
}

/** Labels / annotations as chips. Long values are cut with the full text in a tooltip. */
export function Chips({ map, empty = "none", mono = true }: { map: Record<string, string>; empty?: string; mono?: boolean }) {
  const entries = Object.entries(map ?? {}).sort(([a], [b]) => a.localeCompare(b));
  if (entries.length === 0) return <span className="text-[13px] text-muted">{empty}</span>;
  return (
    <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
      {entries.map(([k, v]) => {
        const text = v === "" ? k : `${k}=${v}`;
        return (
          <li
            key={k}
            className={`max-w-full truncate rounded-full border border-line bg-slate-50 px-2 py-0.5 text-xs text-slate-700 ${mono ? "font-mono" : ""}`}
            title={text}
          >
            {text.length > 120 ? `${text.slice(0, 120)}…` : text}
          </li>
        );
      })}
    </ul>
  );
}

/** Namespace selector + text filter + right-aligned status area above the explorer's tables. */
export function Toolbar({
  namespaces,
  ns,
  q,
  onNs,
  onQ,
  filterLabel,
  right,
  showNs = true,
}: {
  namespaces: string[];
  ns: string;
  q: string;
  onNs: (v: string) => void;
  onQ: (v: string) => void;
  filterLabel: string;
  right?: ReactNode;
  /** false for cluster-scoped kinds */
  showNs?: boolean;
}) {
  const options = ns && !namespaces.includes(ns) ? [ns, ...namespaces] : namespaces;
  return (
    <div className="flex flex-wrap items-center gap-2">
      {showNs && (
        <>
          <label htmlFor="cv-ns" className="text-[13px] text-muted">
            Namespace
          </label>
          <select id="cv-ns" className="select min-h-[36px]" value={ns} onChange={(e) => onNs(e.target.value)}>
            <option value="">All namespaces</option>
            {options.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </>
      )}
      <input
        type="search"
        className="input min-h-[36px] w-[220px] text-[13px]"
        aria-label={filterLabel}
        placeholder={`${filterLabel}  ( / )`}
        data-slash-filter="page"
        value={q}
        onChange={(e) => onQ(e.target.value)}
      />
      {right && <div className="ml-auto flex items-center gap-2 text-xs text-muted">{right}</div>}
    </div>
  );
}

