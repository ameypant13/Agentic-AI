import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api/client";
import { LogSeverityPill, sevRank } from "../components/LogParser";
import { Pill } from "../components/Pill";
import { Empty, ErrorCard, Loading } from "../components/States";
import { useApi } from "../lib/useApi";

const VALUE_LABEL: Record<string, string> = { percent: "percent", count: "count", seconds: "seconds", rate: "per second", none: "—" };

/** Read-only list of the metrics parser's PromQL rules (GET /metric-rules). `focus` expands one rule. */
export function MetricRulesView({ focus }: { focus: string }) {
  const rules = useApi(() => api.metricRules());
  const [q, setQ] = useState("");
  const [sev, setSev] = useState("");
  const [open, setOpen] = useState<Set<string>>(() => new Set(focus ? [focus] : []));
  const rowRefs = useRef(new Map<string, HTMLTableRowElement>());
  const scrolled = useRef(false);

  const all = useMemo(() => [...(rules.data ?? [])].sort((a, b) => sevRank(a.severity) - sevRank(b.severity) || a.id.localeCompare(b.id)), [rules.data]);
  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return all.filter((r) => (!sev || r.severity === sev) && (!needle || [r.id, r.title, r.entity, r.meaning, r.fix, r.expr].some((f) => f.toLowerCase().includes(needle))));
  }, [all, q, sev]);

  useEffect(() => {
    if (!focus || scrolled.current || !rules.data) return;
    const el = rowRefs.current.get(focus);
    if (el) {
      scrolled.current = true;
      el.scrollIntoView({ block: "center", behavior: "smooth" });
    }
  }, [focus, rules.data]);

  const toggle = (id: string) =>
    setOpen((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="search"
          className="input min-h-[36px] w-[240px] text-[13px]"
          aria-label="Search metric rules"
          placeholder="Search id, title, entity, PromQL"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <div role="group" aria-label="Severity" className="inline-flex">
          {(
            [
              ["", "All"],
              ["error", "Error"],
              ["warning", "Warning"],
              ["info", "Info"],
            ] as const
          ).map(([value, label], i, arr) => {
            const on = sev === value;
            return (
              <button
                key={label}
                type="button"
                aria-pressed={on}
                onClick={() => setSev(value)}
                className={`btn rounded-none ${i === 0 ? "rounded-l-btn" : "-ml-px"} ${i === arr.length - 1 ? "rounded-r-btn" : ""} ${on ? "relative z-10 border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
              >
                {label}
              </button>
            );
          })}
        </div>
        <Pill>read-only · internal/metricrules/rules.yaml</Pill>
        <span className="ml-auto text-xs text-muted">{rules.data && (rows.length === all.length ? `${all.length} rules` : `${rows.length} of ${all.length} rules`)}</span>
      </div>

      <section className="card" aria-label="Metric rules">
        {rules.error && !rules.data ? (
          <div className="p-4">
            <ErrorCard title="Couldn't load metric rules" error={rules.error} onRetry={rules.reload} />
          </div>
        ) : !rules.data ? (
          <Loading label="Loading metric rules…" />
        ) : rows.length === 0 ? (
          <Empty title={all.length === 0 ? "No metric rules" : "No rules match"}>{all.length === 0 ? "The metrics parser has no rules." : "Try a different search or severity."}</Empty>
        ) : (
          <div className="overflow-x-auto">
            <table className="table min-w-[760px]">
              <thead>
                <tr>
                  <th scope="col">Rule</th>
                  <th scope="col">Severity</th>
                  <th scope="col">Entity</th>
                  <th scope="col">Value</th>
                  <th scope="col">Meaning · fix</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const isOpen = open.has(r.id);
                  const detailId = `mr-detail-${r.id}`;
                  return (
                    <Fragment key={r.id}>
                      <tr
                        ref={(el) => {
                          if (el) rowRefs.current.set(r.id, el);
                          else rowRefs.current.delete(r.id);
                        }}
                        onClick={() => toggle(r.id)}
                        className={`cursor-pointer hover:bg-slate-50 ${r.id === focus ? "bg-violet-tint/50" : ""}`}
                      >
                        <td className="min-w-[200px]">
                          <button
                            type="button"
                            className="link-btn font-mono text-xs font-medium"
                            aria-expanded={isOpen}
                            aria-controls={detailId}
                            onClick={(e) => {
                              e.stopPropagation();
                              toggle(r.id);
                            }}
                          >
                            <span aria-hidden="true" className="mr-1 inline-block w-2 text-muted">
                              {isOpen ? "▾" : "▸"}
                            </span>
                            {r.id}
                          </button>
                          <div className="mt-0.5 pl-3 text-[13px] text-ink">{r.title}</div>
                        </td>
                        <td>
                          <LogSeverityPill severity={r.severity} />
                        </td>
                        <td>
                          <Pill>{r.entity}</Pill>
                        </td>
                        <td className="whitespace-nowrap text-muted">{VALUE_LABEL[r.value] ?? r.value}</td>
                        <td className="min-w-[300px] max-w-[460px]">
                          {r.meaning}
                          {r.fix && <div className="mt-0.5 text-xs text-muted">{r.fix}</div>}
                        </td>
                      </tr>
                      {isOpen && (
                        <tr id={detailId} className="bg-slate-50">
                          <td colSpan={5}>
                            <div className="mb-1 text-xs font-semibold text-muted">PromQL · evaluated every 60 s; each returned series is one issue</div>
                            <pre className="m-0 max-h-[360px] overflow-auto whitespace-pre rounded-btn bg-ink p-3 font-mono text-xs leading-relaxed text-slate-200">{r.expr}</pre>
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
