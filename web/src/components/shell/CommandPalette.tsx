import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../../api/client";
import type { CertItem, Cluster } from "../../api/types";
import { useCurrentPageContext } from "../../lib/pageContext";
import { providerLabel } from "../../lib/format";
import { SearchIcon, Spinner } from "../Icons";
import { Pill, type Tone } from "../Pill";
import { useCopilotDock } from "./CopilotDock";
import { fuzzyScore } from "./fuzzy";
import { NAV_ITEMS } from "./nav";

type Group = "Actions" | "Pages" | "Clusters" | "Certificates";
const GROUP_ORDER: Group[] = ["Actions", "Clusters", "Pages", "Certificates"];
const KIND: Record<Group, [string, Tone]> = {
  Actions: ["action", "violet"],
  Pages: ["page", "neutral"],
  Clusters: ["cluster", "neutral"],
  Certificates: ["cert", "warning"],
};
/** Rows per group with an empty query / with a query. */
const LIMIT_IDLE = 5;
const LIMIT_QUERY = 8;

type Item = { id: string; group: Group; label: string; hint?: string; search: string; run: () => void };

type Data = {
  clusters?: Cluster[];
  certs?: CertItem[];
  errors: string[];
};

/** Loaded lazily on first open, kept for the session, refreshed in the background after 5 minutes. */
const STALE_MS = 5 * 60_000;
let cache: { at: number; data: Data } | null = null;

async function loadAll(): Promise<Data> {
  const [c, k] = await Promise.allSettled([api.clusters(), api.certificates()]);
  const errors: string[] = [];
  const val = <T,>(r: PromiseSettledResult<T>, what: string): T | undefined => {
    if (r.status === "fulfilled") return r.value;
    errors.push(what);
    return undefined;
  };
  return {
    clusters: val(c, "clusters"),
    certs: val(k, "certificates")?.items,
    errors,
  };
}

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const navigate = useNavigate();
  const ctx = useCurrentPageContext();
  const dock = useCopilotDock();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [data, setData] = useState<Data | null>(cache?.data ?? null);
  const [loading, setLoading] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const restoreFocus = useRef<HTMLElement | null>(null);
  const listId = useId();

  // Open: remember focus, reset, load data if needed. Close: restore focus.
  useEffect(() => {
    if (!open) return;
    restoreFocus.current = document.activeElement as HTMLElement | null;
    setQuery("");
    setActive(0);
    requestAnimationFrame(() => inputRef.current?.focus());
    if (!cache || Date.now() - cache.at > STALE_MS) {
      let cancelled = false;
      setLoading(true);
      loadAll().then((d) => {
        cache = { at: Date.now(), data: d };
        if (cancelled) return;
        setData(d);
        setLoading(false);
      });
      return () => {
        cancelled = true;
        restoreFocus.current?.focus?.();
      };
    }
    setData(cache.data);
    return () => restoreFocus.current?.focus?.();
  }, [open]);

  const go = (to: string) => () => {
    onClose();
    navigate(to);
  };

  const items = useMemo<Item[]>(() => {
    const out: Item[] = [];
    const q = query.trim();
    {
      out.push({
        id: "act:ask-page",
        group: "Actions",
        label: q ? `Ask Copilot: “${q}”` : "Ask Copilot about this page",
        hint: ctx ? `with context · ${ctx.label}` : "opens the Copilot panel",
        search: "ask copilot question chat this page explain",
        run: () => {
          onClose();
          if (q) dock.ask(q);
          else dock.setOpen(true);
        },
      });
      for (const n of NAV_ITEMS) {
        out.push({ id: `page:${n.to}`, group: "Pages", label: n.label, hint: n.to, search: `${n.label} ${n.to} go page`, run: go(n.to) });
      }
      out.push({
        id: "page:/docs/",
        group: "Pages",
        label: "Documentation",
        hint: "/docs/",
        search: "documentation docs help manual guide install",
        run: () => {
          onClose();
          window.open("/docs/", "_blank", "noopener");
        },
      });
      for (const c of data?.clusters ?? []) {
        out.push({
          id: `cluster:${c.id}`,
          group: "Clusters",
          label: c.name,
          hint: [providerLabel(c.provider), c.region, c.tenants.slice(0, 3).join(", ")].filter(Boolean).join(" · "),
          search: `${c.name} ${c.provider} ${c.region} ${c.tenants.join(" ")} cluster`,
          run: go(`/clusters/${encodeURIComponent(c.id)}`),
        });
      }
      const seen = new Set<string>();
      for (const k of data?.certs ?? []) {
        if (!k.host || seen.has(k.host)) continue;
        seen.add(k.host);
        const days = k.daysLeft === undefined ? "" : k.daysLeft < 0 ? `expired ${-k.daysLeft}d ago` : `${k.daysLeft}d left`;
        out.push({
          id: `cert:${k.key}`,
          group: "Certificates",
          label: k.host,
          hint: [days, k.tenant, k.status].filter(Boolean).join(" · "),
          search: `${k.host} ${k.tenant ?? ""} certificate cert tls`,
          run: go("/certificates"),
        });
      }
    }
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, query, ctx]);

  /** Filtered, grouped rows in display order. */
  const rows = useMemo(() => {
    const q = query.trim();
    const byGroup = new Map<Group, { item: Item; score: number }[]>();
    for (const item of items) {
      const score = item.id === "act:ask-page" && q ? 1 : fuzzyScore(q, `${item.label} ${item.search}`);
      if (score < 0) continue;
      const list = byGroup.get(item.group) ?? [];
      list.push({ item, score });
      byGroup.set(item.group, list);
    }
    const limit = q ? LIMIT_QUERY : LIMIT_IDLE;
    const out: { group: Group; items: Item[] }[] = [];
    for (const g of GROUP_ORDER) {
      const list = byGroup.get(g);
      if (!list?.length) continue;
      if (q) list.sort((a, b) => b.score - a.score);
      // With a query, the free-text "Ask Copilot" row goes last in its group; other matches come first.
      const sorted = q && g === "Actions" ? [...list.filter((x) => x.item.id !== "act:ask-page"), ...list.filter((x) => x.item.id === "act:ask-page")] : list;
      out.push({ group: g, items: sorted.slice(0, g === "Pages" && !q ? NAV_ITEMS.length : limit).map((x) => x.item) });
    }
    // With a query, put groups with real matches above the Actions fallback.
    if (q) out.sort((a, b) => (a.group === "Actions" ? 1 : 0) - (b.group === "Actions" ? 1 : 0));
    return out;
  }, [items, query]);

  const flat = useMemo(() => rows.flatMap((r) => r.items), [rows]);
  const current = Math.min(active, Math.max(0, flat.length - 1));

  useEffect(() => setActive(0), [query]);
  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>(`[data-idx="${current}"]`)?.scrollIntoView({ block: "nearest" });
  }, [current]);

  if (!open) return null;

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      onClose();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((current + 1) % Math.max(1, flat.length));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((current - 1 + flat.length) % Math.max(1, flat.length));
    } else if (e.key === "Enter" && !e.nativeEvent.isComposing) {
      e.preventDefault();
      flat[current]?.run();
    } else if (e.key === "Tab") {
      // Focus trap: the input is the only focus stop; options are chosen with the arrows.
      e.preventDefault();
      inputRef.current?.focus();
    }
  };

  const activeId = flat[current] ? `${listId}-${current}` : undefined;
  let idx = -1;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center px-4 pb-4 pt-[9vh]" onKeyDown={onKeyDown}>
      <button type="button" aria-label="Close command palette" tabIndex={-1} className="absolute inset-0 cursor-default border-0 bg-ink/40" onClick={onClose} />
      <div role="dialog" aria-modal="true" aria-label="Command palette" className="card relative flex max-h-[80vh] w-full max-w-[660px] flex-col overflow-hidden shadow-2xl">
        <div className="flex items-center gap-2.5 border-b border-line px-3.5 py-2.5">
          <SearchIcon size={16} className="flex-none text-muted" />
          <label htmlFor={`${listId}-q`} className="sr-only">
            Search or run a command
          </label>
          <input
            id={`${listId}-q`}
            ref={inputRef}
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={activeId}
            aria-autocomplete="list"
            autoComplete="off"
            spellCheck={false}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Jump to a cluster, cert or page… or ask Copilot"
            className="min-h-[32px] min-w-0 flex-1 border-0 bg-transparent text-[15px] text-ink outline-none focus-visible:outline-none"
          />
          {loading && <Spinner className="text-muted" />}
          <span className="kbd">esc</span>
        </div>
        <ul ref={listRef} id={listId} role="listbox" aria-label="Results" className="m-0 min-h-0 flex-1 list-none overflow-y-auto p-0 pb-2 pt-1">
          {rows.map((r) => (
            <li key={r.group} role="presentation">
              <div className="px-3.5 pb-1 pt-2.5 text-[11px] font-semibold uppercase tracking-[0.06em] text-muted" aria-hidden="true">
                {r.group}
              </div>
              <ul role="group" aria-label={r.group} className="m-0 list-none p-0">
                {r.items.map((it) => {
                  idx += 1;
                  const i = idx;
                  const [kind, tone] = KIND[it.group];
                  return (
                    <li
                      key={it.id}
                      id={`${listId}-${i}`}
                      data-idx={i}
                      role="option"
                      aria-selected={i === current}
                      className="pal-item"
                      onMouseMove={() => i !== current && setActive(i)}
                      onMouseDown={(e) => e.preventDefault()}
                      onClick={() => it.run()}
                    >
                      <Pill tone={tone} className="min-w-[66px] justify-center">
                        {kind}
                      </Pill>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate">{it.label}</span>
                        {it.hint && <span className="block truncate text-xs text-muted">{it.hint}</span>}
                      </span>
                      {i === current && (
                        <span className="kbd" aria-hidden="true">
                          ↵
                        </span>
                      )}
                    </li>
                  );
                })}
              </ul>
            </li>
          ))}
          {flat.length === 0 && (
            <li role="presentation" className="px-3.5 py-2.5 text-[13px] text-muted">
              {loading ? "Loading…" : "Nothing matches."}
            </li>
          )}
        </ul>
        <div className="flex flex-wrap gap-x-3.5 gap-y-1 border-t border-line bg-slate-50 px-3.5 py-2 text-xs text-muted">
          <span>↑↓ move</span>
          <span>↵ open</span>
          <span>esc close</span>
          {data?.errors.length ? <span className="text-warn-fg">Couldn&apos;t load {data.errors.join(", ")}</span> : null}
        </div>
      </div>
    </div>
  );
}
