import { useState } from "react";
import type { Recommendation, Resources } from "../api/types";
import { CheckIcon, CopyIcon } from "./Icons";
import type { Tone } from "./Pill";

export function cpu(cores: number): string {
  if (!cores) return "—";
  const m = Math.round(cores * 1000);
  return m % 1000 === 0 ? String(m / 1000) : `${m}m`;
}

export function mem(bytes: number): string {
  if (!bytes) return "—";
  const mi = Math.ceil(bytes / 2 ** 20);
  return mi % 1024 === 0 ? `${mi / 1024}Gi` : mi >= 10240 ? `${(mi / 1024).toFixed(1)}Gi` : `${mi}Mi`;
}

export const usd = (v: number) => {
  const a = Math.abs(v);
  const s = a >= 1000 ? `$${(a / 1000).toFixed(1)}k` : a >= 100 ? `$${a.toFixed(0)}` : `$${a.toFixed(2)}`;
  return v < 0 ? `+${s}` : s;
};

export const CONF_TONE: Record<Recommendation["confidence"], Tone> = { high: "success", medium: "info", low: "warning" };

/** Exact Kubernetes quantities for manifests (no rounding to Gi for display). */
function quantity(bytes: number): string {
  const mi = Math.ceil(bytes / 2 ** 20);
  return mi % 1024 === 0 ? `${mi / 1024}Gi` : `${mi}Mi`;
}

export function helmSnippet(r: Recommendation): string {
  const x = r.recommended;
  const lines = [
    `# ${r.namespace}/${r.kind}/${r.name} · container ${r.container}`,
    `# ${r.days} days: cpu p95 ${cpu(r.cpuP95)}, peak memory ${mem(r.memMax)}`,
    "resources:",
    "  requests:",
    `    cpu: "${cpu(x.cpuRequest)}"`,
    `    memory: "${quantity(x.memRequest)}"`,
    "  limits:",
  ];
  if (x.cpuLimit) lines.push(`    cpu: "${cpu(x.cpuLimit)}"`);
  if (x.memLimit) lines.push(`    memory: "${quantity(x.memLimit)}"`);
  else lines.push("    {}");
  return lines.join("\n");
}

export function ResCell({ r, base }: { r: Resources; base?: Resources }) {
  const changed = (a: number, b?: number) => base !== undefined && Math.abs(a - (b ?? 0)) > 1e-9;
  const cls = (a: number, b?: number) => (changed(a, b) ? "font-semibold text-ink" : "");
  return (
    <div className="font-mono text-xs leading-5">
      <div>
        <span className="text-muted">cpu </span>
        <span className={cls(r.cpuRequest, base?.cpuRequest)}>{cpu(r.cpuRequest)}</span>
        <span className="text-muted"> / </span>
        <span className={cls(r.cpuLimit, base?.cpuLimit)}>{r.cpuLimit ? cpu(r.cpuLimit) : "no limit"}</span>
      </div>
      <div>
        <span className="text-muted">mem </span>
        <span className={cls(r.memRequest, base?.memRequest)}>{mem(r.memRequest)}</span>
        <span className="text-muted"> / </span>
        <span className={cls(r.memLimit, base?.memLimit)}>{r.memLimit ? mem(r.memLimit) : "no limit"}</span>
      </div>
    </div>
  );
}

export function CopyValues({ r }: { r: Recommendation }) {
  const [state, setState] = useState<"" | "ok" | "fail">("");
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(helmSnippet(r));
      setState("ok");
    } catch {
      setState("fail");
    }
    setTimeout(() => setState(""), 2000);
  };
  return (
    <button type="button" className="btn btn-sm" onClick={copy} aria-label={`Copy resource values for ${r.name} ${r.container}`}>
      {state === "ok" ? <CheckIcon size={13} /> : <CopyIcon size={13} />}
      {state === "ok" ? "Copied" : state === "fail" ? "Copy failed" : "Copy values"}
    </button>
  );
}
