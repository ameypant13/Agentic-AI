import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { Link, NavLink, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, streamMessage } from "../api/client";
import type { ChatItem, Step, StreamEvent } from "../api/types";
import { ActionCard } from "../components/ActionButtons";
import { AlertIcon, ArrowRightIcon, CheckIcon, KeyIcon, PlusIcon, Spinner, XIcon } from "../components/Icons";
import { Markdown } from "../components/Markdown";
import { Empty, ErrorCard, Loading } from "../components/States";
import { relativeTime } from "../lib/format";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";

/** A chat item plus client-only state for turns produced in this session. */
type UiItem = ChatItem & {
  streaming?: boolean;
  error?: string;
  investigationTitle?: string;
};

const SUGGESTIONS: { label: string; send: boolean }[] = [
  { label: "Which control planes failed to provision this week?", send: true },
  { label: "Why did <tenant> fail?", send: false },
  { label: "Which certificates expire in the next 14 days?", send: true },
];

const tmpId = () => `tmp-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;

function emptyItem(role: "user" | "assistant", text = ""): UiItem {
  return { id: tmpId(), role, text, steps: [], actions: [], investigationId: null, createdAt: new Date().toISOString() };
}

function applyEvent(item: UiItem, ev: StreamEvent): UiItem {
  switch (ev.event) {
    case "step":
      return { ...item, steps: [...item.steps.filter((s) => s.id !== ev.data.id), { ...ev.data, ok: null }] };
    case "step_done":
      return {
        ...item,
        steps: item.steps.map((s) =>
          s.id === ev.data.id ? { ...s, ok: ev.data.ok, summary: ev.data.summary ?? s.summary } : s,
        ),
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

// ---------- pieces ----------

function StepIcon({ ok }: { ok: boolean | null }) {
  if (ok === null)
    return (
      <span className="mt-px flex h-4 w-4 flex-none items-center justify-center text-violet" aria-label="Running">
        <Spinner size={13} />
      </span>
    );
  return ok ? (
    <span className="mt-px flex h-4 w-4 flex-none items-center justify-center rounded-full bg-ok-bg text-ok-fg" aria-label="Done">
      <CheckIcon size={11} strokeWidth={3} />
    </span>
  ) : (
    <span className="mt-px flex h-4 w-4 flex-none items-center justify-center rounded-full bg-crit-bg text-crit-fg" aria-label="Failed">
      <XIcon size={10} strokeWidth={3} />
    </span>
  );
}

function Steps({ steps, streaming, isAdmin }: { steps: Step[]; streaming: boolean; isAdmin: boolean }) {
  const [open, setOpen] = useState(streaming);
  if (steps.length === 0) return null;
  const n = steps.length;
  const failed = steps.filter((s) => s.ok === false).length;
  const summary = streaming
    ? `Checking… ${n} ${n === 1 ? "thing" : "things"} so far`
    : `Checked ${n} ${n === 1 ? "thing" : "things"}${failed ? ` · ${failed} failed` : ""}`;
  return (
    <details className="card px-3.5 py-3" open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="cursor-pointer text-[13px] font-semibold text-slate-700">{summary}</summary>
      <ol className="m-0 mt-2.5 flex list-none flex-col gap-2 p-0">
        {steps.map((s) => (
          <li key={s.id} className="flex items-start gap-2.5 text-[13px] text-slate-700">
            <StepIcon ok={s.ok} />
            <div className="min-w-0">
              <div>{s.label}</div>
              {isAdmin && s.tool && (
                <div className="mt-0.5 break-all font-mono text-xs text-muted">
                  <span className="text-ink">{s.tool}</span>
                  {s.input ? ` ${s.input}` : ""}
                </div>
              )}
              {isAdmin && s.summary && <div className="mt-0.5 text-xs text-muted">{s.summary}</div>}
            </div>
          </li>
        ))}
      </ol>
    </details>
  );
}

function AssistantTurn({ item, isAdmin }: { item: UiItem; isAdmin: boolean }) {
  const streaming = !!item.streaming;
  return (
    <div className="flex max-w-[92%] flex-col gap-3">
      <Steps steps={item.steps} streaming={streaming} isAdmin={isAdmin} />
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
      {item.actions.length > 0 && (
        <div className="flex flex-col gap-3">
          {item.actions.map((a) => (
            <ActionCard key={a.id} action={a} compact />
          ))}
        </div>
      )}
      {item.investigationId && (
        <div className="text-xs text-muted">
          Investigation:{" "}
          <Link to={`/investigations/${encodeURIComponent(item.investigationId)}`}>
            {item.investigationTitle || "view details"}
          </Link>
        </div>
      )}
      {!streaming && item.keySource && (
        <div className="text-xs text-muted">
          {item.keySource === "personal" ? "Used your Claude key" : "Used the shared team key"}
        </div>
      )}
    </div>
  );
}

function UserBubble({ text }: { text: string }) {
  return (
    <div className="max-w-[80%] self-end whitespace-pre-wrap break-words rounded-[12px_12px_2px_12px] bg-ink px-3.5 py-3 text-sm leading-normal text-white">
      {text}
    </div>
  );
}

type ComposerProps = {
  disabled: boolean;
  onSend: (t: string) => void;
  draft: string;
  setDraft: (t: string) => void;
  textareaRef: RefObject<HTMLTextAreaElement>;
};

function Composer({ disabled, onSend, draft, setDraft, textareaRef }: ComposerProps) {
  const submit = () => {
    const t = draft.trim();
    if (!t || disabled) return;
    onSend(t);
    setDraft("");
  };
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
  };
  return (
    <form
      className="card flex items-end gap-2.5 px-3 py-2.5 focus-within:border-violet"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <label htmlFor="msg" className="sr-only">
        Message Copilot
      </label>
      <textarea
        id="msg"
        ref={textareaRef}
        rows={2}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKeyDown}
        disabled={disabled}
        placeholder={disabled ? "Copilot is replying…" : "Ask about a tenant, a failed job, or a certificate…"}
        className="min-w-0 flex-1 resize-none border-0 bg-transparent text-sm outline-none focus-visible:outline-none disabled:text-muted"
      />
      <button type="submit" className="btn btn-primary" disabled={disabled || !draft.trim()}>
        {disabled && <Spinner />}
        Send
      </button>
    </form>
  );
}

function NoKeyBanner() {
  return (
    <div role="note" className="card flex flex-wrap items-center gap-3 border-warn-border bg-warn-bg/50 p-4">
      <KeyIcon size={18} className="flex-none text-warn-fg" />
      <div className="min-w-0 flex-1 text-[13px] text-slate-800">
        <div className="font-semibold text-warn-fg">No Claude API key available</div>
        There is no shared team key configured, so Copilot needs your personal Claude API key before it can answer.
      </div>
      <Link to="/settings" className="btn btn-primary">
        Add your key <ArrowRightIcon size={14} />
      </Link>
    </div>
  );
}

// ---------- page ----------

export default function CopilotPage() {
  const { chatId } = useParams<{ chatId?: string }>();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const { me } = useMe();

  const chats = useApi(() => api.chats());
  const reloadChats = chats.reload;
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [items, setItems] = useState<UiItem[]>([]);
  const [loadingChat, setLoadingChat] = useState(false);
  const [chatError, setChatError] = useState<Error | null>(null);
  const [streaming, setStreaming] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");

  /** Chat we just created and navigated to ourselves — don't refetch it and wipe the live turn. */
  const ownNavigation = useRef<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const sentFromUrl = useRef<string | null>(null);
  const endRef = useRef<HTMLDivElement>(null);
  const [loadTick, setLoadTick] = useState(0);
  const [loadedFor, setLoadedFor] = useState<string | null>(null);

  // Load the chat whenever the route's chatId changes.
  useEffect(() => {
    if (chatId && ownNavigation.current === chatId) {
      ownNavigation.current = null;
      setLoadedFor(chatId);
      return;
    }
    abortRef.current?.abort();
    abortRef.current = null;
    setStreaming(false);
    setSendError(null);
    setChatError(null);
    if (!chatId) {
      setItems([]);
      setLoadedFor(null);
      return;
    }
    let cancelled = false;
    setLoadingChat(true);
    setItems([]);
    api.chat(chatId).then(
      (chat) => {
        if (cancelled) return;
        setItems(chat.items);
        setLoadingChat(false);
        setLoadedFor(chatId);
      },
      (e: unknown) => {
        if (cancelled) return;
        setChatError(e instanceof Error ? e : new Error(String(e)));
        setLoadingChat(false);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [chatId, loadTick]);

  // Abort any live stream when leaving the page.
  useEffect(() => () => abortRef.current?.abort(), []);

  // Keep the newest content in view while a reply streams in.
  useEffect(() => {
    if (streaming) endRef.current?.scrollIntoView({ block: "end" });
  }, [items, streaming]);

  const send = useCallback(
    async (text: string) => {
      if (streaming) return;
      setSendError(null);
      setStreaming(true);

      let id = chatId;
      if (!id) {
        try {
          const created = await api.createChat(text.slice(0, 80));
          id = created.id;
          ownNavigation.current = id;
          navigate(`/copilot/${encodeURIComponent(id)}`);
        } catch (e) {
          setSendError(e instanceof Error ? e.message : String(e));
          setStreaming(false);
          setDraft(text);
          return;
        }
      }

      const user = emptyItem("user", text);
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
            // Capture the id now: the updater runs later, after `liveId` may have been renamed by `done`.
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
          // A stream that ended without `done`/`error` should not spin forever.
          setItems((prev) => prev.map((it) => (it.id === liveId && it.streaming ? { ...it, streaming: false } : it)));
          setStreaming(false);
          reloadChats();
        }
      }
    },
    [chatId, navigate, streaming, reloadChats],
  );

  // `?send=<prompt>` (from "Investigate" on a finding): send once after the chat loads, then strip it.
  const pending = params.get("send");
  useEffect(() => {
    if (!pending || !chatId || loadedFor !== chatId || loadingChat || streaming) return;
    const key = `${chatId}\n${pending}`;
    const next = new URLSearchParams(params);
    next.delete("send");
    setParams(next, { replace: true });
    if (sentFromUrl.current === key) return;
    sentFromUrl.current = key;
    if (me.claudeKey.source === "none") {
      setDraft(pending);
      return;
    }
    void send(pending);
  }, [pending, chatId, loadedFor, loadingChat, streaming, params, setParams, send, me.claudeKey.source]);

  /** Put a templated suggestion in the composer with its `<placeholder>` selected for typing over. */
  const fillTemplate = (template: string) => {
    setDraft(template);
    requestAnimationFrame(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.focus();
      const start = template.indexOf("<");
      const end = template.indexOf(">") + 1;
      if (start >= 0 && end > start) el.setSelectionRange(start, end);
    });
  };

  const noKey = me.claudeKey.source === "none";
  const empty = !loadingChat && !chatError && items.length === 0;

  return (
    <div className="flex w-full min-w-0 flex-col min-[900px]:flex-row">
      {/* chat list */}
      <aside
        aria-label="Chats"
        className="flex flex-col gap-3 border-b border-line bg-white p-3 min-[900px]:w-[248px] min-[900px]:flex-none min-[900px]:border-b-0 min-[900px]:border-r"
      >
        <button
          type="button"
          className="btn btn-primary w-full"
          onClick={() => navigate("/copilot")}
          disabled={!chatId && items.length === 0}
        >
          <PlusIcon size={14} /> New chat
        </button>
        <div className="px-1 text-xs font-semibold uppercase tracking-wide text-muted">Your chats</div>
        {chats.error ? (
          <ErrorCard title="Couldn't load chats" error={chats.error} onRetry={chats.reload} />
        ) : chats.loading && !chats.data ? (
          <Loading label="Loading chats…" />
        ) : (chats.data ?? []).length === 0 ? (
          <div className="px-1 text-[13px] text-muted">No chats yet.</div>
        ) : (
          <ul className="m-0 flex max-h-[40vh] list-none flex-col gap-0.5 overflow-y-auto p-0 min-[900px]:max-h-none">
            {chats.data!.map((c) => (
              <li key={c.id}>
                <NavLink
                  to={`/copilot/${encodeURIComponent(c.id)}`}
                  className={({ isActive }) => `nav-link flex-col !items-start !gap-0 !py-2 ${isActive ? "active" : ""}`}
                >
                  <span className="line-clamp-2 w-full break-words">{c.title || "Untitled chat"}</span>
                  <span className="text-xs font-normal text-muted">{relativeTime(c.updatedAt)}</span>
                </NavLink>
              </li>
            ))}
          </ul>
        )}
      </aside>

      {/* conversation */}
      <section aria-label="Conversation" className="flex min-w-0 max-w-[860px] flex-1 flex-col gap-5 px-4 pt-6 sm:px-8">
        {chatError ? (
          <ErrorCard title="Couldn't load this chat" error={chatError} onRetry={() => setLoadTick((t) => t + 1)} />
        ) : loadingChat ? (
          <Loading label="Loading conversation…" />
        ) : empty ? (
          <div className="card">
            <Empty title="Ask Copilot about the fleet">
              Copilot investigates control-plane provisioning failures using Control Hub&apos;s API. It can read what you can see; any
              change it proposes shows up as a button for you to approve.
            </Empty>
          </div>
        ) : (
          <div className="flex flex-col gap-5" aria-live="polite" aria-busy={streaming}>
            {items.map((it) =>
              it.role === "user" ? (
                <UserBubble key={it.id} text={it.text} />
              ) : (
                <AssistantTurn key={it.id} item={it} isAdmin={me.isAdmin} />
              ),
            )}
          </div>
        )}

        <div ref={endRef} />

        <div className="sticky bottom-0 mt-auto flex flex-col gap-2.5 bg-page pb-6 pt-3">
          {sendError && <ErrorCard title="Couldn't send your message" error={sendError} />}
          {noKey ? (
            <NoKeyBanner />
          ) : (
            <>
              {empty && (
                <div className="flex flex-wrap gap-2">
                  {SUGGESTIONS.map((s) => (
                    <button
                      key={s.label}
                      type="button"
                      className="btn"
                      disabled={streaming}
                      onClick={() => (s.send ? send(s.label) : fillTemplate(s.label))}
                    >
                      {s.label}
                    </button>
                  ))}
                </div>
              )}
              <Composer
                disabled={streaming || !!chatError}
                onSend={send}
                draft={draft}
                setDraft={setDraft}
                textareaRef={textareaRef}
              />
            </>
          )}
        </div>
      </section>
    </div>
  );
}
