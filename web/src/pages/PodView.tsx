import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { Container, PodDetail } from "../api/types";
import { Dash, EventsTable, clusterUrl, podStatusTone, podUrl, useAutoRefresh, useNow } from "../components/Cluster";
import { ISSUES_POLL_MS, MetricIssueList, sortIssues } from "../components/Issues";
import { NotConnected, PodCharts } from "../components/Metrics";
import { LOG_SEV_BORDER, logRuleUrl, logSevTone, plural, sortHits, worstSeverity } from "../components/LogParser";
import { ArrowLeftIcon, CheckIcon, CopyIcon, RefreshIcon, Spinner } from "../components/Icons";
import { Page } from "../components/Layout";
import { Pill } from "../components/Pill";
import { Empty, ErrorCard, Loading } from "../components/States";
import { age, formatDateTime } from "../lib/format";
import { useApi } from "../lib/useApi";

const TABS = [
  ["overview", "Overview"],
  ["logs", "Logs"],
  ["manifest", "Manifest"],
  ["events", "Events"],
] as const;
type Tab = (typeof TABS)[number][0];

const SINCE_OPTIONS = [
  [15, "15 min"],
  [60, "1 h"],
  [360, "6 h"],
  [1440, "24 h"],
] as const;
const LOG_TAIL = 2000;

/** Window and tail of the overview's log-parser card. */
const PARSER_SINCE_MIN = 360;

const ERROR_RE = /ERROR|Exception|OutOfMemory|FATAL|panic/;
const WARN_RE = /WARN/;

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className="m-0 break-words font-mono text-[13px]">{children}</dd>
    </div>
  );
}

function LogParserCard({ id, ns, name, container }: { id: string; ns: string; name: string; container: string | undefined }) {
  const logs = useApi(
    () => (container ? api.podLogs(id, ns, name, { container, sinceMinutes: PARSER_SINCE_MIN, tail: LOG_TAIL }) : Promise.resolve(null)),
    [id, ns, name, container],
  );
  const hits = useMemo(() => sortHits(logs.data?.parser?.hits ?? []), [logs.data]);
  const summary = !logs.data ? "" : hits.length === 0 ? "clean" : `${plural(hits.length, "known pattern")}`;
  return (
    <section className="card p-4" aria-labelledby="pod-parser">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="pod-parser" className="h2">
          Log parser{summary && ` · ${summary}`}
        </h2>
        <div className="flex items-center gap-2">
          {logs.loading && logs.data && <Spinner />}
          <Pill>rules, not AI</Pill>
        </div>
      </div>
      <div className="mt-2.5 text-[13px]">
        {!container ? (
          <span className="text-muted">This pod has no containers to read logs from.</span>
        ) : logs.error ? (
          <ErrorCard title="Couldn't run the log parser" error={logs.error} onRetry={logs.reload} />
        ) : !logs.data ? (
          <Loading label="Reading the last 6 h of logs…" />
        ) : !logs.data.parser ? (
          <span className="text-muted">The server did not return a parser result for these logs.</span>
        ) : hits.length === 0 ? (
          <span className="text-muted">No known error patterns in the last 6 h.</span>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
            {hits.map((h) => (
              <li key={h.ruleId} className="flex items-baseline gap-2.5">
                <Pill tone={logSevTone(h.severity)} className="flex-none tabular-nums">
                  ×{h.count}
                </Pill>
                <div className="min-w-0">
                  <span className="font-semibold">{h.title}</span> · rule{" "}
                  <Link to={logRuleUrl(h.ruleId)} className="font-mono text-xs">
                    {h.ruleId}
                  </Link>{" "}
                  — {h.meaning}
                  {h.fix && <div className="mt-0.5 text-xs text-muted">Fix: {h.fix}</div>}
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
      {container && logs.data && (
        <div className="mt-3 flex flex-wrap items-center gap-3 text-xs text-muted">
          <span>
            container {logs.data.container || container} · last 6 h{logs.data.truncated && ` · last ${LOG_TAIL} lines`}
          </span>
          {hits.length > 0 && (
            <Link to={`${podUrl(id, ns, name, "logs")}?only=hits`}>Show the matching lines</Link>
          )}
        </div>
      )}
    </section>
  );
}

/** This pod's metric issues: the cluster's issues filtered by namespace + pod. */
function PodMetricIssues({ clusterId, ns, name, now }: { clusterId: string; ns: string; name: string; now: number }) {
  const issues = useApi(() => api.clusterIssues(clusterId), [clusterId]);
  useAutoRefresh(issues.reload, ISSUES_POLL_MS);
  const mine = useMemo(() => (issues.data?.metrics ?? []).filter((i) => i.namespace === ns && i.pod === name), [issues.data, ns, name]);
  const active = sortIssues(mine.filter((i) => i.active));
  const resolved = mine.filter((i) => !i.active);
  const connected = !!issues.data?.grafana;
  return (
    <section className="card p-4" aria-labelledby="pod-metric-issues">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="pod-metric-issues" className="h2">
          Metric issues{issues.data && connected && ` · ${active.length === 0 ? "none active" : `${active.length} active`}`}
        </h2>
        <div className="flex items-center gap-2 text-xs text-muted">
          {issues.loading && issues.data && <Spinner />}
          {issues.data?.evaluatedAt && <span title={formatDateTime(issues.data.evaluatedAt)}>evaluated {age(issues.data.evaluatedAt, now)} ago</span>}
          <Pill>rules, not AI</Pill>
        </div>
      </div>
      <div className="mt-2 text-[13px]">
        {issues.error && !issues.data ? (
          <ErrorCard title="Couldn't load metric issues" error={issues.error} onRetry={issues.reload} />
        ) : !issues.data ? (
          <Loading label="Loading metric issues…" />
        ) : !connected ? (
          <NotConnected />
        ) : active.length === 0 && resolved.length === 0 ? (
          <span className="text-muted">The metrics parser sees no problem with this pod.</span>
        ) : (
          <>
            {active.length > 0 && <MetricIssueList clusterId={clusterId} issues={active} now={now} showEntity={false} />}
            {resolved.length > 0 && (
              <details className={active.length > 0 ? "mt-1 border-t border-line pt-2" : ""}>
                <summary className="cursor-pointer text-[13px] font-medium text-muted">Resolved in the last 24 h ({resolved.length})</summary>
                <MetricIssueList clusterId={clusterId} issues={resolved} now={now} showEntity={false} resolved />
              </details>
            )}
          </>
        )}
      </div>
    </section>
  );
}

function OverviewTab({ pod, now, clusterId }: { pod: PodDetail; now: number; clusterId: string }) {
  const requests = [
    `CPU ${pod.cpuRequest || "—"}`,
    `memory ${pod.memRequest || "—"} / ${pod.memLimit || "no limit"}`,
  ].join(" · ");
  return (
    <div className="flex flex-col gap-4">
      <section className="card p-4" aria-label="Details">
        <dl className="m-0 grid grid-cols-[repeat(auto-fit,minmax(min(190px,100%),1fr))] gap-x-5 gap-y-3">
          <Field label="Namespace">{pod.namespace}</Field>
          <Field label="Node">{pod.node || <span className="font-sans text-muted">not scheduled</span>}</Field>
          <Field label="Owner">{pod.owner || <Dash />}</Field>
          <Field label="Images">
            {pod.images.length === 0 ? <Dash /> : pod.images.map((i) => <div key={i}>{i}</div>)}
          </Field>
          <Field label="Requests / limits">{requests}</Field>
          <Field label="Created">
            {formatDateTime(pod.createdAt)} <span className="font-sans text-muted">({age(pod.createdAt, now)} ago)</span>
          </Field>
        </dl>
      </section>

      <PodMetricIssues clusterId={clusterId} ns={pod.namespace} name={pod.name} now={now} />

      <PodCharts id={clusterId} ns={pod.namespace} pod={pod.name} />

      <LogParserCard id={clusterId} ns={pod.namespace} name={pod.name} container={pod.containers[0]?.name} />

      <section className="card" aria-labelledby="pod-containers">
        <div className="card-head">
          <h2 id="pod-containers" className="h2">
            Containers
          </h2>
        </div>
        {pod.containers.length === 0 ? (
          <Empty title="No containers reported" />
        ) : (
          <div className="overflow-x-auto">
            <ContainersTable containers={pod.containers} />
          </div>
        )}
      </section>

      <section className="card" aria-labelledby="pod-conditions">
        <div className="card-head">
          <h2 id="pod-conditions" className="h2">
            Conditions
          </h2>
        </div>
        {pod.conditions.length === 0 ? (
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
                {pod.conditions.map((c) => (
                  <tr key={c.type}>
                    <td className="whitespace-nowrap font-medium">{c.type}</td>
                    <td>
                      <Pill tone={c.status === "True" ? "success" : c.status === "False" ? "warning" : "neutral"}>{c.status}</Pill>
                    </td>
                    <td className="whitespace-nowrap">{c.reason || <Dash />}</td>
                    <td className="min-w-[280px] break-words">{c.message || <Dash />}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function ContainersTable({ containers }: { containers: Container[] }) {
  return (
    <table className="table min-w-[860px]">
      <thead>
        <tr>
          <th scope="col">Container</th>
          <th scope="col">Image</th>
          <th scope="col">State</th>
          <th scope="col">Ready</th>
          <th scope="col">Restarts</th>
          <th scope="col">Last terminated</th>
        </tr>
      </thead>
      <tbody>
        {containers.map((c) => (
          <tr key={c.name}>
            <td className="whitespace-nowrap font-mono text-xs font-medium">{c.name}</td>
            <td className="font-mono text-xs">{c.image}</td>
            <td className={c.state.startsWith("Running") ? "" : "font-medium text-crit-fg"}>{c.state || <Dash />}</td>
            <td>{c.ready ? <Pill tone="success">Ready</Pill> : <Pill tone="critical">Not ready</Pill>}</td>
            <td className={`tabular-nums ${c.restarts > 3 ? "font-bold text-crit-fg" : ""}`}>{c.restarts}</td>
            <td>{c.lastTerminated || <Dash />}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function LogsTab({ id, ns, name, containers }: { id: string; ns: string; name: string; containers: Container[] }) {
  const [container, setContainer] = useState(containers[0]?.name ?? "");
  const [previous, setPrevious] = useState(false);
  const [since, setSince] = useState<number>(360);
  const [search, setSearch] = useState("");
  const [wrap, setWrap] = useState(false);
  const [params, setParams] = useSearchParams();
  const onlyHits = params.get("only") === "hits";
  const [ruleFilter, setRuleFilter] = useState<string | null>(null);
  const navigate = useNavigate();

  const setOnlyHits = (on: boolean) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (on) next.set("only", "hits");
        else next.delete("only");
        return next;
      },
      { replace: true },
    );

  const logs = useApi(
    () => api.podLogs(id, ns, name, { container: container || undefined, sinceMinutes: since, tail: LOG_TAIL, previous }),
    [id, ns, name, container, previous, since],
  );

  const parser = logs.data?.parser;
  const hits = useMemo(() => sortHits(parser?.hits ?? []), [parser]);
  const sevByRule = useMemo(() => new Map(hits.map((h) => [h.ruleId, h.severity])), [hits]);
  const rulesByLine = useMemo(() => new Map((parser?.matches ?? []).map((m) => [m.line, m.rules ?? []])), [parser]);
  const matchedLines = rulesByLine.size;

  // A rule chip that no longer has hits (after a refresh) stops filtering.
  const activeRule = ruleFilter && sevByRule.has(ruleFilter) ? ruleFilter : null;

  const lines = useMemo(() => {
    const all = (logs.data?.lines ?? []).map((text, idx) => ({ text, idx, rules: rulesByLine.get(idx) ?? [] }));
    const needle = search.toLowerCase();
    return all.filter(
      (l) =>
        (!onlyHits || l.rules.length > 0) &&
        (!activeRule || l.rules.includes(activeRule)) &&
        (!needle || l.text.toLowerCase().includes(needle)),
    );
  }, [logs.data, search, onlyHits, activeRule, rulesByLine]);
  const total = logs.data?.lines.length ?? 0;
  const filtered = !!search || onlyHits || !!activeRule;

  return (
    <section className="card flex flex-col gap-3 p-4" aria-label="Logs">
      <div className="flex flex-wrap items-center gap-2">
        {containers.length > 0 && (
          <>
            <label htmlFor="log-container" className="text-[13px] text-muted">
              Container
            </label>
            <select id="log-container" className="select min-h-[36px]" value={container} onChange={(e) => setContainer(e.target.value)}>
              {containers.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name}
                </option>
              ))}
            </select>
          </>
        )}
        <label htmlFor="log-since" className="text-[13px] text-muted">
          Since
        </label>
        <select id="log-since" className="select min-h-[36px]" value={since} onChange={(e) => setSince(Number(e.target.value))}>
          {SINCE_OPTIONS.map(([m, label]) => (
            <option key={m} value={m}>
              {label}
            </option>
          ))}
        </select>
        <button
          type="button"
          aria-pressed={previous}
          onClick={() => setPrevious((p) => !p)}
          className={`btn ${previous ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
        >
          Previous container
        </button>
        <input
          type="search"
          className="input min-h-[36px] w-[220px] text-[13px]"
          aria-label="Search logs"
          placeholder="Search logs"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <button
          type="button"
          aria-pressed={onlyHits}
          onClick={() => setOnlyHits(!onlyHits)}
          className={`btn ${onlyHits ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
        >
          Only parser hits
        </button>
        <button
          type="button"
          aria-pressed={wrap}
          onClick={() => setWrap((w) => !w)}
          className={`btn ${wrap ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
        >
          Wrap lines
        </button>
        <button type="button" className="btn" onClick={logs.reload} disabled={logs.loading}>
          {logs.loading ? <Spinner /> : <RefreshIcon size={14} />}
          Refresh
        </button>
        <span className="ml-auto text-xs text-muted">
          {logs.data && (
            <>
              {filtered ? `${lines.length} of ${total} lines` : `${total} lines`}
              {logs.data.truncated && <> · truncated to the last {LOG_TAIL}</>}
              {logs.data.container && <> · container {logs.data.container}</>}
              {previous && <> · previous instance</>}
            </>
          )}
        </span>
      </div>

      {logs.data && (
        <div className="flex flex-wrap items-center gap-2 rounded-btn border border-line bg-slate-50 px-3 py-2 text-[13px]" aria-label="Log parser summary">
          <span className="font-medium">Parser:</span>
          {!parser ? (
            <span className="text-muted">no result from the server for these lines</span>
          ) : hits.length === 0 ? (
            <span className="text-muted">no known patterns in these lines</span>
          ) : (
            <>
              <span className="text-muted">
                {plural(hits.length, "rule")} matched · {plural(matchedLines, "line")}
              </span>
              {hits.map((h) => {
                const on = activeRule === h.ruleId;
                return (
                  <button
                    key={h.ruleId}
                    type="button"
                    aria-pressed={on}
                    title={`${h.title} — ${on ? "show all lines" : "show only these lines"}`}
                    onClick={() => setRuleFilter(on ? null : h.ruleId)}
                    className={`inline-flex min-h-[26px] cursor-pointer items-center gap-1.5 rounded-full border px-2 font-mono text-xs ${
                      on ? "border-ink bg-ink text-white" : "border-slate-300 bg-white text-ink hover:bg-slate-100"
                    }`}
                  >
                    <span aria-hidden="true" className="h-2 w-2 flex-none rounded-full" style={{ background: LOG_SEV_BORDER[h.severity] }} />
                    {h.ruleId}
                    <span className={on ? "text-slate-300" : "text-muted"}>×{h.count}</span>
                  </button>
                );
              })}
            </>
          )}
        </div>
      )}

      {logs.error ? (
        <ErrorCard title="Couldn't load logs" error={logs.error} onRetry={logs.reload} />
      ) : logs.loading && !logs.data ? (
        <Loading label="Loading logs…" />
      ) : total === 0 ? (
        <Empty title="No log lines">Nothing was logged in this window. Try a longer “since”, or the previous container.</Empty>
      ) : lines.length === 0 ? (
        <Empty title="No lines match">
          {search ? <>Nothing matches “{search}”{onlyHits || activeRule ? " among the parser hits" : ""}.</> : "No lines matched a log rule in this window."}
        </Empty>
      ) : (
        <div
          role="log"
          aria-label={`Logs of ${container || name}`}
          tabIndex={0}
          className={`max-h-[640px] overflow-auto rounded-btn bg-ink py-2.5 font-mono text-xs leading-[1.7] text-slate-300 ${logs.loading ? "opacity-60" : ""}`}
        >
          {lines.map((l) => {
            const ruleSev = l.rules.length > 0 ? worstSeverity(l.rules.map((r) => sevByRule.get(r) ?? "info")) : "";
            const err = !ruleSev && ERROR_RE.test(l.text);
            const warn = !ruleSev && !err && WARN_RE.test(l.text);
            const tint = ruleSev === "error" ? "bg-[#7F1D1D]/60 text-[#FEE2E2]" : ruleSev ? "bg-white/[0.06]" : err ? "bg-[#7F1D1D] text-[#FEE2E2]" : warn ? "text-[#FBBF24]" : "";
            return (
              <div
                key={l.idx}
                className={`border-l-[3px] pl-[9px] pr-3 ${wrap ? "whitespace-pre-wrap break-all" : "w-max min-w-full whitespace-pre"} ${tint}`}
                style={{ borderLeftColor: ruleSev ? LOG_SEV_BORDER[ruleSev] : "transparent" }}
              >
                {l.text || " "}
                {l.rules.map((r) => (
                  <button
                    key={r}
                    type="button"
                    onClick={() => navigate(logRuleUrl(r))}
                    title={`Open rule ${r}`}
                    className="ml-2 inline-block cursor-pointer select-none whitespace-nowrap rounded border-0 bg-white/15 px-1.5 align-[1px] font-mono text-[10px] leading-4 text-slate-100 hover:bg-white/25"
                    style={{ boxShadow: `inset 2px 0 0 ${LOG_SEV_BORDER[sevByRule.get(r) ?? "info"]}` }}
                  >
                    {r}
                  </button>
                ))}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

function ManifestTab({ manifest }: { manifest: string }) {
  const [copied, setCopied] = useState<"ok" | "fail" | null>(null);
  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(null), 2000);
    return () => window.clearTimeout(t);
  }, [copied]);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(manifest);
      setCopied("ok");
    } catch {
      setCopied("fail");
    }
  };
  return (
    <section className="card p-4" aria-labelledby="pod-manifest">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="pod-manifest" className="h2">
          Manifest
        </h2>
        <div className="flex items-center gap-2">
          <Pill>read-only · secret values are masked</Pill>
          <button type="button" className="btn" onClick={copy} disabled={!manifest}>
            {copied === "ok" ? <CheckIcon size={14} /> : <CopyIcon size={14} />}
            {copied === "ok" ? "Copied" : copied === "fail" ? "Copy failed" : "Copy"}
          </button>
        </div>
      </div>
      {manifest ? (
        <pre className="mb-0 mt-3 max-h-[720px] overflow-auto rounded-btn border border-line bg-slate-50 p-3 font-mono text-xs leading-relaxed">{manifest}</pre>
      ) : (
        <Empty title="No manifest returned" />
      )}
    </section>
  );
}

export default function PodViewPage() {
  const { id = "", ns = "", name = "", tab: tabParam } = useParams();
  const tab: Tab = TABS.some(([t]) => t === tabParam) ? (tabParam as Tab) : "overview";
  const pod = useApi(() => api.pod(id, ns, name), [id, ns, name]);
  const clusters = useApi(() => api.clusters());
  const clusterName = clusters.data?.find((c) => c.id === id)?.name || id;
  const now = useNow(30_000);
  const p = pod.data && pod.data.name === name && pod.data.namespace === ns ? pod.data : undefined;

  return (
    <Page>
      <div className="flex flex-col gap-3">
        <div className="text-xs text-muted">
          <Link to="/clusters">Clusters</Link> / <Link to={clusterUrl(id, "overview")}>{clusterName}</Link> /{" "}
          <Link to={clusterUrl(id, "pods", { ns })}>{ns}</Link>
        </div>
        <div className="flex flex-wrap items-center gap-2.5">
          <Link to={clusterUrl(id, "pods", { ns })} className="btn">
            <ArrowLeftIcon size={14} />
            Back to pods
          </Link>
          <h1 className="m-0 break-all font-mono text-[19px] font-semibold">{name}</h1>
          {p && <Pill tone={podStatusTone(p.status)}>{p.status}</Pill>}
          {p && p.restarts > 3 && <Pill tone="critical">{p.restarts} restarts</Pill>}
          {p?.issue && p.issue !== p.status && <Pill tone={p.issueSeverity === "critical" ? "critical" : "warning"}>{p.issue}</Pill>}
          {pod.loading && p && <Spinner />}
          <button type="button" className="btn ml-auto" onClick={pod.reload} disabled={pod.loading}>
            <RefreshIcon size={14} />
            Refresh
          </button>
        </div>
      </div>

      <nav aria-label="Pod sections" className="-mt-2 flex flex-wrap gap-0.5 border-b border-line">
        {TABS.map(([t, label]) => (
          <Link key={t} to={podUrl(id, ns, name, t)} className={`tab-link ${tab === t ? "active" : ""}`} aria-current={tab === t ? "page" : undefined}>
            {label}
            {t === "events" && p && p.events.length > 0 && <span className="ml-1 text-xs text-muted">({p.events.length})</span>}
          </Link>
        ))}
      </nav>

      {pod.error && !p ? (
        <ErrorCard title="Couldn't load this pod" error={pod.error} onRetry={pod.reload} />
      ) : !p ? (
        <div className="card">
          <Loading label="Loading pod…" />
        </div>
      ) : (
        <>
          {pod.error && <ErrorCard title="Refresh failed — showing the last data" error={pod.error} onRetry={pod.reload} />}
          {tab === "overview" && <OverviewTab pod={p} now={now} clusterId={id} />}
          {tab === "logs" && <LogsTab key={`${id}/${ns}/${name}`} id={id} ns={ns} name={name} containers={p.containers} />}
          {tab === "manifest" && <ManifestTab manifest={p.manifest} />}
          {tab === "events" && (
            <section className="card" aria-label="Pod events">
              {p.events.length === 0 ? (
                <Empty title="No events">Kubernetes keeps events for about an hour.</Empty>
              ) : (
                <div className="overflow-x-auto">
                  <EventsTable events={p.events} showObject={false} now={now} />
                </div>
              )}
            </section>
          )}
        </>
      )}
    </Page>
  );
}
