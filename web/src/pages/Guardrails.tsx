import { api } from "../api/client";
import { Page } from "../components/Layout";
import { PolicyModePill } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { formatDateTime } from "../lib/format";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";

function PolicyCard() {
  const policy = useApi(() => api.policy());
  return (
    <section className="card" aria-labelledby="pol">
      <div className="card-head">
        <h2 id="pol" className="h2">
          Action policy
        </h2>
        <span className="text-xs text-muted">Modes: Auto · Ask requester · Admin approval · Never</span>
      </div>
      {policy.error ? (
        <div className="p-4">
          <ErrorCard title="Couldn't load the policy" error={policy.error} onRetry={policy.reload} />
        </div>
      ) : policy.loading ? (
        <Loading label="Loading policy…" />
      ) : (policy.data ?? []).length === 0 ? (
        <Empty title="No policy rows" />
      ) : (
        <div className="overflow-x-auto">
          <table className="table min-w-[560px]">
            <thead>
              <tr>
                <th scope="col">Action</th>
                <th scope="col">Examples</th>
                <th scope="col">Mode</th>
              </tr>
            </thead>
            <tbody>
              {policy.data!.map((row) => (
                <tr key={row.action}>
                  <td className="font-medium">{row.action}</td>
                  <td className="text-muted">{row.examples}</td>
                  <td>
                    <PolicyModePill mode={row.mode} />
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

function AuditCard() {
  const audit = useApi(() => api.audit(100));
  return (
    <section className="card" aria-labelledby="aud">
      <div className="card-head">
        <h2 id="aud" className="h2">
          Audit log
        </h2>
        <span className="text-xs text-muted">Last 100 entries · admins only</span>
      </div>
      {audit.error ? (
        <div className="p-4">
          <ErrorCard title="Couldn't load the audit log" error={audit.error} onRetry={audit.reload} />
        </div>
      ) : audit.loading ? (
        <Loading label="Loading audit log…" />
      ) : (audit.data ?? []).length === 0 ? (
        <Empty title="No audit entries yet">Every action Copilot proposes or runs is recorded here.</Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="table min-w-[760px]">
            <thead>
              <tr>
                <th scope="col">Time</th>
                <th scope="col">Who</th>
                <th scope="col">Action</th>
                <th scope="col">Target</th>
                <th scope="col">Decision</th>
                <th scope="col">Detail</th>
              </tr>
            </thead>
            <tbody>
              {audit.data!.map((e, i) => (
                <tr key={`${e.at}-${i}`}>
                  <td className="whitespace-nowrap font-mono text-xs">{formatDateTime(e.at)}</td>
                  <td>{e.actor}</td>
                  <td className="font-mono text-xs">{e.action}</td>
                  <td>{e.target}</td>
                  <td>
                    <span className="pill bg-slate-100 text-slate-700">{e.decision}</span>
                  </td>
                  <td className="text-muted">{e.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

export default function GuardrailsPage() {
  const { me } = useMe();
  return (
    <Page>
      <PageHeader
        title="Guardrails"
        subtitle="What Copilot may do on its own, what needs a human, and what it never does. Every action still runs under the requester’s Control Hub permissions."
      />
      <PolicyCard />
      {me.isAdmin && <AuditCard />}
    </Page>
  );
}
