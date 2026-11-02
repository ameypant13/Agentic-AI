import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { Finding, Fleet, FleetIssue } from "../api/types";
import { Since, SourcePill, entityText, fleetIssueUrl, sortIssues, useFleetIssues, useIssueCounts } from "../components/Issues";
import { LogSeverityPill } from "../components/LogParser";
import { Spinner } from "../components/Icons";
import { Page } from "../components/Layout";
import { Pill, SeverityPill } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { formatDateTime, providerLabel, relativeTime } from "../lib/format";
import { useNow } from "../components/Cluster";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";
import { daysText, headline } from "./Certificates";

function Tile({ label, value, sub, attention, to }: { label: string; value: string | number; sub?: string; attention?: boolean; to?: string }) {
  const body = (
    <>
      <div className={`text-[13px] ${attention ? "text-warn-fg" : "text-muted"}`}>{label}</div>
      <div className="mt-1 text-[28px] font-semibold leading-tight text-ink">{value}</div>
      {sub && <div className="text-xs text-muted">{sub}</div>}
    </>
  );
  const cls = `card p-4 ${attention ? "border-warn-border" : ""}`;
  return to ? (
    <Link to={to} className={`${cls} block no-underline hover:border-violet-border hover:bg-slate-50`}>
      {body}
    </Link>
  ) : (
    <div className={cls}>{body}</div>
  );
}

/** "Active issues" tile from the shared GET /issues poll. */
function IssuesTile() {
  const issues = useFleetIssues();
  const counts = useIssueCounts();
  if (!counts)
    return <Tile label="Active issues" value={issues.error ? "—" : "…"} sub={issues.error ? "couldn't load issues" : "loading…"} to="/issues" />;
  return (
    <Tile
      label="Active issues"
      value={counts.errors + counts.warnings}
      sub={`${counts.errors} ${counts.errors === 1 ? "error" : "errors"} · ${counts.warnings} ${counts.warnings === 1 ? "warning" : "warnings"}`}
      attention={counts.errors + counts.warnings > 0}
      to="/issues"
    />
  );
}

const TOP_ISSUES = 5;

function TopIssueRow({ issue, now }: { issue: FleetIssue; now: number }) {
  return (
    <li className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-b border-hairline px-4 py-2.5 text-[13px] last:border-b-0">
      <LogSeverityPill severity={issue.severity} />
      <SourcePill source={issue.source} />
      <span className="font-medium">{issue.title}</span>
      <span className="min-w-0 break-all text-xs text-muted">
        {issue.clusterName} · <span className="font-mono">{entityText(issue)}</span>
      </span>
      {issue.valueText && <span className="font-mono text-xs tabular-nums">{issue.valueText}</span>}
      <span className="ml-auto flex items-center gap-2.5">
        <Since iso={issue.firstSeen} now={now} />
        <Link to={fleetIssueUrl(issue)} className="btn min-h-[30px] px-2.5 text-xs">
          Open
        </Link>
      </span>
    </li>
  );
}

function TopIssuesCard() {
  const issues = useFleetIssues();
  const now = useNow(30_000);
  const active = (issues.data ?? []).filter((i) => i.active !== false);
  const top = sortIssues(active).slice(0, TOP_ISSUES);
  return (
    <section className="card" aria-labelledby="top-issues">
      <div className="card-head">
        <h2 id="top-issues" className="h2">
          Top issues
        </h2>
        <span className="flex items-center gap-2 text-xs text-muted">
          most severe, then newest · metrics and log parsers
          {active.length > 0 && <Link to="/issues">View all {active.length}</Link>}
        </span>
      </div>
      {issues.error && !issues.data ? (
        <div className="p-4">
          <ErrorCard title="Couldn't load issues" error={issues.error} onRetry={issues.reload} />
        </div>
      ) : !issues.data ? (
        <Loading label="Loading issues…" />
      ) : top.length === 0 ? (
        <div className="px-4 py-3.5 text-[13px] text-muted">No active issues on any cluster.</div>
      ) : (
        <ul className="m-0 list-none p-0">
          {top.map((i) => (
            <TopIssueRow key={`${i.source}|${i.clusterId}|${i.ruleId}|${i.namespace}|${i.entityName}`} issue={i} now={now} />
          ))}
        </ul>
      )}
    </section>
  );
}

function topEntries(rec: Record<string, number>, n: number): string {
  return Object.entries(rec)
    .sort((a, b) => b[1] - a[1])
    .slice(0, n)
    .map(([k, v]) => `${v} ${k}`)
    .join(" · ");
}

function KpiTiles({ fleet, realm, findings }: { fleet: Fleet; realm: string; findings: Finding[] }) {
  const cpTotal = realm === "all" ? fleet.controlPlanes.total : (fleet.controlPlanes.byRealm[realm] ?? 0);
  const crit = realm === "all" ? fleet.openFindings.critical : findings.filter((f) => f.severity === "critical").length;
  const warn = realm === "all" ? fleet.openFindings.warning : findings.filter((f) => f.severity === "warning").length;
  const realms = Object.keys(fleet.controlPlanes.byRealm).length;
  return (
    <div className="grid grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))] gap-4">
      <Tile
        label="Control planes"
        value={cpTotal}
        sub={realm === "all" ? (topEntries(fleet.controlPlanes.byStatus, 2) || `${realms} realms`) : `in ${realm}`}
      />
      <Tile label="Needs attention" value={crit + warn} sub={`${crit} critical · ${warn} warning`} attention={crit + warn > 0} />
      <IssuesTile />
      <Tile label="Investigations (7d)" value={fleet.investigations7d} sub="opened by Copilot" />
      <Tile
        label="Last monitor run"
        value={fleet.lastMonitorRun ? relativeTime(fleet.lastMonitorRun) : "Never"}
        sub={fleet.lastMonitorRun ? formatDateTime(fleet.lastMonitorRun) : "monitors have not run yet"}
      />
    </div>
  );
}

/** "Acknowledged" for your own acknowledgement, "Acknowledged by <name>" for someone else's. */
function AckPill({ finding, me }: { finding: Finding; me: string }) {
  const by = finding.acknowledgedBy;
  const mine = !by || by.toLowerCase() === me.toLowerCase();
  const name = finding.acknowledgedByName || by.split("@")[0];
  const when = finding.acknowledgedAt ? ` · ${relativeTime(finding.acknowledgedAt)}` : "";
  return (
    <span title={by ? `Acknowledged by ${by}${when}` : undefined}>
      <Pill tone="info" className="whitespace-normal break-words text-left leading-tight">
        {mine ? "Acknowledged" : `Acknowledged by ${name}`}
      </Pill>
    </span>
  );
}

function FindingRow({ finding, onAcked }: { finding: Finding; onAcked: (f: Finding) => void }) {
  const { me } = useMe();
  const navigate = useNavigate();
  const [busy, setBusy] = useState<"investigate" | "ack" | null>(null);
  const [error, setError] = useState<string | null>(null);

  const investigate = async () => {
    setBusy("investigate");
    setError(null);
    try {
      const { chatId, prompt } = await api.investigateFinding(finding.id);
      navigate(`/copilot/${encodeURIComponent(chatId)}?send=${encodeURIComponent(prompt)}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(null);
    }
  };

  const ack = async () => {
    setBusy("ack");
    setError(null);
    try {
      onAcked(await api.ackFinding(finding.id));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="grid grid-cols-[96px_170px_minmax(0,1fr)_230px] items-center gap-4 border-b border-hairline px-4 py-3.5 last:border-b-0">
      <div className="flex flex-col items-start gap-1">
        <SeverityPill severity={finding.severity} />
        {finding.state === "acknowledged" && <AckPill finding={finding} me={me.email} />}
      </div>
      <div className="min-w-0">
        <div className="truncate text-sm font-semibold" title={finding.entityName}>
          {finding.entityName}
        </div>
        <div className="truncate font-mono text-xs text-muted">
          {providerLabel(finding.provider)} · {finding.realm}
        </div>
      </div>
      <div className="text-sm">
        <span className="font-medium">{finding.title}</span>
        {finding.detail && <span> — {finding.detail}</span>}
        <div className="mt-0.5 text-xs text-muted">
          First seen {relativeTime(finding.firstSeenAt)} · last seen {relativeTime(finding.lastSeenAt)}
          {finding.region && <> · <span className="font-mono">{finding.region}</span></>}
        </div>
        {error && (
          <div role="alert" className="mt-1 text-xs text-crit-fg">
            {error}
          </div>
        )}
      </div>
      <div className="flex flex-wrap justify-end gap-2">
        {finding.investigationId && (
          <Link className="btn" to={`/investigations/${encodeURIComponent(finding.investigationId)}`}>
            View RCA
          </Link>
        )}
        {me.isAdmin && finding.state === "open" && (
          <button type="button" className="btn" onClick={ack} disabled={busy !== null}>
            {busy === "ack" && <Spinner />}
            Acknowledge
          </button>
        )}
        <button type="button" className="btn btn-primary" onClick={investigate} disabled={busy !== null}>
          {busy === "investigate" && <Spinner />}
          Investigate
        </button>
      </div>
    </div>
  );
}

const TOP_CERTS = 6;

/** Soonest-expiring certificates from the certificate inventory. */
function CertCard() {
  const inv = useApi(() => api.certificates());
  const top = (inv.data?.items ?? []).filter((i) => i.daysLeft !== undefined).slice(0, TOP_CERTS);
  return (
    <section className="card p-4" aria-labelledby="certs">
      <div className="mb-1 flex items-baseline justify-between gap-3">
        <h2 id="certs" className="h2">
          Certificates expiring
        </h2>
        <Link to="/certificates" className="text-[13px]">
          View all
        </Link>
      </div>
      <p className="mb-3 mt-0 text-[13px] text-muted">
        Tenant and wildcard certificates, soonest first
        {inv.data?.scannedAt ? ` · scanned ${relativeTime(inv.data.scannedAt)}` : ""}.
      </p>
      {inv.error ? (
        <div className="text-[13px] text-crit-fg">Couldn't load certificates: {inv.error.message}</div>
      ) : !inv.data ? (
        <div className="text-[13px] text-muted">Loading…</div>
      ) : top.length === 0 ? (
        <div className="text-[13px] text-muted">{inv.data.scannedAt ? "No certificates found." : "The first certificate scan is running."}</div>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0 text-[13px]">
          {top.map((i) => (
            <li key={i.key} className="flex justify-between gap-3">
              <span className="min-w-0">
                <span className="break-all font-medium">{i.tenant ? `${i.tenant} · ` : ""}{i.host}</span>
                <span className="block text-xs text-muted">{headline(i) || `${i.kind === "wildcard" ? "Wildcard secret" : "Auto-renew"}: ${i.autoRenew}`}</span>
              </span>
              <span className={`flex-none text-right font-semibold ${i.status === "ok" || i.status === "unknown" ? "text-ink" : i.status === "warning" ? "text-warn-fg" : "text-crit-fg"}`}>
                {daysText(i)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export default function FleetPage() {
  const fleet = useApi(() => api.fleet());
  const findings = useApi(() => api.findings("open"));
  const [realm, setRealm] = useState("all");

  const realms = useMemo(() => {
    const set = new Set<string>(Object.keys(fleet.data?.controlPlanes.byRealm ?? {}));
    findings.data?.forEach((f) => f.realm && set.add(f.realm));
    return [...set].sort();
  }, [fleet.data, findings.data]);

  const visible = useMemo(
    () => (findings.data ?? []).filter((f) => realm === "all" || f.realm === realm),
    [findings.data, realm],
  );

  const onAcked = (updated: Finding) =>
    findings.setData((prev) => prev?.map((f) => (f.id === updated.id ? updated : f)));

  return (
    <Page>
      <PageHeader
        title="Fleet overview"
        subtitle="What needs a human today. Findings come from scheduled monitors and from Copilot investigations."
      >
        {realms.length > 1 && (
          <div className="flex flex-wrap gap-1.5" role="group" aria-label="Filter by realm">
            {["all", ...realms].map((r) => (
              <button
                key={r}
                type="button"
                aria-pressed={realm === r}
                onClick={() => setRealm(r)}
                className={`btn ${realm === r ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
              >
                {r === "all" ? "All realms" : r}
              </button>
            ))}
          </div>
        )}
      </PageHeader>

      {fleet.error ? (
        <ErrorCard title="Couldn't load fleet summary" error={fleet.error} onRetry={fleet.reload} />
      ) : fleet.data ? (
        <KpiTiles fleet={fleet.data} realm={realm} findings={visible} />
      ) : (
        <div className="card">
          <Loading label="Loading fleet summary…" />
        </div>
      )}

      <TopIssuesCard />

      <section className="card" aria-labelledby="att">
        <div className="card-head">
          <h2 id="att" className="h2">
            Needs attention
          </h2>
          <span className="text-xs text-muted">sorted by severity, then newest</span>
        </div>
        {findings.error ? (
          <div className="p-4">
            <ErrorCard title="Couldn't load findings" error={findings.error} onRetry={findings.reload} />
          </div>
        ) : findings.loading && !findings.data ? (
          <Loading label="Loading findings…" />
        ) : visible.length === 0 ? (
          <Empty title="Nothing needs attention">No open findings{realm !== "all" ? ` in ${realm}` : ""}. Monitors will add one here when something fails.</Empty>
        ) : (
          <div className="overflow-x-auto">
            <div className="flex min-w-[800px] flex-col">
              {visible.map((f) => (
                <FindingRow key={f.id} finding={f} onAcked={onAcked} />
              ))}
            </div>
          </div>
        )}
      </section>

      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(320px,100%),1fr))] gap-4">
        <CertCard />
      </div>
    </Page>
  );
}
