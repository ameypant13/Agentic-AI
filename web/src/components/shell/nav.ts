import type { SVGProps } from "react";
import { AlertIcon, BookIcon, ChatIcon, ClusterIcon, DollarIcon, GearIcon, GridIcon, LockIcon, PulseIcon, RulesIcon, SearchIcon, ShieldIcon } from "../Icons";

type IconType = (p: SVGProps<SVGSVGElement> & { size?: number }) => JSX.Element;

export type NavItem = { to: string; label: string; icon: IconType; end: boolean };
export type NavGroup = { label: string; items: NavItem[] };

/** Sidebar groups; the command palette lists the same pages. */
export const NAV_GROUPS: NavGroup[] = [
  {
    label: "Overview",
    items: [
      { to: "/", label: "Fleet overview", icon: GridIcon, end: true },
      { to: "/issues", label: "Issues", icon: AlertIcon, end: false },
      { to: "/digest", label: "Weekly digest", icon: BookIcon, end: false },
    ],
  },
  {
    label: "Explore",
    items: [
      { to: "/clusters", label: "Clusters", icon: ClusterIcon, end: false },
      { to: "/certificates", label: "Certificates", icon: LockIcon, end: false },
      { to: "/costs", label: "Costs", icon: DollarIcon, end: false },
      { to: "/readiness", label: "Upgrade readiness", icon: ShieldIcon, end: false },
      { to: "/investigations", label: "Investigations", icon: SearchIcon, end: false },
      { to: "/learnings", label: "Learnings", icon: BookIcon, end: false },
    ],
  },
  { label: "Ask", items: [{ to: "/copilot", label: "Copilot", icon: ChatIcon, end: false }] },
  {
    label: "Configure",
    items: [
      { to: "/monitors", label: "Monitors", icon: PulseIcon, end: false },
      { to: "/log-rules", label: "Log rules", icon: RulesIcon, end: false },
      { to: "/guardrails", label: "Guardrails", icon: ShieldIcon, end: false },
      { to: "/settings", label: "Settings", icon: GearIcon, end: false },
    ],
  },
];

export const NAV_ITEMS: NavItem[] = NAV_GROUPS.flatMap((g) => g.items);
