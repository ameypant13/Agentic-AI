import { Fragment, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import type { FleetIssue } from "../api/types";
import { clusterUrl, useNow, useRefreshedAt } from "../components/Cluster";
import { RefreshIcon, Spinner } from "../components/Icons";
import { IssueExplain, Since, SourcePill, countIssues, entityText, fleetIssueUrl, sortIssues, useFleetIssues } from "../components/Issues";
import { Page } from "../components/Layout";
import { LogSeverityPill, plural } from "../components/LogParser";
import { Pill } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";

const SEVERITIES = [
  ["", "All"],
  ["error", "Error"],
  ["warning", "Warning"],
] as const;
const SOURCES = [
  ["", "All"],
  ["metrics", "Metrics"],
  ["logs", "Logs"],
] as const;

function Segmented<V extends string>({ label, value, options, onChange }: { label: string; value: V; options: readonly (readonly [V, string])[]; onChange: (v: V) => void }) {
  return (
    <div role="group" aria-label={label} className="inline-flex">
      {options.map(([v, text], i) => {
        const on = value === v;
        return (
          <button
            key={v || "all"}
            type="button"
            aria-pressed={on}
            onClick={() => onChange(v)}
            className={`btn rounded-none ${i === 0 ? "rounded-l-btn" : "-ml-px"} ${i === options.length - 1 ? "rounded-r-btn" : ""} ${
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

const issueKey = (i: FleetIssue) => `${i.source}|${i.clusterId}|${i.ruleId}|${i.namespace}|${i.entityName}`;

type Group = { clusterId: string; clusterName: string; issues: FleetIssue[]; errors: number; warnings: number };

function IssueRows({ group, now, expanded, toggle }: { group: Group; now: number; expanded: Set<string>; toggle: (k: string) => void }) {
  return (
    <table className="table min-w-[760px]">
      <thead>
        <tr>
          <th scope="col">Severity</th>
          <th scope="col">Source</th>
          <th scope="col">Issue</th>
          <th scope="col">Value</th>
          <th scope="col">Since</th>
          <th scope="col">
            <span className="sr-only">Open</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {group.issues.map((i) => {
          const k = issueKey(i);
          const open = expanded.has(k);
          const detailId = `iss-${k.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
          return (
            <Fragment key={k}>
              <tr className="cursor-pointer hover:bg-slate-50" onClick={() => toggle(k)}>
                <td className="w-[96px]">
                  <LogSeverityPill severity={i.severity} />
                </td>
                <td className="w-[90px]">
                  <SourcePill source={i.source} />
                </td>
                <td className="min-w-[280px]">
                  <button
                    type="button"
                    className="link-btn text-left font-medium text-ink hover:text-violet"
                    aria-expanded={open}
                    aria-controls={detailId}
                    onClick={(e) => {
                      e.stopPropagation();
                      toggle(k);
                    }}
                  >
                    <span aria-hidden="true" className="mr-1 inline-block w-2 text-muted">
                      {open ? "▾" : "▸"}
                    </span>
                    {i.title}
                  </button>
                  <div className="mt-0.5 break-all pl-3 font-mono text-xs text-muted">{entityText(i)}</div>
                </td>
                <td className="whitespace-nowrap font-mono text-xs tabular-nums">{i.valueText || <span className="text-muted">—</span>}</td>
                <td>
                  <Since iso={i.firstSeen} now={now} />
                </td>
                <td className="text-right" onClick={(e) => e.stopPropagation()}>
                  <Link to={fleetIssueUrl(i)} className="btn min-h-[30px] px-2.5 text-xs" aria-label={`Open ${entityText(i)}`}>
                    Open
                  </Link>
                </td>
              </tr>
              {open && (
                <tr id={detailId} className="bg-slate-50">
                  <td colSpan={6}>
                    <IssueExplain meaning={i.meaning} fix={i.fix} ruleId={i.ruleId} source={i.source} />
                    <div className="mt-1 text-xs text-muted">
                      last seen <Since iso={i.lastSeen} now={now} prefix="" /> ago
                    </div>
                  </td>
                </tr>
              )}
            </Fragment>
          );
        })}
      </tbody>
    </table>
  );
}

export default function IssuesPage() {
  const issues = useFleetIssues();
  const [params, setParams] = useSearchParams();
  const sev = (params.get("severity") ?? "") as (typeof SEVERITIES)[number][0];
  const source = (params.get("source") ?? "") as (typeof SOURCES)[number][0];
  const cluster = params.get("cluster") ?? "";
  const q = params.get("q") ?? "";
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const now = useNow(15_000);
  const refreshedAt = useRefreshedAt(issues.data);

  const set = (key: string, v: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (v) next.set(key, v);
        else next.delete(key);
        return next;
      },
      { replace: true },
    );

  const all = useMemo(() => (issues.data ?? []).filter((i) => i.active !== false), [issues.data]);
  const clusters = useMemo(() => {
    const m = new Map<string, string>();
    for (const i of all) m.set(i.clusterId, i.clusterName || i.clusterId);
    if (cluster && !m.has(cluster)) m.set(cluster, cluster);
    return [...m.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [all, cluster]);

  const groups = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const rows = all.filter(
      (i) =>
        (!sev || i.severity === sev) &&
        (!source || i.source === source) &&
        (!cluster || i.clusterId === cluster) &&
        (!needle || [i.title, i.entityName, i.namespace, i.ruleId, i.clusterName, i.meaning, i.valueText].some((f) => f?.toLowerCase().includes(needle))),
    );
    const by = new Map<string, Group>();
    for (const i of rows) {
      let g = by.get(i.clusterId);
      if (!g) by.set(i.clusterId, (g = { clusterId: i.clusterId, clusterName: i.clusterName || i.clusterId, issues: [], errors: 0, warnings: 0 }));
      g.issues.push(i);
    }
    const list = [...by.values()];
    for (const g of list) {
      g.issues = sortIssues(g.issues);
      const c = countIssues(g.issues);
      g.errors = c.errors;
      g.warnings = c.warnings;
    }
    // Clusters with errors first (most errors first), then by warnings, then name.
    return list.sort((a, b) => b.errors - a.errors || b.warnings - a.warnings || a.clusterName.localeCompare(b.clusterName));
  }, [all, sev, source, cluster, q]);

  const shownCount = groups.reduce((n, g) => n + g.issues.length, 0);
  const totals = countIssues(all);
  const filtered = !!(sev || source || cluster || q);

  const toggleSet = (setter: typeof setCollapsed) => (k: string) =>
    setter((s) => {
      const next = new Set(s);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  const toggleGroup = toggleSet(setCollapsed);
  const toggleRow = toggleSet(setExpanded);
  const ago = refreshedAt === null ? null : Math.max(0, Math.round((now - refreshedAt) / 1000));

  return (
    <Page>
      <PageHeader
        title="Issues"
        subtitle="Active problems the metrics parser (Grafana) and the log parser see on every cluster you can access. Rules, not AI."
      >
        <div className="flex items-center gap-2 text-xs text-muted">
          {issues.loading && issues.data ? <Spinner /> : null}
          <span>{ago === null ? "refreshes every 30 s" : `refreshed ${ago < 5 ? "just now" : `${ago}s ago`} · every 30 s`}</span>
          <button type="button" className="btn" onClick={issues.reload} disabled={issues.loading}>
            <RefreshIcon size={14} />
            Refresh
          </button>
        </div>
      </PageHeader>

      <div className="flex flex-wrap items-center gap-2">
        <input
          type="search"
          className="input min-h-[36px] w-[240px] text-[13px]"
          aria-label="Search issues"
          placeholder="Search title, entity, rule"
          value={q}
          onChange={(e) => set("q", e.target.value)}
        />
        <Segmented label="Severity" value={sev} options={SEVERITIES} onChange={(v) => set("severity", v)} />
        <Segmented label="Source" value={source} options={SOURCES} onChange={(v) => set("source", v)} />
        <span className="inline-flex items-center gap-2">
          <label htmlFor="iss-cluster" className="text-[13px] text-muted">
            Cluster
          </label>
          <select id="iss-cluster" className="select min-h-[36px] max-w-[260px]" value={cluster} onChange={(e) => set("cluster", e.target.value)}>
            <option value="">All clusters</option>
            {clusters.map(([id, name]) => (
              <option key={id} value={id}>
                {name}
              </option>
            ))}
          </select>
        </span>
        <span className="ml-auto text-xs text-muted" aria-live="polite">
          {issues.data &&
            (filtered ? `${shownCount} of ${totals.total} issues` : `${plural(totals.errors, "error")} · ${plural(totals.warnings, "warning")}${totals.info ? ` · ${totals.info} info` : ""}`)}
        </span>
      </div>

      {issues.error && issues.data && <ErrorCard title="Refresh failed — showing the last data" error={issues.error} onRetry={issues.reload} />}

      {issues.error && !issues.data ? (
        <ErrorCard title="Couldn't load issues" error={issues.error} onRetry={issues.reload} />
      ) : !issues.data ? (
        <div className="card">
          <Loading label="Loading issues…" />
        </div>
      ) : groups.length === 0 ? (
        <div className="card">
          <Empty title={all.length === 0 ? "No active issues" : "No issues match"}>
            {all.length === 0 ? "Neither the metrics parser nor the log parser sees a problem on any cluster right now." : "Try a different severity, source, cluster or search."}
          </Empty>
        </div>
      ) : (
        groups.map((g) => {
          const isCollapsed = collapsed.has(g.clusterId);
          const bodyId = `iss-group-${g.clusterId}`;
          return (
            <section key={g.clusterId} className="card" aria-labelledby={`${bodyId}-h`}>
              <div className={`flex flex-wrap items-center gap-2.5 px-4 py-3 ${isCollapsed ? "" : "border-b border-line"}`}>
                <h2 id={`${bodyId}-h`} className="h2 min-w-0">
                  <button
                    type="button"
                    className="link-btn font-semibold text-ink hover:text-violet"
                    aria-expanded={!isCollapsed}
                    aria-controls={bodyId}
                    onClick={() => toggleGroup(g.clusterId)}
                  >
                    <span aria-hidden="true" className="mr-1.5 inline-block w-2 text-muted">
                      {isCollapsed ? "▸" : "▾"}
                    </span>
                    {g.clusterName}
                  </button>
                </h2>
                {g.errors > 0 && <Pill tone="critical">{plural(g.errors, "error")}</Pill>}
                {g.warnings > 0 && <Pill tone="warning">{plural(g.warnings, "warning")}</Pill>}
                {g.issues.length - g.errors - g.warnings > 0 && <Pill tone="info">{g.issues.length - g.errors - g.warnings} info</Pill>}
                <Link to={clusterUrl(g.clusterId, "overview")} className="ml-auto text-xs">
                  Cluster overview
                </Link>
              </div>
              {!isCollapsed && (
                <div id={bodyId} className="relative overflow-x-auto">
                  <IssueRows group={g} now={now} expanded={expanded} toggle={toggleRow} />
                </div>
              )}
            </section>
          );
        })
      )}
    </Page>
  );
}
