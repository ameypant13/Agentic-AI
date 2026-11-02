import { createContext, useContext, useMemo } from "react";
import { Link } from "react-router-dom";
import type { FleetIssue, MetricIssue } from "../api/types";
import { age, formatDateTime } from "../lib/format";
import type { ApiState } from "../lib/useApi";
import { clusterUrl, nodeUrl, podUrl } from "./Cluster";
import { LogSeverityPill, logRuleUrl, sevRank } from "./LogParser";
import { Pill } from "./Pill";

/** GET /issues refresh interval (the parsers run every 60 s). */
export const ISSUES_POLL_MS = 30_000;

// ---------------------------------------------------------------- shared fleet issues

/** Fleet issues, polled once by the layout and shared by the sidebar badge, Fleet and Issues pages. */
export const IssuesContext = createContext<ApiState<FleetIssue[]> | null>(null);

export function useFleetIssues(): ApiState<FleetIssue[]> {
  const ctx = useContext(IssuesContext);
  if (!ctx) throw new Error("useFleetIssues must be used inside the authenticated layout");
  return ctx;
}

export type IssueCounts = { errors: number; warnings: number; info: number; total: number };

export function countIssues(issues: { severity: string; active?: boolean }[]): IssueCounts {
  const c = { errors: 0, warnings: 0, info: 0, total: 0 };
  for (const i of issues) {
    if (i.active === false) continue;
    c.total++;
    if (i.severity === "error") c.errors++;
    else if (i.severity === "warning") c.warnings++;
    else c.info++;
  }
  return c;
}

export function useIssueCounts(): IssueCounts | undefined {
  const { data } = useFleetIssues();
  return useMemo(() => (data ? countIssues(data) : undefined), [data]);
}

/** Most severe first, then newest (latest firstSeen) first. */
export function sortIssues<T extends { severity: string; firstSeen: string }>(issues: T[]): T[] {
  return [...issues].sort((a, b) => sevRank(a.severity) - sevRank(b.severity) || Date.parse(b.firstSeen) - Date.parse(a.firstSeen));
}

// ---------------------------------------------------------------- links

/** Node name of a node issue: its `node` label, else the entity name before " · <condition>". */
const nodeName = (entityName: string, labels?: Record<string, string> | null) => labels?.node || entityName.split(" · ")[0];

/** Where "Open" goes: pods → the pod page, nodes → the node page, anything else → the cluster overview. */
export function issueUrl(clusterId: string, i: Pick<FleetIssue, "entity" | "entityName" | "namespace" | "pod"> & { labels?: Record<string, string> | null }): string {
  if (i.pod && i.namespace) return podUrl(clusterId, i.namespace, i.pod);
  if (i.entity === "node" && nodeName(i.entityName, i.labels)) return nodeUrl(clusterId, nodeName(i.entityName, i.labels));
  return clusterUrl(clusterId, "overview");
}

export const fleetIssueUrl = (i: FleetIssue) => issueUrl(i.clusterId, i);

/** "namespace / name" for the entity column ("name" alone when there is no namespace). */
export function entityText(i: { namespace: string; entityName: string; entity: string }): string {
  const name = i.entityName || i.entity;
  return i.namespace ? `${i.namespace} / ${name}` : name;
}

/** Rule page link: log rules deep-link to the rule; metric rules open the metric rules tab. */
export function ruleUrl(source: "metrics" | "logs", ruleId: string): string {
  return source === "logs" ? logRuleUrl(ruleId) : `/log-rules?view=metrics&rule=${encodeURIComponent(ruleId)}`;
}

// ---------------------------------------------------------------- pills & rows

export function SourcePill({ source }: { source: string }) {
  return <Pill tone={source === "metrics" ? "violet" : "info"}>{source}</Pill>;
}

export function Since({ iso, now, prefix = "since" }: { iso: string; now?: number; prefix?: string }) {
  return (
    <span className="whitespace-nowrap text-xs text-muted" title={formatDateTime(iso)}>
      {prefix} {age(iso, now)}
    </span>
  );
}

/** Meaning + fix (+ rule link) shown when an issue is expanded. */
export function IssueExplain({ meaning, fix, ruleId, source }: { meaning: string; fix: string; ruleId: string; source: "metrics" | "logs" }) {
  return (
    <div className="flex flex-col gap-1 text-[13px]">
      {meaning && (
        <div>
          <span className="font-semibold">Meaning </span>
          {meaning}
        </div>
      )}
      {fix && (
        <div>
          <span className="font-semibold">Fix </span>
          {fix}
        </div>
      )}
      <div className="text-xs text-muted">
        Rule{" "}
        <Link to={ruleUrl(source, ruleId)} className="font-mono">
          {ruleId}
        </Link>
      </div>
    </div>
  );
}

/**
 * Compact list of metric issues (cluster overview, pod page): severity, title, entity,
 * value, since. Each expands (native <details>) to meaning + fix.
 */
export function MetricIssueList({ clusterId, issues, now, showEntity = true, resolved = false }: { clusterId: string; issues: MetricIssue[]; now: number; showEntity?: boolean; resolved?: boolean }) {
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {issues.map((i) => (
        <li key={`${i.instance}|${i.ruleId}|${i.entityKey}`} className="border-b border-hairline py-2 last:border-b-0">
          <details className="group">
            <summary className="flex cursor-pointer list-none flex-wrap items-baseline gap-x-2.5 gap-y-1 text-[13px] [&::-webkit-details-marker]:hidden">
              <span aria-hidden="true" className="inline-block w-2 text-muted group-open:rotate-90">
                ▸
              </span>
              {resolved ? <Pill>Resolved</Pill> : <LogSeverityPill severity={i.severity} />}
              <span className={`font-medium ${resolved ? "text-muted" : ""}`}>{i.title}</span>
              {showEntity && (
                <Link to={issueUrl(clusterId, i)} className="break-all font-mono text-xs" onClick={(e) => e.stopPropagation()}>
                  {entityText(i)}
                </Link>
              )}
              {i.valueText && <span className="font-mono text-xs tabular-nums">{i.valueText}</span>}
              <span className="ml-auto">
                {resolved && i.resolvedAt ? <Since iso={i.resolvedAt} now={now} prefix="resolved" /> : <Since iso={i.firstSeen} now={now} />}
                {resolved && <span className="text-xs text-muted"> ago</span>}
              </span>
            </summary>
            <div className="mt-2 rounded-btn bg-slate-50 px-3 py-2">
              <IssueExplain meaning={i.meaning} fix={i.fix} ruleId={i.ruleId} source="metrics" />
            </div>
          </details>
        </li>
      ))}
    </ul>
  );
}
