import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent, type RefObject } from "react";
import type { ChartSeries, ChartUnit } from "../api/types";

/**
 * Small dependency-free SVG line chart: several series, dashed reference lines
 * (limit / allocatable / max), unit-aware y ticks, local-time x ticks, a hover (and
 * arrow-key) crosshair with a tooltip of every series' value, a legend, and loading,
 * empty and error states. Width follows the container (ResizeObserver).
 */

// ---------------------------------------------------------------- colours

/**
 * Categorical slots, fixed order (never cycled by rank). Checked with the dataviz
 * validator on white: lightness band, chroma and normal-vision separation pass; the
 * CVD floor band is covered by the legend, the tooltip and dashed reference lines.
 * The first slot is the app's violet; lightness alternates dark / light / mid.
 */
export const SERIES_COLORS = ["#5B21B6", "#EB6834", "#1BAF7A", "#2A78D6", "#EDA100", "#E87BA4", "#008300", "#E34948"];
/** Reference lines (allocatable, max) that belong to no coloured series. */
export const REFERENCE_COLORS = ["#334155", "#94A3B8"];

/** "limit", "allocatable" and "max" series are drawn dashed. */
export const isReferenceSeries = (name: string) => /\b(limit|allocatable|max)\b/i.test(name);

export type ChartLine = {
  key: string;
  label: string;
  color: string;
  dashed: boolean;
  /** [unix seconds, value], sorted by time */
  points: [number, number][];
};

/**
 * API series → chart lines. Solid series take the categorical slots in order; a dashed
 * series takes the colour of the solid series with the same container (a container's
 * memory limit matches its memory line), else a neutral reference colour.
 * Series without points are dropped.
 */
export function toLines(series: ChartSeries[]): ChartLine[] {
  const withPoints = series.filter((s) => (s.points ?? []).length > 0);
  const containerColor = new Map<string, string>();
  let slot = 0;
  let ref = 0;
  const solid = withPoints.filter((s) => !isReferenceSeries(s.name));
  const solidColor = new Map<ChartSeries, string>();
  for (const s of solid) {
    const ct = s.labels?.container ?? "";
    const color = (ct && containerColor.get(ct)) || SERIES_COLORS[slot++ % SERIES_COLORS.length];
    if (ct) containerColor.set(ct, color);
    solidColor.set(s, color);
  }
  return withPoints.map((s, i) => {
    const dashed = isReferenceSeries(s.name);
    const ct = s.labels?.container ?? "";
    let color = solidColor.get(s);
    if (!color) color = (ct && containerColor.get(ct)) || REFERENCE_COLORS[Math.min(ref++, REFERENCE_COLORS.length - 1)];
    // "Memory limit · app" → "app · limit"; "CPU · app" → "app"; others keep their name.
    let label = s.name;
    if (ct) {
      const base = s.name.split(" · ")[0];
      label = dashed ? `${ct} · ${base.split(" ").pop()}` : ct;
    }
    const points = [...(s.points ?? [])].filter((p) => Number.isFinite(p[0]) && Number.isFinite(p[1])).sort((a, b) => a[0] - b[0]);
    return { key: `${s.name}|${i}`, label, color, dashed, points };
  });
}

// ---------------------------------------------------------------- units

const KiB = 1024;
const MiB = KiB * 1024;
const GiB = MiB * 1024;
const TiB = GiB * 1024;

function trim(n: number, digits: number): string {
  return n.toFixed(digits).replace(/\.0+$|(\.\d*?)0+$/, "$1");
}

/** Divisor + suffix used for every tick of a byte axis, picked from the axis maximum. */
function byteScale(max: number): [number, string] {
  if (max >= TiB) return [TiB, "TiB"];
  if (max >= GiB) return [GiB, "GiB"];
  if (max >= MiB) return [MiB, "MiB"];
  if (max >= KiB) return [KiB, "KiB"];
  return [1, "B"];
}

/** A single value with its own best unit (tooltips, aria summary). */
export function formatValue(v: number, unit: ChartUnit): string {
  if (!Number.isFinite(v)) return "—";
  if (unit === "bytes") {
    const [d, s] = byteScale(Math.abs(v));
    return `${trim(v / d, v / d >= 100 ? 0 : v / d >= 10 ? 1 : 2)} ${s}`;
  }
  if (unit === "cores") {
    if (v !== 0 && Math.abs(v) < 1) return `${trim(v * 1000, v * 1000 >= 10 ? 0 : 1)}m`;
    return `${trim(v, v >= 100 ? 0 : 2)} ${v === 1 ? "core" : "cores"}`;
  }
  if (unit === "count") return Number.isInteger(v) ? String(v) : trim(v, 2);
  return trim(v, Math.abs(v) >= 100 ? 0 : 2);
}

/** Round step (1, 2, 2.5, 5 × 10^n) giving about `target` intervals. */
function niceStep(range: number, target: number, integer: boolean): number {
  if (range <= 0) return integer ? 1 : 1;
  const raw = range / target;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const norm = raw / mag;
  const step = (norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10) * mag;
  return integer ? Math.max(1, Math.ceil(step)) : step;
}

type YAxis = { min: number; max: number; ticks: number[]; label: (v: number) => string };

function yAxis(lo: number, hi: number, unit: ChartUnit): YAxis {
  const min = Math.min(0, lo);
  let max = hi > min ? hi : min + (unit === "count" ? 1 : 1);
  if (unit === "bytes") {
    const [d, s] = byteScale(max);
    const step = niceStep((max - min) / d, 4, false) * d;
    max = Math.ceil(max / step) * step;
    const ticks = range(min, max, step);
    const digits = step / d < 1 ? (step / d < 0.1 ? 2 : 1) : 0;
    return { min, max, ticks, label: (v) => `${trim(v / d, digits)} ${s}` };
  }
  if (unit === "cores") {
    const step = niceStep(max - min, 4, false);
    max = Math.ceil(max / step) * step;
    const ticks = range(min, max, step);
    const milli = max < 1;
    return { min, max, ticks, label: (v) => (milli ? `${trim(v * 1000, 0)}m` : trim(v, step < 1 ? (step < 0.1 ? 2 : 1) : 0)) };
  }
  const step = niceStep(max - min, 4, unit === "count");
  max = Math.ceil(max / step) * step;
  return { min, max, ticks: range(min, max, step), label: (v) => trim(v, step < 1 ? 2 : 0) };
}

function range(min: number, max: number, step: number): number[] {
  const out: number[] = [];
  for (let v = Math.ceil(min / step) * step, i = 0; v <= max + step * 1e-6 && i < 20; v += step, i++) out.push(Math.abs(v) < step * 1e-9 ? 0 : v);
  return out;
}

const UNIT_TITLE: Record<string, string> = { cores: "cores", bytes: "", count: "" };

// ---------------------------------------------------------------- time

const MIN = 60;
const HOUR = 3600;
const DAY = 86400;
const TIME_STEPS = [MIN, 5 * MIN, 10 * MIN, 15 * MIN, 30 * MIN, HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY, 7 * DAY];

function timeTicks(from: number, to: number, maxTicks: number): { ticks: number[]; step: number } {
  const span = Math.max(1, to - from);
  const step = TIME_STEPS.find((s) => span / s <= maxTicks) ?? TIME_STEPS[TIME_STEPS.length - 1];
  // Align to local wall-clock boundaries.
  const offset = -new Date(from * 1000).getTimezoneOffset() * 60;
  const first = Math.ceil((from + offset) / step) * step - offset;
  const ticks: number[] = [];
  for (let t = first; t <= to; t += step) ticks.push(t);
  return { ticks, step };
}

function timeLabel(t: number, step: number): string {
  const d = new Date(t * 1000);
  if (step >= DAY) return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  const hm = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
  if (step >= 6 * HOUR && d.getHours() === 0 && d.getMinutes() === 0) return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  return hm;
}

function tooltipTime(t: number, spanSeconds: number): string {
  const d = new Date(t * 1000);
  return spanSeconds > DAY
    ? d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false })
    : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
}

/** Index of the element of sorted `arr` closest to `x`. */
function nearest(arr: number[], x: number): number {
  let lo = 0;
  let hi = arr.length - 1;
  while (hi - lo > 1) {
    const mid = (lo + hi) >> 1;
    if (arr[mid] < x) lo = mid;
    else hi = mid;
  }
  return Math.abs(arr[lo] - x) <= Math.abs(arr[hi] - x) ? lo : hi;
}

/** Median spacing of a line's timestamps (its scrape step). */
function medianStep(points: [number, number][]): number {
  if (points.length < 2) return 0;
  const diffs = points.slice(1).map((p, i) => p[0] - points[i][0]).sort((a, b) => a - b);
  return diffs[diffs.length >> 1];
}

// ---------------------------------------------------------------- component

const HEIGHT = 190;
const PAD = { top: 10, right: 12, bottom: 24 };

function useWidth<T extends HTMLElement>(): [RefObject<T>, number] {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(el.clientWidth);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => setWidth(Math.round(entries[0].contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width];
}

export type ChartProps = {
  title: string;
  unit: ChartUnit;
  lines: ChartLine[];
  /** unix seconds of the requested window; defaults to the data's extent */
  from?: number;
  to?: number;
  loading?: boolean;
  error?: Error | string;
  onRetry?: () => void;
  height?: number;
  /** extra text under the title, e.g. "per container" */
  note?: string;
};

export function Chart({ title, unit, lines, from, to, loading, error, onRetry, height = HEIGHT, note }: ChartProps) {
  const [wrapRef, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const titleId = useId();

  const times = useMemo(() => [...new Set(lines.flatMap((l) => l.points.map((p) => p[0])))].sort((a, b) => a - b), [lines]);
  const valueMaps = useMemo(() => lines.map((l) => ({ map: new Map(l.points), step: medianStep(l.points) })), [lines]);

  const hasData = times.length > 0;
  const x0 = from ?? times[0] ?? 0;
  const x1 = to ?? times[times.length - 1] ?? 1;

  const y = useMemo(() => {
    let lo = Infinity;
    let hi = -Infinity;
    for (const l of lines)
      for (const [, v] of l.points) {
        if (v < lo) lo = v;
        if (v > hi) hi = v;
      }
    return yAxis(Number.isFinite(lo) ? lo : 0, Number.isFinite(hi) ? hi : 1, unit);
  }, [lines, unit]);

  const yLabelWidth = Math.max(28, Math.max(...y.ticks.map((t) => y.label(t).length)) * 6.6 + 10);
  const plotW = Math.max(40, width - yLabelWidth - PAD.right);
  const plotH = height - PAD.top - PAD.bottom;
  const sx = (t: number) => yLabelWidth + ((t - x0) / Math.max(1, x1 - x0)) * plotW;
  const sy = (v: number) => PAD.top + plotH - ((v - y.min) / Math.max(1e-12, y.max - y.min)) * plotH;
  const xt = timeTicks(x0, x1, Math.max(2, Math.floor(plotW / 84)));

  const paths = useMemo(
    () =>
      lines.map((l) => {
        const step = medianStep(l.points);
        let d = "";
        let prev: number | null = null;
        for (const [t, v] of l.points) {
          const gap = prev !== null && step > 0 && t - prev > step * 2.5;
          d += `${d === "" || gap ? "M" : "L"}${sx(t).toFixed(1)},${sy(v).toFixed(1)}`;
          prev = t;
        }
        return d;
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [lines, width, height, x0, x1, y],
  );

  /** Value of line i at time t: exact, else the nearest point within half a step. */
  const valueAt = (i: number, t: number): number | undefined => {
    const { map, step } = valueMaps[i];
    const exact = map.get(t);
    if (exact !== undefined) return exact;
    const pts = lines[i].points;
    if (pts.length === 0) return undefined;
    const j = nearest(
      pts.map((p) => p[0]),
      t,
    );
    return Math.abs(pts[j][0] - t) <= Math.max(step / 2, 1) ? pts[j][1] : undefined;
  };

  const summary = useMemo(() => {
    if (!hasData) return `${title}: no data in this range.`;
    const parts = lines.map((l) => {
      const vals = l.points.map((p) => p[1]);
      const last = vals[vals.length - 1];
      return `${l.label} latest ${formatValue(last, unit)} (range ${formatValue(Math.min(...vals), unit)} to ${formatValue(Math.max(...vals), unit)})`;
    });
    return `${title} chart, ${tooltipTime(x0, x1 - x0)} to ${tooltipTime(x1, x1 - x0)}: ${parts.join("; ")}.`;
  }, [hasData, lines, title, unit, x0, x1]);

  const onPointer = (e: PointerEvent<SVGRectElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const px = e.clientX - rect.left;
    const t = x0 + (px / Math.max(1, rect.width)) * (x1 - x0);
    setHover(nearest(times, t));
  };

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!hasData) return;
    if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
      e.preventDefault();
      const dir = e.key === "ArrowLeft" ? -1 : 1;
      setHover((h) => Math.min(times.length - 1, Math.max(0, h === null ? (dir < 0 ? times.length - 1 : 0) : h + dir)));
    } else if (e.key === "Home") {
      e.preventDefault();
      setHover(0);
    } else if (e.key === "End") {
      e.preventDefault();
      setHover(times.length - 1);
    } else if (e.key === "Escape") setHover(null);
  };

  const hoverT = hover !== null && hover < times.length ? times[hover] : null;
  const hx = hoverT !== null ? sx(hoverT) : 0;
  const tooltipRows =
    hoverT === null
      ? []
      : lines
          .map((l, i) => ({ l, v: valueAt(i, hoverT) }))
          .sort((a, b) => (b.v ?? -Infinity) - (a.v ?? -Infinity));
  const tipLeft = hx > width * 0.6;

  const unitTitle = UNIT_TITLE[unit] ?? "";

  return (
    <figure className="m-0 flex min-w-0 flex-col gap-1.5" aria-labelledby={titleId}>
      <figcaption className="flex flex-wrap items-baseline justify-between gap-x-2">
        <span id={titleId} className="text-[13px] font-semibold">
          {title}
          {unitTitle && <span className="font-normal text-muted"> · {unitTitle}</span>}
        </span>
        {note && <span className="text-xs text-muted">{note}</span>}
      </figcaption>
      <div ref={wrapRef} className="relative w-full" style={{ height }}>
        {error ? (
          <div role="alert" className="flex h-full flex-col items-center justify-center gap-2 rounded-btn border border-dashed border-red-200 bg-crit-bg/30 px-3 text-center text-[13px] text-crit-fg">
            <span>Couldn&apos;t load this chart: {typeof error === "string" ? error : error.message}</span>
            {onRetry && (
              <button type="button" className="btn min-h-[30px] px-2.5 text-xs" onClick={onRetry}>
                Retry
              </button>
            )}
          </div>
        ) : loading && !hasData ? (
          <div role="status" aria-label={`Loading ${title}`} className="h-full w-full animate-pulse rounded-btn bg-slate-100">
            <svg width="100%" height="100%" aria-hidden="true" preserveAspectRatio="none" viewBox="0 0 100 40">
              <path d="M0,30 L15,26 L30,28 L45,18 L60,22 L75,12 L100,16" fill="none" stroke="#E2E8F0" strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
            </svg>
          </div>
        ) : !hasData ? (
          <div className="flex h-full items-center justify-center rounded-btn border border-dashed border-line text-[13px] text-muted">No data in this range</div>
        ) : width > 0 ? (
          <div
            tabIndex={0}
            onKeyDown={onKey}
            onBlur={() => setHover(null)}
            aria-describedby={`${titleId}-hint`}
            className={`h-full rounded-btn focus-visible:outline-offset-4 ${loading ? "opacity-60" : ""}`}
          >
            <span id={`${titleId}-hint`} className="sr-only">
              Use the left and right arrow keys to read values over time.
            </span>
            <svg width={width} height={height} role="img" aria-label={summary} className="block overflow-visible">
              {/* y grid + labels */}
              {y.ticks.map((t) => (
                <g key={t}>
                  <line x1={yLabelWidth} x2={yLabelWidth + plotW} y1={sy(t)} y2={sy(t)} stroke={t === 0 ? "#CBD5E1" : "#F1F5F9"} strokeWidth={1} />
                  <text x={yLabelWidth - 6} y={sy(t)} dy="0.32em" textAnchor="end" fontSize={11} fill="#64748B" style={{ fontVariantNumeric: "tabular-nums" }}>
                    {y.label(t)}
                  </text>
                </g>
              ))}
              {/* x labels */}
              {xt.ticks.map((t) => (
                <text key={t} x={sx(t)} y={height - 6} textAnchor="middle" fontSize={11} fill="#64748B">
                  {timeLabel(t, xt.step)}
                </text>
              ))}
              {/* lines */}
              {lines.map((l, i) => (
                <path
                  key={l.key}
                  d={paths[i]}
                  fill="none"
                  stroke={l.color}
                  strokeWidth={l.dashed ? 1.5 : 2}
                  strokeDasharray={l.dashed ? "5 4" : undefined}
                  strokeLinejoin="round"
                  strokeLinecap="round"
                />
              ))}
              {/* single points would be invisible as paths */}
              {lines.map((l) => (l.points.length === 1 ? <circle key={`${l.key}-pt`} cx={sx(l.points[0][0])} cy={sy(l.points[0][1])} r={3} fill={l.color} /> : null))}
              {/* crosshair */}
              {hoverT !== null && (
                <g pointerEvents="none">
                  <line x1={hx} x2={hx} y1={PAD.top} y2={PAD.top + plotH} stroke="#94A3B8" strokeWidth={1} />
                  {lines.map((l, i) => {
                    const v = valueAt(i, hoverT);
                    return v === undefined ? null : <circle key={l.key} cx={hx} cy={sy(v)} r={4} fill={l.color} stroke="#fff" strokeWidth={2} />;
                  })}
                </g>
              )}
              <rect
                x={yLabelWidth}
                y={PAD.top}
                width={plotW}
                height={plotH}
                fill="transparent"
                onPointerMove={onPointer}
                onPointerDown={onPointer}
                onPointerLeave={() => setHover(null)}
              />
            </svg>
            {hoverT !== null && (
              <div
                className="pointer-events-none absolute z-10 min-w-[150px] max-w-[280px] rounded-btn border border-line bg-white px-2.5 py-2 text-xs shadow-md"
                style={{ top: PAD.top, ...(tipLeft ? { right: width - hx + 10 } : { left: hx + 10 }) }}
              >
                <div className="mb-1 font-medium text-muted">{tooltipTime(hoverT, x1 - x0)}</div>
                <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
                  {tooltipRows.map(({ l, v }) => (
                    <li key={l.key} className="flex items-center gap-2">
                      <Swatch color={l.color} dashed={l.dashed} />
                      <span className="min-w-0 flex-1 truncate">{l.label}</span>
                      <span className="font-mono tabular-nums text-ink">{v === undefined ? "—" : formatValue(v, unit)}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        ) : null}
      </div>
      {lines.length > 1 && hasData && !error && (
        <ul className="m-0 flex list-none flex-wrap gap-x-3 gap-y-1 p-0 text-xs text-slate-700" aria-label={`${title} legend`}>
          {lines.map((l) => (
            <li key={l.key} className="inline-flex items-center gap-1.5">
              <Swatch color={l.color} dashed={l.dashed} />
              {l.label}
            </li>
          ))}
        </ul>
      )}
    </figure>
  );
}

function Swatch({ color, dashed }: { color: string; dashed: boolean }) {
  return (
    <svg width={16} height={8} aria-hidden="true" className="flex-none">
      <line x1={1} x2={15} y1={4} y2={4} stroke={color} strokeWidth={dashed ? 1.5 : 2.5} strokeDasharray={dashed ? "4 3" : undefined} strokeLinecap="round" />
    </svg>
  );
}
