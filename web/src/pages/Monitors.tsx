import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { FindingStateFilter } from "../api/types";
import { Page } from "../components/Layout";
import { FindingStatePill, Pill, SeverityPill } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { formatDateTime, providerLabel } from "../lib/format";
import { useApi } from "../lib/useApi";

const FILTERS: { value: FindingStateFilter; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "resolved", label: "Resolved" },
  { value: "all", label: "All" },
];

function FindingsCard() {
  const [state, setState] = useState<FindingStateFilter>("open");
  const findings = useApi(() => api.findings(state), [state]);

  return (
    <section className="card" aria-labelledby="fnd">
      <div className="card-head">
        <div>
          <h2 id="fnd" className="h2">
            Findings
          </h2>
          <div className="mt-0.5 text-xs text-muted">Everything the monitors have flagged.</div>
        </div>
        <div className="flex items-center gap-2">
          <label htmlFor="state" className="text-[13px] text-muted">
            State
          </label>
          <select id="state" className="select" value={state} onChange={(e) => setState(e.target.value as FindingStateFilter)}>
            {FILTERS.map((f) => (
              <option key={f.value} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
        </div>
      </div>
      {findings.error ? (
        <div className="p-4">
          <ErrorCard title="Couldn't load findings" error={findings.error} onRetry={findings.reload} />
        </div>
      ) : findings.loading ? (
        <Loading label="Loading findings…" />
      ) : (findings.data ?? []).length === 0 ? (
        <Empty title={state === "open" ? "Nothing needs attention" : "No findings"}>
          {state === "open" ? "No open findings right now." : `No ${state === "all" ? "" : state + " "}findings to show.`}
        </Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="table min-w-[860px]">
            <thead>
              <tr>
                <th scope="col">Severity</th>
                <th scope="col">Tenant</th>
                <th scope="col">Finding</th>
                <th scope="col">State</th>
                <th scope="col">First seen</th>
                <th scope="col">Last seen</th>
                <th scope="col">
                  <span className="sr-only">Investigation</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {findings.data!.map((f) => (
                <tr key={f.id}>
                  <td>
                    <SeverityPill severity={f.severity} />
                  </td>
                  <td>
                    <div className="font-medium">{f.entityName}</div>
                    <div className="font-mono text-xs text-muted">
                      {[f.provider && providerLabel(f.provider), f.realm, f.region].filter(Boolean).join(" · ")}
                    </div>
                  </td>
                  <td>
                    <div className="font-medium">{f.title}</div>
                    <div className="text-xs text-muted">{f.detail}</div>
                  </td>
                  <td>
                    <FindingStatePill state={f.state} />
                    {f.resolvedAt && <div className="mt-1 text-xs text-muted">{formatDateTime(f.resolvedAt)}</div>}
                  </td>
                  <td className="whitespace-nowrap font-mono text-xs text-muted">{formatDateTime(f.firstSeenAt)}</td>
                  <td className="whitespace-nowrap font-mono text-xs text-muted">{formatDateTime(f.lastSeenAt)}</td>
                  <td className="whitespace-nowrap text-right">
                    {f.investigationId && <Link to={`/investigations/${encodeURIComponent(f.investigationId)}`}>View RCA</Link>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function MonitorsCard() {
  const monitors = useApi(() => api.monitors());
  return (
    <section className="card" aria-labelledby="mon">
      <div className="card-head">
        <div>
          <h2 id="mon" className="h2">
            Monitor definitions
          </h2>
          <div className="mt-0.5 text-xs text-muted">Scheduled checks. A hit opens a finding on Fleet overview.</div>
        </div>
      </div>
      {monitors.error ? (
        <div className="p-4">
          <ErrorCard title="Couldn't load monitors" error={monitors.error} onRetry={monitors.reload} />
        </div>
      ) : monitors.loading ? (
        <Loading label="Loading monitors…" />
      ) : (monitors.data ?? []).length === 0 ? (
        <Empty title="No monitors configured" />
      ) : (
        <div className="overflow-x-auto">
          <table className="table min-w-[640px]">
            <thead>
              <tr>
                <th scope="col">Monitor</th>
                <th scope="col">Condition</th>
                <th scope="col">Every</th>
                <th scope="col">Notify</th>
                <th scope="col">Status</th>
              </tr>
            </thead>
            <tbody>
              {monitors.data!.map((m) => (
                <tr key={m.key}>
                  <td>
                    <div className="font-medium">{m.name}</div>
                    <div className="font-mono text-xs text-muted">{m.key}</div>
                  </td>
                  <td className="font-mono text-xs">{m.condition}</td>
                  <td className="whitespace-nowrap">{m.interval}</td>
                  <td>{m.notify || <span className="text-muted">—</span>}</td>
                  <td>{m.enabled ? <Pill tone="success">Enabled</Pill> : <Pill>Disabled</Pill>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

export default function MonitorsPage() {
  return (
    <Page>
      <PageHeader title="Monitors" subtitle="Findings raised by scheduled checks, and the checks themselves." />
      <FindingsCard />
      <MonitorsCard />
    </Page>
  );
}
