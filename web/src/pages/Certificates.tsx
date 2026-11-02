import { Fragment, useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import type { CertItem, CertKind, CertRepairResult, CertStatus, WildcardResult } from "../api/types";
import { Spinner } from "../components/Icons";
import { Page } from "../components/Layout";
import { Pill, type Tone } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { formatDateTime, providerLabel, relativeTime } from "../lib/format";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";

const STATUS_TONE: Record<CertStatus, Tone> = { expired: "critical", critical: "critical", warning: "warning", unknown: "neutral", ok: "success" };
const STATUS_LABEL: Record<CertStatus, string> = { expired: "Expired", critical: "Critical", warning: "Warning", unknown: "Unknown", ok: "OK" };

export function CertStatusPill({ status }: { status: CertStatus }) {
  return <Pill tone={STATUS_TONE[status]}>{STATUS_LABEL[status]}</Pill>;
}

const KIND_LABEL: Record<CertKind, string> = { tenant: "Tenant", wildcard: "Wildcard", acm: "ACM", gcp: "Cert Manager" };
const SOURCE_LABEL: Record<CertItem["source"], string> = {
  acm: "ACM",
  "gcp-certificate-manager": "GCP Certificate Manager",
  "wildcard-secret": "Wildcard secret",
  unknown: "Unknown",
};

export function daysText(item: CertItem): string {
  if (item.daysLeft === undefined) return "—";
  if (item.daysLeft < 0) return `expired ${-item.daysLeft}d ago`;
  return `${item.daysLeft}d`;
}

/** The main problem in one line, for compact lists. */
export function headline(item: CertItem): string {
  const p =
    item.problems.find((x) => x.severity === "critical") ??
    item.problems.find((x) => x.severity === "warning") ??
    (item.status === "unknown" ? item.problems[0] : undefined);
  return p?.message ?? "";
}

const FILTERS: { value: "all" | CertKind | "problems"; label: string }[] = [
  { value: "all", label: "All" },
  { value: "problems", label: "Needs attention" },
  { value: "tenant", label: "Tenants" },
  { value: "wildcard", label: "Wildcards" },
  { value: "acm", label: "ACM" },
  { value: "gcp", label: "Cert Manager" },
];

function needsAttention(i: CertItem) {
  return (i.status !== "ok" && i.status !== "unknown") || i.problems.some((p) => p.severity !== "info");
}

const ACTION_TONE = { create: "warning", update: "warning", none: "success" } as const;

/** Plan, then apply, control-hub's repair of the DNS records renewal needs. Admins only. */
function RepairPanel({ item, onApplied, autoPlan }: { item: CertItem; onApplied: () => void; autoPlan: boolean }) {
  const [plan, setPlan] = useState<CertRepairResult | null>(null);
  const [done, setDone] = useState<CertRepairResult | null>(null);
  const [busy, setBusy] = useState<"plan" | "apply" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const run = async (dryRun: boolean) => {
    setBusy(dryRun ? "plan" : "apply");
    setError(null);
    try {
      const res = await api.repairCertificate(item.key, dryRun);
      if (dryRun) setPlan(res);
      else {
        setDone(res);
        onApplied();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };
  // Opened from the row's Renew button: show the plan straight away.
  useEffect(() => {
    if (autoPlan) void run(true);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const shown = done ?? plan;
  const toChange = plan?.changes.filter((c) => c.action !== "none").length ?? 0;
  return (
    <div className="col-span-full rounded-md border border-hairline bg-white p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="font-semibold">Renew certificate</div>
          <div className="text-xs text-muted">
            {item.repair?.kind === "acm-validation" ? "ACM" : "Certificate Manager"} renews this certificate itself once the DNS records it validates
            against are in place. control-hub checks those records and recreates any that are missing or wrong (taken from the cloud API, in its own DNS
            zones only, never deleting anything), which lets renewal go through. Runs with your admin login.
          </div>
        </div>
        <div className="flex gap-2">
          {!done && (
            <button type="button" className="btn" onClick={() => run(true)} disabled={busy !== null}>
              {busy === "plan" && <Spinner />}
              {plan ? "Re-check" : "Check renewal"}
            </button>
          )}
          {plan && !done && (
            <button type="button" className="btn btn-primary" onClick={() => run(false)} disabled={busy !== null || toChange === 0}>
              {busy === "apply" && <Spinner />}
              {toChange === 0 ? "Records already correct" : `Renew: write ${toChange} ${toChange === 1 ? "record" : "records"}`}
            </button>
          )}
        </div>
      </div>
      {error && (
        <div className="mt-2 text-crit-fg" role="alert">
          {error}
        </div>
      )}
      {shown && (
        <div className="mt-2 overflow-x-auto">
          <table className="table min-w-[640px]">
            <thead>
              <tr>
                <th scope="col">Record (CNAME)</th>
                <th scope="col">Now</th>
                <th scope="col">Should be</th>
                <th scope="col">{done ? "Result" : "Change"}</th>
              </tr>
            </thead>
            <tbody>
              {shown.changes.map((c) => (
                <tr key={c.name}>
                  <td className="break-all font-mono text-xs">{c.name}</td>
                  <td className="break-all font-mono text-xs">{c.current || <span className="text-crit-fg">missing</span>}</td>
                  <td className="break-all font-mono text-xs">{c.desired}</td>
                  <td>
                    {c.error ? (
                      <Pill tone="critical">failed: {c.error}</Pill>
                    ) : done ? (
                      <Pill tone={c.applied ? "success" : "neutral"}>{c.applied ? "written" : "unchanged"}</Pill>
                    ) : (
                      <Pill tone={ACTION_TONE[c.action]}>{c.action === "none" ? "correct" : c.action}</Pill>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {done && <div className="mt-2 text-muted">{done.next} The certificate list refreshes after the next scan.</div>}
        </div>
      )}
    </div>
  );
}

const STEP_LABEL: Record<WildcardResult["step"], string> = {
  "renew-in-acm": "Renew in ACM",
  sync: "Sync renewed certificate",
  "up-to-date": "Up to date",
  manual: "Renew by hand",
};

/** Renew & sync of control-hub's wildcard: renew in ACM, then copy to control-hub and every tenant cluster. */
function WildcardPanel({ item, onApplied, autoPlan }: { item: CertItem; onApplied: () => void; autoPlan: boolean }) {
  const [plan, setPlan] = useState<WildcardResult | null>(null);
  const [done, setDone] = useState<WildcardResult | null>(null);
  const [force, setForce] = useState(false);
  const [busy, setBusy] = useState<"plan" | "apply" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const run = async (dryRun: boolean) => {
    setBusy(dryRun ? "plan" : "apply");
    setError(null);
    try {
      const res = await api.renewWildcard(item.key, dryRun, force);
      if (dryRun) {
        setPlan(res);
        setDone(null);
      } else {
        setDone(res);
        onApplied();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };
  useEffect(() => {
    if (autoPlan) void run(true);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const shown = done ?? plan;
  const updates = plan?.changes.filter((c) => c.action === "update").length ?? 0;
  const canApply =
    plan && !done && (plan.step === "sync" ? updates > 0 : plan.step === "renew-in-acm" && !plan.source?.renewalStatus?.startsWith("PENDING"));
  const applyLabel = plan?.step === "sync" ? `Sync to ${updates} ${updates === 1 ? "secret" : "secrets"}` : "Request renewal in ACM";
  const day = (v?: string) => (v ? new Date(v).toISOString().slice(0, 10) : "—");
  return (
    <div className="col-span-full rounded-md border border-hairline bg-white p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="font-semibold">Renew &amp; sync wildcard</div>
          <div className="text-xs text-muted">
            ACM renews this exported certificate; control-hub then exports it and updates its own secret and the copy on every tenant cluster
            (only where that copy is an older certificate for the same domain). Each click does the next step. Runs with your admin login.
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {plan?.step === "up-to-date" && !done && (
            <label className="flex items-center gap-1 text-xs text-muted">
              <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} /> renew early (charged)
            </label>
          )}
          <button type="button" className="btn" onClick={() => run(true)} disabled={busy !== null}>
            {busy === "plan" && <Spinner />}
            {plan ? "Re-check" : "Check renewal"}
          </button>
          {plan && !done && (canApply || (plan.step === "up-to-date" && force)) && (
            <button type="button" className="btn btn-primary" onClick={() => run(false)} disabled={busy !== null}>
              {busy === "apply" && <Spinner />}
              {plan.step === "up-to-date" ? "Request renewal in ACM" : applyLabel}
            </button>
          )}
        </div>
      </div>
      {error && (
        <div className="mt-2 text-crit-fg" role="alert">
          {error}
        </div>
      )}
      {shown && (
        <div className="mt-2 flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2 text-[13px]">
            <Pill tone={shown.step === "sync" || shown.step === "renew-in-acm" ? "warning" : shown.step === "manual" ? "critical" : "success"}>
              {STEP_LABEL[shown.step]}
            </Pill>
            <span className="text-muted">
              {shown.domain} · secret expires {day(shown.currentExpiry)}
              {shown.source && ` · ACM (${shown.source.region}) expires ${day(shown.source.notAfter)}`}
              {shown.source?.renewalStatus && ` · ${shown.source.renewalStatus}`}
            </span>
          </div>
          <div className="text-[13px]">{shown.next}</div>
          {shown.changes.length > 0 && (
            <div className="overflow-x-auto">
              <table className="table min-w-[640px]">
                <thead>
                  <tr>
                    <th scope="col">Where</th>
                    <th scope="col">Secret</th>
                    <th scope="col">Now expires</th>
                    <th scope="col">After</th>
                    <th scope="col">{done ? "Result" : "Change"}</th>
                  </tr>
                </thead>
                <tbody>
                  {shown.changes.map((c) => (
                    <tr key={c.target + c.secret}>
                      <td>{c.target}</td>
                      <td className="break-all font-mono text-xs">{c.secret}</td>
                      <td className="whitespace-nowrap">{day(c.currentExpiry)}</td>
                      <td className="whitespace-nowrap">{c.newExpiry ? day(c.newExpiry) : "—"}</td>
                      <td>
                        {c.error ? (
                          <Pill tone="critical">failed: {c.error}</Pill>
                        ) : done ? (
                          <Pill tone={c.applied ? "success" : "neutral"}>{c.applied ? "updated" : c.action === "current" ? "already current" : "skipped"}</Pill>
                        ) : (
                          <span title={c.reason}>
                            <Pill tone={c.action === "update" ? "warning" : c.action === "current" ? "success" : "neutral"}>
                              {c.action === "update" ? "update" : c.action === "current" ? "current" : "skip"}
                            </Pill>
                            {c.reason && <span className="ml-1 text-xs text-muted">{c.reason}</span>}
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Detail({ item, canRepair, onRepaired, autoPlan }: { item: CertItem; canRepair: boolean; onRepaired: () => void; autoPlan: boolean }) {
  return (
    <div className="grid gap-4 bg-slate-50 px-4 py-3 text-[13px] md:grid-cols-2">
      {canRepair && item.repair?.kind === "wildcard-renew" && <WildcardPanel item={item} onApplied={onRepaired} autoPlan={autoPlan} />}
      {canRepair && item.repair && item.repair.kind !== "wildcard-renew" && <RepairPanel item={item} onApplied={onRepaired} autoPlan={autoPlan} />}
      <div className="min-w-0">
        {item.problems.length > 0 && (
          <>
            <h3 className="mb-1 mt-0 text-[13px] font-semibold">Problems</h3>
            <ul className="m-0 mb-3 flex list-none flex-col gap-1.5 p-0">
              {item.problems.map((p, i) => (
                <li key={i} className="flex gap-2">
                  <Pill tone={p.severity === "critical" ? "critical" : p.severity === "warning" ? "warning" : "info"}>{p.severity}</Pill>
                  <span className="min-w-0 break-words">{p.message}</span>
                </li>
              ))}
            </ul>
          </>
        )}
        <h3 className="mb-1 mt-0 text-[13px] font-semibold">Checks</h3>
        {item.checks.length === 0 ? (
          <div className="text-muted">No checks ran.</div>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {item.checks.map((c, i) => (
              <li key={i} className="flex gap-2">
                <span className={c.ok ? "text-ok-fg" : "text-crit-fg"} aria-label={c.ok ? "passed" : "failed"}>
                  {c.ok ? "✓" : "✗"}
                </span>
                <span className="font-medium">{c.name}</span>
                <span className="min-w-0 break-all text-muted">{c.detail}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
      <dl className="m-0 grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1">
        <dt className="text-muted">Subject</dt>
        <dd className="m-0 break-all font-mono text-xs">{item.subject || "—"}</dd>
        <dt className="text-muted">Issuer</dt>
        <dd className="m-0">{item.issuer || "—"}</dd>
        <dt className="text-muted">Expires</dt>
        <dd className="m-0">{item.notAfter ? formatDateTime(item.notAfter) : "—"}</dd>
        <dt className="text-muted">Source</dt>
        <dd className="m-0 break-all">
          {SOURCE_LABEL[item.source]}
          {item.sourceRef && <div className="font-mono text-xs text-muted">{item.sourceRef}</div>}
        </dd>
        <dt className="text-muted">Auto-renew</dt>
        <dd className="m-0">{item.autoRenew}</dd>
        {item.sans && item.sans.length > 0 && (
          <>
            <dt className="text-muted">Names</dt>
            <dd className="m-0 break-all font-mono text-xs">{item.sans.join(", ")}</dd>
          </>
        )}
        {item.usedBy && item.usedBy.length > 0 && (
          <>
            <dt className="text-muted">Served by</dt>
            <dd className="m-0 break-all font-mono text-xs">{item.usedBy.join(", ")}</dd>
          </>
        )}
      </dl>
    </div>
  );
}

export default function CertificatesPage() {
  const { me } = useMe();
  const inv = useApi(() => api.certificates());
  const [filter, setFilter] = useState<(typeof FILTERS)[number]["value"]>("all");
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [renewing, setRenewing] = useState<string | null>(null);
  const [queued, setQueued] = useState(false);

  const items = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (inv.data?.items ?? []).filter((i) => {
      if (filter === "problems" ? !needsAttention(i) : filter !== "all" && i.kind !== filter) return false;
      if (!needle) return true;
      return [i.host, i.tenant, i.sourceRef, ...(i.usedBy ?? [])].some((s) => s?.toLowerCase().includes(needle));
    });
  }, [inv.data, filter, q]);

  const rescan = async () => {
    await api.scanCertificates();
    setQueued(true);
  };

  const counts = inv.data?.counts;
  return (
    <Page>
      <PageHeader
        title="Certificates"
        subtitle="Every certificate tenants depend on, soonest expiry first. Each scan also checks what renewal needs: the host's DNS record, ACM validation CNAMEs and Certificate Manager DNS authorizations."
      >
        {me.isAdmin && (
          <button type="button" className="btn" onClick={rescan} disabled={queued || inv.data?.running}>
            {(queued || inv.data?.running) && <Spinner />}
            {inv.data?.running ? "Scanning…" : queued ? "Scan queued" : "Rescan now"}
          </button>
        )}
      </PageHeader>

      {inv.error ? (
        <ErrorCard title="Couldn't load certificates" error={inv.error} onRetry={inv.reload} />
      ) : !inv.data ? (
        <div className="card">
          <Loading label="Loading certificates…" />
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-muted">
            <span>
              {inv.data.scannedAt
                ? `Scanned ${relativeTime(inv.data.scannedAt)} · rescans every ${Math.round(inv.data.intervalMinutes / 60)}h`
                : "The first scan is running."}
            </span>
            {counts && (
              <span>
                <span className="font-semibold text-crit-fg">{counts.expired + counts.critical}</span> critical ·{" "}
                <span className="font-semibold text-warn-fg">{counts.warning}</span> warning · {counts.ok} OK
                {counts.unknown > 0 ? ` · ${counts.unknown} unreachable` : ""}
              </span>
            )}
          </div>
          {inv.data.errors.length > 0 && (
            <div className="card border-warn-border p-3 text-[13px]" role="status">
              <div className="font-medium text-warn-fg">Some sources could not be read, so their certificates are missing:</div>
              <ul className="mb-0 mt-1 pl-5 text-muted">
                {inv.data.errors.map((e) => (
                  <li key={e} className="break-all">
                    {e}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <section className="card" aria-labelledby="certs-list">
            <div className="card-head flex-wrap gap-2">
              <h2 id="certs-list" className="h2">
                {items.length} {items.length === 1 ? "certificate" : "certificates"}
              </h2>
              <div className="flex flex-wrap items-center gap-2">
                <div className="flex flex-wrap gap-1.5" role="group" aria-label="Filter certificates">
                  {FILTERS.map((f) => (
                    <button
                      key={f.value}
                      type="button"
                      aria-pressed={filter === f.value}
                      onClick={() => setFilter(f.value)}
                      className={`btn ${filter === f.value ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
                    >
                      {f.label}
                    </button>
                  ))}
                </div>
                <input
                  className="input w-56"
                  type="search"
                  placeholder="Filter by host or tenant"
                  aria-label="Filter by host or tenant"
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                />
              </div>
            </div>
            {items.length === 0 ? (
              <Empty title="No certificates">{inv.data.scannedAt ? "Nothing matches this filter." : "Results appear when the first scan finishes."}</Empty>
            ) : (
              <div className="overflow-x-auto">
                <table className="table min-w-[960px]">
                  <thead>
                    <tr>
                      <th scope="col">Status</th>
                      <th scope="col">Expires in</th>
                      <th scope="col">Host</th>
                      <th scope="col">Tenant</th>
                      <th scope="col">Type</th>
                      <th scope="col">Source</th>
                      <th scope="col">Problem</th>
                      {me.isAdmin && <th scope="col">Action</th>}
                    </tr>
                  </thead>
                  <tbody>
                    {items.map((i) => (
                      <Fragment key={i.key}>
                        <tr
                          className="cursor-pointer hover:bg-slate-50"
                          onClick={() => setOpen(open === i.key ? null : i.key)}
                          aria-expanded={open === i.key}
                        >
                          <td>
                            <CertStatusPill status={i.status} />
                          </td>
                          <td className={`whitespace-nowrap font-semibold ${i.status === "ok" || i.status === "unknown" ? "" : i.status === "warning" ? "text-warn-fg" : "text-crit-fg"}`}>
                            {daysText(i)}
                          </td>
                          <td className="break-all font-mono text-xs">
                            <button type="button" className="text-left text-ink hover:underline" onClick={() => setOpen(open === i.key ? null : i.key)}>
                              {i.host}
                            </button>
                          </td>
                          <td>
                            {i.tenant || "—"}
                            {(i.provider || i.realm) && (
                              <div className="text-xs text-muted">
                                {[i.provider && providerLabel(i.provider), i.realm, i.region].filter(Boolean).join(" · ")}
                              </div>
                            )}
                          </td>
                          <td>{KIND_LABEL[i.kind]}</td>
                          <td>
                            {SOURCE_LABEL[i.source]}
                            <div className="text-xs text-muted">auto-renew: {i.autoRenew}</div>
                          </td>
                          <td className="max-w-[360px] text-[13px]">{headline(i) || <span className="text-muted">—</span>}</td>
                          {me.isAdmin && (
                            <td onClick={(e) => e.stopPropagation()}>
                              {i.repair ? (
                                <button
                                  type="button"
                                  className={`btn whitespace-nowrap ${i.repair.broken ? "btn-primary" : ""}`}
                                  disabled={!inv.data!.repairEnabled}
                                  title={
                                    inv.data!.repairEnabled
                                      ? "Check and restore the DNS records renewal needs"
                                      : "Turned off until control-hub ships POST /api/certificates/repair (CERT_REPAIR_ENABLED)"
                                  }
                                  onClick={() => {
                                    setRenewing(i.key);
                                    setOpen(i.key);
                                  }}
                                >
                                  {i.repair.kind === "wildcard-renew" ? "Renew & sync" : "Renew"}
                                </button>
                              ) : (
                                <span className="text-xs text-muted" title="Not managed by ACM or Certificate Manager; renewed by hand">
                                  manual
                                </span>
                              )}
                            </td>
                          )}
                        </tr>
                        {open === i.key && (
                          <tr>
                            <td colSpan={me.isAdmin ? 8 : 7} className="p-0">
                              <Detail
                                item={i}
                                canRepair={me.isAdmin && inv.data!.repairEnabled}
                                onRepaired={() => setQueued(true)}
                                autoPlan={renewing === i.key}
                              />
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </Page>
  );
}
