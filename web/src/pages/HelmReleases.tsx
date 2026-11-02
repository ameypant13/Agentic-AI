import { useMemo } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { HelmRelease } from "../api/types";
import { Dash, TableCard, Toolbar, useNow, workloadsOverviewUrl } from "../components/Cluster";
import { useCluster, useClusterSection, useNsQuery } from "../components/ClusterShell";
import { RefreshStatus } from "../components/Explorer";
import { LockIcon } from "../components/Icons";
import { Pill, type Tone } from "../components/Pill";
import { Loading } from "../components/States";
import { age } from "../lib/format";
import { useApi } from "../lib/useApi";

function helmTone(status: string): Tone {
  const s = status.toLowerCase();
  if (s === "deployed") return "success";
  if (s === "failed") return "critical";
  if (s.startsWith("pending") || s === "uninstalling") return "warning";
  return "neutral";
}

function Releases({ id, ns, q, setQuery }: { id: string; ns: string; q: string; setQuery: (p: Record<string, string>) => void }) {
  const { namespaces } = useCluster();
  const rel = useApi(() => api.helmReleases(id, ns), [id, ns]);
  const now = useNow(30_000);
  const total = rel.data?.length ?? 0;
  const rows = useMemo(() => {
    const needle = q.toLowerCase();
    return (rel.data ?? [])
      .filter((r: HelmRelease) => !needle || [r.name, r.namespace, r.chart, r.chartVersion, r.appVersion, r.status].some((f) => f.toLowerCase().includes(needle)))
      .sort((a, b) => a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name));
  }, [rel.data, q]);

  return (
    <div className="flex flex-col gap-3">
      <Toolbar
        namespaces={namespaces ?? []}
        ns={ns}
        q={q}
        onNs={(v) => setQuery({ ns: v })}
        onQ={(v) => setQuery({ q: v })}
        filterLabel="Filter releases"
        right={
          <>
            <span className="tabular-nums">{rel.data ? (q ? `${rows.length} of ${total}` : `${total} ${total === 1 ? "release" : "releases"}`) : ""}</span>
            <RefreshStatus data={rel.data} loading={rel.loading} onRefresh={rel.reload} label="releases" />
          </>
        }
      />
      <div className="flex items-center gap-1.5 text-xs text-muted">
        <LockIcon size={12} /> Release values are never read — only each release's latest revision metadata.
      </div>
      <TableCard
        label="Helm releases"
        loading={rel.loading}
        error={rel.error}
        onRetry={rel.reload}
        rows={rel.data ? rows : undefined}
        emptyTitle={total === 0 ? "No Helm releases" : "No releases match"}
        emptyText={total === 0 ? `No releases in ${ns || "any namespace"}.` : "Try a different filter."}
      >
        {(list) => (
          <table className="table min-w-[920px]">
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Namespace</th>
                <th scope="col">Chart</th>
                <th scope="col">Chart version</th>
                <th scope="col">App version</th>
                <th scope="col">Revision</th>
                <th scope="col">Status</th>
                <th scope="col">Updated</th>
              </tr>
            </thead>
            <tbody>
              {list.map((r) => (
                <tr key={`${r.namespace}/${r.name}`}>
                  <td className="whitespace-nowrap font-mono text-xs font-medium">{r.name}</td>
                  <td className="whitespace-nowrap">
                    <Link to={workloadsOverviewUrl(id, r.namespace)}>{r.namespace}</Link>
                  </td>
                  <td className="whitespace-nowrap">{r.chart || <Dash />}</td>
                  <td className="whitespace-nowrap font-mono text-xs">{r.chartVersion || <Dash />}</td>
                  <td className="whitespace-nowrap font-mono text-xs">{r.appVersion || <Dash />}</td>
                  <td className="tabular-nums">{r.revision}</td>
                  <td>
                    <span title={r.description || undefined}>
                      <Pill tone={helmTone(r.status)}>{r.status || "unknown"}</Pill>
                    </span>
                  </td>
                  <td className="whitespace-nowrap text-muted" title={r.updated}>
                    {r.updated ? age(r.updated, now) : <Dash />}
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

export default function HelmReleasesPage() {
  const { id } = useCluster();
  const { ns, q, setQuery } = useNsQuery();
  useClusterSection("Helm releases");
  if (ns === undefined) return <Loading label="Loading namespaces…" />;
  return <Releases key={`${id}|${ns}`} id={id} ns={ns} q={q} setQuery={setQuery} />;
}
