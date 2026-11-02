import { Fragment, useMemo, useState } from "react";
import { Link, Navigate, useParams } from "react-router-dom";
import { api } from "../api/client";
import type { APIResource, ResourceRow, ResourceTable } from "../api/types";
import {
  NAMESPACE_KEY,
  NODE_KEY,
  POD_KEY,
  TableCard,
  Toolbar,
  resourceListUrl,
  resourceUrl,
  useAutoRefresh,
  useNow,
  workloadsOverviewUrl,
} from "../components/Cluster";
import { resourceLabel, useCluster, useClusterSection, useNsQuery } from "../components/ClusterShell";
import { RefreshStatus, statusTone } from "../components/Explorer";
import { Pill } from "../components/Pill";
import { ErrorCard, Loading } from "../components/States";
import { age } from "../lib/format";
import { useApi } from "../lib/useApi";

const REFRESH_MS = 15_000;
/** The server caps a list at this many rows. */
const MAX_ROWS = 2000;
const WIDE_KEY = "aic.resourceList.wide";

function readWide(): boolean {
  try {
    return window.localStorage.getItem(WIDE_KEY) === "1";
  } catch {
    return false;
  }
}

function Cell({ column, value }: { column: string; value: string }) {
  if (!value) return <span className="text-muted">—</span>;
  if (value === "<pending>") return <Pill tone="warning">&lt;pending&gt;</Pill>;
  if (column === "Status" || column === "Phase") return <Pill tone={statusTone(value)}>{value}</Pill>;
  const frac = column === "Ready" ? /^(\d+)\/(\d+)$/.exec(value) : null;
  const behind = frac && frac[1] !== frac[2];
  return (
    <span className={`block max-w-[360px] truncate ${behind ? "font-semibold text-warn-fg" : ""}`} title={value}>
      {value}
    </span>
  );
}

function ListTable({
  id,
  rkey,
  table,
  rows,
  wide,
  showNs,
  now,
}: {
  id: string;
  rkey: string;
  table: ResourceTable;
  rows: ResourceRow[];
  wide: boolean;
  showNs: boolean;
  now: number;
}) {
  const cols = table.columns.map((c, i) => ({ ...c, i })).filter((c) => wide || c.priority === 0);
  const nameIdx = Math.max(0, table.columns.findIndex((c) => c.name === "Name"));
  const href = (r: ResourceRow) => (rkey === NAMESPACE_KEY ? workloadsOverviewUrl(id, r.name) : resourceUrl(id, rkey, r.name, r.namespace));
  return (
    <table className="table" style={{ minWidth: `${Math.max(640, (cols.length + (showNs ? 1 : 0)) * 130)}px` }}>
      <thead>
        <tr>
          {cols.map((c) => (
            <Fragment key={c.i}>
              <th scope="col" title={c.priority > 0 ? "wide column" : undefined}>
                {c.name}
              </th>
              {showNs && c.i === nameIdx && <th scope="col">Namespace</th>}
            </Fragment>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={`${r.namespace}/${r.name}`}>
            {cols.map((c) => {
              const v = r.cells[c.i] ?? "";
              if (c.i === nameIdx)
                return (
                  <Fragment key={c.i}>
                    <td>
                      <Link
                        to={href(r)}
                        className="block max-w-[420px] truncate whitespace-nowrap font-mono text-xs font-medium"
                        title={rkey === NAMESPACE_KEY ? `Workloads in ${r.name}` : r.name}
                      >
                        {r.name || v}
                      </Link>
                    </td>
                    {showNs && <td className="whitespace-nowrap">{r.namespace || <span className="text-muted">—</span>}</td>}
                  </Fragment>
                );
              if (c.name === "Age" && r.createdAt)
                return (
                  <td key={c.i} className="whitespace-nowrap text-muted" title={r.createdAt}>
                    {age(r.createdAt, now)}
                  </td>
                );
              return (
                <td key={c.i} className={c.type === "integer" || c.type === "number" ? "tabular-nums" : ""}>
                  <Cell column={c.name} value={v} />
                </td>
              );
            })}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function List({ id, rkey, meta, ns }: { id: string; rkey: string; meta: APIResource | undefined; ns: string }) {
  const { namespaces } = useCluster();
  const { q, setQuery } = useNsQuery();
  const namespaced = meta?.namespaced ?? true;
  const listNs = namespaced ? ns : "";
  const table = useApi(() => api.resources(id, rkey, listNs), [id, rkey, listNs]);
  const [wide, setWide] = useState(readWide);
  const now = useNow(30_000);
  useAutoRefresh(table.reload, REFRESH_MS);

  const total = table.data?.rows.length ?? 0;
  const rows = useMemo(() => {
    const all = table.data?.rows ?? [];
    if (!q) return all;
    const needle = q.toLowerCase();
    return all.filter((r) => [r.name, r.namespace, ...r.cells].some((f) => f && f.toLowerCase().includes(needle)));
  }, [table.data, q]);
  const hasWide = (table.data?.columns ?? []).some((c) => c.priority > 0);
  const label = meta ? resourceLabel(meta) : rkey.split("/")[2];

  const toggleWide = () => {
    setWide((w) => {
      try {
        window.localStorage.setItem(WIDE_KEY, w ? "0" : "1");
      } catch {
        // storage blocked; the toggle still works for this page
      }
      return !w;
    });
  };

  return (
    <div className="flex flex-col gap-3">
      <Toolbar
        namespaces={namespaces ?? []}
        ns={ns}
        q={q}
        onNs={(v) => setQuery({ ns: v })}
        onQ={(v) => setQuery({ q: v })}
        filterLabel={`Filter ${label.toLowerCase()}`}
        showNs={namespaced}
        right={
          <>
            <span className="tabular-nums">
              {table.data ? (q ? `${rows.length} of ${total}` : `${total} ${total === 1 ? "item" : "items"}`) : ""}
            </span>
            {hasWide && (
              <button type="button" className={`btn min-h-[30px] px-2.5 text-xs ${wide ? "border-violet-border bg-violet-tint text-violet" : ""}`} aria-pressed={wide} onClick={toggleWide}>
                Wide
              </button>
            )}
            <RefreshStatus data={table.data} loading={table.loading} onRefresh={table.reload} label={label.toLowerCase()} />
          </>
        }
      />
      {total >= MAX_ROWS && (
        <div className="text-xs text-warn-fg">Showing the first {MAX_ROWS} — pick a namespace or filter to narrow the list.</div>
      )}
      <TableCard
        label={label}
        loading={table.loading}
        error={table.error}
        onRetry={table.reload}
        rows={table.data ? rows : undefined}
        emptyTitle={total === 0 ? `No ${label.toLowerCase()}` : `No ${label.toLowerCase()} match`}
        emptyText={total === 0 ? (namespaced ? `There are none in ${ns || "any namespace"}.` : "The cluster has none.") : "Try a different filter."}
      >
        {(list) => <ListTable id={id} rkey={rkey} table={table.data!} rows={list} wide={wide} showNs={namespaced && !ns} now={now} />}
      </TableCard>
    </div>
  );
}

export default function ResourceListPage() {
  const { group = "", version = "", resource = "" } = useParams();
  const rkey = `${group}/${version}/${resource}`;
  const { id, resources } = useCluster();
  const { ns, nsParam } = useNsQuery();
  const meta = resources.data?.find((r) => r.key === rkey);
  useClusterSection(meta ? resourceLabel(meta) : resource);

  if (rkey === POD_KEY || rkey === NODE_KEY) return <Navigate to={resourceListUrl(id, rkey, nsParam ?? undefined)} replace />;
  if (!resources.data && resources.loading) return <Loading label="Discovering kinds…" />;
  if (ns === undefined && (meta?.namespaced ?? true)) return <Loading label="Loading namespaces…" />;

  return (
    <>
      {resources.data && !meta && (
        <ErrorCard
          title="Unknown kind"
          error={`This cluster does not list ${rkey} among the kinds it serves; showing whatever the API returns.`}
        />
      )}
      <List key={`${id}|${rkey}|${ns ?? ""}`} id={id} rkey={rkey} meta={meta} ns={ns ?? ""} />
    </>
  );
}
