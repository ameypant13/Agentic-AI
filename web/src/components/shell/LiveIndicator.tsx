import { useEffect, useState } from "react";
import { useLocation } from "react-router-dom";

function ago(s: number): string {
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min`;
  return `${Math.floor(m / 60)} h`;
}

/**
 * "Live · updated 12s ago": counts from when the current page was opened (a navigation restarts it).
 * Pages poll their own data on their own timers; this is not a push connection, and the tooltip says so.
 */
export function LiveIndicator() {
  const location = useLocation();
  const [since, setSince] = useState(() => Date.now());
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const t = Date.now();
    setSince(t);
    setNow(t);
  }, [location.pathname, location.search]);

  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);

  const s = Math.max(0, Math.floor((now - since) / 1000));
  const tip = "Time since this page was opened. Pages refresh their own data in the background; this is not a live stream.";
  return (
    <span className="inline-flex items-center gap-2 whitespace-nowrap text-xs text-muted" title={tip}>
      <span className="live-dot" aria-hidden="true" />
      {/* Ticking text is hidden from screen readers; the label gives a stable summary. */}
      <span aria-hidden="true" className="tabular-nums">
        Live · updated {ago(s)} ago
      </span>
      <span className="sr-only">Page opened {ago(s)} ago</span>
    </span>
  );
}
