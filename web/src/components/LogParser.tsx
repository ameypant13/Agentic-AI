import type { Hit, LogSeverity, PodHit, PodScan } from "../api/types";
import { age } from "../lib/format";
import { Pill, type Tone } from "./Pill";

/** In-app URL of the log rules page, optionally deep-linked to one rule. */
export const logRuleUrl = (id?: string) => `/log-rules${id ? `?rule=${encodeURIComponent(id)}` : ""}`;

const SEV_RANK: Record<string, number> = { error: 0, warning: 1, info: 2 };

export function sevRank(s: string): number {
  return SEV_RANK[s] ?? 3;
}

export function logSevTone(s: string): Tone {
  return s === "error" ? "critical" : s === "warning" ? "warning" : s === "info" ? "info" : "neutral";
}

export const LOG_SEV_LABEL: Record<LogSeverity, string> = { error: "Error", warning: "Warning", info: "Info" };

/** Left-border colour of a matched line in the dark log viewer. */
export const LOG_SEV_BORDER: Record<string, string> = { error: "#EF4444", warning: "#F59E0B", info: "#60A5FA" };

export function LogSeverityPill({ severity }: { severity: string }) {
  return <Pill tone={logSevTone(severity)}>{LOG_SEV_LABEL[severity as LogSeverity] ?? severity}</Pill>;
}

/** Worst severity of a set of severities ("" when empty). */
export function worstSeverity(sevs: Iterable<string>): string {
  let worst = "";
  for (const s of sevs) if (!worst || sevRank(s) < sevRank(worst)) worst = s;
  return worst;
}

/** Hits sorted errors first, then by count. */
export function sortHits<T extends Pick<Hit, "severity" | "count">>(hits: T[]): T[] {
  return [...hits].sort((a, b) => sevRank(a.severity) - sevRank(b.severity) || b.count - a.count);
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** Tooltip for a pod's stored hits: active ones with their 60-min count, resolved ones marked with last seen. */
export function scanHitsTitle(scan: PodScan): string | undefined {
  const hits = sortHits(scan.hits ?? []);
  if (hits.length === 0) return scan.top || undefined;
  return hits
    .map((h) => (h.active ? `${h.title} ×${h.count} — last seen ${age(h.lastSeen)} ago` : `Resolved: ${h.title} — last seen ${age(h.lastSeen)} ago`))
    .join("\n");
}

/** One stored hit's status: neutral "Resolved" pill + last seen when resolved, else just last seen. */
export function ScanHitStatus({ hit, now }: { hit: Pick<PodHit, "active" | "lastSeen">; now?: number }) {
  return (
    <span className="inline-flex items-baseline gap-1.5 text-xs text-muted">
      {!hit.active && <Pill>Resolved</Pill>}
      last seen {age(hit.lastSeen, now)} ago
    </span>
  );
}

/** "N errors" (red) / "N warnings" (amber) from the 60-min counts of one pod's scan; null when it is clean. */
export function PodScanPill({ scan }: { scan: PodScan }) {
  if (scan.error)
    return (
      <span className="text-xs text-muted" title={scan.error}>
        scan failed
      </span>
    );
  if (scan.errors > 0)
    return (
      <span title={scanHitsTitle(scan)}>
        <Pill tone="critical">{plural(scan.errors, "error")}</Pill>
      </span>
    );
  if (scan.warnings > 0)
    return (
      <span title={scanHitsTitle(scan)}>
        <Pill tone="warning">{plural(scan.warnings, "warning")}</Pill>
      </span>
    );
  return null;
}

export { plural };
