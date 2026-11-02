import { useMemo } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { APIResource, Pod, ResourceTable } from "../api/types";
import { IssuePill, PodStatus, Toolbar, clusterUrl, podStatusColor, podUrl, resourceListUrl, resourceUrl, useAutoRefresh } from "../components/Cluster";
import { resourceLabel, useCluster, useClusterSection, useNsQuery } from "../components/ClusterShell";
import { RefreshStatus, rowReadiness, type Readiness } from "../components/Explorer";
import { Pill } from "../components/Pill";
import { ErrorCard, Loading } from "../components/States";
import { useApi } from "../lib/useApi";

const REFRESH_MS = 15_000;
const ATTENTION_MAX = 25;

/** Workload kinds in Lens's order: (group, resource). */
const KINDS: [string, string][] = [
  ["apps", "deployments"],
  ["apps", "daemonsets"],
  ["apps", "statefulsets"],
  ["apps", "replicasets"],
  ["batch", "jobs"],
  ["batch", "cronjobs"],
];

type KindResult = { meta: APIResource; table?: ResourceTable; error?: Error };
type Problem = { meta: APIResource; name: string; namespace: string; readiness: Readiness };

const POD_BUCKETS = [
  { label: "Running", color: "#059669" },
  { label: "Pending", color: "#B45309" },
  { label: "Failed", color: "#B91C1C" },
  { label: "Succeeded", color: "#64748B" },
] as const;

function podBucket(p: Pod): (typeof POD_BUCKETS)[number]["label"] {
  if (p.status === "Running") return "Running";
  if (p.status === "Completed" || p.status === "Succeeded") return "Succeeded";
  return podStatusColor(p.status) === "#B45309" ? "Pending" : "Failed";
}

function podHealthy(p: Pod): boolean {
  if (p.status === "Completed" || p.status === "Succeeded") return true;
  if (p.status !== "Running") return false;
  const [a, b] = p.ready.split("/");
  return a === b;
}

function Bar({ ok, total }: { ok: number; total: number }) {
  const pct = total === 0 ? 100 : Math.round((ok / total) * 100);
  return (
    <div aria-hidden="true" className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-line">
      <div className={`h-full ${ok < total ? "bg-[#B45309]" : "bg-[#059669]"}`} style={{ width: `${pct}%` }} />
    </div>
  );
}

function KindCard({ id, ns, r }: { id: string; ns: string; r: KindResult }) {
  const label = resourceLabel(r.meta);
  const rows = r.table?.rows ?? [];
  const states = rows.map((row) => rowReadiness(r.table!, row.cells));
  const ok = states.filter((s) => !s || s.state === "ok").length;
  const bad = states.filter((s) => s?.state === "bad").length;
  const progress = states.filter((s) => s?.state === "progress").length;
  const suspended = states.filter((s) => s?.state === "suspended").length;
  const word = r.meta.resource === "jobs" ? "complete" : r.meta.resource === "cronjobs" ? "active" : "ready";
  const counted = ok + bad + progress; // suspended cronjobs are neither ready nor broken
  return (
    <Link to={resourceListUrl(id, r.meta.key, ns)} className="card block px-3.5 py-3 text-ink no-underline hover:border-violet-border hover:text-ink">
      <div className="text-xs text-muted">{label}</div>
      {r.error ? (
        <div className="mt-1 text-[13px] text-crit-fg" title={r.error.message}>
          Couldn't load
        </div>
      ) : (
        <>
          <div className={`text-xl font-semibold tabular-nums ${ok < counted ? "text-warn-fg" : ""}`}>
            {ok}/{counted} <span className="text-[13px] font-normal text-muted">{word}</span>
          </div>
          <Bar ok={ok} total={counted} />
          <div className="mt-1.5 text-xs text-muted">
            {rows.length === 0
              ? "none"
              : [bad && `${bad} ${r.meta.resource === "jobs" ? "failed" : "not ready"}`, progress && `${progress} in progress`, suspended && `${suspended} suspended`].filter(Boolean).join(" · ") || "all healthy"}
          </div>
        </>
      )}
    </Link>
  );
}

function PodsCard({ id, ns, pods, error }: { id: string; ns: string; pods: Pod[] | undefined; error: Error | undefined }) {
  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const p of pods ?? []) m.set(podBucket(p), (m.get(podBucket(p)) ?? 0) + 1);
    return m;
  }, [pods]);
  const healthy = (pods ?? []).filter(podHealthy).length;
  const total = pods?.length ?? 0;
  return (
    <Link to={clusterUrl(id, "pods", { ns })} className="card block px-3.5 py-3 text-ink no-underline hover:border-violet-border hover:text-ink">
      <div className="text-xs text-muted">Pods</div>
      {error && !pods ? (
        <div className="mt-1 text-[13px] text-crit-fg" title={error.message}>
          Couldn't load
        </div>
      ) : !pods ? (
        <div className="text-xl font-semibold text-muted">…</div>
      ) : (
        <>
          <div className={`text-xl font-semibold tabular-nums ${healthy < total ? "text-warn-fg" : ""}`}>
            {healthy}/{total} <span className="text-[13px] font-normal text-muted">healthy</span>
          </div>
          <Bar ok={healthy} total={total} />
          <ul className="m-0 mt-1.5 flex list-none flex-wrap gap-x-3 gap-y-0.5 p-0 text-xs text-muted">
            {POD_BUCKETS.filter((b) => counts.get(b.label)).map((b) => (
              <li key={b.label} className="inline-flex items-center gap-1">
                <span aria-hidden="true" className="inline-block h-2 w-2 rounded-full" style={{ background: b.color }} />
                {b.label} {counts.get(b.label)}
              </li>
            ))}
          </ul>
        </>
      )}
    </Link>
  );
}

function Overview({ id, ns, q, setQuery }: { id: string; ns: string; q: string; setQuery: (p: Record<string, string>) => void }) {
  const { namespaces, resources } = useCluster();
  const metas = useMemo(
    () => KINDS.flatMap(([g, res]) => resources.data?.filter((r) => r.group === g && r.resource === res).slice(0, 1) ?? []),
    [resources.data],
  );
  const pods = useApi(() => api.clusterPods(id, ns), [id, ns]);
  const kinds = useApi<KindResult[]>(
    () =>
      Promise.all(
        metas.map((meta) =>
          api.resources(id, meta.key, ns).then(
            (table) => ({ meta, table }),
            (e: unknown) => ({ meta, error: e instanceof Error ? e : new Error(String(e)) }),
          ),
        ),
      ),
    [id, ns, metas],
  );
  const { reload: reloadPods } = pods;
  const { reload: reloadKinds } = kinds;
  const reload = useMemo(
    () => () => {
      reloadPods();
      reloadKinds();
    },
    [reloadPods, reloadKinds],
  );
  useAutoRefresh(reload, REFRESH_MS);

  const needle = q.toLowerCase();
  const problems = useMemo<Problem[]>(
    () =>
      (kinds.data ?? []).flatMap((k) =>
        (k.table?.rows ?? []).flatMap((row) => {
          const readiness = rowReadiness(k.table!, row.cells);
          if (!readiness || (readiness.state !== "bad" && readiness.state !== "progress")) return [];
          if (needle && !`${row.name} ${row.namespace} ${k.meta.kind}`.toLowerCase().includes(needle)) return [];
          return [{ meta: k.meta, name: row.name, namespace: row.namespace, readiness }];
        }),
      ),
    [kinds.data, needle],
  );
  const badPods = useMemo(
    () =>
      (pods.data ?? [])
        .filter((p) => !podHealthy(p) && (!needle || `${p.name} ${p.namespace} ${p.status} ${p.issue}`.toLowerCase().includes(needle)))
        .sort((a, b) => (a.issueSeverity === "critical" ? 0 : 1) - (b.issueSeverity === "critical" ? 0 : 1) || b.restarts - a.restarts),
    [pods.data, needle],
  );
  const loading = pods.loading || kinds.loading;
  const loaded = pods.data !== undefined && kinds.data !== undefined;

  return (
    <div className="flex flex-col gap-4">
      <Toolbar
        namespaces={namespaces ?? []}
        ns={ns}
        q={q}
        onNs={(v) => setQuery({ ns: v })}
        onQ={(v) => setQuery({ q: v })}
        filterLabel="Filter not-ready workloads"
        right={<RefreshStatus data={kinds.data} loading={loading} onRefresh={reload} label="workloads" />}
      />
      {resources.error && !resources.data && <ErrorCard title="Couldn't list the cluster's kinds" error={resources.error} onRetry={resources.reload} />}
      {kinds.error && <ErrorCard title="Couldn't load workloads" error={kinds.error} onRetry={kinds.reload} />}

      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(180px,100%),1fr))] gap-3">
        <PodsCard id={id} ns={ns} pods={pods.data} error={pods.error} />
        {kinds.data
          ? kinds.data.map((k) => <KindCard key={k.meta.key} id={id} ns={ns} r={k} />)
          : metas.map((m) => (
              <div key={m.key} className="card px-3.5 py-3">
                <div className="text-xs text-muted">{resourceLabel(m)}</div>
                <div className="text-xl font-semibold text-muted">…</div>
              </div>
            ))}
      </div>

      <section className="card" aria-labelledby="wo-attention">
        <div className="card-head">
          <h2 id="wo-attention" className="h2">
            Not ready
          </h2>
          <span className="text-xs text-muted">workloads below their desired count, failed jobs, and pods that are not running and ready</span>
        </div>
        <div className="flex flex-col gap-3 p-4">
          {!loaded ? (
            <Loading label="Checking workloads…" />
          ) : problems.length === 0 && badPods.length === 0 ? (
            <div className="text-[13px] text-muted">
              {needle ? "Nothing not-ready matches the filter." : `Everything in ${ns || "all namespaces"} is ready.`}
            </div>
          ) : (
            <ul className="m-0 flex list-none flex-col gap-2 p-0 text-[13px]">
              {problems.slice(0, ATTENTION_MAX).map((p) => (
                <li key={`${p.meta.key}/${p.namespace}/${p.name}`} className="flex flex-wrap items-baseline gap-2">
                  <Pill tone={p.readiness.state === "bad" ? "critical" : "warning"}>{p.meta.kind}</Pill>
                  <Link to={resourceUrl(id, p.meta.key, p.name, p.namespace)} className="font-mono text-xs">
                    {p.name}
                  </Link>
                  <span className="text-xs text-muted">
                    {!ns && `${p.namespace} · `}
                    {p.readiness.text}
                  </span>
                </li>
              ))}
              {badPods.slice(0, ATTENTION_MAX).map((p) => (
                <li key={`pod/${p.namespace}/${p.name}`} className="flex flex-wrap items-baseline gap-2">
                  <Pill tone={p.issueSeverity === "critical" || podBucket(p) === "Failed" ? "critical" : "warning"}>Pod</Pill>
                  <Link to={podUrl(id, p.namespace, p.name)} className="font-mono text-xs">
                    {p.name}
                  </Link>
                  <span className="inline-flex items-baseline gap-2 text-xs text-muted">
                    {!ns && `${p.namespace} · `}
                    <PodStatus status={p.status} /> · {p.ready} ready
                    {p.restarts > 0 && ` · ${p.restarts} restarts`}
                  </span>
                  {p.issue && p.issue !== p.status && <IssuePill issue={p.issue} severity={p.issueSeverity} />}
                </li>
              ))}
              {(problems.length > ATTENTION_MAX || badPods.length > ATTENTION_MAX) && (
                <li className="text-xs text-muted">
                  Showing the first {ATTENTION_MAX} of each — narrow with the filter or open a list.
                </li>
              )}
            </ul>
          )}
        </div>
      </section>
    </div>
  );
}

export default function WorkloadsOverviewPage() {
  const { id, resources } = useCluster();
  const { ns, q, setQuery } = useNsQuery();
  useClusterSection("Workloads");
  if (ns === undefined) return <Loading label="Loading namespaces…" />;
  if (!resources.data && resources.loading) return <Loading label="Discovering kinds…" />;
  return <Overview key={`${id}|${ns}`} id={id} ns={ns} q={q} setQuery={setQuery} />;
}
