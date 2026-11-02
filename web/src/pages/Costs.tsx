import { Fragment, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { CostChange, CostClusterDay, CostClusterTotal, CostWorkload, Recommendation, ReviewBatch, RightsizingReview } from "../api/types";
import { useMe } from "../lib/me";
import { AlertIcon, SearchIcon } from "../components/Icons";
import { Page } from "../components/Layout";
import { Pill } from "../components/Pill";
import { CONF_TONE, CopyValues, cpu, helmSnippet, mem, ResCell } from "../components/Rightsizing";
import { useCopilotDock } from "../components/shell/CopilotDock";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { relativeTime } from "../lib/format";
import { useApi } from "../lib/useApi";

const RANGES = [7, 30, 90];

const money = (v: number) =>
  v >= 1000 ? `$${(v / 1000).toFixed(1)}k` : v >= 100 ? `$${v.toFixed(0)}` : `$${v.toFixed(2)}`;
const pct = (part: number, whole: number) => (whole > 0 ? `${Math.round((100 * part) / whole)}%` : "—");

type Totals = Record<string, { monthlySaving: number; needsMore: number; count: number }>;
type Filter = "all" | "savings" | "risks";

const needsMore = (r: Recommendation) => r.oomDays > 0 || r.throttledPct >= 10 || r.monthlySaving < -0.5;

function Tile({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: "good" | "warn" }) {
  const color = tone === "good" ? "text-ok-fg" : tone === "warn" ? "text-warn-fg" : "text-ink";
  return (
    <div className="card px-4 py-3">
      <div className="text-[13px] text-muted">{label}</div>
      <div className={`mt-0.5 text-[24px] font-semibold leading-tight ${color}`}>{value}</div>
      {sub && <div className="truncate text-xs text-muted">{sub}</div>}
    </div>
  );
}

/** Daily cost as stacked bars: allocated (violet) and idle (grey). */
function DailyBars({ days, from, to }: { days: CostClusterDay[]; from: string; to: string }) {
  const series = useMemo(() => {
    const byDay = new Map<string, { alloc: number; idle: number; partial: boolean }>();
    for (let d = new Date(from + "T00:00:00Z"); d <= new Date(to + "T00:00:00Z"); d.setUTCDate(d.getUTCDate() + 1)) {
      byDay.set(d.toISOString().slice(0, 10), { alloc: 0, idle: 0, partial: false });
    }
    for (const d of days) {
      const e = byDay.get(d.day);
      if (!e) continue;
      e.alloc += d.allocatedCost;
      e.idle += d.idleCost;
      e.partial ||= d.hours < 23.9;
    }
    return [...byDay.entries()].map(([day, v]) => ({ day, ...v }));
  }, [days, from, to]);
  const max = Math.max(1, ...series.map((s) => s.alloc + s.idle));
  const W = 720;
  const H = 110;
  const bw = W / Math.max(1, series.length);
  return (
    <figure className="m-0">
      <svg viewBox={`0 0 ${W} ${H + 16}`} className="h-auto w-full" role="img" aria-label="Daily cost, allocated and idle">
        {series.map((s, i) => {
          const ha = (s.alloc / max) * H;
          const hi = (s.idle / max) * H;
          const x = i * bw + bw * 0.15;
          const w = bw * 0.7;
          return (
            <g key={s.day} opacity={s.partial ? 0.55 : 1}>
              <title>{`${s.day}: ${money(s.alloc + s.idle)} (${money(s.idle)} idle)${s.partial ? " · day in progress or still filling in" : ""}`}</title>
              <rect x={x} y={H - ha - hi} width={w} height={hi} fill="#CBD5E1" />
              <rect x={x} y={H - ha} width={w} height={ha} fill="#5B21B6" />
              {(i === 0 || i === series.length - 1 || i % Math.ceil(series.length / 6) === 0) && (
                <text x={x + w / 2} y={H + 12} textAnchor="middle" fontSize="10" fill="#64748B">
                  {s.day.slice(5)}
                </text>
              )}
            </g>
          );
        })}
      </svg>
      <figcaption className="flex flex-wrap gap-x-4 text-xs text-muted">
        <span>
          <span className="mr-1 inline-block h-2 w-2 rounded-sm bg-violet" /> used by workloads
        </span>
        <span>
          <span className="mr-1 inline-block h-2 w-2 rounded-sm bg-slate-300" /> idle
        </span>
        <span>peak {money(max)}/day</span>
      </figcaption>
    </figure>
  );
}

/** Clusters whose recent daily cost moved well away from their baseline, and why. */
function CostChanges({ changes, rule, onSelect, single }: { changes: CostChange[]; rule: string; onSelect?: (c: string) => void; single?: boolean }) {
  const dock = useCopilotDock();
  if (changes.length === 0) return null;
  const ask = (c: CostChange) =>
    dock.ask(
      `Explain why ${c.cluster}'s compute cost changed: ${c.summary} Drivers: ${c.drivers.map((d) => d.what).join("; ")}. ` +
        `Use query_costs (group_by changes and workload) and change_timeline for the cluster to find what caused it, and say whether it is expected.`,
    );
  return (
    <section className="card border-warn-border" aria-label="Cost changes">
      <div className="card-head">
        <h2 className="h2">{single ? "Cost changed recently" : "Cost changes"}</h2>
        <span className="text-xs text-muted">{rule}</span>
      </div>
      <ul className="m-0 list-none divide-y divide-line p-0">
        {changes.slice(0, single ? 1 : 6).map((c) => (
          <li key={c.cluster} className="px-4 py-3">
            <div className="flex flex-wrap items-center gap-2">
              {single || !onSelect ? (
                <span className="font-medium">{c.cluster}</span>
              ) : (
                <button type="button" className="font-medium text-ink hover:underline" onClick={() => onSelect(c.cluster)}>
                  {c.cluster}
                </button>
              )}
              <Pill tone={c.deltaPerDay > 0 ? "warning" : "success"}>
                {c.deltaPerDay > 0 ? "+" : ""}
                {c.pct}% · {c.deltaPerDay > 0 ? "+" : "−"}
                {money(Math.abs(c.deltaPerDay))}/day
              </Pill>
              <span className="text-xs text-muted">
                {money(c.baselinePerDay)}/day ({c.baselineFrom.slice(5)}–{c.baselineTo.slice(5)}) → {money(c.recentPerDay)}/day ({c.recentFrom.slice(5)}–
                {c.recentTo.slice(5)})
              </span>
              <button type="button" className="btn btn-sm ml-auto" onClick={() => ask(c)}>
                Ask Copilot why
              </button>
            </div>
            {c.drivers.length > 0 && (
              <ul className="mb-0 mt-1.5 pl-5 text-[13px]">
                {c.drivers.map((d) => (
                  <li key={d.kind + d.what}>
                    {d.what}
                    {d.perDay !== 0 && d.kind !== "nodes" && (
                      <span className="text-muted">
                        {" "}
                        ({d.perDay > 0 ? "+" : "−"}
                        {money(Math.abs(d.perDay))}/day)
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

const VERDICT_TONE = { safe: "success", adjust: "warning", risky: "critical" } as const;

/** Claude's verdict on one recommendation, from the last batch review. */
function ReviewNote({ review }: { review?: RightsizingReview }) {
  if (!review) return null;
  if (review.status === "pending" && !review.verdict) return <div className="mt-2 text-xs text-muted">Claude is reviewing this…</div>;
  if (!review.verdict) return null;
  const adjusted = review.verdict === "adjust" && (review.cpuRequest > 0 || review.memRequest > 0);
  return (
    <div className="mt-2 rounded-btn border border-line bg-slate-50 p-2 text-[13px]">
      <span className="mr-1.5">
        <Pill tone={VERDICT_TONE[review.verdict]}>Claude: {review.verdict}</Pill>
      </span>
      {review.reason}
      {adjusted && (
        <span className="ml-1 font-mono text-xs">
          (suggests cpu {review.cpuRequest > 0 ? cpu(review.cpuRequest) : "as recommended"}, memory {review.memRequest > 0 ? mem(review.memRequest) : "as recommended"})
        </span>
      )}
      {review.status === "pending" && <span className="ml-1 text-xs text-muted">· re-reviewing</span>}
    </div>
  );
}

/** Admins send the fleet's most valuable recommendations to Claude as one batch. */
function ReviewControl({ batch, onStarted }: { batch: ReviewBatch | null; onStarted: () => void }) {
  const { me } = useMe();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  if (!me.isAdmin) return null;
  const running = batch?.Status === "in_progress";
  const start = async () => {
    setBusy(true);
    setErr("");
    try {
      await api.submitCostReview(50);
      onStarted();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="flex flex-col items-end gap-0.5">
      <button
        type="button"
        className="btn"
        disabled={busy || running}
        onClick={start}
        title="Claude reviews the 50 most valuable recommendations (and every shortfall) through the Batch API, at half price"
      >
        {running ? `Claude reviewing: ${batch!.Succeeded + batch!.Errored}/${batch!.Requests}` : "Review with Claude"}
      </button>
      <span className="text-xs text-muted">
        {err
          ? err
          : batch?.Status === "ended"
            ? `last review ${relativeTime(batch.EndedAt ?? batch.SubmittedAt)} · ${batch.Succeeded} checked`
            : batch?.Status === "failed"
              ? `last review failed: ${batch.Error}`
              : running
                ? "results usually arrive within minutes"
                : ""}
      </span>
    </div>
  );
}

/** The cluster list: sticky on wide screens, a select on narrow ones. */
function ClusterRail({
  clusters,
  totals,
  selected,
  onSelect,
  errors,
  fleetTotal,
}: {
  clusters: CostClusterTotal[];
  totals: Totals;
  selected: string;
  onSelect: (c: string) => void;
  errors: Record<string, string>;
  fleetTotal: number;
}) {
  const [q, setQ] = useState("");
  const shown = clusters.filter((c) => !q || `${c.cluster} ${c.tenant}`.toLowerCase().includes(q.toLowerCase()));
  const row = (active: boolean) =>
    `block w-full rounded-btn px-2.5 py-2 text-left text-[13px] ${active ? "bg-violet-tint text-ink" : "hover:bg-slate-50"}`;
  return (
    <>
      <label className="lg:hidden">
        <span className="sr-only">Cluster</span>
        <select className="select min-h-[40px] w-full" value={selected} onChange={(e) => onSelect(e.target.value)}>
          <option value="">All clusters · {money(fleetTotal)}</option>
          {clusters.map((c) => (
            <option key={c.cluster} value={c.cluster}>
              {c.cluster} · {money(c.nodeCost)}
            </option>
          ))}
        </select>
      </label>
      <nav aria-label="Clusters" className="card sticky top-4 hidden max-h-[calc(100vh-2rem)] flex-col overflow-hidden lg:flex">
        <div className="relative border-b border-line p-2">
          <SearchIcon size={14} className="pointer-events-none absolute left-4 top-1/2 -translate-y-1/2 text-muted" />
          <label htmlFor="cost-cluster-q" className="sr-only">
            Filter clusters
          </label>
          <input id="cost-cluster-q" className="input min-h-[34px] pl-8 text-[13px]" placeholder="Filter clusters" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <ul className="m-0 min-h-0 flex-1 list-none overflow-y-auto p-1.5">
          <li>
            <button type="button" className={row(selected === "")} aria-current={selected === "" ? "page" : undefined} onClick={() => onSelect("")}>
              <div className="flex justify-between font-medium">
                <span>All clusters</span>
                <span>{money(fleetTotal)}</span>
              </div>
            </button>
          </li>
          {shown.map((c) => {
            const t = totals[c.cluster];
            const active = selected === c.cluster;
            return (
              <li key={c.cluster}>
                <button type="button" className={row(active)} aria-current={active ? "page" : undefined} onClick={() => onSelect(c.cluster)}>
                  <div className="flex items-center gap-1.5">
                    <span className="min-w-0 flex-1 truncate font-medium">{c.cluster}</span>
                    {errors[c.cluster] && (
                      <span className="flex-none text-warn-fg" title="The last cost run failed">
                        <AlertIcon size={13} />
                        <span className="sr-only">The last cost run failed</span>
                      </span>
                    )}
                    <span className="flex-none tabular-nums">{money(c.nodeCost)}</span>
                  </div>
                  <div className="mt-0.5 flex justify-between gap-2 text-xs text-muted">
                    <span className="truncate">
                      {c.tenant ? `${c.tenant} · ` : ""}idle {pct(c.idleCost, c.nodeCost)}
                    </span>
                    {t && t.monthlySaving >= 1 ? (
                      <span className="flex-none text-ok-fg">save {money(t.monthlySaving)}</span>
                    ) : t && t.needsMore > 0 ? (
                      <span className="flex-none text-warn-fg">{t.needsMore} need more</span>
                    ) : null}
                  </div>
                </button>
              </li>
            );
          })}
          {shown.length === 0 && <li className="px-2.5 py-2 text-[13px] text-muted">No cluster matches.</li>}
        </ul>
      </nav>
    </>
  );
}

/** One row per workload: its cost and, inline, what right-sizing would change. */
type Row = {
  key: string;
  namespace: string;
  kind: string;
  name: string;
  cost?: CostWorkload;
  recs: Recommendation[];
  saving: number;
  risky: boolean;
};

function sumRes(recs: Recommendation[], side: "current" | "recommended", field: "cpuRequest" | "memRequest") {
  return recs.reduce((s, r) => s + r[side][field], 0);
}

function WorkloadTable({ cluster, days, reviews }: { cluster: string; days: number; reviews: Map<string, RightsizingReview> }) {
  const wl = useApi(() => api.costWorkloads(days, cluster), [cluster, days]);
  const recs = useApi(() => api.costRecommendations(days, cluster), [cluster, days]);
  const dock = useCopilotDock();
  const [params, setParams] = useSearchParams();
  const filter = (params.get("filter") as Filter) || "all";
  const ns = params.get("ns") ?? "";
  const sort = params.get("sort") === "saving" ? "saving" : "cost";
  const [open, setOpen] = useState<string | null>(null);
  const set = (k: string, v: string) =>
    setParams(
      (p) => {
        if (v) p.set(k, v);
        else p.delete(k);
        return p;
      },
      { replace: true },
    );

  const rows = useMemo(() => {
    const map = new Map<string, Row>();
    const get = (namespace: string, kind: string, name: string) => {
      const key = `${namespace}/${kind}/${name}`;
      let r = map.get(key);
      if (!r) {
        r = { key, namespace, kind, name, recs: [], saving: 0, risky: false };
        map.set(key, r);
      }
      return r;
    };
    for (const w of wl.data?.workloads ?? []) get(w.namespace, w.kind, w.name).cost = w;
    for (const r of recs.data?.recommendations ?? []) {
      const row = get(r.namespace, r.kind, r.name);
      row.recs.push(r);
      row.saving += r.monthlySaving;
      row.risky ||= needsMore(r);
    }
    return [...map.values()];
  }, [wl.data, recs.data]);

  const namespaces = useMemo(() => [...new Set(rows.map((r) => r.namespace))].sort(), [rows]);
  const cost = (r: Row) => (r.cost ? r.cost.cpuCost + r.cost.memCost : 0);
  const shown = rows
    .filter((r) => (!ns || r.namespace === ns) && (filter === "all" || (filter === "savings" ? r.saving >= 1 : r.risky)))
    .sort((a, b) => (sort === "saving" ? b.saving - a.saving : cost(b) - cost(a)));
  const shownSaving = shown.reduce((s, r) => s + Math.max(0, r.saving), 0);

  const ask = (r: Recommendation) =>
    dock.ask(
      `Review this right-sizing recommendation for ${r.namespace}/${r.kind}/${r.name} (container ${r.container}) on cluster ${cluster}: ` +
        `current requests/limits cpu ${cpu(r.current.cpuRequest)}/${r.current.cpuLimit ? cpu(r.current.cpuLimit) : "none"}, memory ` +
        `${mem(r.current.memRequest)}/${r.current.memLimit ? mem(r.current.memLimit) : "none"}; over ${r.days} days cpu p95 ${cpu(r.cpuP95)}, ` +
        `cpu max ${cpu(r.cpuMax)}, peak memory ${mem(r.memMax)}, OOM-killed on ${r.oomDays} days, throttled ${r.throttledPct.toFixed(0)}%. ` +
        `Recommended cpu ${cpu(r.recommended.cpuRequest)} with no CPU limit and memory ${mem(r.recommended.memRequest)}. ` +
        `Is it safe? Check for JVM heap settings, spikes, restarts and anything that makes it risky, and say what you would change.`,
    );

  const error = wl.error ?? recs.error;
  const chip = (active: boolean) => `btn btn-sm ${active ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`;

  return (
    <section className="card" aria-labelledby="wl-h">
      <div className="card-head flex-wrap gap-2">
        <h2 id="wl-h" className="h2">
          Workloads
        </h2>
        <div className="flex flex-wrap items-center gap-1.5">
          <label>
            <span className="sr-only">Namespace</span>
            <select className="select min-h-[32px] text-[13px]" value={ns} onChange={(e) => set("ns", e.target.value)}>
              <option value="">All namespaces</option>
              {namespaces.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
          <div className="flex gap-1" role="group" aria-label="Show">
            {(
              [
                ["all", "All"],
                ["savings", "Savings"],
                ["risks", "Needs more"],
              ] as [Filter, string][]
            ).map(([f, label]) => (
              <button key={f} type="button" aria-pressed={filter === f} className={chip(filter === f)} onClick={() => set("filter", f === "all" ? "" : f)}>
                {label}
              </button>
            ))}
          </div>
          <label>
            <span className="sr-only">Sort by</span>
            <select className="select min-h-[32px] text-[13px]" value={sort} onChange={(e) => set("sort", e.target.value === "cost" ? "" : e.target.value)}>
              <option value="cost">Sort: cost</option>
              <option value="saving">Sort: saving</option>
            </select>
          </label>
        </div>
      </div>
      {error ? (
        <div className="p-4">
          <ErrorCard
            title="Couldn't load workloads"
            error={error}
            onRetry={() => {
              wl.reload();
              recs.reload();
            }}
          />
        </div>
      ) : !wl.data || !recs.data ? (
        <Loading label="Loading workloads…" />
      ) : shown.length === 0 ? (
        <Empty title={filter === "risks" ? "Nothing under-sized" : filter === "savings" ? "Nothing to trim" : "No workloads"}>
          {filter === "risks"
            ? "No workload here was OOM-killed, throttled or short of its request."
            : filter === "savings"
              ? "Every workload is sized within a dollar a month of its use."
              : "No cost recorded for this range."}
        </Empty>
      ) : (
        <>
          {filter === "savings" && (
            <div className="border-b border-line px-4 py-2 text-[13px]">
              Right-sizing these {shown.length} would save about <span className="font-semibold">{money(shownSaving)}</span> a month.
            </div>
          )}
          <div className="overflow-x-auto">
            <table className="table min-w-[860px]">
              <thead>
                <tr>
                  <th scope="col">Workload</th>
                  <th scope="col" className="text-right">
                    Cost
                  </th>
                  <th scope="col">CPU request</th>
                  <th scope="col">Memory request</th>
                  <th scope="col" className="text-right">
                    <abbr
                      title="Monthly saving from KRR-style sizing: CPU request = highest daily 95th-percentile use with no CPU limit; memory = peak + 15%."
                      className="no-underline"
                    >
                      Saves / mo
                    </abbr>
                  </th>
                </tr>
              </thead>
              <tbody>
                {shown.slice(0, 200).map((r) => {
                  const isOpen = open === r.key;
                  const has = r.recs.length > 0;
                  const arrow = (cur: number, rec: number, fmt: (v: number) => string) =>
                    !has ? (
                      <span className="text-muted">—</span>
                    ) : Math.abs(cur - rec) < 1e-9 ? (
                      <span className="font-mono text-xs">{fmt(cur)}</span>
                    ) : (
                      <span className="font-mono text-xs">
                        <span className="text-muted">{fmt(cur)}</span> → <span className="font-semibold text-ink">{fmt(rec)}</span>
                      </span>
                    );
                  return (
                    <Fragment key={r.key}>
                      <tr className={isOpen ? "bg-slate-50" : ""}>
                        <td>
                          <button
                            type="button"
                            className="flex w-full items-start gap-1.5 text-left"
                            aria-expanded={isOpen}
                            aria-controls={`wl-${r.key}`}
                            onClick={() => setOpen(isOpen ? null : r.key)}
                          >
                            <span aria-hidden="true" className={`mt-0.5 inline-block w-3 flex-none text-muted transition-transform ${isOpen ? "rotate-90" : ""}`}>
                              ›
                            </span>
                            <span className="min-w-0">
                              <span className="font-medium text-ink">{r.name}</span>
                              <span className="block text-xs text-muted">
                                {r.namespace} · {r.kind}
                                {r.recs.length > 1 && ` · ${r.recs.length} containers`}
                              </span>
                            </span>
                            {r.recs.some((x) => x.oomDays > 0) && <Pill tone="critical">OOM</Pill>}
                            {r.recs.some((x) => x.throttledPct >= 10) && <Pill tone="warning">throttled</Pill>}
                            {(() => {
                              const vs = r.recs.map((x) => reviews.get(`${x.cluster}|${x.namespace}|${x.kind}|${x.name}|${x.container}`)?.verdict).filter(Boolean);
                              const worst = vs.includes("risky") ? "risky" : vs.includes("adjust") ? "adjust" : vs.length ? "safe" : "";
                              return worst ? <Pill tone={VERDICT_TONE[worst as "safe"]}>Claude: {worst}</Pill> : null;
                            })()}
                          </button>
                        </td>
                        <td className="text-right font-semibold tabular-nums">{r.cost ? money(cost(r)) : <span className="font-normal text-muted">—</span>}</td>
                        <td>{arrow(sumRes(r.recs, "current", "cpuRequest"), sumRes(r.recs, "recommended", "cpuRequest"), cpu)}</td>
                        <td>{arrow(sumRes(r.recs, "current", "memRequest"), sumRes(r.recs, "recommended", "memRequest"), mem)}</td>
                        <td className={`text-right font-semibold tabular-nums ${r.saving < -0.5 ? "text-warn-fg" : r.saving >= 1 ? "text-ok-fg" : ""}`}>
                          {!has || Math.abs(r.saving) < 0.5 ? (
                            <span className="font-normal text-muted">—</span>
                          ) : r.saving < 0 ? (
                            `needs +${money(-r.saving)}`
                          ) : (
                            money(r.saving)
                          )}
                        </td>
                      </tr>
                      {isOpen && (
                        <tr id={`wl-${r.key}`} className="bg-slate-50">
                          <td colSpan={5} className="pt-0">
                            {r.cost && (
                              <p className="m-0 mb-2 text-xs text-muted">
                                Over {days} days: CPU {r.cost.cpuRequestCoreHours.toFixed(0)} core-h requested, {r.cost.cpuUsageCoreHours.toFixed(0)} used ·
                                memory {r.cost.memRequestGiBHours.toFixed(0)} GiB-h requested, {r.cost.memUsageGiBHours.toFixed(0)} used · CPU{" "}
                                {money(r.cost.cpuCost)}, memory {money(r.cost.memCost)}
                              </p>
                            )}
                            {!has ? (
                              <p className="m-0 text-[13px] text-muted">
                                No right-sizing for this workload: bare pods and one-off jobs have nothing to resize, and new workloads need a day of data.
                              </p>
                            ) : (
                              <div className="grid gap-2">
                                {r.recs.map((x) => (
                                  <div key={x.container} className="rounded-btn border border-line bg-white p-3">
                                    <div className="mb-2 flex flex-wrap items-center gap-2">
                                      <span className="font-mono text-[13px] font-medium">{x.container}</span>
                                      <Pill tone={CONF_TONE[x.confidence]}>{x.confidence}</Pill>
                                      {x.oomDays > 0 && <Pill tone="critical">OOM {x.oomDays}d</Pill>}
                                      {x.throttledPct >= 10 && <Pill tone="warning">throttled {x.throttledPct.toFixed(0)}%</Pill>}
                                      <span className="text-xs text-muted">
                                        {x.days}d · {x.replicas} replica{x.replicas === 1 ? "" : "s"}
                                      </span>
                                      <span className="ml-auto flex gap-1.5">
                                        <CopyValues r={x} />
                                        <button type="button" className="btn btn-sm" onClick={() => ask(x)}>
                                          Ask Copilot
                                        </button>
                                      </span>
                                    </div>
                                    <div className="grid gap-3 sm:grid-cols-3">
                                      <div>
                                        <div className="text-xs text-muted">Used</div>
                                        <div className="font-mono text-xs leading-5">
                                          <div>
                                            cpu p95 {cpu(x.cpuP95)} · max {cpu(x.cpuMax)}
                                          </div>
                                          <div>mem peak {mem(x.memMax)}</div>
                                        </div>
                                      </div>
                                      <div>
                                        <div className="text-xs text-muted">Now (request / limit)</div>
                                        <ResCell r={x.current} />
                                      </div>
                                      <div>
                                        <div className="text-xs text-muted">Recommended</div>
                                        <ResCell r={x.recommended} base={x.current} />
                                      </div>
                                    </div>
                                    {x.notes.length > 0 && (
                                      <ul className="mb-0 mt-2 pl-5 text-[13px]">
                                        {x.notes.map((n) => (
                                          <li key={n}>{n}</li>
                                        ))}
                                      </ul>
                                    )}
                                    <ReviewNote review={reviews.get(`${x.cluster}|${x.namespace}|${x.kind}|${x.name}|${x.container}`)} />
                                    <details className="mt-2">
                                      <summary className="cursor-pointer text-xs text-muted">Chart values</summary>
                                      <pre className="m-0 mt-1 overflow-x-auto rounded-btn border border-line bg-slate-50 p-2 font-mono text-xs">{helmSnippet(x)}</pre>
                                    </details>
                                  </div>
                                ))}
                                <p className="m-0 text-xs text-muted">
                                  Prophecy services are installed with Helm: set these in the control plane&apos;s chart values. Editing the Deployment
                                  directly restarts its pods and is reverted on the next upgrade.
                                </p>
                              </div>
                            )}
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}

export default function CostsPage() {
  const [params, setParams] = useSearchParams();
  const days = RANGES.includes(Number(params.get("days"))) ? Number(params.get("days")) : 30;
  const selected = params.get("cluster") ?? "";
  const sum = useApi(() => api.costs(days), [days]);
  const totals = useApi(() => api.costRecommendationTotals(days), [days]);
  const reviews = useApi(() => api.costReviews(), []);
  const reviewMap = useMemo(() => new Map((reviews.data?.reviews ?? []).map((r) => [r.key, r])), [reviews.data]);
  useEffect(() => {
    if (reviews.data?.batch?.Status !== "in_progress") return;
    const t = setInterval(reviews.reload, 60_000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reviews.data?.batch?.Status]);
  const changes = useApi(() => api.costChanges(), []);
  const allChanges = changes.data?.changes ?? [];
  const t: Totals = totals.data?.clusters ?? {};
  const pending = !totals.data && !totals.error;
  const savingText = (v: number | undefined) => (pending ? "…" : v ? `${money(v)}/mo` : "—");

  const update = (patch: Record<string, string>) =>
    setParams((p) => {
      for (const [k, v] of Object.entries(patch)) {
        if (v) p.set(k, v);
        else p.delete(k);
      }
      return p;
    });
  const select = (c: string) => {
    update({ cluster: c, ns: "" });
    window.scrollTo({ top: 0 });
  };

  const data = sum.data;
  const cluster = data?.clusters.find((c) => c.cluster === selected);
  const fleetSaving = Object.values(t).reduce((s, x) => s + x.monthlySaving, 0);
  const fleetNeeds = Object.values(t).reduce((s, x) => s + x.needsMore, 0);
  const types = (c: CostClusterTotal) =>
    Object.entries(JSON.parse(c.nodeTypes || "{}") as Record<string, number>)
      .map(([k, n]) => `${n}× ${k}`)
      .join(", ");

  return (
    <Page>
      <PageHeader title="Costs" subtitle="Estimated compute cost per cluster and workload at on-demand list prices, with right-sizing from actual use.">
        <ReviewControl batch={reviews.data?.batch ?? null} onStarted={reviews.reload} />
        <div className="flex gap-1.5" role="group" aria-label="Range">
          {RANGES.map((r) => (
            <button
              key={r}
              type="button"
              aria-pressed={days === r}
              onClick={() => update({ days: r === 30 ? "" : String(r) })}
              className={`btn ${days === r ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
            >
              {r} days
            </button>
          ))}
        </div>
      </PageHeader>

      {sum.error ? (
        <ErrorCard title="Couldn't load costs" error={sum.error} onRetry={sum.reload} />
      ) : !data ? (
        <div className="card">
          <Loading label="Loading costs…" />
        </div>
      ) : data.clusters.length === 0 ? (
        <div className="card">
          <Empty title="No cost data yet">The cost worker fills in today first, then the past {days} days one day at a time. Check back in an hour.</Empty>
        </div>
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-[250px_minmax(0,1fr)]">
          <ClusterRail clusters={data.clusters} totals={t} selected={cluster ? selected : ""} onSelect={select} errors={data.errors} fleetTotal={data.total} />

          <div className="flex min-w-0 flex-col gap-4">
            {cluster ? (
              <>
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <h2 className="m-0 text-[20px] font-semibold">{cluster.cluster}</h2>
                  <span className="text-[13px] text-muted">
                    {[cluster.tenant, cluster.instance, `${cluster.nodes} nodes`, types(cluster)].filter(Boolean).join(" · ")}
                  </span>
                </div>
                {data.errors[cluster.cluster] && (
                  <div className="card border-warn-border p-3 text-[13px]" role="status">
                    <span className="font-medium text-warn-fg">The last run failed:</span> <span className="break-all">{data.errors[cluster.cluster]}</span>
                  </div>
                )}
                {cluster.estimatedNodes > 0 && (
                  <div className="card border-warn-border p-3 text-[13px]" role="status">
                    <span className="font-medium text-warn-fg">Default rates.</span> {cluster.estimatedNodes} node{cluster.estimatedNodes === 1 ? "" : "s"}{" "}
                    have no known list price and are priced at OpenCost&apos;s default per-vCPU and per-GiB rates.
                  </div>
                )}
                <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
                  <Tile label={`Compute, ${days} days`} value={money(cluster.nodeCost)} sub={`${data.from} → ${data.to}`} />
                  <Tile label="Idle" value={pct(cluster.idleCost, cluster.nodeCost)} sub={`${money(cluster.idleCost)} no workload used`} />
                  <Tile label="Could save" value={savingText(t[cluster.cluster]?.monthlySaving)} tone="good" sub="by right-sizing requests" />
                  <Tile
                    label="Need more"
                    value={pending ? "…" : String(t[cluster.cluster]?.needsMore ?? 0)}
                    tone={(t[cluster.cluster]?.needsMore ?? 0) > 0 ? "warn" : undefined}
                    sub="OOM-killed or throttled"
                  />
                </div>
                <CostChanges changes={allChanges.filter((c) => c.cluster === cluster.cluster)} rule={changes.data?.rule ?? ""} single />
                <section className="card px-4 py-3" aria-label="Daily cost">
                  <DailyBars days={data.days.filter((d) => d.cluster === cluster.cluster)} from={data.from} to={data.to} />
                </section>
                <WorkloadTable cluster={cluster.cluster} days={days} reviews={reviewMap} />
              </>
            ) : (
              <>
                <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
                  <Tile label={`Compute, ${days} days`} value={money(data.total)} sub={`${data.from} → ${data.to}`} />
                  <Tile label="Idle" value={pct(data.idle, data.total)} sub={`${money(data.idle)} no workload used`} />
                  <Tile label="Could save" value={savingText(fleetSaving)} tone="good" sub="by right-sizing requests" />
                  <Tile label="Need more" value={pending ? "…" : String(fleetNeeds)} tone={fleetNeeds > 0 ? "warn" : undefined} sub={`containers · updated ${data.lastRun ? relativeTime(data.lastRun) : "—"}`} />
                </div>
                <CostChanges changes={allChanges} rule={changes.data?.rule ?? ""} onSelect={select} />
                <section className="card px-4 py-3" aria-label="Daily cost, all clusters">
                  <DailyBars days={data.days} from={data.from} to={data.to} />
                </section>
                <section className="card" aria-labelledby="by-cluster">
                  <div className="card-head">
                    <h2 id="by-cluster" className="h2">
                      Clusters
                    </h2>
                    <span className="text-xs text-muted">pick one for its workloads and right-sizing</span>
                  </div>
                  <div className="overflow-x-auto">
                    <table className="table min-w-[640px]">
                      <thead>
                        <tr>
                          <th scope="col">Cluster</th>
                          <th scope="col" className="text-right">
                            Cost
                          </th>
                          <th scope="col" className="text-right">
                            Idle
                          </th>
                          <th scope="col" className="text-right">
                            Could save / mo
                          </th>
                          <th scope="col" className="text-right">
                            Need more
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {data.clusters.map((c) => (
                          <tr key={c.cluster} className="cursor-pointer hover:bg-slate-50" onClick={() => select(c.cluster)}>
                            <td>
                              <button type="button" className="text-left font-medium text-ink hover:underline" onClick={() => select(c.cluster)}>
                                {c.cluster}
                              </button>
                              <div className="text-xs text-muted">{[c.tenant, c.instance].filter(Boolean).join(" · ")}</div>
                            </td>
                            <td className="text-right font-semibold tabular-nums">{money(c.nodeCost)}</td>
                            <td className="text-right tabular-nums">{pct(c.idleCost, c.nodeCost)}</td>
                            <td className="text-right tabular-nums text-ok-fg">{pending ? "…" : t[c.cluster]?.monthlySaving ? money(t[c.cluster].monthlySaving) : "—"}</td>
                            <td className={`text-right tabular-nums ${(t[c.cluster]?.needsMore ?? 0) > 0 ? "text-warn-fg" : "text-muted"}`}>
                              {t[c.cluster]?.needsMore || "—"}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </section>
              </>
            )}
            <p className="m-0 text-xs text-muted">{data.note}</p>
          </div>
        </div>
      )}
    </Page>
  );
}
