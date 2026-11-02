import { Fragment, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { LogPattern, LogRule, LogRuleInput, LogSeverity } from "../api/types";
import { CheckIcon, PlusIcon, Spinner, XIcon } from "../components/Icons";
import { Page } from "../components/Layout";
import { LOG_SEV_LABEL, LogSeverityPill } from "../components/LogParser";
import { Pill } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { relativeTime } from "../lib/format";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";
import { MetricRulesView } from "./MetricRules";

const TEST_DEBOUNCE_MS = 250;
const SEVERITIES: LogSeverity[] = ["error", "warning", "info"];
/** Service-filter values that are not a service name. */
const ANY_SERVICE = "";
const ALL_PODS = "\u0000all-pods";

const list = (v: string[] | null | undefined) => v ?? [];

// ---------------------------------------------------------------- JS preview of a Go RE2 pattern

/** Compiles a Go RE2 pattern as a JS RegExp for a preview. Leading `(?i)`-style flags become JS flags. */
function compilePreview(pattern: string): { re: RegExp | null; error: string } {
  if (!pattern) return { re: null, error: "" };
  let src = pattern;
  let flags = "";
  const m = /^\(\?([imsU]+)\)/.exec(src);
  if (m) {
    src = src.slice(m[0].length);
    for (const f of m[1]) if (f !== "U" && !flags.includes(f)) flags += f;
  }
  try {
    return { re: new RegExp(src, flags), error: "" };
  } catch (e) {
    return { re: null, error: e instanceof Error ? e.message : String(e) };
  }
}

// ---------------------------------------------------------------- tester

type TestState = { line: string; loading: boolean; data?: LogRule[]; error?: Error };

function Tester({ onOpenRule }: { onOpenRule: (id: string) => void }) {
  const [line, setLine] = useState("");
  const [pod, setPod] = useState("");
  const [state, setState] = useState<TestState>({ line: "", loading: false });
  const callId = useRef(0);

  useEffect(() => {
    const id = ++callId.current;
    if (!line.trim()) {
      setState({ line: "", loading: false });
      return;
    }
    setState((s) => ({ ...s, loading: true }));
    const t = window.setTimeout(() => {
      api.testLogLine(line, pod.trim() || undefined).then(
        (data) => id === callId.current && setState({ line, loading: false, data }),
        (e: unknown) => id === callId.current && setState({ line, loading: false, error: e instanceof Error ? e : new Error(String(e)) }),
      );
    }, TEST_DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [line, pod]);

  return (
    <section className="card flex flex-col gap-3 p-4" aria-labelledby="lr-tester">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="lr-tester" className="h2">
          Test a log line
        </h2>
        {state.loading && <Spinner />}
      </div>
      <div className="flex flex-wrap gap-2">
        <div className="min-w-[240px] flex-[3_1_360px]">
          <label htmlFor="lr-test-line" className="sr-only">
            Log line to test
          </label>
          <input
            id="lr-test-line"
            className="input font-mono text-[13px]"
            placeholder="Paste a log line, e.g. java.lang.OutOfMemoryError: Java heap space"
            value={line}
            onChange={(e) => setLine(e.target.value)}
            spellCheck={false}
            autoComplete="off"
          />
        </div>
        <div className="min-w-[160px] flex-[1_1_200px]">
          <label htmlFor="lr-test-pod" className="sr-only">
            Pod name (optional)
          </label>
          <input
            id="lr-test-pod"
            className="input font-mono text-[13px]"
            placeholder="Pod name (optional)"
            value={pod}
            onChange={(e) => setPod(e.target.value)}
            spellCheck={false}
            autoComplete="off"
          />
        </div>
      </div>
      <div className="text-[13px]" aria-live="polite">
        {state.error ? (
          <span className="text-crit-fg">Couldn't test the line: {state.error.message}</span>
        ) : !state.data || !line.trim() ? (
          <span className="text-muted">Type or paste a line to see which rules match it. Add a pod name to only try the rules that apply to that pod.</span>
        ) : state.data.length === 0 ? (
          <span className="text-muted">No rule matches. Unknown errors are what the Copilot is for.</span>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
            {state.data.map((r) => (
              <li key={r.id} className="flex flex-wrap items-baseline gap-2">
                <LogSeverityPill severity={r.severity} />
                <button type="button" className="link-btn font-mono text-xs" onClick={() => onOpenRule(r.id)}>
                  {r.id}
                </button>
                <span className="font-medium">{r.title}</span>
                <span className="text-muted">— {r.meaning}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

// ---------------------------------------------------------------- form

type Draft = {
  id: string;
  title: string;
  services: string;
  pattern: string;
  severity: LogSeverity;
  meaning: string;
  fix: string;
  source: string;
  examples: string;
  notExamples: string;
};

const draftFrom = (r?: LogRule): Draft => ({
  id: r?.id ?? "",
  title: r?.title ?? "",
  services: list(r?.services).join(", "),
  pattern: r?.pattern ?? "",
  severity: r?.severity ?? "error",
  meaning: r?.meaning ?? "",
  fix: r?.fix ?? "",
  source: r?.source ?? "",
  examples: list(r?.examples).join("\n"),
  notExamples: list(r?.not_examples).join("\n"),
});

const lines = (s: string) =>
  s
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);

function toInput(d: Draft): LogRuleInput {
  return {
    id: d.id.trim(),
    title: d.title.trim(),
    services: d.services
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean),
    pattern: d.pattern,
    severity: d.severity,
    meaning: d.meaning.trim(),
    fix: d.fix.trim(),
    source: d.source,
    examples: lines(d.examples),
    not_examples: lines(d.notExamples),
  };
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

function Preview({ pattern, examples, notExamples }: { pattern: string; examples: string[]; notExamples: string[] }) {
  const { re, error } = useMemo(() => compilePreview(pattern), [pattern]);
  if (!pattern) return <div className="text-xs text-muted">Enter a pattern to preview the examples against it.</div>;
  if (!re)
    return (
      <div className="text-xs text-warn-fg">
        The browser can't compile this pattern ({error}). The server checks it with Go RE2 when you save.
      </div>
    );
  const row = (text: string, wantMatch: boolean) => {
    const ok = re.test(text) === wantMatch;
    return (
      <li key={`${wantMatch}-${text}`} className="flex items-start gap-2">
        <span className={`mt-0.5 flex-none ${ok ? "text-ok-fg" : "text-crit-fg"}`} aria-label={ok ? "as expected" : "not as expected"}>
          {ok ? <CheckIcon size={14} /> : <XIcon size={14} />}
        </span>
        <span className="min-w-0 break-all font-mono">{text}</span>
        <span className="ml-auto flex-none text-muted">{wantMatch ? (ok ? "matches" : "does not match") : ok ? "no match" : "matches"}</span>
      </li>
    );
  };
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="text-muted">Preview with JavaScript regex — a hint only; the server validates with Go RE2.</div>
      {examples.length === 0 && notExamples.length === 0 ? (
        <div className="text-muted">Add examples to see them checked.</div>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-1 rounded-btn border border-line bg-slate-50 p-2.5">
          {examples.map((e) => row(e, true))}
          {notExamples.map((e) => row(e, false))}
        </ul>
      )}
    </div>
  );
}

function RuleForm({
  rule,
  prefill,
  note,
  onClose,
  onSaved,
}: {
  rule: LogRule | null;
  /** a drafted rule to review and save as new */
  prefill?: LogRule;
  note?: string;
  onClose: () => void;
  onSaved: (r: LogRule) => void;
}) {
  const editing = rule !== null;
  const [d, setD] = useState<Draft>(() => draftFrom(rule ?? prefill ?? undefined));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const firstRef = useRef<HTMLInputElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const set = (patch: Partial<Draft>) => setD((prev) => ({ ...prev, ...patch }));

  useEffect(() => {
    firstRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeRef.current();
    };
    document.addEventListener("keydown", onKey);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prevOverflow;
    };
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      const body = toInput(d);
      const saved = editing ? await api.updateLogRule(rule.id, body) : await api.createLogRule(body);
      onSaved(saved);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
      setSaving(false);
    }
  };

  const title = editing ? `Edit ${rule.id}` : prefill ? "Review drafted rule" : "New log rule";
  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center overflow-y-auto bg-ink/40 px-4 py-8" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div role="dialog" aria-modal="true" aria-labelledby="lr-form-title" className="card w-full max-w-[760px] shadow-xl">
        <form onSubmit={submit}>
          <div className="card-head">
            <h2 id="lr-form-title" className="h2">
              {title}
            </h2>
            <button type="button" className="btn min-h-[30px] px-2" onClick={onClose} aria-label="Close">
              <XIcon size={14} />
            </button>
          </div>
          <div className="flex flex-col gap-3.5 p-4">
            {note && (
              <div className="rounded-btn bg-violet-tint px-3 py-2 text-[13px] text-ink">
                {note} Claude drafted it and the console checked that the pattern compiles, matches the examples and none of the
                not-examples. Review it, then save; nothing is stored until you do.
              </div>
            )}
            {editing && rule.builtin && (
              <div className="text-xs text-muted">Editing a built-in rule stores an override. Reset brings back the default.</div>
            )}
            <div className="grid grid-cols-[repeat(auto-fit,minmax(min(220px,100%),1fr))] gap-3">
              <Field id="lr-id" label="ID" hint={editing ? "IDs can't be changed." : "kebab-case, e.g. jvm-oom"}>
                <input
                  id="lr-id"
                  ref={editing ? undefined : firstRef}
                  className="input font-mono text-[13px] read-only:bg-slate-50 read-only:text-muted"
                  value={d.id}
                  readOnly={editing}
                  required
                  onChange={(e) => set({ id: e.target.value })}
                  spellCheck={false}
                  autoComplete="off"
                />
              </Field>
              <Field id="lr-severity" label="Severity">
                <select id="lr-severity" className="select min-h-[40px] w-full" value={d.severity} onChange={(e) => set({ severity: e.target.value as LogSeverity })}>
                  {SEVERITIES.map((s) => (
                    <option key={s} value={s}>
                      {LOG_SEV_LABEL[s]}
                    </option>
                  ))}
                </select>
              </Field>
            </div>
            <Field id="lr-title" label="Title">
              <input id="lr-title" ref={editing ? firstRef : undefined} className="input" value={d.title} required onChange={(e) => set({ title: e.target.value })} />
            </Field>
            <Field id="lr-services" label="Services" hint="Comma-separated pod-name prefixes. Leave empty to match every pod.">
              <input
                id="lr-services"
                className="input font-mono text-[13px]"
                placeholder="metagraph, sparkedge"
                value={d.services}
                onChange={(e) => set({ services: e.target.value })}
                spellCheck={false}
                autoComplete="off"
              />
            </Field>
            <Field id="lr-pattern" label="Pattern" hint="Go RE2 regular expression.">
              <textarea
                id="lr-pattern"
                className="input min-h-[64px] py-2 font-mono text-[13px]"
                value={d.pattern}
                required
                rows={2}
                onChange={(e) => set({ pattern: e.target.value })}
                spellCheck={false}
              />
            </Field>
            <Field id="lr-meaning" label="Meaning">
              <textarea id="lr-meaning" className="input min-h-[64px] py-2" rows={2} value={d.meaning} onChange={(e) => set({ meaning: e.target.value })} />
            </Field>
            <Field id="lr-fix" label="Fix">
              <textarea id="lr-fix" className="input min-h-[64px] py-2" rows={2} value={d.fix} onChange={(e) => set({ fix: e.target.value })} />
            </Field>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(min(280px,100%),1fr))] gap-3">
              <Field id="lr-examples" label="Examples" hint="One per line. Each must match.">
                <textarea
                  id="lr-examples"
                  className="input min-h-[96px] py-2 font-mono text-xs"
                  rows={4}
                  value={d.examples}
                  onChange={(e) => set({ examples: e.target.value })}
                  spellCheck={false}
                />
              </Field>
              <Field id="lr-not-examples" label="Not examples" hint="One per line. None may match.">
                <textarea
                  id="lr-not-examples"
                  className="input min-h-[96px] py-2 font-mono text-xs"
                  rows={4}
                  value={d.notExamples}
                  onChange={(e) => set({ notExamples: e.target.value })}
                  spellCheck={false}
                />
              </Field>
            </div>
            <Preview pattern={d.pattern} examples={lines(d.examples)} notExamples={lines(d.notExamples)} />
            {error && <ErrorCard title="The server rejected this rule" error={error} />}
          </div>
          <div className="flex flex-wrap justify-end gap-2 border-t border-line px-4 py-3">
            <button type="button" className="btn" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving && <Spinner />}
              {editing ? "Save changes" : "Create rule"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- table

function StatePills({ rule, edited }: { rule: LogRule; edited: boolean }) {
  return (
    <div className="flex flex-wrap gap-1">
      {!rule.builtin ? <Pill tone="info">Custom</Pill> : edited ? <Pill tone="violet">Edited</Pill> : <Pill>Built-in</Pill>}
      {!rule.enabled && <Pill tone="warning">Disabled</Pill>}
    </div>
  );
}

function ExampleList({ title, items }: { title: string; items: string[] }) {
  return (
    <div className="min-w-0">
      <div className="mb-1 text-xs font-semibold text-muted">{title}</div>
      {items.length === 0 ? (
        <div className="text-xs text-muted">none</div>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-1 p-0">
          {items.map((x, i) => (
            <li key={i} className="break-all rounded-btn border border-line bg-white px-2 py-1 font-mono text-xs">
              {x}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

type RowAction = "toggle" | "reset";

/** "Log rules" | "Metric rules" tabs; the metric rules view is `?view=metrics`. */
function RulesTabs({ view }: { view: "logs" | "metrics" | "patterns" }) {
  const { me } = useMe();
  return (
    <nav aria-label="Rule libraries" className="-mt-2 flex flex-wrap gap-0.5 border-b border-line">
      <Link to="/log-rules" className={`tab-link ${view === "logs" ? "active" : ""}`} aria-current={view === "logs" ? "page" : undefined}>
        Log rules
      </Link>
      {me.isAdmin && (
        <Link
          to="/log-rules?view=patterns"
          className={`tab-link ${view === "patterns" ? "active" : ""}`}
          aria-current={view === "patterns" ? "page" : undefined}
        >
          New error patterns
        </Link>
      )}
      <Link to="/log-rules?view=metrics" className={`tab-link ${view === "metrics" ? "active" : ""}`} aria-current={view === "metrics" ? "page" : undefined}>
        Metric rules
      </Link>
    </nav>
  );
}

export default function LogRulesPage() {
  const [params] = useSearchParams();
  if (params.get("view") === "metrics")
    return (
      <Page>
        <PageHeader
          title="Metric rules"
          subtitle="PromQL the metrics parser evaluates through Grafana every minute. Each series a rule returns is an active issue; it resolves when the rule stops returning it."
        />
        <RulesTabs view="metrics" />
        <MetricRulesView focus={params.get("rule") ?? ""} />
      </Page>
    );
  if (params.get("view") === "patterns") return <PatternsView />;
  return <LogRulesView />;
}

function LogRulesView() {
  const { me } = useMe();
  const isAdmin = me.isAdmin;
  const rules = useApi(() => api.logRules());
  const [params, setParams] = useSearchParams();
  const ruleParam = params.get("rule") ?? "";

  const [q, setQ] = useState("");
  const [sev, setSev] = useState<LogSeverity | "">("");
  const [service, setService] = useState(ANY_SERVICE);
  const [showDisabled, setShowDisabled] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(ruleParam ? [ruleParam] : []));
  const [form, setForm] = useState<{ rule: LogRule | null; prefill?: LogRule; note?: string; patternId?: string } | null>(null);
  const location = useLocation();
  const navigate = useNavigate();
  // A rule drafted elsewhere (the Learnings page) arrives in the navigation state.
  useEffect(() => {
    const st = location.state as { draft?: LogRule; note?: string } | null;
    if (st?.draft) {
      setForm({ rule: null, prefill: st.draft, note: st.note });
      navigate(location.pathname + location.search, { replace: true, state: null });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state]);
  const [busy, setBusy] = useState<{ id: string; action: RowAction } | null>(null);
  const [actionError, setActionError] = useState<{ id: string; error: Error } | null>(null);
  const rowRefs = useRef(new Map<string, HTMLTableRowElement>());
  const scrolledFor = useRef("");
  const [scrollTick, setScrollTick] = useState(0);

  const all = rules.data ?? [];
  const isEdited = (r: LogRule) => r.builtin && r.edited;

  const services = useMemo(() => [...new Set(all.flatMap((r) => list(r.services)))].sort(), [all]);

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return all.filter((r) => {
      if (!showDisabled && !r.enabled && r.id !== ruleParam) return false;
      if (sev && r.severity !== sev) return false;
      const svc = list(r.services);
      if (service === ALL_PODS && svc.length > 0) return false;
      if (service !== ANY_SERVICE && service !== ALL_PODS && !svc.includes(service)) return false;
      if (!needle) return true;
      return [r.id, r.title, r.pattern, r.meaning].some((f) => f.toLowerCase().includes(needle));
    });
  }, [all, q, sev, service, showDisabled, ruleParam]);

  // Deep link: expand and scroll to ?rule=<id> once it is on screen.
  useEffect(() => {
    if (!ruleParam || !rules.data || scrolledFor.current === ruleParam) return;
    setExpanded((s) => (s.has(ruleParam) ? s : new Set(s).add(ruleParam)));
    const el = rowRefs.current.get(ruleParam);
    if (el) {
      scrolledFor.current = ruleParam;
      el.scrollIntoView({ block: "center", behavior: "smooth" });
    }
  }, [ruleParam, rules.data, rows, scrollTick]);

  const openRule = (id: string) => {
    scrolledFor.current = "";
    setScrollTick((t) => t + 1);
    setQ("");
    setSev("");
    setService(ANY_SERVICE);
    setParams({ rule: id }, { replace: true });
  };

  const toggleExpanded = (id: string) =>
    setExpanded((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  /** Replace a rule in the list, keeping its 24 h hit count (mutations answer with 0). */
  const upsert = (saved: LogRule) =>
    rules.setData((prev) => {
      const cur = prev ?? [];
      const old = cur.find((r) => r.id === saved.id);
      const merged = { ...saved, hits24h: old?.hits24h ?? saved.hits24h ?? 0 };
      const next = old ? cur.map((r) => (r.id === saved.id ? merged : r)) : [...cur, merged];
      return next.sort((a, b) => a.id.localeCompare(b.id));
    });

  const run = async (rule: LogRule, action: RowAction) => {
    if (action === "reset") {
      const msg = rule.builtin
        ? `Reset ${rule.id} to its built-in default? Your edits and the disabled state are discarded.`
        : `Delete the custom rule ${rule.id}? This cannot be undone.`;
      if (!window.confirm(msg)) return;
    }
    setBusy({ id: rule.id, action });
    setActionError(null);
    try {
      if (action === "toggle") {
        upsert(rule.enabled ? await api.disableLogRule(rule.id) : await api.enableLogRule(rule.id));
      } else {
        await api.deleteLogRule(rule.id);
        if (rule.builtin) {
          // The DELETE answers {}; fetch the default back.
          const fresh = await api.logRules();
          rules.setData(fresh);
        } else {
          rules.setData((prev) => (prev ?? []).filter((r) => r.id !== rule.id));
        }
      }
    } catch (e) {
      setActionError({ id: rule.id, error: e instanceof Error ? e : new Error(String(e)) });
    } finally {
      setBusy(null);
    }
  };

  const onSaved = (saved: LogRule) => {
    upsert(saved);
    if (form?.patternId) void api.setPatternStatus(form.patternId, "ruled", saved.id).catch(() => undefined);
    setForm(null);
    setExpanded((s) => new Set(s).add(saved.id));
  };

  const colCount = isAdmin ? 8 : 7;
  const disabledCount = all.filter((r) => !r.enabled).length;

  return (
    <Page>
      <PageHeader
        title="Log rules"
        subtitle="Known messages our services print. The parser matches them in milliseconds; the Copilot only explains what they mean."
      >
        {isAdmin && (
          <button type="button" className="btn btn-primary" onClick={() => setForm({ rule: null })}>
            <PlusIcon size={14} />
            New rule
          </button>
        )}
      </PageHeader>

      <RulesTabs view="logs" />

      <Tester onOpenRule={openRule} />

      <div className="flex flex-wrap items-center gap-2">
        <input
          type="search"
          className="input min-h-[36px] w-[240px] text-[13px]"
          aria-label="Search rules"
          placeholder="Search id, title, pattern, meaning"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <div role="group" aria-label="Severity" className="inline-flex">
          {([["", "All"], ...SEVERITIES.map((s) => [s, LOG_SEV_LABEL[s]])] as [LogSeverity | "", string][]).map(([value, label], i, arr) => {
            const on = sev === value;
            return (
              <button
                key={label}
                type="button"
                aria-pressed={on}
                onClick={() => setSev(value)}
                className={`btn rounded-none ${i === 0 ? "rounded-l-btn" : "-ml-px"} ${i === arr.length - 1 ? "rounded-r-btn" : ""} ${on ? "relative z-10 border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
              >
                {label}
              </button>
            );
          })}
        </div>
        <label htmlFor="lr-service" className="text-[13px] text-muted">
          Service
        </label>
        <select id="lr-service" className="select min-h-[36px]" value={service} onChange={(e) => setService(e.target.value)}>
          <option value={ANY_SERVICE}>Any service</option>
          <option value={ALL_PODS}>All pods (unscoped rules)</option>
          {services.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <button
          type="button"
          aria-pressed={showDisabled}
          onClick={() => setShowDisabled((v) => !v)}
          className={`btn ${showDisabled ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`}
        >
          Show disabled{disabledCount > 0 && ` (${disabledCount})`}
        </button>
        <span className="ml-auto text-xs text-muted">
          {rules.data && (rows.length === all.length ? `${all.length} rules` : `${rows.length} of ${all.length} rules`)}
        </span>
      </div>

      {actionError && (
        <ErrorCard title={`Couldn't update ${actionError.id}`} error={actionError.error} onRetry={() => setActionError(null)} />
      )}

      <section className="card" aria-label="Log rules">
        {rules.error && !rules.data ? (
          <div className="p-4">
            <ErrorCard title="Couldn't load log rules" error={rules.error} onRetry={rules.reload} />
          </div>
        ) : !rules.data ? (
          <Loading label="Loading log rules…" />
        ) : rows.length === 0 ? (
          <Empty title={all.length === 0 ? "No log rules" : "No rules match"}>
            {all.length === 0 ? "The rule library is empty." : "Try a different search, severity or service, or show disabled rules."}
          </Empty>
        ) : (
          <div className="overflow-x-auto">
            <table className="table min-w-[1060px]">
              <thead>
                <tr>
                  <th scope="col">Rule</th>
                  <th scope="col">Services</th>
                  <th scope="col">Pattern</th>
                  <th scope="col">Severity</th>
                  <th scope="col">Meaning · fix</th>
                  <th scope="col">Hits 24 h</th>
                  <th scope="col">State</th>
                  {isAdmin && (
                    <th scope="col">
                      <span className="sr-only">Actions</span>
                    </th>
                  )}
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const open = expanded.has(r.id);
                  const svc = list(r.services);
                  const ed = isEdited(r);
                  const rowBusy = busy?.id === r.id;
                  const canReset = !r.builtin || ed || !r.enabled;
                  return (
                    <Fragment key={r.id}>
                      <tr
                        ref={(el) => {
                          if (el) rowRefs.current.set(r.id, el);
                          else rowRefs.current.delete(r.id);
                        }}
                        onClick={() => toggleExpanded(r.id)}
                        className={`cursor-pointer hover:bg-slate-50 ${r.id === ruleParam ? "bg-violet-tint/50" : ""} ${r.enabled ? "" : "text-muted"}`}
                      >
                        <td className="min-w-[180px]">
                          <button
                            type="button"
                            className="link-btn font-mono text-xs font-medium"
                            aria-expanded={open}
                            aria-controls={`lr-detail-${r.id}`}
                            onClick={(e) => {
                              e.stopPropagation();
                              toggleExpanded(r.id);
                            }}
                          >
                            <span aria-hidden="true" className="mr-1 inline-block w-2 text-muted">
                              {open ? "▾" : "▸"}
                            </span>
                            {r.id}
                          </button>
                          <div className="mt-0.5 pl-3 text-[13px] text-ink">{r.title}</div>
                        </td>
                        <td>
                          <div className="flex max-w-[160px] flex-wrap gap-1">
                            {svc.length === 0 ? (
                              <span className="text-xs text-muted">all pods</span>
                            ) : (
                              svc.map((s) => (
                                <Pill key={s} className="font-mono">
                                  {s}
                                </Pill>
                              ))
                            )}
                          </div>
                        </td>
                        <td className="min-w-[170px] max-w-[240px] break-all font-mono text-xs">{r.pattern}</td>
                        <td>
                          <LogSeverityPill severity={r.severity} />
                        </td>
                        <td className="min-w-[220px] max-w-[320px]">
                          {r.meaning}
                          {r.fix && <div className="mt-0.5 text-xs text-muted">{r.fix}</div>}
                        </td>
                        <td className={`tabular-nums ${r.hits24h > 0 ? "font-semibold" : "text-muted"}`}>{r.hits24h}</td>
                        <td>
                          <StatePills rule={r} edited={ed} />
                        </td>
                        {isAdmin && (
                          <td onClick={(e) => e.stopPropagation()}>
                            <div className="flex flex-col items-stretch gap-1 whitespace-nowrap">
                              <button type="button" className="btn min-h-[30px] px-2.5 text-xs" onClick={() => setForm({ rule: r })} disabled={rowBusy}>
                                Edit
                              </button>
                              <button type="button" className="btn min-h-[30px] px-2.5 text-xs" onClick={() => run(r, "toggle")} disabled={rowBusy}>
                                {rowBusy && busy?.action === "toggle" && <Spinner size={12} />}
                                {r.enabled ? "Disable" : "Enable"}
                              </button>
                              {canReset && (
                                <button
                                  type="button"
                                  className="btn min-h-[30px] px-2.5 text-xs text-crit-fg"
                                  onClick={() => run(r, "reset")}
                                  disabled={rowBusy}
                                >
                                  {rowBusy && busy?.action === "reset" && <Spinner size={12} />}
                                  {r.builtin ? "Reset" : "Delete"}
                                </button>
                              )}
                            </div>
                          </td>
                        )}
                      </tr>
                      {open && (
                        <tr id={`lr-detail-${r.id}`} className="bg-slate-50">
                          <td colSpan={colCount}>
                            <div className="grid grid-cols-[repeat(auto-fit,minmax(min(320px,100%),1fr))] gap-4 py-1">
                              <ExampleList title="Examples (must match)" items={list(r.examples)} />
                              <ExampleList title="Not examples (must not match)" items={list(r.not_examples)} />
                            </div>
                            <div className="mt-3 text-xs">
                              <span className="font-semibold text-muted">Source </span>
                              {r.source ? <span className="break-all font-mono">{r.source}</span> : <span className="text-muted">—</span>}
                            </div>
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {form && <RuleForm rule={form.rule} prefill={form.prefill} note={form.note} onClose={() => setForm(null)} onSaved={onSaved} />}
    </Page>
  );
}

// ---------------------------------------------------------------- new error patterns

type PatternStatus = LogPattern["status"];

/** Error messages no rule matches, grouped into templates by the log parser; Claude drafts a rule on request. */
function PatternsView() {
  const { me } = useMe();
  const [status, setStatus] = useState<PatternStatus>("new");
  const [q, setQ] = useState("");
  const list = useApi(() => (me.isAdmin ? api.logPatterns(status) : Promise.resolve([] as LogPattern[])), [status, me.isAdmin]);
  const [busy, setBusy] = useState<string | null>(null);
  const [notes, setNotes] = useState<Record<string, { tone: "error" | "info"; text: string }>>({});
  const [form, setForm] = useState<{ prefill: LogRule; patternId: string; note: string } | null>(null);
  const [open, setOpen] = useState<string | null>(null);

  const draft = async (p: LogPattern) => {
    setBusy(p.id);
    setNotes((n) => ({ ...n, [p.id]: { tone: "info", text: "" } }));
    try {
      const d = await api.draftRuleFromPattern(p.id);
      if (!d.applicable) {
        setNotes((n) => ({ ...n, [p.id]: { tone: "info", text: `No rule proposed: ${d.reason || "not specific enough to be actionable."}` } }));
      } else {
        setForm({ prefill: d.rule, patternId: p.id, note: `Drafted from an error pattern in ${p.service}.` });
      }
    } catch (e) {
      setNotes((n) => ({ ...n, [p.id]: { tone: "error", text: e instanceof Error ? e.message : String(e) } }));
    } finally {
      setBusy(null);
    }
  };
  const setPattern = async (p: LogPattern, s: PatternStatus) => {
    setBusy(p.id);
    try {
      await api.setPatternStatus(p.id, s);
      list.reload();
    } catch (e) {
      setNotes((n) => ({ ...n, [p.id]: { tone: "error", text: e instanceof Error ? e.message : String(e) } }));
    } finally {
      setBusy(null);
    }
  };

  const needle = q.trim().toLowerCase();
  const rows = (list.data ?? []).filter((p) => !needle || `${p.service} ${p.template}`.toLowerCase().includes(needle));
  const chip = (active: boolean) => `btn btn-sm ${active ? "border-ink bg-ink text-white hover:bg-ink hover:text-white" : ""}`;

  return (
    <Page>
      <PageHeader
        title="New error patterns"
        subtitle="Error messages no log rule matches yet, grouped into templates as the parser reads logs. Turn the actionable ones into rules so the monitors catch them without AI; ignore the noise."
      />
      <RulesTabs view="patterns" />
      {!me.isAdmin ? (
        <div className="card">
          <Empty title="Admins only">Patterns show raw error messages from tenant clusters.</Empty>
        </div>
      ) : (
        <section className="card" aria-label="Error patterns">
          <div className="card-head flex-wrap gap-2">
            <div className="flex gap-1" role="group" aria-label="Status">
              {(
                [
                  ["new", "To review"],
                  ["ruled", "Turned into rules"],
                  ["ignored", "Ignored"],
                ] as [PatternStatus, string][]
              ).map(([s, label]) => (
                <button key={s} type="button" aria-pressed={status === s} className={chip(status === s)} onClick={() => setStatus(s)}>
                  {label}
                </button>
              ))}
            </div>
            <label className="ml-auto">
              <span className="sr-only">Filter patterns</span>
              <input className="input min-h-[32px] w-[260px] text-[13px]" placeholder="Filter by service or text" value={q} onChange={(e) => setQ(e.target.value)} />
            </label>
          </div>
          {list.error ? (
            <div className="p-4">
              <ErrorCard title="Couldn't load patterns" error={list.error} onRetry={list.reload} />
            </div>
          ) : !list.data ? (
            <Loading label="Loading patterns…" />
          ) : rows.length === 0 ? (
            <Empty title={status === "new" ? "Nothing new" : status === "ruled" ? "No patterns turned into rules yet" : "Nothing ignored"}>
              {status === "new" ? "Every error message the parser saw recently matches a rule, or was reviewed." : "Patterns appear here as you act on them."}
            </Empty>
          ) : (
            <ul className="m-0 list-none divide-y divide-line p-0">
              {rows.slice(0, 200).map((p) => {
                const clusters = Object.keys(p.clusters);
                const note = notes[p.id];
                return (
                  <li key={p.id} className="px-4 py-3">
                    <div className="flex flex-wrap items-start gap-3">
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted">
                          <span className="font-mono font-medium text-ink">{p.service}</span>
                          {p.isNew && <Pill tone="warning">new today</Pill>}
                          <span>
                            {p.last24h} in 24 h · {p.last6h} in 6 h · {p.count} total
                          </span>
                          <span title={clusters.join(", ")}>
                            · {clusters.length === 1 ? clusters[0] : `${clusters.length} clusters`}
                          </span>
                          <span>· first {relativeTime(p.firstSeen)}</span>
                          {p.ruleId && (
                            <Link to={`/log-rules?rule=${encodeURIComponent(p.ruleId)}`} className="font-mono">
                              · rule {p.ruleId}
                            </Link>
                          )}
                        </div>
                        <div className="mt-1 break-words font-mono text-[13px] text-ink">{p.template}</div>
                        <button type="button" className="mt-1 text-xs text-muted hover:underline" aria-expanded={open === p.id} onClick={() => setOpen(open === p.id ? null : p.id)}>
                          {open === p.id ? "Hide example" : "Show an example line"}
                        </button>
                        {open === p.id && (
                          <pre className="m-0 mt-1 overflow-x-auto whitespace-pre-wrap break-words rounded-btn border border-line bg-slate-50 p-2 font-mono text-xs">{p.example}</pre>
                        )}
                        {note?.text && (
                          <div role={note.tone === "error" ? "alert" : "status"} className={`mt-1 text-xs ${note.tone === "error" ? "text-crit-fg" : "text-muted"}`}>
                            {note.text}
                          </div>
                        )}
                      </div>
                      <div className="flex flex-none gap-1.5">
                        {p.status === "new" && (
                          <>
                            <button type="button" className="btn btn-sm" disabled={busy === p.id} onClick={() => setPattern(p, "ignored")}>
                              Ignore
                            </button>
                            <button type="button" className="btn btn-sm btn-primary" disabled={busy === p.id} onClick={() => draft(p)}>
                              {busy === p.id && <Spinner />}
                              Draft rule
                            </button>
                          </>
                        )}
                        {p.status === "ignored" && (
                          <button type="button" className="btn btn-sm" disabled={busy === p.id} onClick={() => setPattern(p, "new")}>
                            Restore
                          </button>
                        )}
                      </div>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      )}
      {form && (
        <RuleForm
          rule={null}
          prefill={form.prefill}
          note={form.note}
          onClose={() => setForm(null)}
          onSaved={(saved) => {
            void api.setPatternStatus(form.patternId, "ruled", saved.id).finally(list.reload);
            setForm(null);
          }}
        />
      )}
    </Page>
  );
}
