import { useState } from "react";
import { api } from "../api/client";
import { useMe } from "../lib/me";
import type { Action, ActionPlan } from "../api/types";
import { CheckIcon, CopyIcon, LockIcon, Spinner, XIcon } from "./Icons";
import { ActionStatusPill } from "./Pill";

/**
 * One proposed remediation. Proposed → primary button + Dismiss; running → spinner;
 * finished → status pill and the backend's result text.
 */
export function ActionCard({ action, onChange, compact = false }: { action: Action; onChange?: (a: Action) => void; compact?: boolean }) {
  const [current, setCurrent] = useState(action);
  const [busy, setBusy] = useState<"execute" | "dismiss" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { me } = useMe();

  const run = async (kind: "execute" | "dismiss") => {
    const before = current;
    setBusy(kind);
    setError(null);
    if (kind === "execute") setCurrent({ ...current, status: "running" });
    try {
      const next = kind === "execute" ? await api.executeAction(current.id) : await api.dismissAction(current.id);
      setCurrent(next);
      onChange?.(next);
    } catch (e) {
      setCurrent(before);
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const status = current.status;
  return (
    <div className={compact ? "flex flex-col gap-2" : "card flex flex-col gap-2 p-3"}>
      {!compact && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="text-sm font-semibold">
            {current.title} <span className="font-mono text-xs font-normal text-muted">· {current.targetName}</span>
          </div>
          <ActionStatusPill status={status} />
        </div>
      )}
      {current.description && <div className="text-[13px] text-slate-700">{current.description}</div>}
      {current.plan && status === "proposed" && <PlanView plan={current.plan} />}

      {status === "proposed" && me.readOnly && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
          <LockIcon size={12} />
          Dry run only: the console is read-only, so this can't be executed here. An admin can apply the steps by hand.
          {compact && <span className="font-mono">{current.title} · {current.targetName}</span>}
        </div>
      )}
      {status === "proposed" && !me.readOnly && (
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" className="btn btn-primary" disabled={busy !== null} onClick={() => run("execute")}>
            {current.title}
          </button>
          <button type="button" className="btn" disabled={busy !== null} onClick={() => run("dismiss")}>
            {busy === "dismiss" && <Spinner />}
            Dismiss
          </button>
          {compact && <span className="font-mono text-xs text-muted">{current.targetName}</span>}
        </div>
      )}
      {status === "running" && (
        <div role="status" className="flex items-center gap-2 text-[13px] text-info-fg">
          <Spinner /> Running {current.title.toLowerCase()} on <span className="font-mono">{current.targetName}</span>…
        </div>
      )}
      {(status === "succeeded" || status === "failed") && (
        <div
          role="status"
          className={`flex items-start gap-2 rounded-btn px-3 py-2 text-[13px] ${status === "succeeded" ? "bg-ok-bg text-ok-fg" : "bg-crit-bg text-crit-fg"}`}
        >
          {status === "succeeded" ? <CheckIcon size={14} className="mt-0.5 flex-none" /> : <XIcon size={14} className="mt-0.5 flex-none" />}
          <span className="min-w-0 break-words">
            <strong>{current.title}</strong> {status === "succeeded" ? "succeeded" : "failed"}
            {current.result ? ` — ${current.result}` : "."}
          </span>
        </div>
      )}
      {status === "dismissed" && compact && <div className="text-xs text-muted">Dismissed: {current.title}</div>}
      {error && (
        <div role="alert" className="text-[13px] text-crit-fg">
          {error}
        </div>
      )}
    </div>
  );
}

/** The dry run: what would happen, what was checked, the exact steps and the risks. */
function PlanView({ plan }: { plan: ActionPlan }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(plan.steps.join("\n"));
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable: the steps stay selectable */
    }
  };
  return (
    <div className="flex flex-col gap-1.5 rounded-btn border border-line bg-slate-50 p-2.5 text-[13px]">
      <div>
        <span className="text-xs font-semibold uppercase tracking-wide text-muted">Dry run</span> {plan.summary}
      </div>
      {plan.checks.length > 0 && (
        <ul className="m-0 list-disc pl-5 text-xs text-slate-700">
          {plan.checks.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ul>
      )}
      {plan.steps.length > 0 && (
        <div className="relative">
          <pre className="m-0 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-btn bg-white p-2 pr-9 font-mono text-xs">
            {plan.steps.join("\n")}
          </pre>
          <button type="button" className="btn absolute right-1 top-1 px-1.5 py-1" onClick={copy} aria-label="Copy steps">
            {copied ? <CheckIcon size={12} /> : <CopyIcon size={12} />}
          </button>
        </div>
      )}
      {plan.risks && plan.risks.length > 0 && (
        <ul className="m-0 list-none p-0 text-xs text-warn-fg">
          {plan.risks.map((r) => (
            <li key={r}>Risk: {r}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
