import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { Link, Outlet, useLocation, useParams, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { CLUSTER_KIND_LABEL, isMetricsOnlyId, type APIResource, type Cluster, type ClusterDetail } from "../api/types";
import { MetricsOnlyOverview } from "../pages/ClusterView";
import { initials, providerLabel } from "../lib/format";
import { useApi, type ApiState } from "../lib/useApi";
import { NAMESPACE_KEY, clusterUrl, helmUrl, infraStatusTone, resourceListUrl, useSlashToFocus, workloadsOverviewUrl } from "./Cluster";
import { Spinner } from "./Icons";
import { Pill } from "./Pill";
import { ErrorCard } from "./States";

// ---------------------------------------------------------------- context

type ClusterCtx = {
  id: string;
  cluster: Cluster | undefined;
  clusters: ApiState<Cluster[]>;
  detail: ApiState<ClusterDetail>;
  /** undefined while the cluster detail is loading */
  namespaces: string[] | undefined;
  resources: ApiState<APIResource[]>;
  setSection: (s: string) => void;
};

const Ctx = createContext<ClusterCtx | null>(null);

export function useCluster(): ClusterCtx {
  const c = useContext(Ctx);
  if (!c) throw new Error("useCluster must be used inside the cluster explorer");
  return c;
}

/** Sets the "<cluster> · <section>" title of the explorer header while the page is mounted. */
export function useClusterSection(section: string) {
  const { setSection } = useCluster();
  useEffect(() => setSection(section), [section, setSection]);
}

/** Namespace whose view the explorer opens on when `?ns=` is absent and the cluster has it. */
export const DEFAULT_NS = "prophecy";

/**
 * `?ns=` and `?q=` of the explorer pages. `?ns=` absent → DEFAULT_NS if the cluster has it, else all;
 * `?ns=` empty → all namespaces. `ns` is undefined while namespaces are loading.
 */
export function useNsQuery() {
  const { namespaces, detail } = useCluster();
  const [params, setParams] = useSearchParams();
  const nsParam = params.get("ns");
  const q = params.get("q") ?? "";
  let ns: string | undefined;
  if (nsParam !== null) ns = nsParam;
  else if (namespaces) ns = namespaces.includes(DEFAULT_NS) ? DEFAULT_NS : "";
  else if (detail.error) ns = "";

  const setQuery = useCallback(
    (patch: Record<string, string | undefined>) =>
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          for (const [k, v] of Object.entries(patch)) {
            if (v === undefined) continue;
            // ns keeps an explicit "" (= all namespaces); other keys are dropped when empty
            if (v || k === "ns") next.set(k, v);
            else next.delete(k);
          }
          return next;
        },
        { replace: true },
      ),
    [setParams],
  );
  return { ns, nsParam, q, setQuery, params };
}

// ---------------------------------------------------------------- tree model

type TreeItem = {
  label: string;
  to: string;
  /** path prefix(es) that mark this item active */
  match: (sub: string) => boolean;
  /** carry the current `?ns=` to this page */
  nsScoped: boolean;
  /** extra text the filter matches */
  keywords: string;
};
type TreeSection = { name: string; items: TreeItem[]; groups: { group: string; items: TreeItem[] }[] };

const SECTION_ORDER = ["Cluster", "Workloads", "Config", "Network", "Storage", "Access Control", "Helm", "Custom Resources", "Other"];

/** Order of the well-known kinds inside their section (by resource name). Anything else follows by label. */
const ORDER: Record<string, string[]> = {
  Workloads: ["deployments", "daemonsets", "statefulsets", "replicasets", "jobs", "cronjobs"],
  Config: [
    "configmaps",
    "secrets",
    "resourcequotas",
    "limitranges",
    "horizontalpodautoscalers",
    "poddisruptionbudgets",
    "priorityclasses",
    "runtimeclasses",
    "leases",
    "mutatingwebhookconfigurations",
    "validatingwebhookconfigurations",
  ],
  Network: [
    "services",
    "endpoints",
    "endpointslices",
    "ingresses",
    "ingressclasses",
    "networkpolicies",
    "gatewayclasses",
    "gateways",
    "httproutes",
    "grpcroutes",
    "tlsroutes",
    "tcproutes",
    "udproutes",
    "referencegrants",
  ],
  Storage: [
    "persistentvolumeclaims",
    "persistentvolumes",
    "storageclasses",
    "volumesnapshots",
    "volumesnapshotclasses",
    "volumesnapshotcontents",
    "volumeattachments",
    "csidrivers",
    "csinodes",
    "csistoragecapacities",
  ],
  "Access Control": ["serviceaccounts", "roles", "rolebindings", "clusterroles", "clusterrolebindings"],
};

const LABELS: Record<string, string> = {
  horizontalpodautoscalers: "HPA",
  poddisruptionbudgets: "Pod Disruption Budgets",
  mutatingwebhookconfigurations: "Mutating Webhooks",
  validatingwebhookconfigurations: "Validating Webhooks",
  persistentvolumeclaims: "Persistent Volume Claims",
  persistentvolumes: "Persistent Volumes",
  storageclasses: "Storage Classes",
  customresourcedefinitions: "Definitions",
};

/** Groups whose kinds belong in a built-in section even though the server files them elsewhere. */
const GROUP_SECTION: Record<string, string> = {
  "gateway.networking.k8s.io": "Network",
  "snapshot.storage.k8s.io": "Storage",
};

/** Kinds that have a fixed tree entry (their own tab or page). */
const STATIC = new Set(["core/pods", "core/nodes", "core/namespaces", "core/events", "events.k8s.io/events"]);

/** "Ingress" → "Ingresses", "NetworkPolicy" → "NetworkPolicies"; kinds already plural ("Endpoints") stay. */
export function pluralKind(kind: string, resource?: string): string {
  if (resource && resource === kind.toLowerCase()) return kind;
  if (/(s|x|z|ch|sh)$/.test(kind)) return `${kind}es`;
  if (/[^aeiou]y$/.test(kind)) return `${kind.slice(0, -1)}ies`;
  return `${kind}s`;
}

export function resourceLabel(r: Pick<APIResource, "kind" | "resource">): string {
  return LABELS[r.resource] ?? pluralKind(r.kind, r.resource);
}

function buildTree(id: string, resources: APIResource[] | undefined): TreeSection[] {
  const e = encodeURIComponent;
  const base = `/clusters/${e(id)}`;
  const exact = (...paths: string[]) => (sub: string) => paths.includes(sub);
  const prefix = (p: string) => (sub: string) => sub === p || sub.startsWith(`${p}/`);
  const tab = (t: string, label: string, nsScoped: boolean, extra?: (sub: string) => boolean): TreeItem => ({
    label,
    to: `${base}/${t}`,
    match: (sub) => exact(`/${t}`)(sub) || (t === "overview" && sub === "") || !!extra?.(sub),
    nsScoped,
    keywords: t,
  });
  const generic = (r: APIResource, label: string): TreeItem => ({
    label,
    to: resourceListUrl(id, r.key),
    match: prefix(`/r/${r.key.split("/").map(e).join("/")}`),
    nsScoped: r.namespaced,
    keywords: `${r.kind} ${r.resource} ${r.group}`,
  });

  const sections = new Map<string, TreeSection>(SECTION_ORDER.map((n) => [n, { name: n, items: [], groups: [] }]));
  const nsKey = NAMESPACE_KEY.split("/").map(e).join("/");
  sections.get("Cluster")!.items.push(
    tab("overview", "Overview", false),
    tab("nodes", "Nodes", false, prefix("/nodes")),
    { label: "Namespaces", to: `${base}/r/${nsKey}`, match: prefix(`/r/${nsKey}`), nsScoped: false, keywords: "namespace ns" },
    tab("events", "Events", true),
  );
  sections.get("Workloads")!.items.push(
    { label: "Overview", to: workloadsOverviewUrl(id), match: exact("/workloads-overview"), nsScoped: true, keywords: "workloads" },
    tab("pods", "Pods", true),
  );
  sections.get("Helm")!.items.push({ label: "Releases", to: helmUrl(id), match: exact("/helm"), nsScoped: true, keywords: "helm charts releases" });

  // Served kinds, bucketed by section.
  const buckets = new Map<string, APIResource[]>();
  for (const r of resources ?? []) {
    if (STATIC.has(`${r.group || "core"}/${r.resource}`)) continue;
    const section = GROUP_SECTION[r.group] ?? (SECTION_ORDER.includes(r.category) ? r.category : "Other");
    if (!buckets.has(section)) buckets.set(section, []);
    buckets.get(section)!.push(r);
  }

  for (const [name, list] of buckets) {
    const section = sections.get(name)!;
    // The same resource name served by two groups gets its group in the label.
    const dupes = new Set(list.map((r) => r.resource).filter((res, i, all) => all.indexOf(res) !== i));
    const label = (r: APIResource) => (dupes.has(r.resource) ? `${resourceLabel(r)} (${r.group || "core"})` : resourceLabel(r));

    if (name === "Custom Resources") {
      const defs = list.filter((r) => r.resource === "customresourcedefinitions");
      section.items.push(...defs.map((r) => generic(r, label(r))));
      const byGroup = new Map<string, APIResource[]>();
      for (const r of list) {
        if (r.resource === "customresourcedefinitions") continue;
        const g = r.group || "core";
        if (!byGroup.has(g)) byGroup.set(g, []);
        byGroup.get(g)!.push(r);
      }
      section.groups = [...byGroup.entries()]
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([group, rs]) => ({ group, items: rs.map((r) => generic(r, label(r))).sort((a, b) => a.label.localeCompare(b.label)) }));
      continue;
    }

    const order = ORDER[name] ?? [];
    const rank = (r: APIResource) => {
      const i = order.indexOf(r.resource);
      return i === -1 ? order.length : i;
    };
    section.items.push(
      ...[...list].sort((a, b) => rank(a) - rank(b) || label(a).localeCompare(label(b))).map((r) => generic(r, label(r))),
    );
  }

  return [...sections.values()].filter((s) => s.items.length > 0 || s.groups.length > 0);
}

// ---------------------------------------------------------------- collapsed state

const COLLAPSE_KEY = "aic.clusterTree.collapsed";

function readCollapsed(): Record<string, boolean> {
  try {
    const raw = window.localStorage.getItem(COLLAPSE_KEY);
    const v: unknown = raw ? JSON.parse(raw) : {};
    return v && typeof v === "object" ? (v as Record<string, boolean>) : {};
  } catch {
    return {};
  }
}

function useCollapsed() {
  const [state, setState] = useState<Record<string, boolean>>(readCollapsed);
  const toggle = useCallback((key: string, current: boolean) => {
    setState((prev) => {
      const next = { ...prev, [key]: !current };
      try {
        window.localStorage.setItem(COLLAPSE_KEY, JSON.stringify(next));
      } catch {
        // storage blocked (private mode); keep the in-memory state
      }
      return next;
    });
  }, []);
  return { state, toggle };
}

// ---------------------------------------------------------------- frame

function Chevron({ open }: { open: boolean }) {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2.5}
      aria-hidden="true"
      className={`flex-none transition-transform ${open ? "rotate-90" : ""}`}
    >
      <path d="M9 6l6 6-6 6" />
    </svg>
  );
}

/** Sub-path of the explorer after `/clusters/<id>`, e.g. "/r/apps/v1/deployments". */
function useSubPath(): string {
  const { pathname } = useLocation();
  const parts = pathname.split("/");
  return parts.length > 3 ? `/${parts.slice(3).join("/")}`.replace(/\/$/, "") : "";
}

/** Where a cluster in the rail links to: the same list (not a detail page, which won't exist there). */
function railTarget(sub: string): string {
  if (sub.startsWith("/r/")) return sub.split("/").slice(0, 5).join("/");
  if (sub.startsWith("/nodes/")) return "/nodes";
  return sub;
}

function Rail({ clusters, currentId }: { clusters: ApiState<Cluster[]>; currentId: string }) {
  const sub = railTarget(useSubPath());
  return (
    <nav
      aria-label="Clusters"
      className="flex flex-none flex-row items-center gap-2.5 overflow-x-auto bg-ink px-3 py-2.5 min-[900px]:w-[60px] min-[900px]:flex-col min-[900px]:overflow-x-visible min-[900px]:px-2.5 min-[900px]:py-3"
    >
      <Link to="/clusters" className="rail-btn" aria-label="All clusters" title="All clusters">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} aria-hidden="true">
          <path d="M4 6h16M4 12h16M4 18h16" />
        </svg>
      </Link>
      <span aria-hidden="true" className="h-6 w-px flex-none bg-slate-700 min-[900px]:h-px min-[900px]:w-6" />
      {clusters.loading && !clusters.data && <Spinner className="text-slate-300" />}
      {clusters.error && !clusters.data && (
        <span className="text-slate-300" title={`Couldn't load clusters: ${clusters.error.message}`}>
          !
        </span>
      )}
      {clusters.data?.map((c) => {
        const active = c.id === currentId;
        return (
          <Link
            key={c.id}
            to={`/clusters/${encodeURIComponent(c.id)}${sub}`}
            className={`rail-btn relative ${active ? "active" : ""}`}
            aria-label={c.name || c.id}
            aria-current={active ? "page" : undefined}
            title={`${c.name || c.id}${c.error ? " — unreachable" : ""}`}
          >
            {initials(c.name || c.id)}
            {c.error && <span aria-hidden="true" className="absolute right-1 top-1 h-2 w-2 rounded-full bg-red-500" />}
          </Link>
        );
      })}
    </nav>
  );
}

function Tree({ id, cluster, resources }: { id: string; cluster: Cluster | undefined; resources: ApiState<APIResource[]> }) {
  const sub = useSubPath();
  const [params] = useSearchParams();
  const nsParam = params.get("ns");
  const [filter, setFilter] = useState("");
  const [openNarrow, setOpenNarrow] = useState(false);
  const { state: collapsed, toggle } = useCollapsed();
  const sections = useMemo(() => buildTree(id, resources.data), [id, resources.data]);

  const needle = filter.trim().toLowerCase();
  const matches = (it: TreeItem, ...extra: string[]) =>
    !needle || [it.label, it.keywords, ...extra].some((s) => s.toLowerCase().includes(needle));

  const href = (it: TreeItem) => {
    if (!it.nsScoped || nsParam === null) return it.to;
    return `${it.to}${it.to.includes("?") ? "&" : "?"}ns=${encodeURIComponent(nsParam)}`;
  };

  const link = (it: TreeItem, indent = false) => {
    const active = it.match(sub);
    return (
      <li key={it.to}>
        <Link
          to={href(it)}
          className={`tree-link ${indent ? "min-[900px]:!pl-[38px] pl-6" : ""} ${active ? "active" : ""}`}
          aria-current={active ? "page" : undefined}
          title={it.label}
        >
          <span className="block truncate">{it.label}</span>
        </Link>
      </li>
    );
  };

  let shown = 0;
  const body = sections.map((s) => {
    const items = s.items.filter((it) => matches(it));
    const groups = s.groups
      .map((g) => ({ ...g, items: g.items.filter((it) => matches(it, g.group)) }))
      .filter((g) => g.items.length > 0);
    if (items.length === 0 && groups.length === 0) return null;
    shown += items.length + groups.reduce((n, g) => n + g.items.length, 0);
    const isCollapsed = !needle && !!collapsed[s.name];
    const headId = `tree-${s.name.replace(/\W+/g, "-")}`;
    return (
      <div key={s.name} className="flex flex-col">
        <button
          type="button"
          id={headId}
          className="flex w-full cursor-pointer items-center gap-1.5 rounded-btn border-0 bg-transparent px-2 pb-1 pt-3 text-left text-[11px] font-semibold uppercase tracking-wide text-muted hover:text-ink"
          aria-expanded={!isCollapsed}
          onClick={() => toggle(s.name, !!collapsed[s.name])}
        >
          <Chevron open={!isCollapsed} />
          {s.name}
          {s.name === "Custom Resources" && groups.length > 0 && <span className="ml-auto font-normal normal-case">
              <span className="sr-only">, </span>
              {groups.length}
              <span className="sr-only"> API groups</span>
            </span>}
        </button>
        {!isCollapsed && (
          <ul className="m-0 flex list-none flex-col gap-0.5 p-0" aria-labelledby={headId}>
            {items.map((it) => link(it))}
            {groups.map((g) => {
              const key = `crd:${g.group}`;
              // API groups start collapsed; one holding the open page starts expanded.
              const holdsActive = g.items.some((it) => it.match(sub));
              const stored = collapsed[key];
              const gCollapsed = !needle && (stored === undefined ? !holdsActive : stored);
              return (
                <li key={key}>
                  <button
                    type="button"
                    className="flex w-full cursor-pointer items-center gap-1.5 rounded-btn border-0 bg-transparent py-1.5 pl-3 pr-2 text-left text-[13px] text-slate-700 hover:bg-[#EEF0F4] hover:text-ink min-[900px]:pl-[14px]"
                    aria-expanded={!gCollapsed}
                    onClick={() => toggle(key, gCollapsed)}
                    title={g.group}
                  >
                    <Chevron open={!gCollapsed} />
                    <span className="min-w-0 truncate">{g.group}</span>
                    <span className="ml-auto text-xs text-muted">
                      <span className="sr-only">, </span>
                      {g.items.length}
                      <span className="sr-only"> kinds</span>
                    </span>
                  </button>
                  {!gCollapsed && <ul className="m-0 flex list-none flex-col gap-0.5 p-0">{g.items.map((it) => link(it, true))}</ul>}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    );
  });

  return (
    <nav
      aria-label="Resources"
      className="flex flex-none flex-col border-b border-line bg-white p-2 min-[900px]:sticky min-[900px]:top-0 min-[900px]:max-h-screen min-[900px]:w-[228px] min-[900px]:self-start min-[900px]:overflow-y-auto min-[900px]:border-b-0 min-[900px]:border-r"
    >
      <div className="flex items-start justify-between gap-2 px-2 pb-1 pt-2">
        <div className="min-w-0">
          <div className="truncate text-[13px] font-semibold" title={cluster?.name ?? id}>
            {cluster?.name || id}
          </div>
          {cluster && (
            <div className="text-xs text-muted">
              {providerLabel(cluster.provider)} · {cluster.region || "—"}
            </div>
          )}
        </div>
        <button
          type="button"
          className="btn min-h-[30px] px-2.5 text-xs min-[900px]:hidden"
          aria-expanded={openNarrow}
          aria-controls="cluster-tree-body"
          onClick={() => setOpenNarrow((o) => !o)}
        >
          {openNarrow ? "Hide resources" : "Resources"}
        </button>
      </div>
      <div id="cluster-tree-body" className={`${openNarrow ? "flex" : "hidden"} flex-col min-[900px]:flex`}>
        <div className="px-1 pb-1 pt-2">
          <input
            type="search"
            className="input min-h-[32px] text-[13px]"
            placeholder="Filter kinds  ( / )"
            aria-label="Filter resource kinds"
            data-slash-filter="tree"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") setFilter("");
            }}
          />
        </div>
        {resources.loading && !resources.data && (
          <div role="status" className="flex items-center gap-2 px-2 pt-2 text-xs text-muted">
            <Spinner size={12} /> Discovering kinds…
          </div>
        )}
        {resources.error && !resources.data && (
          <div className="px-2 pt-2 text-xs text-crit-fg">
            Couldn't list the cluster's kinds.{" "}
            <button type="button" className="link-btn text-xs" onClick={resources.reload}>
              Retry
            </button>
          </div>
        )}
        {body}
        {needle && shown === 0 && <div className="px-2 pt-3 text-xs text-muted">No kinds match “{filter}”.</div>}
      </div>
    </nav>
  );
}

/** Rail + resource tree + "<cluster> · <section>" header around every cluster explorer page. */
export function ClusterShell() {
  const { id = "" } = useParams();
  const clusters = useApi(() => api.clusters());
  const detail = useApi<ClusterDetail>(() => api.cluster(id), [id]);
  const metricsOnly = isMetricsOnlyId(id);
  // Metrics-only clusters have no API server to discover resources from.
  const resources = useApi(() => (metricsOnly ? Promise.resolve([] as APIResource[]) : api.apiResources(id)), [id]);
  const [section, setSection] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  useSlashToFocus(rootRef);

  // The detail and the list carry the same Cluster fields; prefer the detail, fall back to the list row.
  const fromList = clusters.data?.find((c) => c.id === id);
  const cluster: Cluster | undefined = detail.data?.id === id ? detail.data : fromList;
  const namespaces = detail.data?.id === id ? detail.data.namespaces : undefined;

  const ctx = useMemo<ClusterCtx>(
    () => ({ id, cluster, clusters, detail, namespaces, resources, setSection }),
    [id, cluster, clusters, detail, namespaces, resources],
  );

  return (
    <Ctx.Provider value={ctx}>
      <div ref={rootRef} className="flex w-full min-w-0 flex-col min-[900px]:flex-row">
        <Rail clusters={clusters} currentId={id} />
        {!metricsOnly && <Tree id={id} cluster={cluster} resources={resources} />}
        <div className="flex min-w-0 flex-1 flex-col gap-4 px-4 py-5 sm:px-6">
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="m-0 text-xl font-semibold">
              <Link to={clusterUrl(id, "overview")} className="text-ink no-underline hover:text-violet">
                {cluster?.name || id}
              </Link>
              {section && <span className="font-normal text-muted"> · {section}</span>}
            </h1>
            {cluster?.error ? (
              <Pill tone="critical">Unreachable</Pill>
            ) : (
              cluster?.status && <Pill tone={infraStatusTone(cluster.status)}>{cluster.status}</Pill>
            )}
            {cluster?.kubernetesVersion && <Pill>K8s {cluster.kubernetesVersion}</Pill>}
            {cluster?.kind && cluster.kind !== "infrastructure" && <Pill tone="neutral">{CLUSTER_KIND_LABEL[cluster.kind]}</Pill>}
            {metricsOnly && <Pill tone="info">metrics only</Pill>}
            {cluster && cluster.tenants.length > 0 && (
              <span className="text-xs text-muted" title={cluster.tenants.join(", ")}>
                {cluster.tenants.length === 1 ? cluster.tenants[0] : `${cluster.tenants.length} tenants`}
              </span>
            )}
          </div>

          {detail.error && <ErrorCard title="Couldn't load this cluster" error={detail.error} onRetry={detail.reload} />}
          {cluster?.error && <ErrorCard title="The cluster could not be reached" error={cluster.error} onRetry={detail.reload} />}

          {metricsOnly ? <MetricsOnlyOverview id={id} cluster={cluster} loading={detail.loading} /> : <Outlet />}
        </div>
      </div>
    </Ctx.Provider>
  );
}


