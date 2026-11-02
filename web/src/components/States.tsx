import type { ReactNode } from "react";
import { AlertIcon, Spinner } from "./Icons";

export function Loading({ label = "Loading…" }: { label?: string }) {
  return (
    <div role="status" className="flex items-center gap-2 px-4 py-6 text-sm text-muted">
      <Spinner />
      {label}
    </div>
  );
}

export function ErrorCard({ error, onRetry, title = "Something went wrong" }: { error: Error | string; onRetry?: () => void; title?: string }) {
  const message = typeof error === "string" ? error : error.message;
  return (
    <div role="alert" className="card flex items-start gap-3 border-red-200 bg-crit-bg/40 p-4">
      <AlertIcon size={18} className="mt-0.5 flex-none text-crit-fg" />
      <div className="min-w-0 flex-1">
        <div className="text-sm font-semibold text-crit-fg">{title}</div>
        <div className="mt-0.5 break-words text-[13px] text-slate-700">{message}</div>
      </div>
      {onRetry && (
        <button type="button" className="btn" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="px-4 py-10 text-center">
      <div className="text-sm font-semibold text-ink">{title}</div>
      {children && <div className="mx-auto mt-1 max-w-md text-[13px] text-muted">{children}</div>}
    </div>
  );
}

export function PageHeader({ title, subtitle, children }: { title: string; subtitle?: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="m-0 text-2xl font-semibold">{title}</h1>
        {subtitle && <p className="mb-0 mt-1 text-sm text-muted">{subtitle}</p>}
      </div>
      {children}
    </div>
  );
}
