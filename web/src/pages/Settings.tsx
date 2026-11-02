import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { ClaudeKey } from "../api/types";
import { GrafanaSettings } from "../components/GrafanaSettings";
import { KeyIcon, Spinner } from "../components/Icons";
import { Page } from "../components/Layout";
import { Pill } from "../components/Pill";
import { PageHeader } from "../components/States";
import { useMe } from "../lib/me";

function SourceLine({ k }: { k: ClaudeKey }) {
  if (k.source === "personal") return <>Chats use <strong>your personal key</strong>.</>;
  if (k.source === "shared") return <>Chats use <strong>the shared team key</strong>.</>;
  return <>No key is available — Copilot can&apos;t answer until you add one.</>;
}

const TABS = [
  ["claude", "Claude API key"],
  ["grafana", "Grafana"],
] as const;
type Tab = (typeof TABS)[number][0];

export default function SettingsPage() {
  const [params] = useSearchParams();
  const tab: Tab = params.get("tab") === "grafana" ? "grafana" : "claude";
  return (
    <Page>
      <PageHeader title="Settings" />
      <nav aria-label="Settings sections" className="-mt-2 flex flex-wrap gap-0.5 border-b border-line">
        {TABS.map(([t, label]) => (
          <Link key={t} to={t === "claude" ? "/settings" : `/settings?tab=${t}`} className={`tab-link ${tab === t ? "active" : ""}`} aria-current={tab === t ? "page" : undefined}>
            {label}
          </Link>
        ))}
      </nav>
      {tab === "grafana" ? <GrafanaSettings /> : <ClaudeKeySettings />}
    </Page>
  );
}

function ClaudeKeySettings() {
  const { me, setMe } = useMe();
  const key = me.claudeKey;
  const [apiKey, setApiKey] = useState("");
  const [busy, setBusy] = useState<"save" | "remove" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const update = (k: ClaudeKey) => setMe({ ...me, claudeKey: k });

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!apiKey.trim()) return;
    setBusy("save");
    setError(null);
    setNotice(null);
    try {
      update(await api.setClaudeKey(apiKey.trim()));
      setApiKey("");
      setNotice("Key saved. New chats will use it.");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  const remove = async () => {
    setBusy("remove");
    setError(null);
    setNotice(null);
    try {
      update(await api.deleteClaudeKey());
      setNotice("Key removed.");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="card max-w-[640px] p-5" aria-labelledby="ck">
      <div className="flex items-center gap-2">
        <KeyIcon size={16} className="text-violet" />
        <h2 id="ck" className="h2">
          Claude API key
        </h2>
      </div>
      <p className="mb-0 mt-2 text-[13px] leading-relaxed text-slate-700">
        Copilot chats use your personal Claude API key when you set one; otherwise they use the shared team key. The key is
        checked with the Claude API before it is stored and is never shown again.
      </p>

      <dl className="mb-0 mt-4 grid grid-cols-[repeat(auto-fit,minmax(min(160px,100%),1fr))] gap-3 text-[13px]">
        <div>
          <dt className="text-muted">Personal key</dt>
          <dd className="m-0 mt-0.5">
            {key.configured ? <Pill tone="success">Configured</Pill> : <Pill>Not set</Pill>}
          </dd>
        </div>
        <div>
          <dt className="text-muted">Ends in</dt>
          <dd className="m-0 mt-0.5 font-mono">{key.configured && key.last4 ? `••••${key.last4}` : "—"}</dd>
        </div>
        <div>
          <dt className="text-muted">Next chat uses</dt>
          <dd className="m-0 mt-0.5">
            {key.source === "personal" ? (
              <Pill tone="violet">Your key</Pill>
            ) : key.source === "shared" ? (
              <Pill tone="info">Shared team key</Pill>
            ) : (
              <Pill tone="critical">No key</Pill>
            )}
          </dd>
        </div>
      </dl>
      <p className="mb-0 mt-3 text-[13px] text-muted">
        <SourceLine k={key} />
      </p>

      <form onSubmit={save} className="mt-4 flex flex-col gap-2 border-t border-line pt-4">
        <label htmlFor="apikey" className="label">
          {key.configured ? "Replace your key" : "Your API key"}
        </label>
        <div className="flex flex-wrap gap-2">
          <input
            id="apikey"
            type="password"
            autoComplete="off"
            spellCheck={false}
            className="input min-w-[220px] flex-1 font-mono"
            placeholder="sk-ant-…"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            aria-describedby="apikey-note"
          />
          <button type="submit" className="btn btn-primary" disabled={busy !== null || !apiKey.trim()}>
            {busy === "save" && <Spinner />}
            Save
          </button>
          {key.configured && (
            <button type="button" className="btn" onClick={remove} disabled={busy !== null}>
              {busy === "remove" && <Spinner />}
              Remove
            </button>
          )}
        </div>
        <p id="apikey-note" className="m-0 text-xs text-muted">
          Use an API key from{" "}
          <a href="https://console.anthropic.com" target="_blank" rel="noreferrer noopener">
            console.anthropic.com
          </a>
          . Claude.ai Pro/Max subscriptions can&apos;t be used here.
        </p>
        {error && (
          <div role="alert" className="rounded-btn bg-crit-bg px-3 py-2 text-[13px] text-crit-fg">
            {error}
          </div>
        )}
        {notice && (
          <div role="status" className="rounded-btn bg-ok-bg px-3 py-2 text-[13px] text-ok-fg">
            {notice}
          </div>
        )}
      </form>
    </section>
  );
}
