import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import { api, streamMessage } from "../../api/client";
import type { ChatItem, StreamEvent } from "../../api/types";
import { useMe } from "../../lib/me";
import { useCurrentPageContext } from "../../lib/pageContext";
import { ActionCard } from "../ActionButtons";
import { AlertIcon, ArrowRightIcon, CheckIcon, KeyIcon, PlusIcon, SparkIcon, Spinner, XIcon } from "../Icons";
import { Markdown } from "../Markdown";

// ---------- open / closed state, shared with the header, the palette and pages ----------

const OPEN_KEY = "aic.dock";
/** Below this width the dock is an overlay drawer instead of a column. */
export const DOCK_WIDE_QUERY = "(min-width: 1100px)";

function initialOpen(): boolean {
  try {
    const v = window.localStorage.getItem(OPEN_KEY);
    if (v === "open") return true;
    if (v === "closed") return false;
  } catch {
    // storage blocked
  }
  // Collapsed until the person opens it; it then remembers their choice.
  return false;
}

type Pending = { text: string; n: number };

type DockCtx = {
  open: boolean;
  setOpen: (open: boolean) => void;
  toggle: () => void;
  /** Open the dock and ask `question` (with the current page context). */
  ask: (question: string) => void;
  pending: Pending | null;
  clearPending: () => void;
};

const Ctx = createContext<DockCtx | null>(null);

export function CopilotDockProvider({ children }: { children: ReactNode }) {
  const [open, setOpenState] = useState(initialOpen);
  const [pending, setPending] = useState<Pending | null>(null);

  const setOpen = useCallback((o: boolean) => {
    setOpenState(o);
    try {
      window.localStorage.setItem(OPEN_KEY, o ? "open" : "closed");
    } catch {
      // storage blocked
    }
  }, []);
  const toggle = useCallback(() => setOpenState((o) => {
    try {
      window.localStorage.setItem(OPEN_KEY, o ? "closed" : "open");
    } catch {
      // storage blocked
    }
    return !o;
  }), []);
  const ask = useCallback(
    (text: string) => {
      setOpen(true);
      setPending((p) => ({ text, n: (p?.n ?? 0) + 1 }));
    },
    [setOpen],
  );
  const clearPending = useCallback(() => setPending(null), []);

  const value = useMemo(() => ({ open, setOpen, toggle, ask, pending, clearPending }), [open, setOpen, toggle, ask, pending, clearPending]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useCopilotDock(): DockCtx {
  const c = useContext(Ctx);
  if (!c) throw new Error("useCopilotDock must be used inside CopilotDockProvider");
  return c;
}

/** The dock is not shown on the full Copilot page. */
export function dockHiddenOn(pathname: string): boolean {
  return pathname === "/copilot" || pathname.startsWith("/copilot/");
}

// ---------- chat ----------

type UiItem = ChatItem & { streaming?: boolean; error?: string; investigationTitle?: string; display?: string };

const GENERIC_SUGGESTIONS = [
  "What should I fix first?",
  "Which certificates expire in the next 14 days?",
  "Which control planes failed to provision this week?",
];

const tmpId = () => `tmp-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;

function emptyItem(role: "user" | "assistant", text = ""): UiItem {
  return { id: tmpId(), role, text, steps: [], actions: [], investigationId: null, createdAt: new Date().toISOString() };
}

/** Same reducer as the Copilot page. */
function applyEvent(item: UiItem, ev: StreamEvent): UiItem {
  switch (ev.event) {
    case "step":
      return { ...item, steps: [...item.steps.filter((s) => s.id !== ev.data.id), { ...ev.data, ok: null }] };
    case "step_done":
      return {
        ...item,
        steps: item.steps.map((s) => (s.id === ev.data.id ? { ...s, ok: ev.data.ok, summary: ev.data.summary ?? s.summary } : s)),
      };
    case "text":
      return { ...item, text: item.text + (ev.data.delta ?? "") };
    case "action":
      return { ...item, actions: [...item.actions.filter((a) => a.id !== ev.data.id), ev.data] };
    case "investigation":
      return { ...item, investigationId: ev.data.id, investigationTitle: ev.data.title };
    case "done":
      return { ...item, id: ev.data.itemId || item.id, keySource: ev.data.keySource, streaming: false };
    case "error":
      return { ...item, error: ev.data.message || "The reply failed.", streaming: false };
    default:
      return item;
  }
}

function StepIcon({ ok }: { ok: boolean | null }) {
  if (ok === null)
    return (
      <span className="flex h-4 w-4 flex-none items-center justify-center text-violet" aria-label="Running">
        <Spinner size={12} />
      </span>
    );
  return ok ? (
    <span className="flex h-4 w-4 flex-none items-center justify-center rounded-full bg-ok-bg text-ok-fg" aria-label="Done">
      <CheckIcon size={10} strokeWidth={3} />
    </span>
  ) : (
    <span className="flex h-4 w-4 flex-none items-center justify-center rounded-full bg-crit-bg text-crit-fg" aria-label="Failed">
      <XIcon size={9} strokeWidth={3} />
    </span>
  );
}

function AssistantTurn({ item }: { item: UiItem }) {
  const streaming = !!item.streaming;
  return (
    <div className="flex flex-col gap-2">
      {item.steps.length > 0 && (
        <ol className="m-0 flex list-none flex-col gap-1.5 p-0">
          {item.steps.map((s) => (
            <li key={s.id} className="flex items-start gap-2 text-xs text-muted">
              <StepIcon ok={s.ok} />
              <span className="min-w-0 break-words">{s.label}</span>
            </li>
          ))}
        </ol>
      )}
      {item.text ? (
        <Markdown text={item.text} />
      ) : streaming ? (
        <div className="flex items-center gap-2 text-[13px] text-muted" role="status">
          <Spinner /> Working on it…
        </div>
      ) : null}
      {item.error && (
        <div role="alert" className="flex items-start gap-2 rounded-btn bg-crit-bg px-3 py-2 text-[13px] text-crit-fg">
          <AlertIcon size={14} className="mt-0.5 flex-none" />
          <span className="break-words">{item.error}</span>
        </div>
      )}
      {item.actions.map((a) => (
        <ActionCard key={a.id} action={a} compact />
      ))}
      {item.investigationId && (
        <div className="text-xs text-muted">
          Investigation:{" "}
          <Link to={`/investigations/${encodeURIComponent(item.investigationId)}`}>{item.investigationTitle || "view details"}</Link>
        </div>
      )}
      {!streaming && item.keySource && (
        <div className="text-xs text-muted">{item.keySource === "personal" ? "Used your Claude key" : "Used the shared team key"}</div>
      )}
    </div>
  );
}

function NoKeyNote() {
  return (
    <div role="note" className="flex flex-col gap-2 rounded-card border border-warn-border bg-warn-bg/50 p-3 text-[13px] text-slate-800">
      <div className="flex items-center gap-2 font-semibold text-warn-fg">
        <KeyIcon size={16} className="flex-none" /> No Claude API key available
      </div>
      There is no shared team key configured, so Copilot needs your personal Claude API key before it can answer.
      <Link to="/settings" className="btn btn-primary self-start">
        Add your key <ArrowRightIcon size={14} />
      </Link>
    </div>
  );
}

/**
 * Right-hand Copilot panel on every page but /copilot. Same chat API and streaming as the Copilot page; the first
 * question of a chat (and any question asked after moving to another page) carries the page's context.
 */
export function CopilotDock() {
  const { open, setOpen, pending, clearPending } = useCopilotDock();
  const { me } = useMe();
  const ctx = useCurrentPageContext();
  const location = useLocation();
  const hidden = dockHiddenOn(location.pathname);

  const [chatId, setChatId] = useState<string | null>(null);
  const [items, setItems] = useState<UiItem[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [wide, setWide] = useState(() => {
    try {
      return window.matchMedia(DOCK_WIDE_QUERY).matches;
    } catch {
      return true;
    }
  });
  const abortRef = useRef<AbortController | null>(null);
  /** Context prompt last sent in this chat; a new one is prefixed again. */
  const sentContext = useRef<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const noKey = me.claudeKey.source === "none";

  useEffect(() => {
    let mq: MediaQueryList;
    try {
      mq = window.matchMedia(DOCK_WIDE_QUERY);
    } catch {
      return;
    }
    const on = (e: MediaQueryListEvent) => setWide(e.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);

  useEffect(() => () => abortRef.current?.abort(), []);

  useEffect(() => {
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [items]);

  // Focus the composer when the user opens the dock (not when it starts open on page load).
  const wasOpen = useRef(open);
  useEffect(() => {
    if (open && !wasOpen.current && !hidden) requestAnimationFrame(() => textareaRef.current?.focus());
    wasOpen.current = open;
  }, [open, hidden]);

  const send = useCallback(
    async (question: string) => {
      if (streaming) return;
      setSendError(null);
      setStreaming(true);

      const prompt = ctx?.prompt ?? "";
      const withContext = prompt && sentContext.current !== prompt;
      const text = withContext ? `Context: ${prompt}\n\nQuestion: ${question}` : question;

      let id = chatId;
      if (!id) {
        try {
          const created = await api.createChat(question.slice(0, 80));
          id = created.id;
          setChatId(id);
        } catch (e) {
          setSendError(e instanceof Error ? e.message : String(e));
          setStreaming(false);
          setDraft(question);
          return;
        }
      }
      if (withContext) sentContext.current = prompt;

      const user: UiItem = { ...emptyItem("user", text), display: question };
      const assistant: UiItem = { ...emptyItem("assistant"), streaming: true };
      let liveId = assistant.id;
      setItems((prev) => [...prev, user, assistant]);

      const ctrl = new AbortController();
      abortRef.current = ctrl;
      try {
        await streamMessage(
          id,
          text,
          (ev) => {
            const target = liveId;
            setItems((prev) => prev.map((it) => (it.id === target ? applyEvent(it, ev) : it)));
            if (ev.event === "done" && ev.data.itemId) liveId = ev.data.itemId;
          },
          ctrl.signal,
        );
      } catch (e) {
        if (ctrl.signal.aborted) return;
        const message = e instanceof Error ? e.message : String(e);
        setItems((prev) => prev.map((it) => (it.id === liveId ? { ...it, error: message, streaming: false } : it)));
      } finally {
        if (abortRef.current === ctrl) {
          abortRef.current = null;
          setItems((prev) => prev.map((it) => (it.id === liveId && it.streaming ? { ...it, streaming: false } : it)));
          setStreaming(false);
        }
      }
    },
    [chatId, ctx?.prompt, streaming],
  );

  // Questions from pages and the palette (ask()).
  useEffect(() => {
    if (!pending || streaming) return;
    clearPending();
    if (noKey) {
      setDraft(pending.text);
      return;
    }
    void send(pending.text);
  }, [pending, streaming, clearPending, noKey, send]);

  const newChat = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    setStreaming(false);
    setChatId(null);
    setItems([]);
    setSendError(null);
    sentContext.current = null;
    textareaRef.current?.focus();
  };

  const submit = () => {
    const t = draft.trim();
    if (!t || streaming) return;
    setDraft("");
    void send(t);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
  };

  if (hidden) return null;

  if (!open) {
    // Collapsed: a thin rail on wide screens; on narrow screens the header button is enough.
    return wide ? (
      <aside aria-label="Copilot, collapsed" className="sticky top-0 flex h-screen w-[52px] flex-none flex-col items-center gap-2 border-l border-line bg-white pt-3">
        <button
          type="button"
          className="btn btn-ghost min-h-[96px] flex-col gap-2 px-1.5"
          onClick={() => setOpen(true)}
          aria-label="Open Copilot"
          aria-expanded={false}
          aria-controls="copilot-dock"
        >
          <SparkIcon size={16} className="text-violet" />
          <span className="text-xs [writing-mode:vertical-rl]">Copilot</span>
        </button>
        {streaming && <Spinner className="text-violet" />}
      </aside>
    ) : null;
  }

  const suggestions = ctx?.suggestions?.length ? ctx.suggestions : GENERIC_SUGGESTIONS;
  const empty = items.length === 0;

  return (
    <>
      {!wide && (
        <button
          type="button"
          aria-label="Close Copilot"
          tabIndex={-1}
          className="fixed inset-0 z-30 cursor-default border-0 bg-ink/40"
          onClick={() => setOpen(false)}
        />
      )}
      <aside
        id="copilot-dock"
        aria-label="Copilot"
        onKeyDown={(e) => {
          if (e.key === "Escape" && !wide) setOpen(false);
        }}
        className={
          wide
            ? "sticky top-0 flex h-screen w-[360px] flex-none flex-col border-l border-line bg-white"
            : "fixed inset-y-0 right-0 z-40 flex w-full max-w-[420px] flex-col border-l border-line bg-white shadow-2xl"
        }
      >
        <div className="flex items-center gap-2 border-b border-line px-3 py-2.5">
          <SparkIcon size={16} className="text-violet" />
          <strong className="text-sm">Copilot</strong>
          <span className="text-xs text-muted">read-only tools</span>
          <div className="ml-auto flex items-center gap-1">
            <button type="button" className="btn btn-ghost btn-sm" onClick={newChat} disabled={empty && !chatId} title="Start a new chat">
              <PlusIcon size={13} /> New
            </button>
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => setOpen(false)} aria-label="Close Copilot">
              <XIcon size={14} />
            </button>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-1.5 border-b border-line bg-slate-50 px-3 py-2">
          <span className="text-xs text-muted">Context</span>
          <span className="pill whitespace-normal bg-violet-tint text-violet">{ctx?.label ?? "No page context"}</span>
        </div>

        <div ref={listRef} className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-3" aria-live="polite" aria-busy={streaming}>
          {empty ? (
            noKey ? (
              <NoKeyNote />
            ) : (
              <>
                <div className="text-[11px] font-semibold uppercase tracking-[0.06em] text-muted">Suggested for this page</div>
                {suggestions.map((q) => (
                  <button
                    key={q}
                    type="button"
                    className="btn h-auto justify-start whitespace-normal py-1.5 text-left"
                    disabled={streaming}
                    onClick={() => void send(q)}
                  >
                    {q}
                  </button>
                ))}
                <p className="m-0 mt-1 text-xs text-muted">
                  Copilot gets this page&apos;s context with your first question. It reads what you can see; any change it proposes shows up
                  as a button for you to approve.
                </p>
              </>
            )
          ) : (
            items.map((it) =>
              it.role === "user" ? (
                <div
                  key={it.id}
                  className="max-w-[88%] self-end whitespace-pre-wrap break-words rounded-[10px_10px_2px_10px] bg-ink px-3 py-2 text-[13px] text-white"
                >
                  {it.display ?? it.text}
                </div>
              ) : (
                <AssistantTurn key={it.id} item={it} />
              ),
            )
          )}
        </div>

        <div className="flex flex-col gap-2 border-t border-line px-3 py-2.5">
          {sendError && (
            <div role="alert" className="flex items-start gap-2 rounded-btn bg-crit-bg px-3 py-2 text-[13px] text-crit-fg">
              <AlertIcon size={14} className="mt-0.5 flex-none" />
              <span className="break-words">Couldn&apos;t send your message: {sendError}</span>
            </div>
          )}
          {noKey && !empty && <NoKeyNote />}
          <label htmlFor="dock-q" className="sr-only">
            Ask Copilot
          </label>
          <textarea
            id="dock-q"
            ref={textareaRef}
            rows={2}
            className="input resize-none py-2 text-[13px]"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={onKeyDown}
            disabled={noKey}
            placeholder={noKey ? "Add a Claude key to ask Copilot" : ctx ? `Ask about ${ctx.label}` : "Ask about what you see"}
          />
          <div className="flex items-center gap-2">
            {chatId ? (
              <Link to={`/copilot/${encodeURIComponent(chatId)}`} className="text-xs">
                Open in Copilot
              </Link>
            ) : (
              <span className="text-xs text-muted">Enter to send</span>
            )}
            <button type="button" className="btn btn-primary btn-sm ml-auto" onClick={submit} disabled={streaming || noKey || !draft.trim()}>
              {streaming && <Spinner />}
              Ask
            </button>
          </div>
        </div>
      </aside>
    </>
  );
}
