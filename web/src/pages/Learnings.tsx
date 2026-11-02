import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api } from "../api/client";
import { LEARNING_CATEGORIES, type Learning, type LearningInput, type LearningStatus } from "../api/types";
import { Page } from "../components/Layout";
import { PlusIcon, SparkIcon, Spinner } from "../components/Icons";
import { Pill, type Tone } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { relativeTime } from "../lib/format";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";

const TABS: { status: LearningStatus; label: string }[] = [
  { status: "draft", label: "To review" },
  { status: "approved", label: "Approved" },
  { status: "retired", label: "Retired" },
];

const STATUS_TONE: Record<LearningStatus, Tone> = { draft: "warning", approved: "success", retired: "neutral" };

type Draft = Required<Omit<LearningInput, "status" | "clearReview">>;

const EMPTY: Draft = { title: "", category: "unknown", symptoms: "", rootCause: "", howToConfirm: "", fix: "", kgNodes: "" };

function toDraft(l: Learning): Draft {
  return { title: l.title, category: l.category || "unknown", symptoms: l.symptoms, rootCause: l.rootCause, howToConfirm: l.howToConfirm, fix: l.fix, kgNodes: l.kgNodes };
}

function Field({ id, label, hint, children }: { id: string; label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <label htmlFor={id} className="label">
        {label}
      </label>
      {children}
      {hint && <div className="mt-1 text-xs text-muted">{hint}</div>}
    </div>
  );
}

/** Editor for one learning; also the review panel for drafts. */
function Editor({ learning, onDone, onCancel }: { learning: Learning | null; onDone: (l: Learning | null) => void; onCancel: () => void }) {
  const [d, setD] = useState<Draft>(learning ? toDraft(learning) : EMPTY);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const firstRef = useRef<HTMLInputElement>(null);
  const set = (p: Partial<Draft>) => setD((x) => ({ ...x, ...p }));

  useEffect(() => {
    setD(learning ? toDraft(learning) : EMPTY);
    setError(null);
    firstRef.current?.focus();
  }, [learning]);

  const run = async (fn: () => Promise<Learning | null>) => {
    setBusy(true);
    setError(null);
    try {
      onDone(await fn());
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setBusy(false);
    }
  };
  const navigate = useNavigate();
  const [ruleNote, setRuleNote] = useState("");
  const draftRule = async () => {
    if (!learning) return;
    setBusy(true);
    setRuleNote("");
    try {
      const d = await api.draftRuleFromLearning(learning.id);
      if (!d.applicable) setRuleNote(`No log rule proposed: ${d.reason || "the symptoms hold no log message to match."}`);
      else navigate("/log-rules", { state: { draft: d.rule, note: `Drafted from learning ${learning.id}.` } });
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setBusy(false);
    }
  };
  const save = (status?: LearningStatus, clearReview?: boolean) =>
    run(() => (learning ? api.updateLearning(learning.id, { ...d, status, clearReview }) : api.createLearning({ ...d, status })));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save();
  };

  const status = learning?.status;
  return (
    <form className="card" onSubmit={onSubmit} aria-labelledby="lrn-editor">
      <div className="card-head flex-wrap gap-2">
        <div className="min-w-0">
          <h2 id="lrn-editor" className="h2">
            {learning ? (status === "draft" ? "Review draft" : "Edit learning") : "New learning"}
          </h2>
          {learning && (
            <div className="mt-0.5 text-xs text-muted">
              <span className="font-mono">{learning.id}</span> · by {learning.createdBy}
              {learning.approvedBy && ` · approved by ${learning.approvedBy}`}
            </div>
          )}
        </div>
        {status && <Pill tone={STATUS_TONE[status]}>{status}</Pill>}
      </div>
      <div className="grid gap-3 p-4">
        {status === "draft" && learning?.createdBy.startsWith("Copilot") && (
          <p className="m-0 rounded-md bg-slate-50 p-3 text-[13px] text-muted">
            The Copilot wrote this from an investigation. Check that it is a general pattern, that the symptoms are what someone would search
            for, and that it holds no customer details or secrets. It is used in investigations only after you approve it.
          </p>
        )}
        {learning?.reviewNote && (
          <div className="rounded-md border border-warn-border p-3 text-[13px]" role="status">
            <div className="font-medium text-warn-fg">An investigation contradicted this learning</div>
            <div className="mt-1 whitespace-pre-wrap">{learning.reviewNote}</div>
            <button type="button" className="btn btn-sm mt-2" disabled={busy} onClick={() => save(undefined, true)}>
              Mark as dealt with
            </button>
          </div>
        )}
        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] gap-3">
          <Field id="lrn-title" label="Title">
            <input id="lrn-title" ref={firstRef} className="input" value={d.title} required maxLength={200} onChange={(e) => set({ title: e.target.value })} />
          </Field>
          <Field id="lrn-category" label="Category">
            <select id="lrn-category" className="select min-h-[40px] w-full" value={d.category} onChange={(e) => set({ category: e.target.value })}>
              {LEARNING_CATEGORIES.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <Field id="lrn-symptoms" label="Symptoms" hint="Exact error strings, failed steps, statuses and alert names: what the Copilot searches by.">
          <textarea id="lrn-symptoms" className="input min-h-[72px] py-2 font-mono text-[13px]" rows={3} required value={d.symptoms} onChange={(e) => set({ symptoms: e.target.value })} />
        </Field>
        <Field id="lrn-cause" label="Root cause">
          <textarea id="lrn-cause" className="input min-h-[72px] py-2" rows={3} required value={d.rootCause} onChange={(e) => set({ rootCause: e.target.value })} />
        </Field>
        <Field id="lrn-confirm" label="How to confirm" hint="The specific checks that prove it, so the next investigation runs them first.">
          <textarea id="lrn-confirm" className="input min-h-[72px] py-2" rows={3} value={d.howToConfirm} onChange={(e) => set({ howToConfirm: e.target.value })} />
        </Field>
        <Field id="lrn-fix" label="Fix">
          <textarea id="lrn-fix" className="input min-h-[64px] py-2" rows={2} value={d.fix} onChange={(e) => set({ fix: e.target.value })} />
        </Field>
        <Field id="lrn-kg" label="Knowledge-graph nodes" hint="Optional, comma separated, e.g. 04-controlplane.">
          <input id="lrn-kg" className="input font-mono text-[13px]" value={d.kgNodes} onChange={(e) => set({ kgNodes: e.target.value })} />
        </Field>
        {learning && learning.investigations.length > 0 && (
          <div className="text-[13px]">
            <span className="text-muted">Seen in </span>
            {learning.investigations.map((id, i) => (
              <span key={id}>
                {i > 0 && ", "}
                <Link to={`/investigations/${encodeURIComponent(id)}`} className="font-mono">
                  {id}
                </Link>
              </span>
            ))}
            {learning.entities.length > 0 && <span className="text-muted"> · on {learning.entities.join(", ")}</span>}
          </div>
        )}
        {ruleNote && (
          <p className="m-0 text-[13px] text-muted" role="status">
            {ruleNote}
          </p>
        )}
        {error && <ErrorCard title="Couldn't save" error={error} />}
      </div>
      <div className="flex flex-wrap justify-end gap-2 border-t border-line px-4 py-3">
        {status === "draft" && (
          <button
            type="button"
            className="btn mr-auto text-crit-fg"
            disabled={busy}
            onClick={() => window.confirm("Delete this draft?") && run(() => api.deleteLearning(learning!.id).then(() => null))}
          >
            Delete draft
          </button>
        )}
        {status === "approved" && (
          <>
            <button type="button" className="btn" disabled={busy} onClick={() => save("retired")}>
              Retire
            </button>
            <button
              type="button"
              className="btn mr-auto"
              disabled={busy}
              onClick={draftRule}
              title="Claude drafts a log rule from the symptoms, so the monitors catch this without AI"
            >
              Draft log rule
            </button>
          </>
        )}
        {status === "retired" && (
          <button type="button" className="btn mr-auto" disabled={busy} onClick={() => save("approved")}>
            Restore
          </button>
        )}
        <button type="button" className="btn" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="btn" disabled={busy}>
          {learning ? "Save" : "Save as draft"}
        </button>
        {status !== "approved" && status !== "retired" && (
          <button type="button" className="btn btn-primary" disabled={busy} onClick={() => save("approved")}>
            {learning ? "Approve" : "Save and approve"}
          </button>
        )}
      </div>
    </form>
  );
}

export default function LearningsPage() {
  const { me } = useMe();
  const [tab, setTab] = useState<LearningStatus>("draft");
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  // Rewordings Claude suggested for the current query ("Search by meaning").
  const [alts, setAlts] = useState<string[]>([]);
  const [expanding, setExpanding] = useState(false);
  const [expandError, setExpandError] = useState("");
  const [selected, setSelected] = useState<Learning | null>(null);
  const [creating, setCreating] = useState(false);
  const list = useApi(
    () => (me.isAdmin ? api.learnings(tab, query, alts) : Promise.resolve([] as Learning[])),
    [tab, query, alts.join("\n"), me.isAdmin],
  );
  const drafts = useApi(() => (me.isAdmin ? api.learnings("draft") : Promise.resolve([] as Learning[])), [me.isAdmin]);

  useEffect(() => {
    const t = setTimeout(() => {
      setQuery(q.trim());
      setAlts([]);
      setExpandError("");
    }, 250);
    return () => clearTimeout(t);
  }, [q]);

  const searchByMeaning = async () => {
    if (!query) return;
    setExpanding(true);
    setExpandError("");
    try {
      const r = await api.expandLearningQuery(query);
      setAlts(r.alternatives ?? []);
    } catch (e) {
      setExpandError(e instanceof Error ? e.message : String(e));
    } finally {
      setExpanding(false);
    }
  };

  if (!me.isAdmin) {
    return (
      <Page>
        <PageHeader title="Learnings" subtitle="Patterns from past investigations that the Copilot checks first." />
        <div className="card">
          <Empty title="Admins only">Learnings hold root causes from investigations, so only console admins can review them.</Empty>
        </div>
      </Page>
    );
  }

  const done = (l: Learning | null) => {
    setSelected(null);
    setCreating(false);
    if (l && l.status !== tab) setTab(l.status);
    list.reload();
    drafts.reload();
  };
  const rows = list.data ?? [];
  const editing = creating || selected;

  return (
    <Page>
      <PageHeader
        title="Learnings"
        subtitle="Failure patterns distilled from investigations. The Copilot searches approved ones before it investigates, drafts new ones after, and you decide what it may rely on."
      >
        <button
          type="button"
          className="btn btn-primary"
          onClick={() => {
            setSelected(null);
            setCreating(true);
          }}
        >
          <PlusIcon size={14} /> New learning
        </button>
      </PageHeader>

      <div className="flex flex-wrap items-center gap-2">
        <div className="flex gap-1.5" role="tablist" aria-label="Status">
          {TABS.map((t) => (
            <button
              key={t.status}
              type="button"
              role="tab"
              aria-selected={tab === t.status}
              onClick={() => {
                setTab(t.status);
                setSelected(null);
              }}
              className={`btn ${tab === t.status ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
            >
              {t.label}
              {t.status === "draft" && (drafts.data?.length ?? 0) > 0 && (
                <span className="ml-1.5 rounded-full bg-warn-bg px-1.5 text-xs text-warn-fg">{drafts.data!.length}</span>
              )}
            </button>
          ))}
        </div>
        <label htmlFor="lrn-q" className="sr-only">
          Search learnings
        </label>
        <input
          id="lrn-q"
          className="input ml-auto w-full max-w-[320px]"
          placeholder="Search symptoms, causes…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <button
          type="button"
          className="btn"
          disabled={!query || expanding || alts.length > 0}
          onClick={searchByMeaning}
          title="Claude rewords your search (synonyms, likely causes, related errors) and searches for all of them"
        >
          {expanding ? <Spinner /> : <SparkIcon size={14} />} Search by meaning
        </button>
      </div>
      {(alts.length > 0 || expandError) && (
        <p className="m-0 text-sm text-muted">
          {expandError ? (
            <span className="text-crit-fg">{expandError}</span>
          ) : (
            <>Also searched: {alts.map((a) => `“${a}”`).join(", ")}</>
          )}
        </p>
      )}

      <div className={`grid gap-4 ${editing ? "lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]" : ""}`}>
        <section className="card min-w-0" aria-label="Learnings">
          {list.error ? (
            <div className="p-4">
              <ErrorCard title="Couldn't load learnings" error={list.error} onRetry={list.reload} />
            </div>
          ) : list.loading && !list.data ? (
            <Loading label="Loading learnings…" />
          ) : rows.length === 0 ? (
            <Empty title={query ? "Nothing matches" : tab === "draft" ? "Nothing to review" : `No ${tab} learnings`}>
              {tab === "draft"
                ? "The Copilot drafts a learning after an investigation that no existing learning covered."
                : "Approve drafts, or add a pattern you already know with New learning."}
            </Empty>
          ) : (
            <ul className="m-0 list-none divide-y divide-line p-0">
              {rows.map((l) => (
                <li key={l.id}>
                  <button
                    type="button"
                    onClick={() => {
                      setCreating(false);
                      setSelected(l);
                    }}
                    aria-current={selected?.id === l.id}
                    className={`block w-full px-4 py-3 text-left hover:bg-slate-50 ${selected?.id === l.id ? "bg-violet-tint" : ""}`}
                  >
                    <div className="flex items-start gap-2">
                      <span className="min-w-0 flex-1 font-medium text-ink">{l.title}</span>
                      {l.contradicted > 0 && l.reviewNote && <Pill tone="critical">contradicted</Pill>}
                      {l.status === "approved" && l.confirmed >= 3 && (
                        <span title="Confirmed by 3+ investigations: worth a log rule">
                          <Pill tone="violet">ready for a rule</Pill>
                        </span>
                      )}
                      {l.category && <Pill>{l.category}</Pill>}
                    </div>
                    <div className="mt-1 line-clamp-2 text-[13px] text-muted">{l.symptoms}</div>
                    <div className="mt-1 text-xs text-muted">
                      seen {l.timesSeen}× · confirmed {l.confirmed}
                      {l.contradicted > 0 && ` · contradicted ${l.contradicted}`} · last {relativeTime(l.lastSeen)}
                    </div>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
        {editing && (
          <Editor
            learning={selected}
            onDone={done}
            onCancel={() => {
              setSelected(null);
              setCreating(false);
            }}
          />
        )}
      </div>
    </Page>
  );
}
