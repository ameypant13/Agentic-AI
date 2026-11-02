import type { ReactNode } from "react";
import type { ActionStatus, Confidence, FindingState, PolicyMode, Severity } from "../api/types";

export type Tone = "critical" | "warning" | "success" | "info" | "neutral" | "violet";

const TONES: Record<Tone, string> = {
  critical: "bg-crit-bg text-crit-fg",
  warning: "bg-warn-bg text-warn-fg",
  success: "bg-ok-bg text-ok-fg",
  info: "bg-info-bg text-info-fg",
  neutral: "bg-slate-100 text-slate-700",
  violet: "bg-violet-tint text-violet",
};

export function Pill({ tone = "neutral", children, className = "" }: { tone?: Tone; children: ReactNode; className?: string }) {
  return <span className={`pill ${TONES[tone]} ${className}`}>{children}</span>;
}

export function SeverityPill({ severity }: { severity: Severity }) {
  return severity === "critical" ? <Pill tone="critical">Critical</Pill> : <Pill tone="warning">Warning</Pill>;
}

export function FindingStatePill({ state }: { state: FindingState }) {
  const tone: Tone = state === "open" ? "warning" : state === "acknowledged" ? "info" : "success";
  return <Pill tone={tone}>{state[0].toUpperCase() + state.slice(1)}</Pill>;
}

export function policyModeTone(mode: PolicyMode): Tone {
  switch (mode) {
    case "Never":
      return "critical";
    case "Auto":
      return "info";
    case "Ask requester":
    case "Admin approval":
      return "warning";
    default:
      return "neutral";
  }
}

export function PolicyModePill({ mode }: { mode: PolicyMode }) {
  return <Pill tone={policyModeTone(mode)}>{mode}</Pill>;
}

export function ConfidencePill({ confidence }: { confidence: Confidence }) {
  const tone: Tone = confidence === "high" ? "success" : confidence === "medium" ? "warning" : "neutral";
  return <Pill tone={tone}>Confidence: {confidence}</Pill>;
}

export function ActionStatusPill({ status }: { status: ActionStatus }) {
  const map: Record<ActionStatus, [Tone, string]> = {
    proposed: ["violet", "Proposed"],
    running: ["info", "Running"],
    succeeded: ["success", "Succeeded"],
    failed: ["critical", "Failed"],
    dismissed: ["neutral", "Dismissed"],
  };
  const [tone, label] = map[status] ?? ["neutral", status];
  return <Pill tone={tone}>{label}</Pill>;
}
