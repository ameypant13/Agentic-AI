import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { OwnerRef, ResourceDetail } from "../api/types";
import { Chips, Dash, EventsTable, PodsMiniTable, resourceListUrl, resourceUrl, useNow, workloadsOverviewUrl } from "../components/Cluster";
import { resourceLabel, useCluster, useClusterSection } from "../components/ClusterShell";
import { Field, YamlViewer } from "../components/Explorer";
import { ArrowLeftIcon, LockIcon, RefreshIcon, Spinner } from "../components/Icons";
import { Pill } from "../components/Pill";
import { Empty, ErrorCard, Loading } from "../components/States";
import { age, formatDateTime } from "../lib/format";
import { useApi } from "../lib/useApi";

const TABS = [
  ["overview", "Overview"],
  ["yaml", "YAML"],
  ["events", "Events"],
] as const;
type Tab = (typeof TABS)[number][0];

/** How far up the owner chain the overview follows (Pod → ReplicaSet → Deployment). */
const MAX_OWNER_DEPTH = 3;
/** Annotations collapse when there are more than this many, or they are long. */
const ANNOTATIONS_FOLD = 4;
const ANNOTATIONS_FOLD_CHARS = 400;

/** One owner, then (fetched) its own owners: "ReplicaSet x → Deployment y". */
function OwnerLink({ id, owner, ns, depth }: { id: string; owner: OwnerRef; ns: string; depth: number }) {
  const { resources } = useCluster();
  const meta = resources.data?.find((r) => r.key === owner.key);
  const ownerNs = meta && !meta.namespaced ? "" : ns;
  const follow = !!owner.key && depth < MAX_OWNER_DEPTH && (!meta || meta.namespaced);
  const parents = useApi<OwnerRef[]>(
    () => (follow ? api.resource(id, owner.key, owner.name, ownerNs).then((d) => d.owners ?? []) : Promise.resolve([])),
    [id, owner.key, owner.name, ownerNs, follow],
  );
  return (
    <span className="inline-flex flex-wrap items-baseline gap-x-1.5">
      <span className="font-sans text-xs text-muted">{owner.kind}</span>
      {owner.key ? <Link to={resourceUrl(id, owner.key, owner.name, ownerNs)}>{owner.name}</Link> : <span>{owner.name}</span>}
      {(parents.data ?? []).map((p) => (
        <span key={`${p.kind}/${p.name}`} className="inline-flex flex-wrap items-baseline gap-x-1.5">
          <span aria-label="owned by" className="text-muted">
            →
          </span>
          <OwnerLink id={id} owner={p} ns={ns} depth={depth + 1} />
        </span>
      ))}
    </span>
  );
}

function Annotations({ map }: { map: Record<string, string> }) {
  const entries = Object.entries(map ?? {});
  const long = entries.length > ANNOTATIONS_FOLD || entries.reduce((n, [k, v]) => n + k.length + v.length, 0) > ANNOTATIONS_FOLD_CHARS;
  const [open, setOpen] = useState(false);
  if (entries.length === 0) return <span className="text-[13px] text-muted">none</span>;
  if (!long || open)
    return (
      <div className="flex flex-col items-start gap-2">
        <Chips map={map} />
        {long && (
          <button type="button" className="link-btn text-xs" aria-expanded={true} onClick={() => setOpen(false)}>
            Show fewer
          </button>
        )}
      </div>
    );
  const first = Object.fromEntries(entries.sort(([a], [b]) => a.localeCompare(b)).slice(0, ANNOTATIONS_FOLD));
  return (
    <div className="flex flex-col items-start gap-2">
      <Chips map={first} />
      <button type="button" className="link-btn text-xs" aria-expanded={false} onClick={() => setOpen(true)}>
        Show all {entries.length} annotations
      </button>
    </div>
  );
}

function Overview({ id, d, now }: { id: string; d: ResourceDetail; now: number }) {
  return (
    <div className="flex flex-col gap-4">
      <section className="card p-4" aria-label="Details">
        <dl className="m-0 grid grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))] gap-x-5 gap-y-3">
          <Field label="Kind">{d.kind}</Field>
          <Field label="API version">{d.apiVersion}</Field>
          <Field label="Name">{d.name}</Field>
          <Field label="Namespace">{d.namespace ? <Link to={workloadsOverviewUrl(id, d.namespace)}>{d.namespace}</Link> : <span className="font-sans text-muted">cluster-scoped</span>}</Field>
          <Field label="Created">
            {formatDateTime(d.createdAt)} <span className="font-sans text-muted">({age(d.createdAt, now)} ago)</span>
          </Field>
          <Field label="Controlled by">
            {(d.owners ?? []).length === 0 ? (
              <Dash />
            ) : (
              <div className="flex flex-col gap-1">
                {d.owners.map((o) => (
                  <OwnerLink key={`${o.kind}/${o.name}`} id={id} owner={o} ns={d.namespace} depth={1} />
                ))}
              </div>
            )}
          </Field>
          {d.selector && (
            <div className="col-span-full min-w-0">
              <dt className="text-xs text-muted">Selector</dt>
              <dd className="m-0 mt-0.5">
                <code className="break-all rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs">{d.selector}</code>
              </dd>
            </div>
          )}
        </dl>
      </section>

      <section className="card flex flex-col gap-3 p-4" aria-label="Labels and annotations">
        <div>
          <h2 className="mb-2 mt-0 text-[13px] font-semibold">Labels</h2>
          <Chips map={d.labels} />
        </div>
        <div>
          <h2 className="mb-2 mt-0 text-[13px] font-semibold">Annotations</h2>
          <Annotations map={d.annotations} />
        </div>
      </section>

      {(d.pods ?? []).length > 0 && (
        <section className="card" aria-labelledby="rd-pods">
          <div className="card-head">
            <h2 id="rd-pods" className="h2">
              Pods · {d.pods.length}
            </h2>
            {d.selector && <span className="font-mono text-xs text-muted">{d.selector}</span>}
          </div>
          <div className="overflow-x-auto">
            <PodsMiniTable id={id} pods={d.pods} showNs={false} now={now} />
          </div>
        </section>
      )}
    </div>
  );
}

export default function ResourceDetailPage() {
  const { group = "", version = "", resource = "", name = "" } = useParams();
  const rkey = `${group}/${version}/${resource}`;
  const [params] = useSearchParams();
  const ns = params.get("ns") ?? "";
  const tabParam = params.get("tab");
  const tab: Tab = TABS.some(([t]) => t === tabParam) ? (tabParam as Tab) : "overview";
  const { id, resources } = useCluster();
  const meta = resources.data?.find((r) => r.key === rkey);
  const plural = meta ? resourceLabel(meta) : resource;
  useClusterSection(plural);

  const detail = useApi(() => api.resource(id, rkey, name, ns), [id, rkey, name, ns]);
  const now = useNow(30_000);
  const d = detail.data && detail.data.name === name ? detail.data : undefined;
  const kind = d?.kind || meta?.kind || resource;
  const isSecret = kind === "Secret";

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2.5">
        <Link to={resourceListUrl(id, rkey, meta?.namespaced ? ns : undefined)} className="btn">
          <ArrowLeftIcon size={14} />
          {plural}
        </Link>
        <h2 className="m-0 break-all text-[17px] font-semibold">
          <span className="font-normal text-muted">{kind} · </span>
          <span className="font-mono">{name}</span>
        </h2>
        {ns && <Pill tone="violet">{ns}</Pill>}
        {d?.createdAt && (
          <span className="text-xs text-muted" title={d.createdAt}>
            created {age(d.createdAt, now)} ago
          </span>
        )}
        {detail.loading && d && <Spinner />}
        <button type="button" className="btn ml-auto" onClick={detail.reload} disabled={detail.loading}>
          <RefreshIcon size={14} />
          Refresh
        </button>
      </div>

      {isSecret && (
        <div role="note" className="flex items-center gap-2 rounded-card border border-violet-border bg-violet-tint px-3.5 py-2.5 text-[13px] text-violet">
          <LockIcon size={14} className="flex-none" />
          Values are never shown in the console. Keys are listed with their size only.
        </div>
      )}

      <nav aria-label={`${kind} sections`} className="flex flex-wrap gap-0.5 border-b border-line">
        {TABS.map(([t, label]) => (
          <Link
            key={t}
            to={resourceUrl(id, rkey, name, ns, t)}
            replace
            className={`tab-link ${tab === t ? "active" : ""}`}
            aria-current={tab === t ? "page" : undefined}
          >
            {label}
            {t === "events" && d && d.events.length > 0 && <span className="ml-1 text-xs text-muted">({d.events.length})</span>}
          </Link>
        ))}
      </nav>

      {detail.error && !d ? (
        <ErrorCard title={`Couldn't load this ${kind}`} error={detail.error} onRetry={detail.reload} />
      ) : !d ? (
        <div className="card">
          <Loading label={`Loading ${kind}…`} />
        </div>
      ) : (
        <>
          {detail.error && <ErrorCard title="Refresh failed — showing the last data" error={detail.error} onRetry={detail.reload} />}
          {tab === "overview" && <Overview id={id} d={d} now={now} />}
          {tab === "yaml" && <YamlViewer yaml={d.yaml} />}
          {tab === "events" && (
            <section className="card" aria-label={`${kind} events`}>
              {(d.events ?? []).length === 0 ? (
                <Empty title="No events">Kubernetes keeps events for about an hour.</Empty>
              ) : (
                <div className="overflow-x-auto">
                  <EventsTable events={d.events} showObject={false} now={now} />
                </div>
              )}
            </section>
          )}
        </>
      )}
    </div>
  );
}
