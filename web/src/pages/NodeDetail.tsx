import { type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import type { NodeDetail } from "../api/types";
import { Chips, Dash, EventsTable, HIGH_PCT, PodsMiniTable, clusterUrl, useNow } from "../components/Cluster";
import { useCluster, useClusterSection } from "../components/ClusterShell";
import { Field } from "../components/Explorer";
import { ArrowLeftIcon, RefreshIcon, Spinner } from "../components/Icons";
import { NodeCharts } from "../components/Metrics";
import { Pill } from "../components/Pill";
import { Empty, ErrorCard, Loading } from "../components/States";
import { age, formatDateTime } from "../lib/format";
import { useApi } from "../lib/useApi";

function Tile({ label, value, sub, warn = false }: { label: string; value: ReactNode; sub?: ReactNode; warn?: boolean }) {
  return (
    <div className="card px-3.5 py-3">
      <div className="text-xs text-muted">{label}</div>
      <div className={`text-xl font-semibold tabular-nums ${warn ? "text-warn-fg" : ""}`}>{value}</div>
      {sub && <div className="text-xs text-muted">{sub}</div>}
    </div>
  );
}

/** A condition is bad when Ready is not True, or any other condition (the pressures) is True. */
function conditionBad(type: string, status: string): boolean {
  return type === "Ready" ? status !== "True" : status === "True";
}

function Card({ title, id, children, right }: { title: string; id: string; children: ReactNode; right?: ReactNode }) {
  return (
    <section className="card" aria-labelledby={id}>
      <div className="card-head">
        <h2 id={id} className="h2">
          {title}
        </h2>
        {right}
      </div>
      {children}
    </section>
  );
}

function Body({ id, n, now }: { id: string; n: NodeDetail; now: number }) {
  const resKeys = [...new Set([...Object.keys(n.capacity ?? {}), ...Object.keys(n.allocatable ?? {})])].sort((a, b) => {
    const rank = (k: string) => ["cpu", "memory", "pods", "ephemeral-storage"].indexOf(k);
    const [ra, rb] = [rank(a), rank(b)];
    return (ra === -1 ? 99 : ra) - (rb === -1 ? 99 : rb) || a.localeCompare(b);
  });
  const maxPods = n.allocatable?.pods;
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(170px,100%),1fr))] gap-3">
        <Tile label="CPU requested" value={`${Math.round(n.cpuRequestedPct)}%`} sub={n.allocatable?.cpu ? `of ${n.allocatable.cpu} allocatable` : undefined} warn={n.cpuRequestedPct > HIGH_PCT} />
        <Tile label="Memory requested" value={`${Math.round(n.memRequestedPct)}%`} sub={n.allocatable?.memory ? `of ${n.allocatable.memory} allocatable` : undefined} warn={n.memRequestedPct > HIGH_PCT} />
        {n.cpuUsage && <Tile label="CPU usage" value={<span className="font-mono text-lg">{n.cpuUsage}</span>} sub="live, from metrics-server" />}
        {n.memUsage && <Tile label="Memory usage" value={<span className="font-mono text-lg">{n.memUsage}</span>} sub="live, from metrics-server" />}
        <Tile label="Pods" value={(n.pods ?? []).length} sub={maxPods ? `of ${maxPods} allowed` : undefined} />
      </div>

      <NodeCharts id={id} node={n.name} />

      <section className="card p-4" aria-label="System info">
        <dl className="m-0 grid grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))] gap-x-5 gap-y-3">
          <Field label="Pool">{n.pool || <Dash />}</Field>
          <Field label="Instance type">{n.instanceType || <Dash />}</Field>
          <Field label="Kubelet">{n.version || <Dash />}</Field>
          <Field label="OS">{n.os || <Dash />}</Field>
          <Field label="Kernel">{n.kernel || <Dash />}</Field>
          <Field label="Container runtime">{n.runtime || <Dash />}</Field>
          <Field label="Provider ID">{n.providerId || <Dash />}</Field>
          <Field label="Created">
            {formatDateTime(n.createdAt)} <span className="font-sans text-muted">({age(n.createdAt, now)} ago)</span>
          </Field>
          <Field label="Addresses">{(n.addresses ?? []).length === 0 ? <Dash /> : n.addresses.map((a) => <div key={a}>{a}</div>)}</Field>
        </dl>
      </section>

      <Card title="Conditions" id="nd-conditions">
        {(n.conditions ?? []).length === 0 ? (
          <Empty title="No conditions reported" />
        ) : (
          <div className="overflow-x-auto">
            <table className="table min-w-[640px]">
              <thead>
                <tr>
                  <th scope="col">Type</th>
                  <th scope="col">Status</th>
                  <th scope="col">Reason</th>
                  <th scope="col">Message</th>
                </tr>
              </thead>
              <tbody>
                {n.conditions.map((c) => {
                  const bad = conditionBad(c.type, c.status);
                  return (
                    <tr key={c.type} className={bad ? "bg-warn-bg/40" : ""}>
                      <td className={`whitespace-nowrap ${bad ? "font-semibold text-warn-fg" : "font-medium"}`}>{c.type}</td>
                      <td>
                        <Pill tone={bad ? (c.type === "Ready" ? "critical" : "warning") : "success"}>{c.status}</Pill>
                      </td>
                      <td className="whitespace-nowrap">{c.reason || <Dash />}</td>
                      <td className="min-w-[280px] break-words text-slate-700">{c.message || <Dash />}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <div className="grid gap-4 min-[1100px]:grid-cols-2">
        <Card title="Capacity vs allocatable" id="nd-capacity">
          {resKeys.length === 0 ? (
            <Empty title="Not reported" />
          ) : (
            <div className="overflow-x-auto">
              <table className="table">
                <thead>
                  <tr>
                    <th scope="col">Resource</th>
                    <th scope="col">Capacity</th>
                    <th scope="col">Allocatable</th>
                  </tr>
                </thead>
                <tbody>
                  {resKeys.map((k) => (
                    <tr key={k}>
                      <td className="whitespace-nowrap font-mono text-xs">{k}</td>
                      <td className="whitespace-nowrap font-mono text-xs">{n.capacity?.[k] ?? <Dash />}</td>
                      <td className="whitespace-nowrap font-mono text-xs">{n.allocatable?.[k] ?? <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>
        <section className="card flex flex-col gap-3 p-4" aria-label="Taints and labels">
          <div>
            <h2 className="mb-2 mt-0 text-[13px] font-semibold">Taints</h2>
            {(n.taints ?? []).length === 0 ? (
              <span className="text-[13px] text-muted">none</span>
            ) : (
              <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
                {n.taints.map((t) => (
                  <li key={t}>
                    <Pill tone="warning" className="font-mono">
                      {t}
                    </Pill>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <h2 className="mb-2 mt-0 text-[13px] font-semibold">Labels</h2>
            <Chips map={n.labels} />
          </div>
        </section>
      </div>

      <Card title={`Pods · ${(n.pods ?? []).length}`} id="nd-pods">
        {(n.pods ?? []).length === 0 ? (
          <Empty title="No pods on this node" />
        ) : (
          <div className="overflow-x-auto">
            <PodsMiniTable id={id} pods={n.pods} showNs showNode={false} now={now} />
          </div>
        )}
      </Card>

      <Card title="Events" id="nd-events">
        {(n.events ?? []).length === 0 ? (
          <Empty title="No events">Kubernetes keeps events for about an hour.</Empty>
        ) : (
          <div className="overflow-x-auto">
            <EventsTable events={n.events} showObject={false} now={now} />
          </div>
        )}
      </Card>
    </div>
  );
}

export default function NodeDetailPage() {
  const { name = "" } = useParams();
  const { id } = useCluster();
  useClusterSection("Nodes");
  const node = useApi(() => api.node(id, name), [id, name]);
  const now = useNow(30_000);
  const n = node.data && node.data.name === name ? node.data : undefined;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2.5">
        <Link to={clusterUrl(id, "nodes")} className="btn">
          <ArrowLeftIcon size={14} />
          Nodes
        </Link>
        <h2 className="m-0 break-all text-[17px] font-semibold">
          <span className="font-normal text-muted">Node · </span>
          <span className="font-mono">{name}</span>
        </h2>
        {n && <Pill tone={n.status === "Ready" ? "success" : n.status === "SchedulingDisabled" ? "warning" : "critical"}>{n.status}</Pill>}
        {n?.unschedulable && n.status !== "SchedulingDisabled" && <Pill tone="warning">Unschedulable</Pill>}
        {n?.pressure.map((p) => (
          <Pill key={p} tone="warning">
            {p}
          </Pill>
        ))}
        {node.loading && n && <Spinner />}
        <button type="button" className="btn ml-auto" onClick={node.reload} disabled={node.loading}>
          <RefreshIcon size={14} />
          Refresh
        </button>
      </div>
      {node.error && !n ? (
        <ErrorCard title="Couldn't load this node" error={node.error} onRetry={node.reload} />
      ) : !n ? (
        <div className="card">
          <Loading label="Loading node…" />
        </div>
      ) : (
        <>
          {node.error && <ErrorCard title="Refresh failed — showing the last data" error={node.error} onRetry={node.reload} />}
          <Body id={id} n={n} now={now} />
        </>
      )}
    </div>
  );
}
