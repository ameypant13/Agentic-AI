import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { ResourceTable } from "../api/types";
import { useNow, useRefreshedAt } from "./Cluster";
import { CheckIcon, CopyIcon, RefreshIcon, Spinner } from "./Icons";
import { Pill, type Tone } from "./Pill";
import { Empty } from "./States";

/** "Live · refreshed 4s ago" + refresh button, as on the pods tab. */
export function RefreshStatus({ data, loading, onRefresh, label }: { data: unknown; loading: boolean; onRefresh: () => void; label: string }) {
  const refreshedAt = useRefreshedAt(data);
  const now = useNow(1000);
  const ago = refreshedAt === null ? null : Math.max(0, Math.round((now - refreshedAt) / 1000));
  return (
    <>
      {loading && data ? <Spinner /> : <span aria-hidden="true" className="inline-block h-2 w-2 rounded-full bg-[#059669]" />}
      <span aria-live="off">{ago === null ? "Live" : `Live · refreshed ${ago}s ago`}</span>
      <button type="button" className="btn min-h-[30px] px-2" onClick={onRefresh} aria-label={`Refresh ${label}`} disabled={loading}>
        <RefreshIcon size={14} />
      </button>
    </>
  );
}

/** Label/value pair of a metadata grid. */
export function Field({ label, children, mono = true }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className={`m-0 break-words text-[13px] ${mono ? "font-mono" : ""}`}>{children}</dd>
    </div>
  );
}

/** Generic status word → pill tone (phases, helm statuses, conditions). */
export function statusTone(v: string): Tone {
  const s = v.toLowerCase();
  if (/fail|error|lost|crashloop|backoff|invalid|unknown|notready|evicted|oomkilled/.test(s)) return "critical";
  if (/pending|progress|terminat|released|waiting|creating|suspend|superseded/.test(s)) return "warning";
  if (/running|active|bound|available|complete|succeeded|ready|true|healthy|deployed|established/.test(s)) return "success";
  return "neutral";
}

// ---------------------------------------------------------------- YAML

function highlight(line: string, needle: string): ReactNode {
  if (!needle) return line;
  const lower = line.toLowerCase();
  const parts: ReactNode[] = [];
  let from = 0;
  for (let i = lower.indexOf(needle); i !== -1; i = lower.indexOf(needle, i + needle.length)) {
    if (i > from) parts.push(line.slice(from, i));
    parts.push(
      <mark key={i} className="rounded-sm bg-[#FDE68A] text-ink">
        {line.slice(i, i + needle.length)}
      </mark>,
    );
    from = i + needle.length;
  }
  if (from < line.length) parts.push(line.slice(from));
  return parts.map((p, i) => <Fragment key={i}>{p}</Fragment>);
}

/** Read-only YAML with line numbers, copy, and search that highlights and steps through matches. */
export function YamlViewer({ yaml, title = "YAML", note = "read-only · secret values are hidden" }: { yaml: string; title?: string; note?: string }) {
  const [copied, setCopied] = useState<"ok" | "fail" | null>(null);
  const [query, setQuery] = useState("");
  const [cur, setCur] = useState(0);
  const lineRefs = useRef<(HTMLDivElement | null)[]>([]);
  const lines = useMemo(() => yaml.replace(/\n$/, "").split("\n"), [yaml]);
  const needle = query.toLowerCase();
  const hits = useMemo(() => (needle ? lines.flatMap((l, i) => (l.toLowerCase().includes(needle) ? [i] : [])) : []), [lines, needle]);

  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(null), 2000);
    return () => window.clearTimeout(t);
  }, [copied]);
  useEffect(() => setCur(0), [needle]);
  useEffect(() => {
    if (hits.length === 0) return;
    lineRefs.current[hits[Math.min(cur, hits.length - 1)]]?.scrollIntoView({ block: "nearest" });
  }, [cur, hits]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(yaml);
      setCopied("ok");
    } catch {
      setCopied("fail");
    }
  };
  const step = (d: number) => hits.length > 0 && setCur((c) => (c + d + hits.length) % hits.length);
  const width = String(lines.length).length;
  const active = hits.length > 0 ? hits[Math.min(cur, hits.length - 1)] : -1;

  return (
    <section className="card p-4" aria-labelledby="yaml-head">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="yaml-head" className="h2">
          {title}
        </h2>
        <div className="flex flex-wrap items-center gap-2">
          <Pill>{note}</Pill>
          <input
            type="search"
            className="input min-h-[32px] w-[200px] text-[13px]"
            placeholder="Search YAML  ( / )"
            aria-label="Search YAML"
            data-slash-filter="page"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                step(e.shiftKey ? -1 : 1);
              }
            }}
          />
          {needle && (
            <span className="text-xs tabular-nums text-muted" aria-live="polite">
              {hits.length === 0 ? "no matches" : `${Math.min(cur, hits.length - 1) + 1} / ${hits.length} lines`}
            </span>
          )}
          {hits.length > 1 && (
            <>
              <button type="button" className="btn min-h-[30px] px-2 text-xs" onClick={() => step(-1)} aria-label="Previous match">
                ↑
              </button>
              <button type="button" className="btn min-h-[30px] px-2 text-xs" onClick={() => step(1)} aria-label="Next match">
                ↓
              </button>
            </>
          )}
          <button type="button" className="btn" onClick={copy} disabled={!yaml}>
            {copied === "ok" ? <CheckIcon size={14} /> : <CopyIcon size={14} />}
            {copied === "ok" ? "Copied" : copied === "fail" ? "Copy failed" : "Copy"}
          </button>
        </div>
      </div>
      {yaml ? (
        <div
          className="mt-3 max-h-[720px] overflow-auto rounded-btn border border-line bg-slate-50 py-2 font-mono text-xs leading-relaxed"
          role="region"
          aria-label={`${title} (read-only)`}
          tabIndex={0}
        >
          {lines.map((l, i) => (
            <div
              key={i}
              ref={(el) => {
                lineRefs.current[i] = el;
              }}
              className={`flex min-w-max ${i === active ? "bg-[#FEF3C7]" : ""}`}
            >
              <span aria-hidden="true" className="sticky left-0 select-none border-r border-line bg-slate-50 pl-3 pr-2 text-right text-slate-400" style={{ minWidth: `${width + 2.5}ch` }}>
                {i + 1}
              </span>
              <span className="whitespace-pre pl-3 pr-4">{highlight(l, needle) || " "}</span>
            </div>
          ))}
        </div>
      ) : (
        <Empty title="No YAML returned" />
      )}
    </section>
  );
}

// ---------------------------------------------------------------- workload readiness

export type Readiness = { state: "ok" | "progress" | "bad" | "suspended"; text: string };

/**
 * Readiness of one row of a workload table, read from the API server's own columns:
 * Ready "a/b" (deployments, statefulsets), Ready vs Desired (replicasets, daemonsets),
 * Status / Completions (jobs), Suspend (cronjobs). null when the table has none of these.
 */
export function rowReadiness(table: ResourceTable, cells: string[]): Readiness | null {
  const col = (name: string) => {
    const i = table.columns.findIndex((c) => c.name.toLowerCase() === name.toLowerCase());
    return i === -1 ? undefined : cells[i];
  };
  const ready = col("Ready");
  const frac = ready !== undefined ? /^(\d+)\/(\d+)$/.exec(ready.trim()) : null;
  if (frac) {
    const [a, b] = [Number(frac[1]), Number(frac[2])];
    return a >= b ? { state: "ok", text: ready! } : { state: a === 0 && b > 0 ? "bad" : "progress", text: `${ready} ready` };
  }
  const desired = col("Desired");
  if (ready !== undefined && desired !== undefined && /^\d+$/.test(ready) && /^\d+$/.test(desired)) {
    const [a, b] = [Number(ready), Number(desired)];
    return a >= b ? { state: "ok", text: `${a}/${b}` } : { state: a === 0 ? "bad" : "progress", text: `${a}/${b} ready` };
  }
  const status = col("Status");
  const completions = col("Completions");
  if (status !== undefined && completions !== undefined) {
    if (/fail/i.test(status)) return { state: "bad", text: status };
    if (/complete/i.test(status)) return { state: "ok", text: status };
    return { state: "progress", text: `${status} ${completions}`.trim() };
  }
  if (completions !== undefined) {
    const m = /^(\d+)\/(\d+)/.exec(completions);
    if (m) return Number(m[1]) >= Number(m[2]) ? { state: "ok", text: completions } : { state: "progress", text: `${completions} complete` };
  }
  const suspend = col("Suspend");
  if (suspend !== undefined) return /true/i.test(suspend) ? { state: "suspended", text: "suspended" } : { state: "ok", text: "scheduled" };
  return null;
}
