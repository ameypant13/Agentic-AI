import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { ReadinessReport, ReadinessStatus } from "../api/types";
import { CheckIcon, Spinner, XIcon, AlertIcon } from "../components/Icons";
import { Page } from "../components/Layout";
import { Pill, type Tone } from "../components/Pill";
import { useCopilotDock } from "../components/shell/CopilotDock";
import { Empty, ErrorCard, PageHeader } from "../components/States";
import { relativeTime, providerLabel } from "../lib/format";

const VERDICT: Record<ReadinessReport["verdict"], { label: string; tone: Tone; box: string }> = {
  go: { label: "Go", tone: "success", box: "border-ok-fg/30 bg-ok-bg" },
  caution: { label: "Go with care", tone: "warning", box: "border-warn-border bg-warn-bg" },
  "no-go": { label: "No-go", tone: "critical", box: "border-crit-fg/30 bg-crit-bg" },
  unknown: { label: "Can't tell", tone: "neutral", box: "border-line bg-slate-50" },
};

const STATUS: Record<ReadinessStatus, { label: string; tone: Tone }> = {
  pass: { label: "pass", tone: "success" },
  warn: { label: "warn", tone: "warning" },
  fail: { label: "fail", tone: "critical" },
  unknown: { label: "unknown", tone: "neutral" },
};

function StatusIcon({ s }: { s: ReadinessStatus }) {
  if (s === "pass") return <CheckIcon size={16} className="text-ok-fg" />;
  if (s === "fail") return <XIcon size={16} className="text-crit-fg" />;
  if (s === "warn") return <AlertIcon size={16} className="text-warn-fg" />;
  return <span className="inline-block h-4 w-4 text-center text-muted">?</span>;
}

const ORDER: Record<ReadinessStatus, number> = { fail: 0, warn: 1, unknown: 2, pass: 3 };

export default function ReadinessPage() {
  const [params, setParams] = useSearchParams();
  const cp = params.get("cp") ?? "";
  const [q, setQ] = useState(cp);
  const [rep, setRep] = useState<ReadinessReport | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(false);
  const dock = useCopilotDock();

  useEffect(() => {
    if (!cp) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    setRep(null);
    api
      .readiness(cp)
      .then((r) => !cancelled && setRep(r))
      .catch((e) => !cancelled && setError(e instanceof Error ? e : new Error(String(e))))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [cp]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (q.trim()) setParams({ cp: q.trim() });
  };
  const checks = rep ? [...rep.checks].sort((a, b) => ORDER[a.status] - ORDER[b.status]) : [];
  const v = rep ? VERDICT[rep.verdict] : null;

  return (
    <Page>
      <PageHeader
        title="Upgrade readiness"
        subtitle="Twelve checks before you upgrade a tenant: control plane, findings, certificates, nodes, pods, Helm, Postgres, disk, Athena, memory history, recent changes and orphaned go-sandboxes."
      />
      <form className="flex max-w-[560px] gap-2" onSubmit={submit} role="search">
        <label htmlFor="rd-cp" className="sr-only">
          Control plane
        </label>
        <input id="rd-cp" className="input" placeholder="Tenant name or control plane ID, e.g. capgroup" value={q} onChange={(e) => setQ(e.target.value)} />
        <button type="submit" className="btn btn-primary" disabled={loading || !q.trim()}>
          {loading && <Spinner />}
          Check
        </button>
      </form>

      {error ? (
        <ErrorCard title="Couldn't run the checks" error={error} />
      ) : loading ? (
        <div className="card p-6 text-[13px] text-muted">
          <Spinner /> Running twelve checks against control-hub, the cluster, Grafana and Athena…
        </div>
      ) : !rep ? (
        <div className="card">
          <Empty title="Pick a tenant">Enter a tenant or control plane to see whether it's safe to upgrade.</Empty>
        </div>
      ) : (
        <>
          <section className={`card border p-4 ${v!.box}`} aria-label="Verdict">
            <div className="flex flex-wrap items-center gap-3">
              <span className="text-[22px] font-semibold">{v!.label}</span>
              <span className="text-[15px] font-medium">{rep.controlPlane.Customer || rep.controlPlane.ID}</span>
              <span className="text-[13px] text-muted">
                {[providerLabel(rep.controlPlane.Provider), rep.controlPlane.Region, rep.controlPlane.ProphecyVersion && `Prophecy ${rep.controlPlane.ProphecyVersion}`, rep.controlPlane.ClusterName]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
              <span className="ml-auto flex gap-1.5">
                <button type="button" className="btn btn-sm" onClick={() => setParams({ cp })}>
                  Re-run
                </button>
                <button
                  type="button"
                  className="btn btn-sm"
                  onClick={() =>
                    dock.ask(
                      `Explain the upgrade readiness of ${rep.controlPlane.Customer || rep.controlPlane.ID} (verdict ${rep.verdict}) and what to do first. ` +
                        `Run upgrade_readiness for it and investigate the failing checks.`,
                    )
                  }
                >
                  Ask Copilot
                </button>
              </span>
            </div>
            <p className="mb-0 mt-1 text-[13px]">{rep.summary}</p>
            <p className="mb-0 mt-1 text-xs text-muted">Checked {relativeTime(rep.at)}</p>
          </section>
          <section className="card" aria-label="Checks">
            <ul className="m-0 list-none divide-y divide-line p-0">
              {checks.map((c) => (
                <li key={c.id} className="flex gap-3 px-4 py-3">
                  <span className="mt-0.5 flex-none">
                    <StatusIcon s={c.status} />
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{c.name}</span>
                      <Pill tone={STATUS[c.status].tone}>{STATUS[c.status].label}</Pill>
                    </div>
                    <div className="mt-0.5 break-words text-[13px] text-muted">{c.detail}</div>
                    {c.hint && <div className="mt-1 text-[13px]">{c.hint}</div>}
                  </div>
                </li>
              ))}
            </ul>
          </section>
        </>
      )}
    </Page>
  );
}
