import { useEffect, useState, type ReactNode } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { ApiError, api } from "../api/client";
import type { FleetIssue, Me } from "../api/types";
import { initials } from "../lib/format";
import { MeContext } from "../lib/me";
import { useApi } from "../lib/useApi";
import { useAutoRefresh } from "./Cluster";
import { ISSUES_POLL_MS, IssuesContext, countIssues } from "./Issues";
import { BookIcon, LockIcon, LogoIcon, LogoutIcon, SearchIcon, SparkIcon } from "./Icons";
import { Pill } from "./Pill";
import { CommandPalette } from "./shell/CommandPalette";
import { CopilotDock, CopilotDockProvider, dockHiddenOn, useCopilotDock } from "./shell/CopilotDock";
import { MoonIcon, SunIcon } from "./shell/icons";
import { LiveIndicator } from "./shell/LiveIndicator";
import { NAV_GROUPS } from "./shell/nav";
import { useTheme } from "./shell/theme";
import { ErrorCard, Loading } from "./States";

const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const PALETTE_KEY = IS_MAC ? "⌘K" : "Ctrl K";

function ThemeToggle() {
  const { dark, toggle } = useTheme();
  return (
    <button type="button" className="btn btn-ghost btn-sm" onClick={toggle} aria-pressed={dark} aria-label="Dark mode" title={dark ? "Switch to light mode" : "Switch to dark mode"}>
      {dark ? <SunIcon size={15} /> : <MoonIcon size={15} />}
      <span className="hidden sm:inline">{dark ? "Light" : "Dark"}</span>
    </button>
  );
}

function DockToggle() {
  const { open, toggle } = useCopilotDock();
  const { pathname } = useLocation();
  if (dockHiddenOn(pathname)) return null;
  return (
    <button
      type="button"
      className={`btn btn-sm ${open ? "border-violet-border bg-violet-tint text-violet hover:bg-violet-tint" : ""}`}
      onClick={toggle}
      aria-expanded={open}
      aria-controls="copilot-dock"
    >
      <SparkIcon size={14} />
      Copilot
    </button>
  );
}

/** Standard padded content column used by every page except Copilot. */
export function Page({ children }: { children: ReactNode }) {
  return <div className="flex w-full max-w-[1200px] flex-col gap-6 px-4 py-6 sm:px-8">{children}</div>;
}

function Brand() {
  return (
    <div className="flex items-center gap-2.5 text-base font-bold">
      <LogoIcon size={22} className="text-violet" />
      Control Hub <Pill tone="violet">AI Console</Pill>
    </div>
  );
}

export function Layout() {
  const [me, setMe] = useState<Me | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loggingOut, setLoggingOut] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();

  const loadMe = () => {
    setError(null);
    api.me().then(setMe, (e: unknown) => {
      if (e instanceof ApiError && e.status === 401) {
        navigate(`/login?next=${encodeURIComponent(location.pathname + location.search)}`, { replace: true });
        return;
      }
      setError(e instanceof Error ? e : new Error(String(e)));
    });
  };

  useEffect(loadMe, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Fleet issues: polled here once for the sidebar badge, the Fleet and the Issues pages.
  const signedIn = me?.email ?? "";
  const issues = useApi(() => (signedIn ? api.issues() : Promise.resolve(undefined as unknown as FleetIssue[])), [signedIn]);
  useAutoRefresh(issues.reload, ISSUES_POLL_MS);
  const activeErrors = issues.data ? countIssues(issues.data).errors : 0;

  const logout = async () => {
    setLoggingOut(true);
    try {
      await api.logout();
    } catch {
      // Even if the call fails, leave the app; the session cookie may already be gone.
    }
    navigate("/login", { replace: true });
  };

  const [paletteOpen, setPaletteOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  // Close the palette on navigation (e.g. browser back).
  useEffect(() => setPaletteOpen(false), [location.pathname]);

  if (error) {
    return (
      <div className="mx-auto max-w-xl p-8">
        <ErrorCard title="Couldn't load your session" error={error} onRetry={loadMe} />
      </div>
    );
  }
  if (!me) return <Loading label="Loading AI Console…" />;

  return (
    <MeContext.Provider value={{ me, setMe }}>
      <IssuesContext.Provider value={issues}>
      <CopilotDockProvider>
      <a href="#main" className="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-50 focus:rounded-btn focus:bg-white focus:px-3 focus:py-2">
        Skip to content
      </a>
      <div className="flex min-h-screen flex-col">
        <header className="flex flex-wrap items-center gap-x-4 gap-y-2.5 border-b border-line bg-white px-4 py-3 sm:px-6">
          <div className="flex flex-wrap items-center gap-2.5">
            <Brand />
            {me.controlHub && (
              <span title={`Connected to ${me.controlHub}`}>
                <Pill tone={/prod/i.test(me.controlHub) ? "critical" : "info"}>{me.controlHub}</Pill>
              </span>
            )}
          </div>
          <button
            type="button"
            className="btn order-last w-full justify-start bg-slate-50 font-normal text-muted min-[700px]:order-none min-[700px]:w-auto min-[700px]:max-w-[400px] min-[700px]:flex-[1_1_220px]"
            onClick={() => setPaletteOpen(true)}
            aria-haspopup="dialog"
            aria-keyshortcuts={IS_MAC ? "Meta+K" : "Control+K"}
          >
            <SearchIcon size={15} className="flex-none" />
            <span className="truncate">Search or run…</span>
            <span className="kbd ml-auto" aria-hidden="true">
              {PALETTE_KEY}
            </span>
          </button>
          <div className="ml-auto flex flex-wrap items-center gap-2 text-[13px] text-slate-700">
            <LiveIndicator />
            <ThemeToggle />
            <DockToggle />
            <span
              aria-hidden="true"
              className="flex h-8 w-8 flex-none items-center justify-center rounded-full bg-ink text-xs font-semibold text-white"
            >
              {initials(me.username || me.email)}
            </span>
            <span className="hidden max-w-[240px] truncate sm:inline" title={me.email}>
              {me.email}
            </span>
            <span className="sr-only sm:hidden">Signed in as {me.email}</span>
            {me.isAdmin && <Pill tone="violet">Admin</Pill>}
            <button type="button" className="btn btn-sm" onClick={logout} disabled={loggingOut}>
              <LogoutIcon size={14} />
              Log out
            </button>
          </div>
        </header>
        {me.readOnly && (
          <div role="note" className="flex items-center gap-2 border-b border-violet-border bg-violet-tint px-4 py-1.5 text-[13px] text-violet sm:px-6">
            <LockIcon size={13} className="flex-none" />
            <span>
              <strong>Read-only:</strong> connected to {me.controlHub || "control-hub"}. Nothing in the console can change clusters or control-hub.
            </span>
          </div>
        )}

        <div className="flex flex-1 flex-col min-[900px]:flex-row">
          <nav
            aria-label="Main"
            className="flex flex-row flex-wrap gap-0.5 border-b border-line bg-white px-3 py-2 min-[900px]:w-[232px] min-[900px]:flex-none min-[900px]:flex-col min-[900px]:flex-nowrap min-[900px]:border-b-0 min-[900px]:border-r min-[900px]:py-2"
          >
            {NAV_GROUPS.map((g) => (
              <div key={g.label} role="group" aria-labelledby={`nav-${g.label}`} className="contents min-[900px]:flex min-[900px]:flex-col min-[900px]:gap-0.5">
                <div id={`nav-${g.label}`} className="nav-group sr-only min-[900px]:not-sr-only">
                  {g.label}
                </div>
                {g.items.map(({ to, label, icon: Icon, end }) => (
                  <NavLink
                    key={to}
                    to={to}
                    end={end}
                    className={({ isActive }) => `nav-link ${isActive ? "active" : ""}`}
                  >
                    <Icon size={16} />
                    {label}
                    {to === "/issues" && activeErrors > 0 && (
                      <span
                        className="ml-auto inline-flex min-w-[20px] items-center justify-center rounded-full bg-crit-bg px-1.5 text-xs font-semibold tabular-nums text-crit-fg"
                        aria-label={`${activeErrors} active ${activeErrors === 1 ? "error" : "errors"}`}
                      >
                        {activeErrors}
                      </span>
                    )}
                  </NavLink>
                ))}
              </div>
            ))}
            {/* A separate static site (MkDocs), so a plain link rather than a client route. */}
            <a href="/docs/" target="_blank" rel="noopener" className="nav-link">
              <BookIcon size={16} />
              Documentation
            </a>
            <div className="mt-4 hidden border-t border-line p-3 text-xs text-muted min-[900px]:block">
              Creating and changing tenants, and billing, stay in the existing Control Hub UI.
            </div>
          </nav>

          <main id="main" className="flex min-w-0 flex-1">
            <Outlet />
          </main>

          <CopilotDock />
        </div>
      </div>
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
      </CopilotDockProvider>
      </IssuesContext.Provider>
    </MeContext.Provider>
  );
}

export { Brand };
