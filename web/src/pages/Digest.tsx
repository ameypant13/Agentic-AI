import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { Spinner } from "../components/Icons";
import { Page } from "../components/Layout";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { formatDateTime, relativeTime } from "../lib/format";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";

export default function DigestPage() {
  const { me } = useMe();
  const d = useApi(() => (me.isAdmin ? api.digest() : Promise.reject(new Error("admins only"))), [me.isAdmin]);
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  const send = async () => {
    setSending(true);
    setResult(null);
    try {
      const r = await api.sendDigest();
      setResult({ ok: r.sent, text: r.message });
      d.reload();
    } catch (e) {
      setResult({ ok: false, text: e instanceof Error ? e.message : String(e) });
    } finally {
      setSending(false);
    }
  };

  if (!me.isAdmin) {
    return (
      <Page>
        <PageHeader title="Weekly digest" subtitle="The week across the fleet, posted to Slack." />
        <div className="card">
          <Empty title="Admins only">The digest names every investigation, so only console admins can see it.</Empty>
        </div>
      </Page>
    );
  }

  return (
    <Page>
      <PageHeader
        title="Weekly digest"
        subtitle="The last 7 days across the fleet, from the console's own data: findings, certificates, cost, investigations and what waits for review. No AI."
      >
        <button type="button" className="btn btn-primary" onClick={send} disabled={sending || !d.data}>
          {sending && <Spinner />}
          Send to Slack now
        </button>
      </PageHeader>
      {result && (
        <div role="status" className={`card p-3 text-[13px] ${result.ok ? "" : "border-warn-border"}`}>
          {result.text}
        </div>
      )}
      {d.error ? (
        <ErrorCard title="Couldn't build the digest" error={d.error} onRetry={d.reload} />
      ) : !d.data ? (
        <div className="card">
          <Loading label="Building the digest…" />
        </div>
      ) : (
        <>
          <div className="text-[13px] text-muted">
            Schedule: {d.data.schedule} · last sent {d.data.lastSent ? relativeTime(d.data.lastSent) : "never"} · covers{" "}
            {formatDateTime(d.data.digest.from)} → {formatDateTime(d.data.digest.to)}
          </div>
          <section className="card p-4" aria-label="Digest">
            <h2 className="h2 mb-2">{d.data.digest.headline}</h2>
            <div className="grid gap-4 md:grid-cols-2">
              {d.data.digest.sections.map((s) => (
                <div key={s.title} className="min-w-0">
                  <div className="mb-1 flex items-baseline gap-2">
                    <h3 className="m-0 text-[14px] font-semibold">{s.title}</h3>
                    {s.link && (
                      <Link to={s.link} className="text-xs">
                        open
                      </Link>
                    )}
                  </div>
                  <ul className="m-0 pl-5 text-[13px]">
                    {s.lines.map((l, i) => (
                      <li key={i} className={l.startsWith("  ") ? "ml-3 list-[circle]" : ""}>
                        {l.trim()}
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          </section>
          <details className="card p-4">
            <summary className="cursor-pointer text-[13px] font-medium">What Slack receives</summary>
            <pre className="m-0 mt-2 overflow-x-auto whitespace-pre-wrap rounded-btn border border-line bg-slate-50 p-3 font-mono text-xs">{d.data.slack}</pre>
          </details>
        </>
      )}
    </Page>
  );
}
