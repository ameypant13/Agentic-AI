import { useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { ChartSeries, ClusterMetrics, MetricsQuery } from "../api/types";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";
import { Chart, toLines } from "./Chart";
import { Spinner } from "./Icons";

/**
 * Chart panels backed by GET /clusters/{id}/metrics. `range` values are Go durations
 * (the server parses them with time.ParseDuration, which has no "d" unit).
 */
export const RANGES = [
  ["1h", "1 h"],
  ["6h", "6 h"],
  ["24h", "24 h"],
  ["168h", "7 d"],
] as const;
export type Range = (typeof RANGES)[number][0];

const RANGE_SECONDS: Record<Range, number> = { "1h": 3600, "6h": 6 * 3600, "24h": 86400, "168h": 7 * 86400 };

export const settingsGrafanaUrl = "/settings?tab=grafana";

export function RangePicker({ value, onChange, label = "Chart range" }: { value: Range; onChange: (r: Range) => void; label?: string }) {
  return (
    <div role="group" aria-label={label} className="inline-flex">
      {RANGES.map(([r, text], i) => {
        const on = value === r;
        return (
          <button
            key={r}
            type="button"
            aria-pressed={on}
            onClick={() => onChange(r)}
            className={`btn min-h-[30px] rounded-none px-2.5 text-xs ${i === 0 ? "rounded-l-btn" : "-ml-px"} ${i === RANGES.length - 1 ? "rounded-r-btn" : ""} ${
              on ? "relative z-10 border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""
            }`}
          >
            {text}
          </button>
        );
      })}
    </div>
  );
}

/** "Not connected to Grafana" line; admins get a link to Settings → Grafana. */
export function NotConnected({ what = "metrics issues and charts are unavailable for this cluster" }: { what?: string }) {
  const { me } = useMe();
  return (
    <div className="text-[13px] text-muted">
      Not connected to Grafana — {what}.
      {me.isAdmin && (
        <>
          {" "}
          <Link to={settingsGrafanaUrl}>Map it in Settings → Grafana</Link>
        </>
      )}
    </div>
  );
}

type ChartSpec = {
  title: string;
  note?: string;
  /** which series belong to this chart */
  match: (s: ChartSeries) => boolean;
  /** hide the chart when it has no series (instead of an empty state) */
  optional?: boolean;
  /** with `optional`: no skeleton before the first response either */
  hideWhileLoading?: boolean;
};

const byName = (re: RegExp) => (s: ChartSeries) => re.test(s.name);

function windowOf(m: ClusterMetrics | undefined, range: Range): { from?: number; to?: number } {
  const to = m?.to ? Date.parse(m.to) / 1000 : undefined;
  const from = m?.from ? Date.parse(m.from) / 1000 : to !== undefined ? to - RANGE_SECONDS[range] : undefined;
  return { from: Number.isFinite(from) ? from : undefined, to: Number.isFinite(to) ? to : undefined };
}

/**
 * Card with a range picker and a grid of charts for one metrics panel. Charts whose
 * series are empty are hidden when `optional`; while loading every non-optional chart
 * shows its skeleton.
 */
function MetricsCard({
  id,
  query,
  specs,
  title,
  headingId,
  extra,
}: {
  id: string;
  query: Omit<MetricsQuery, "range">;
  specs: ChartSpec[];
  title: string;
  headingId: string;
  extra?: ReactNode;
}) {
  const [range, setRange] = useState<Range>("6h");
  const m = useApi(() => api.clusterMetrics(id, { ...query, range }), [id, query.panel, query.ns, query.pod, query.node, range]);
  const data = m.data;
  const series = useMemo(() => data?.series ?? [], [data]);
  const unavailable = data && !data.available;
  const { from, to } = windowOf(data, range);

  const shown = specs
    .map((spec) => {
      const own = series.filter(spec.match);
      const fallback = spec.title === "Restarts" ? "count" : spec.title.startsWith("CPU") ? "cores" : "bytes";
      return { spec, unit: own[0]?.unit ?? fallback, lines: toLines(own) };
    })
    // Optional charts appear only once they have data; while loading the first time, show skeletons for the rest.
    .filter((c) => !c.spec.optional || c.lines.length > 0 || (!data && !c.spec.hideWhileLoading));
  const nothing = !!data && shown.length === 0;

  return (
    <section className="card" aria-labelledby={headingId}>
      <div className="card-head">
        <h2 id={headingId} className="h2">
          {title}
        </h2>
        <div className="flex flex-wrap items-center gap-2">
          {m.loading && data && <Spinner />}
          {data?.available && data.instance && (
            <span className="font-mono text-xs text-muted" title="Grafana instance / metrics cluster">
              {data.instance} / {data.cluster}
            </span>
          )}
          <RangePicker value={range} onChange={setRange} />
        </div>
      </div>
      <div className="p-4">
        {unavailable ? (
          <NotConnected what="charts are unavailable for this cluster" />
        ) : nothing ? (
          <div className="flex min-h-[120px] items-center justify-center rounded-btn border border-dashed border-line text-[13px] text-muted">No data in this range</div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fit,minmax(min(360px,100%),1fr))] gap-5">
            {shown.map(({ spec, unit, lines }) => (
              <Chart
                key={spec.title}
                title={spec.title}
                note={spec.note}
                unit={unit}
                lines={lines}
                from={from}
                to={to}
                loading={m.loading}
                error={m.error && !data ? m.error : undefined}
                onRetry={m.reload}
              />
            ))}
          </div>
        )}
        {m.error && data && <div className="mt-3 text-xs text-crit-fg">Refresh failed — showing the last data: {m.error.message}</div>}
        {extra}
      </div>
    </section>
  );
}

// ---------------------------------------------------------------- panels

const CLUSTER_SPECS: ChartSpec[] = [
  { title: "CPU", note: "used vs allocatable", match: (s) => s.unit === "cores" },
  { title: "Memory", note: "used, requested, allocatable", match: (s) => s.unit === "bytes" },
];

export function ClusterCharts({ id }: { id: string }) {
  return <MetricsCard id={id} query={{ panel: "cluster" }} specs={CLUSTER_SPECS} title="Charts" headingId="cv-charts" />;
}

const POD_SPECS: ChartSpec[] = [
  { title: "CPU", note: "per container", match: byName(/^CPU\b/), optional: true },
  { title: "Memory", note: "per container · dashed = limit", match: byName(/^Memory\b/), optional: true },
  { title: "Restarts", note: "per container", match: byName(/^Restarts\b/), optional: true, hideWhileLoading: true },
  { title: "JVM heap", note: "used vs max", match: byName(/^JVM heap\b/), optional: true, hideWhileLoading: true },
];

export function PodCharts({ id, ns, pod }: { id: string; ns: string; pod: string }) {
  return <MetricsCard id={id} query={{ panel: "pod", ns, pod }} specs={POD_SPECS} title="Charts" headingId="pod-charts" />;
}

const NODE_SPECS: ChartSpec[] = [
  { title: "CPU", note: "dashed = allocatable", match: (s) => s.unit === "cores" },
  { title: "Memory", note: "dashed = allocatable", match: (s) => s.unit === "bytes" },
];

export function NodeCharts({ id, node }: { id: string; node: string }) {
  return <MetricsCard id={id} query={{ panel: "node", node }} specs={NODE_SPECS} title="Charts" headingId="nd-charts" />;
}
