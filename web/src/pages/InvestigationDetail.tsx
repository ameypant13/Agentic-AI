import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import type { Evidence, Investigation } from "../api/types";
import { ActionCard } from "../components/ActionButtons";
import { CheckIcon, LockIcon, XIcon } from "../components/Icons";
import { Page } from "../components/Layout";
import { Markdown } from "../components/Markdown";
import { ConfidencePill, Pill } from "../components/Pill";
import { ErrorCard, Loading } from "../components/States";
import { formatDateTime, formatTime } from "../lib/format";
import { useApi } from "../lib/useApi";

function EvidenceRow({ ev }: { ev: Evidence }) {
  return (
    <li className="grid grid-cols-[64px_minmax(0,1fr)] gap-3 border-t border-hairline py-2.5 text-[13px]">
      <span className="font-mono text-muted" title={formatDateTime(ev.at)}>
        {formatTime(ev.at)}
      </span>
      <div className="min-w-0">
        <div className="flex items-start gap-1.5">
          {ev.ok ? (
            <CheckIcon size={13} className="mt-0.5 flex-none text-ok-fg" aria-hidden="true" />
          ) : (
            <XIcon size={13} className="mt-0.5 flex-none text-crit-fg" aria-hidden="true" />
          )}
          <span className="sr-only">{ev.ok ? "Succeeded:" : "Failed:"}</span>
          <div className="min-w-0 break-words">
            <span className="font-mono">{ev.tool}</span>
            {ev.input && <span className="font-mono text-muted"> {ev.input}</span>} — {ev.summary}
          </div>
        </div>
      </div>
    </li>
  );
}

function AdminCards({ inv }: { inv: Investigation }) {
  return (
    <>
      <section className="card border-violet-border p-4" aria-labelledby="rc">
        <div className="flex items-center gap-2">
          <LockIcon size={14} className="text-violet" />
          <h2 id="rc" className="h2 text-[15px]">
            Root cause
          </h2>
          <span className="text-xs text-muted">admins only</span>
        </div>
        <div className="mt-2.5">
          {inv.rootCause ? <Markdown text={inv.rootCause} /> : <p className="m-0 text-sm text-muted">Not determined.</p>}
        </div>
        <div className="mt-3 grid grid-cols-[repeat(auto-fit,minmax(min(160px,100%),1fr))] gap-3 text-[13px]">
          <div>
            <div className="text-muted">Category</div>
            <div>{inv.category || "—"}</div>
          </div>
          <div>
            <div className="text-muted">Confidence</div>
            <div className="capitalize">{inv.confidence}</div>
          </div>
          <div>
            <div className="text-muted">Entity</div>
            <div className="break-all font-mono text-xs">{inv.entityId}</div>
          </div>
        </div>
      </section>

      {inv.recommendedFix !== undefined && (
        <section className="card p-4" aria-labelledby="fx">
          <h2 id="fx" className="h2 text-[15px]">
            Recommended fix
          </h2>
          <div className="mt-2.5">
            {inv.recommendedFix ? <Markdown text={inv.recommendedFix} /> : <p className="m-0 text-sm text-muted">None recorded.</p>}
          </div>
        </section>
      )}
    </>
  );
}

function Detail({ inv }: { inv: Investigation }) {
  const isAdminView = inv.rootCause !== undefined || inv.evidence !== undefined || inv.recommendedFix !== undefined;
  return (
    <>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="font-mono text-xs text-muted">
            <Link to="/investigations">Investigations</Link> / {inv.id}
          </div>
          <h1 className="mb-0 mt-1.5 text-2xl font-semibold">{inv.title}</h1>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-[13px] text-muted">
            <ConfidencePill confidence={inv.confidence} />
            {inv.category && <Pill>{inv.category}</Pill>}
            <span>
              {inv.entityName} · opened by {inv.requester} via Copilot · {formatDateTime(inv.createdAt)}
            </span>
          </div>
        </div>
        {inv.chatId && (
          <Link to={`/copilot/${encodeURIComponent(inv.chatId)}`} className="btn">
            Open chat
          </Link>
        )}
      </div>

      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(420px,100%),1fr))] items-start gap-4">
        <div className="flex flex-col gap-4">
          {isAdminView && <AdminCards inv={inv} />}

          <section className="card p-4" aria-labelledby="req">
            <h2 id="req" className="h2 text-[15px]">
              {isAdminView ? "What the requester was told" : "Summary"}
            </h2>
            <blockquote className="mb-0 ml-0 mr-0 mt-2.5 rounded-btn bg-slate-50 p-3 text-[13px] leading-relaxed text-slate-700">
              {inv.summaryForRequester ? <Markdown text={inv.summaryForRequester} /> : "No summary yet."}
            </blockquote>
          </section>

          {!isAdminView && <ActionsCard inv={inv} />}
        </div>

        {isAdminView && (
          <div className="flex flex-col gap-4">
            <section className="card p-4" aria-labelledby="evid">
              <h2 id="evid" className="h2 text-[15px]">
                Evidence · tool calls
              </h2>
              {(inv.evidence ?? []).length === 0 ? (
                <p className="mb-0 mt-2 text-[13px] text-muted">No tool calls recorded.</p>
              ) : (
                <ol className="m-0 mt-2 list-none p-0">
                  {inv.evidence!.map((ev, i) => (
                    <EvidenceRow key={`${ev.at}-${i}`} ev={ev} />
                  ))}
                </ol>
              )}
              {inv.chatId && (
                <Link to={`/copilot/${encodeURIComponent(inv.chatId)}`} className="text-[13px]">
                  Full transcript
                </Link>
              )}
            </section>
            <ActionsCard inv={inv} />
          </div>
        )}
      </div>
    </>
  );
}

function ActionsCard({ inv }: { inv: Investigation }) {
  return (
    <section className="card p-4" aria-labelledby="acts">
      <h2 id="acts" className="h2 text-[15px]">
        Actions
      </h2>
      {inv.actions.length === 0 ? (
        <p className="mb-0 mt-2 text-[13px] text-muted">No actions were proposed.</p>
      ) : (
        <div className="mt-2.5 flex flex-col gap-3">
          {inv.actions.map((a) => (
            <ActionCard key={a.id} action={a} />
          ))}
        </div>
      )}
    </section>
  );
}

export default function InvestigationDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const inv = useApi(() => api.investigation(id), [id]);

  return (
    <Page>
      {inv.error ? (
        <>
          <div className="font-mono text-xs text-muted">
            <Link to="/investigations">Investigations</Link> / {id}
          </div>
          <ErrorCard title="Couldn't load this investigation" error={inv.error} onRetry={inv.reload} />
        </>
      ) : inv.loading || !inv.data ? (
        <div className="card">
          <Loading label="Loading investigation…" />
        </div>
      ) : (
        <Detail inv={inv.data} />
      )}
    </Page>
  );
}
