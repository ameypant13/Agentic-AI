import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { Cluster } from "../api/types";
import { Dash, infraStatusTone } from "../components/Cluster";
import { Page } from "../components/Layout";
import { Pill } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { providerLabel } from "../lib/format";
import { useApi } from "../lib/useApi";

const CLOUDS = ["all", "aws", "gcp", "azure"] as const;
const HIGH_PCT = 80;

function matches(c: Cluster, q: string): boolean {
  if (!q) return true;
  const needle = q.toLowerCase();
  return [c.name, c.region, c.provider, ...c.tenants].some((s) => s.toLowerCase().includes(needle));
}

/** The cloud says the cluster no longer exists, though control-hub still lists it. */
const isGone = (c: Cluster) => /deleted|not found in/i.test(c.error);

/** Attention first: unreachable, nodes not ready, pods not ready; then busiest CPU. */
function attention(c: Cluster): number {
  const s = c.summary;
  if (c.error) return 0;
  if (!s) return 4;
  if (s.nodesReady < s.nodes) return 1;
  if (s.podsNotReady > 0) return 2;
  return 3;
}
function byAttention(a: Cluster, b: Cluster): number {
  const d = attention(a) - attention(b);
  if (d) return d;
  const ca = a.summary?.cpuRequestedPct ?? -1;
  const cb = b.summary?.cpuRequestedPct ?? -1;
  if (ca !== cb) return cb - ca;
  return a.name.localeCompare(b.name);
}

function Mini({ pct, label }: { pct: number; label: string }) {
  const v = Math.round(pct);
  const high = v > HIGH_PCT;
  return (
    <div className="flex items-center gap-1.5" title={`${label}: ${v}%`}>
      <div aria-hidden="true" className="h-1.5 w-10 flex-none overflow-hidden rounded-full bg-line">
        <div className={`h-full ${high ? "bg-[#B45309]" : "bg-violet"}`} style={{ width: `${Math.min(100, Math.max(0, v))}%` }} />
      </div>
      <span className={`tabular-nums ${high ? "font-semibold text-warn-fg" : ""}`}>{v}%</span>
    </div>
  );
}

const TAG: Record<string, { label: string; title: string }> = {
  infrastructure: { label: "", title: "" },
  imported: { label: "imported", title: "Imported into control-hub: no cluster credentials, so metrics, issues and charts come from Grafana only" },
  "sap-control-plane": { label: "SAP CP", title: "SAP control plane cluster: no cluster credentials, metrics from Grafana only" },
  "sap-execution-plane": { label: "SAP EP", title: "SAP execution plane cluster: no cluster credentials, metrics from Grafana only" },
};

/** OPERATIONAL (control planes) and ready (infrastructures) mean the same here. */
const statusLabel = (s: string) => (s.toUpperCase() === "OPERATIONAL" ? "ready" : s.toLowerCase());

function Status({ c }: { c: Cluster }) {
  // A metrics-only cluster's "error" is missing metrics, not an outage.
  if (c.error && (c.access !== "metrics" || isGone(c))) {
    return (
      <span title={c.error}>
        <Pill tone="critical">{isGone(c) ? "Gone" : "Unreachable"}</Pill>
        <span className="sr-only">: {c.error}</span>
      </span>
    );
  }
  if (c.pending) return <span className="text-xs text-muted">loading…</span>;
  return c.status ? <Pill tone={infraStatusTone(statusLabel(c.status))}>{statusLabel(c.status)}</Pill> : <Dash />;
}

/** One compact line per cluster. */
function Row({ c, indent = false }: { c: Cluster; indent?: boolean }) {
  const s = c.summary;
  const tag = TAG[c.kind ?? "infrastructure"];
  const tenants = c.tenants.join(", ");
  return (
    <tr>
      <td className={`max-w-0 ${indent ? "pl-8" : ""}`}>
        <div className="flex min-w-0 items-center gap-1.5">
          <Link to={`/clusters/${encodeURIComponent(c.id)}/overview`} className="truncate font-semibold" title={c.name}>
            {c.name || c.id}
          </Link>
          {tag.label && (
            <span className="flex-none rounded bg-slate-100 px-1.5 py-px text-[11px] text-slate-600" title={tag.title}>
              {tag.label}
            </span>
          )}
        </div>
      </td>
      <td className="max-w-0">
        <div className="truncate" title={tenants}>
          {tenants || <Dash />}
        </div>
      </td>
      <td className="max-w-0 whitespace-nowrap">
        <div className="truncate" title={`${providerLabel(c.provider)} · ${c.region}`}>
          {providerLabel(c.provider)} <span className="font-mono text-xs text-muted">{c.region}</span>
        </div>
      </td>
      <td className="whitespace-nowrap tabular-nums">
        {s ? <span className={s.nodesReady < s.nodes ? "font-semibold text-warn-fg" : ""}>{s.nodesReady}/{s.nodes}</span> : <Dash />}
      </td>
      <td>{s ? <Mini pct={s.cpuRequestedPct} label={`CPU requested (${s.cpuDetail})`} /> : <Dash />}</td>
      <td>{s ? <Mini pct={s.memRequestedPct} label={`Memory requested (${s.memDetail})`} /> : <Dash />}</td>
      <td className="whitespace-nowrap tabular-nums" title={s?.source === "thanos" ? "Counts from Grafana's metrics" : undefined}>
        {s ? (
          <>
            {s.pods}
            {s.podsNotReady > 0 && (
              <span className="ml-1 text-xs font-semibold text-warn-fg" title={`${s.podsNotReady} not ready`}>
                ({s.podsNotReady}!)
              </span>
            )}
          </>
        ) : (
          <Dash />
        )}
      </td>
      <td className="whitespace-nowrap">
        <Status c={c} />
      </td>
    </tr>
  );
}

function SectionHeader({ title, count, note, open, onToggle }: { title: string; count: number; note?: ReactNode; open: boolean; onToggle: () => void }) {
  return (
    <tr className="bg-slate-50">
      <th scope="rowgroup" colSpan={8} className="!py-2">
        <button type="button" onClick={onToggle} aria-expanded={open} className="flex w-full items-center gap-2 text-left">
          <span aria-hidden="true" className={`inline-block transition-transform ${open ? "rotate-90" : ""}`}>
            ▸
          </span>
          <span className="text-[13px] font-semibold text-ink">{title}</span>
          <span className="text-xs font-normal text-muted">{count}</span>
          {note && <span className="ml-2 min-w-0 truncate text-xs font-normal text-muted">{note}</span>}
        </button>
      </th>
    </tr>
  );
}

/** SAP: one row per tenant, its control and execution plane clusters underneath. */
function SapTenantRow({ tenant, clusters }: { tenant: string; clusters: Cluster[] }) {
  const [open, setOpen] = useState(false);
  const cps = clusters.filter((c) => c.kind === "sap-control-plane").length;
  const eps = clusters.length - cps;
  const first = clusters[0];
  return (
    <>
      <tr>
        <td className="max-w-0">
          <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="flex min-w-0 items-center gap-1.5 text-left">
            <span aria-hidden="true" className={`inline-block text-muted transition-transform ${open ? "rotate-90" : ""}`}>
              ▸
            </span>
            <span className="truncate font-semibold text-ink">{tenant}</span>
          </button>
        </td>
        <td className="text-xs text-muted">
          {cps} CP{cps === 1 ? "" : "s"} · {eps} EP{eps === 1 ? "" : "s"}
        </td>
        <td className="max-w-0 whitespace-nowrap">
          <div className="truncate">
            {providerLabel(first.provider)} <span className="font-mono text-xs text-muted">{first.region}</span>
          </div>
        </td>
        <td colSpan={4} className="text-xs text-muted">
          no metrics
        </td>
        <td>
          <Status c={first} />
        </td>
      </tr>
      {open && clusters.map((c) => <Row key={c.id} c={c} indent />)}
    </>
  );
}

type SectionKey = "managed" | "imported" | "sap" | "gone";

export default function ClustersPage() {
  const list = useApi(() => api.clusters());
  // Cards still being computed come back as pending; ask again a few times.
  const retries = useRef(0);
  const anyPending = (list.data ?? []).some((c) => c.pending);
  useEffect(() => {
    if (!anyPending || list.loading || retries.current >= 10) return;
    const t = setTimeout(() => {
      retries.current++;
      list.reload();
    }, 3000);
    return () => clearTimeout(t);
  }, [anyPending, list.loading, list.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const cloudParam = (params.get("cloud") ?? "all").toLowerCase();
  const cloud = (CLOUDS as readonly string[]).includes(cloudParam) ? cloudParam : "all";
  const [open, setOpen] = useState<Record<SectionKey, boolean>>({ managed: true, imported: true, sap: true, gone: false });

  const setParam = (key: string, value: string, empty: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === empty) next.delete(key);
        else next.set(key, value);
        return next;
      },
      { replace: true },
    );

  const groups = useMemo(() => {
    const rows = (list.data ?? []).filter((c) => cloud === "all" || c.provider.toLowerCase() === cloud).filter((c) => matches(c, q.trim()));
    const gone = rows.filter(isGone);
    const live = rows.filter((c) => !isGone(c));
    const managed = live.filter((c) => (c.kind ?? "infrastructure") === "infrastructure").sort(byAttention);
    const imported = live.filter((c) => c.kind === "imported").sort(byAttention);
    const sapAll = live.filter((c) => c.kind === "sap-control-plane" || c.kind === "sap-execution-plane");
    const sapByTenant = new Map<string, Cluster[]>();
    for (const c of sapAll) {
      const t = c.tenants[0] || c.name;
      sapByTenant.set(t, [...(sapByTenant.get(t) ?? []), c]);
    }
    const sap = [...sapByTenant.entries()]
      .map(([tenant, cs]) => ({ tenant, clusters: cs.sort((a, b) => (a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind === "sap-control-plane" ? -1 : 1)) }))
      .sort((a, b) => a.tenant.localeCompare(b.tenant));
    return { managed, imported, sap, sapCount: sapAll.length, sapWithMetrics: sapAll.filter((c) => c.summary).length, gone, total: rows.length };
  }, [list.data, cloud, q]);

  const toggle = (k: SectionKey) => setOpen((o) => ({ ...o, [k]: !o[k] }));

  return (
    <Page>
      <PageHeader title="Clusters" subtitle="Every Kubernetes cluster control-hub knows about, read-only. Managed clusters open in the explorer; the rest show Grafana's metrics.">
        <div className="flex flex-wrap items-center gap-2">
          <label htmlFor="cluster-q" className="sr-only">
            Filter clusters
          </label>
          <input
            id="cluster-q"
            type="search"
            className="input min-h-[36px] w-[220px] text-[13px]"
            placeholder="Filter: cluster, tenant, region"
            value={q}
            onChange={(e) => setParam("q", e.target.value, "")}
          />
          <div className="flex flex-wrap gap-1.5" role="group" aria-label="Filter by cloud">
            {CLOUDS.map((c) => (
              <button
                key={c}
                type="button"
                aria-pressed={cloud === c}
                onClick={() => setParam("cloud", c, "all")}
                className={`btn ${cloud === c ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
              >
                {c === "all" ? "All clouds" : providerLabel(c)}
              </button>
            ))}
          </div>
        </div>
      </PageHeader>

      <section className="card" aria-label="Clusters">
        {list.error ? (
          <div className="p-4">
            <ErrorCard title="Couldn't load clusters" error={list.error} onRetry={list.reload} />
          </div>
        ) : list.loading && !list.data ? (
          <Loading label="Loading clusters…" />
        ) : (list.data ?? []).length === 0 ? (
          <Empty title="No clusters">control-hub returned no clusters you can read.</Empty>
        ) : groups.total === 0 ? (
          <Empty title="No clusters match">Try a different filter or cloud.</Empty>
        ) : (
          <div className="overflow-x-auto">
            <table className="table w-full min-w-[760px] table-fixed">
              <colgroup>
                <col className="w-[25%]" />
                <col className="w-[15%]" />
                <col className="w-[14%]" />
                <col className="w-[7%]" />
                <col className="w-[10%]" />
                <col className="w-[10%]" />
                <col className="w-[8%]" />
                <col className="w-[11%]" />
              </colgroup>
              <thead>
                <tr>
                  <th scope="col">Cluster</th>
                  <th scope="col">Tenant</th>
                  <th scope="col">Cloud · region</th>
                  <th scope="col">Nodes</th>
                  <th scope="col">CPU req.</th>
                  <th scope="col">Mem req.</th>
                  <th scope="col" title="(n!) = pods not ready">Pods</th>
                  <th scope="col">Status</th>
                </tr>
              </thead>
              <tbody>
                {groups.managed.length > 0 && (
                  <>
                    <SectionHeader title="Managed" count={groups.managed.length} note="full explorer" open={open.managed} onToggle={() => toggle("managed")} />
                    {open.managed && groups.managed.map((c) => <Row key={c.id} c={c} />)}
                  </>
                )}
                {groups.imported.length > 0 && (
                  <>
                    <SectionHeader
                      title="Imported"
                      count={groups.imported.length}
                      note="no cluster credentials in control-hub · metrics from Grafana"
                      open={open.imported}
                      onToggle={() => toggle("imported")}
                    />
                    {open.imported && groups.imported.map((c) => <Row key={c.id} c={c} />)}
                  </>
                )}
                {groups.sap.length > 0 && (
                  <>
                    <SectionHeader
                      title="SAP (GKE)"
                      count={groups.sapCount}
                      note={
                        groups.sapWithMetrics === 0
                          ? "no metrics yet: the GCP Grafana returns no data for these clusters"
                          : `${groups.sap.length} tenants · metrics from Grafana`
                      }
                      open={open.sap}
                      onToggle={() => toggle("sap")}
                    />
                    {open.sap &&
                      groups.sap.map((g) => (
                        <Fragment key={g.tenant}>
                          <SapTenantRow tenant={g.tenant} clusters={g.clusters} />
                        </Fragment>
                      ))}
                  </>
                )}
                {groups.gone.length > 0 && (
                  <>
                    <SectionHeader
                      title="Gone from the cloud"
                      count={groups.gone.length}
                      note="control-hub still lists these, but the cloud says they no longer exist"
                      open={open.gone}
                      onToggle={() => toggle("gone")}
                    />
                    {open.gone && groups.gone.map((c) => <Row key={c.id} c={c} />)}
                  </>
                )}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </Page>
  );
}
