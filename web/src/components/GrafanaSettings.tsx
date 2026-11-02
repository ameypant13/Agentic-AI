import { Fragment, useEffect, useState, type FormEvent } from "react";
import { api } from "../api/client";
import type { Cluster, GrafanaInstance, GrafanaMapping, GrafanaStatus } from "../api/types";
import { age, formatDateTime, providerLabel } from "../lib/format";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";
import { Dash, useNow } from "./Cluster";
import { PlusIcon, RefreshIcon, Spinner } from "./Icons";
import { Pill } from "./Pill";
import { Empty, ErrorCard, Loading } from "./States";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

function Notice({ kind, children }: { kind: "error" | "ok"; children: string }) {
  return kind === "error" ? (
    <div role="alert" className="rounded-btn bg-crit-bg px-3 py-2 text-[13px] text-crit-fg">
      {children}
    </div>
  ) : (
    <div role="status" className="rounded-btn bg-ok-bg px-3 py-2 text-[13px] text-ok-fg">
      {children}
    </div>
  );
}

// ---------------------------------------------------------------- add instance

function AddInstanceForm({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [name, setName] = useState("");
  const [url, setUrl] = useState("https://");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const urlOk = /^https:\/\/[^\s/]+/.test(url.trim());
  const valid = name.trim() !== "" && urlOk && token.trim() !== "";

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!valid) return;
    setBusy(true);
    setError(null);
    try {
      await api.addGrafanaInstance({ name: name.trim(), url: url.trim().replace(/\/+$/, ""), token: token.trim() });
      setToken("");
      onAdded();
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3 border-b border-line bg-slate-50 px-4 py-4" aria-label="Add a Grafana instance">
      <div className="grid gap-3 min-[760px]:grid-cols-[minmax(0,180px)_minmax(0,1fr)_minmax(0,1fr)]">
        <div>
          <label htmlFor="gi-name" className="label">
            Name
          </label>
          <input id="gi-name" className="input font-mono" placeholder="azure" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" spellCheck={false} />
        </div>
        <div>
          <label htmlFor="gi-url" className="label">
            URL
          </label>
          <input
            id="gi-url"
            type="url"
            className="input font-mono"
            placeholder="https://monitoring.example.com"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            aria-invalid={url !== "https://" && !urlOk}
            aria-describedby="gi-url-note"
            autoComplete="off"
            spellCheck={false}
          />
          <p id="gi-url-note" className="m-0 mt-1 text-xs text-muted">
            Must start with https://
          </p>
        </div>
        <div>
          <label htmlFor="gi-token" className="label">
            Service-account token
          </label>
          <input
            id="gi-token"
            type="password"
            className="input font-mono"
            placeholder="glsa_…"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            aria-describedby="gi-token-note"
          />
        </div>
      </div>
      <p id="gi-token-note" className="m-0 text-xs text-muted">
        Create a service account with the Viewer role in that Grafana: Administration → Users and access → Service accounts, then add a token. The
        token is checked against Grafana, stored encrypted and never shown again.
      </p>
      {error && <Notice kind="error">{error}</Notice>}
      <div className="flex gap-2">
        <button type="submit" className="btn btn-primary" disabled={busy || !valid}>
          {busy && <Spinner />}
          Add instance
        </button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// ---------------------------------------------------------------- instances

function InstancesTable({
  instances,
  isAdmin,
  now,
  onDelete,
  deleting,
}: {
  instances: GrafanaInstance[];
  isAdmin: boolean;
  now: number;
  onDelete: (g: GrafanaInstance) => void;
  deleting: string | null;
}) {
  const [open, setOpen] = useState<Set<string>>(new Set());
  const toggle = (k: string) =>
    setOpen((s) => {
      const next = new Set(s);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  const cols = isAdmin ? 9 : 8;
  return (
    <table className="table min-w-[1000px]">
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">URL</th>
          <th scope="col">Source</th>
          <th scope="col">Clusters</th>
          <th scope="col">Log clusters</th>
          <th scope="col">Checked</th>
          <th scope="col">Evaluated</th>
          <th scope="col">Errors</th>
          {isAdmin && (
            <th scope="col">
              <span className="sr-only">Actions</span>
            </th>
          )}
        </tr>
      </thead>
      <tbody>
        {instances.map((g) => {
          const clusters = [...(g.clusters ?? [])].sort();
          const ruleErrors = g.ruleErrors ?? [];
          const isOpen = open.has(g.name);
          const listId = `gi-clusters-${g.name}`;
          return (
            <Fragment key={g.name}>
              <tr>
                <td className="whitespace-nowrap font-mono text-xs font-medium">{g.name}</td>
                <td className="max-w-[260px] break-all font-mono text-xs">
                  <a href={g.url} target="_blank" rel="noreferrer noopener">
                    {g.url}
                  </a>
                </td>
                <td>
                  <Pill tone={g.source === "secret" ? "neutral" : "violet"}>{g.source}</Pill>
                </td>
                <td>
                  {clusters.length === 0 ? (
                    <span className="tabular-nums text-muted">0</span>
                  ) : (
                    <button type="button" className="link-btn whitespace-nowrap text-[13px]" aria-expanded={isOpen} aria-controls={listId} onClick={() => toggle(g.name)}>
                      <span aria-hidden="true" className="mr-1 inline-block w-2 text-muted">
                        {isOpen ? "▾" : "▸"}
                      </span>
                      {clusters.length} {clusters.length === 1 ? "cluster" : "clusters"}
                    </button>
                  )}
                </td>
                <td className="tabular-nums">{g.logClusters}</td>
                <td className="whitespace-nowrap text-muted" title={g.checkedAt ? formatDateTime(g.checkedAt) : undefined}>
                  {g.checkedAt ? `${age(g.checkedAt, now)} ago` : "never"}
                </td>
                <td className="whitespace-nowrap text-muted" title={g.evaluatedAt ? formatDateTime(g.evaluatedAt) : undefined}>
                  {g.evaluatedAt ? `${age(g.evaluatedAt, now)} ago` : "not yet"}
                </td>
                <td className="min-w-[200px] max-w-[320px]">
                  {g.error ? <div className="break-words text-[13px] text-crit-fg">{g.error}</div> : ruleErrors.length === 0 && <Dash />}
                  {ruleErrors.length > 0 && (
                    <details className={g.error ? "mt-1" : ""}>
                      <summary className="cursor-pointer text-[13px] font-medium text-warn-fg">
                        {ruleErrors.length} rule {ruleErrors.length === 1 ? "error" : "errors"}
                      </summary>
                      <ul className="m-0 mt-1 flex list-none flex-col gap-1 p-0">
                        {ruleErrors.map((r) => (
                          <li key={r} className="break-words font-mono text-xs">
                            {r}
                          </li>
                        ))}
                      </ul>
                    </details>
                  )}
                </td>
                {isAdmin && (
                  <td className="text-right">
                    {g.source === "settings" ? (
                      <button type="button" className="btn min-h-[30px] px-2.5 text-xs text-crit-fg" onClick={() => onDelete(g)} disabled={deleting !== null}>
                        {deleting === g.name && <Spinner size={12} />}
                        Delete
                      </button>
                    ) : (
                      <span className="text-xs text-muted" title="Defined in the deployment secret">
                        from secret
                      </span>
                    )}
                  </td>
                )}
              </tr>
              {isOpen && (
                <tr id={listId} className="bg-slate-50">
                  <td colSpan={cols}>
                    <div className="mb-1.5 text-xs text-muted">Metrics clusters (the `cluster` label) this instance reports</div>
                    <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
                      {clusters.map((c) => (
                        <li key={c} className="rounded-full border border-line bg-white px-2 py-0.5 font-mono text-xs text-slate-700">
                          {c}
                        </li>
                      ))}
                    </ul>
                  </td>
                </tr>
              )}
            </Fragment>
          );
        })}
      </tbody>
    </table>
  );
}

// ---------------------------------------------------------------- mappings

type Draft = { instance: string; promCluster: string; lokiCluster: string };

function MappingEditor({
  cluster,
  instances,
  onCancel,
  onSaved,
}: {
  cluster: Cluster;
  instances: GrafanaInstance[];
  onCancel: () => void;
  onSaved: (m: GrafanaMapping | null) => void;
}) {
  const cur = cluster.grafana;
  const [d, setD] = useState<Draft>({
    instance: cur?.instance ?? instances[0]?.name ?? "",
    promCluster: cur?.promCluster ?? "",
    lokiCluster: cur?.lokiCluster ?? "",
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inst = instances.find((i) => i.name === d.instance);
  const options = [...(inst?.clusters ?? [])].sort();
  const promOptions = d.promCluster && !options.includes(d.promCluster) ? [d.promCluster, ...options] : options;
  const key = cluster.id.replace(/[^a-zA-Z0-9_-]/g, "_");

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!d.instance || !d.promCluster) return;
    setBusy(true);
    setError(null);
    try {
      onSaved(await api.setGrafanaMapping(cluster.name, { instance: d.instance, promCluster: d.promCluster, lokiCluster: d.lokiCluster.trim() }));
    } catch (err) {
      setError(errMsg(err));
      setBusy(false);
    }
  };

  return (
    <form onSubmit={save} className="flex flex-col gap-3" aria-label={`Edit the Grafana mapping of ${cluster.name}`}>
      <div className="grid gap-3 min-[760px]:grid-cols-3">
        <div>
          <label htmlFor={`gm-inst-${key}`} className="label">
            Instance
          </label>
          <select
            id={`gm-inst-${key}`}
            className="select min-h-[36px] w-full"
            value={d.instance}
            onChange={(e) => setD({ ...d, instance: e.target.value, promCluster: "" })}
          >
            {instances.length === 0 && <option value="">No instances</option>}
            {instances.map((i) => (
              <option key={i.name} value={i.name}>
                {i.name}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor={`gm-prom-${key}`} className="label">
            Metrics cluster
          </label>
          <select id={`gm-prom-${key}`} className="select min-h-[36px] w-full font-mono" value={d.promCluster} onChange={(e) => setD({ ...d, promCluster: e.target.value })}>
            <option value="">{promOptions.length === 0 ? "This instance reports no clusters" : "Pick a cluster…"}</option>
            {promOptions.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor={`gm-loki-${key}`} className="label">
            Logs cluster <span className="font-normal text-muted">(Loki k8s_cluster_name, optional)</span>
          </label>
          <input
            id={`gm-loki-${key}`}
            className="input min-h-[36px] font-mono text-[13px]"
            value={d.lokiCluster}
            onChange={(e) => setD({ ...d, lokiCluster: e.target.value })}
            autoComplete="off"
            spellCheck={false}
          />
        </div>
      </div>
      {error && <Notice kind="error">{error}</Notice>}
      <div className="flex gap-2">
        <button type="submit" className="btn btn-primary" disabled={busy || !d.instance || !d.promCluster}>
          {busy && <Spinner />}
          Save override
        </button>
        <button type="button" className="btn" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function MappingCard({ instances, isAdmin }: { instances: GrafanaInstance[]; isAdmin: boolean }) {
  const clusters = useApi(() => api.clusters());
  const [editing, setEditing] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<{ id: string; message: string } | null>(null);
  const cols = isAdmin ? 6 : 5;

  const update = (id: string, m: GrafanaMapping | null) => clusters.setData((prev) => prev?.map((c) => (c.id === id ? { ...c, grafana: m } : c)));

  const reset = async (c: Cluster) => {
    if (!window.confirm(`Reset ${c.name} to its automatic Grafana mapping?`)) return;
    setBusy(c.id);
    setError(null);
    try {
      update(c.id, (await api.resetGrafanaMapping(c.name)) ?? null);
    } catch (err) {
      setError({ id: c.id, message: errMsg(err) });
    } finally {
      setBusy(null);
    }
  };

  const rows = [...(clusters.data ?? [])].sort((a, b) => a.name.localeCompare(b.name));

  return (
    <section className="card" aria-labelledby="gm-title">
      <div className="card-head">
        <div>
          <h2 id="gm-title" className="h2">
            Cluster mapping
          </h2>
          <p className="m-0 mt-0.5 text-xs text-muted">
            Each control-hub cluster is matched to an instance&apos;s metrics <span className="font-mono">cluster</span> and Loki{" "}
            <span className="font-mono">k8s_cluster_name</span> labels automatically. Admins can override a match.
          </p>
        </div>
        {clusters.loading && clusters.data && <Spinner />}
      </div>
      {clusters.error && !clusters.data ? (
        <div className="p-4">
          <ErrorCard title="Couldn't load clusters" error={clusters.error} onRetry={clusters.reload} />
        </div>
      ) : !clusters.data ? (
        <Loading label="Loading clusters…" />
      ) : rows.length === 0 ? (
        <Empty title="No clusters">control-hub shows you no clusters.</Empty>
      ) : (
        <div className="relative overflow-x-auto">
          <table className="table min-w-[680px]">
            <thead>
              <tr>
                <th scope="col">Control-hub cluster</th>
                <th scope="col">Instance</th>
                <th scope="col">Metrics cluster</th>
                <th scope="col">Logs cluster</th>
                <th scope="col">Mapping</th>
                {isAdmin && (
                  <th scope="col">
                    <span className="sr-only">Actions</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {rows.map((c) => {
                const m = c.grafana ?? null;
                const isEditing = editing === c.id;
                return (
                  <Fragment key={c.id}>
                    <tr className={isEditing ? "bg-violet-tint/40" : ""}>
                      <td>
                        <div className="whitespace-nowrap font-mono text-xs font-medium">{c.name}</div>
                        <div className="text-xs text-muted">
                          {providerLabel(c.provider)} · {c.region || "—"}
                        </div>
                      </td>
                      <td className="whitespace-nowrap font-mono text-xs">{m?.instance || <Dash />}</td>
                      <td className="whitespace-nowrap font-mono text-xs">{m?.promCluster || <Dash />}</td>
                      <td className="whitespace-nowrap font-mono text-xs">{m?.lokiCluster || <Dash />}</td>
                      <td>{!m ? <Pill>not mapped</Pill> : m.override ? <Pill tone="violet">override</Pill> : <Pill tone="success">auto</Pill>}</td>
                      {isAdmin && (
                        <td className="text-right">
                          <div className="flex flex-col items-stretch gap-1 whitespace-nowrap">
                            <button
                              type="button"
                              className="btn min-h-[30px] px-2.5 text-xs"
                              onClick={() => setEditing(isEditing ? null : c.id)}
                              aria-expanded={isEditing}
                              disabled={busy !== null || instances.length === 0}
                              title={instances.length === 0 ? "Add a Grafana instance first" : undefined}
                            >
                              Edit
                            </button>
                            {m?.override && (
                              <button type="button" className="btn min-h-[30px] px-2.5 text-xs" onClick={() => reset(c)} disabled={busy !== null}>
                                {busy === c.id && <Spinner size={12} />}
                                Reset to automatic
                              </button>
                            )}
                          </div>
                          {error?.id === c.id && (
                            <div role="alert" className="mt-1 text-left text-xs text-crit-fg">
                              {error.message}
                            </div>
                          )}
                        </td>
                      )}
                    </tr>
                    {isEditing && (
                      <tr className="bg-violet-tint/40">
                        <td colSpan={cols}>
                          <MappingEditor
                            cluster={c}
                            instances={instances}
                            onCancel={() => setEditing(null)}
                            onSaved={(saved) => {
                              update(c.id, saved);
                              setEditing(null);
                            }}
                          />
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------- tab

export function GrafanaSettings() {
  const { me } = useMe();
  const isAdmin = me.isAdmin;
  const status = useApi(() => api.grafana());
  const [refreshing, setRefreshing] = useState(false);
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [notice, setNotice] = useState<{ kind: "error" | "ok"; text: string } | null>(null);
  const now = useNow(15_000);

  useEffect(() => {
    if (notice?.kind !== "ok") return;
    const t = window.setTimeout(() => setNotice(null), 4000);
    return () => window.clearTimeout(t);
  }, [notice]);

  const apply = (s: GrafanaStatus) => status.setData(s);

  const refresh = async () => {
    setRefreshing(true);
    setNotice(null);
    try {
      apply(await api.refreshGrafana());
      setNotice({ kind: "ok", text: "Reloaded every instance's cluster names." });
    } catch (err) {
      setNotice({ kind: "error", text: errMsg(err) });
    } finally {
      setRefreshing(false);
    }
  };

  const remove = async (g: GrafanaInstance) => {
    if (!window.confirm(`Delete the Grafana instance ${g.name}? Clusters mapped to it lose their metrics issues and charts.`)) return;
    setDeleting(g.name);
    setNotice(null);
    try {
      await api.deleteGrafanaInstance(g.name);
      status.reload();
      setNotice({ kind: "ok", text: `Deleted ${g.name}.` });
    } catch (err) {
      setNotice({ kind: "error", text: errMsg(err) });
    } finally {
      setDeleting(null);
    }
  };

  const instances = [...(status.data?.instances ?? [])].sort((a, b) => a.name.localeCompare(b.name));

  return (
    <div className="flex flex-col gap-4">
      <section className="card" aria-labelledby="gi-title">
        <div className="card-head">
          <div>
            <h2 id="gi-title" className="h2">
              Grafana instances
            </h2>
            <p className="m-0 mt-0.5 text-xs text-muted">
              Read with a Viewer token. The metrics parser evaluates its rules on every instance each minute.
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <button type="button" className="btn" onClick={refresh} disabled={refreshing || !status.data}>
              {refreshing ? <Spinner /> : <RefreshIcon size={14} />}
              Refresh
            </button>
            {isAdmin && !adding && (
              <button type="button" className="btn btn-primary" onClick={() => setAdding(true)}>
                <PlusIcon size={14} />
                Add instance
              </button>
            )}
          </div>
        </div>
        {adding && (
          <AddInstanceForm
            onClose={() => setAdding(false)}
            onAdded={() => {
              setAdding(false);
              status.reload();
              setNotice({ kind: "ok", text: "Instance added." });
            }}
          />
        )}
        {notice && (
          <div className="border-b border-line px-4 py-3">
            <Notice kind={notice.kind}>{notice.text}</Notice>
          </div>
        )}
        {status.error && !status.data ? (
          <div className="p-4">
            <ErrorCard title="Couldn't load Grafana instances" error={status.error} onRetry={status.reload} />
          </div>
        ) : !status.data ? (
          <Loading label="Loading Grafana instances…" />
        ) : instances.length === 0 ? (
          <Empty title="No Grafana instances">
            Add one {isAdmin ? "here" : "(an admin can)"} or set <span className="font-mono">GRAFANA_&lt;NAME&gt;_URL</span> and{" "}
            <span className="font-mono">GRAFANA_&lt;NAME&gt;_TOKEN</span> in the deployment secret.
          </Empty>
        ) : (
          <div className="relative overflow-x-auto">
            <InstancesTable instances={instances} isAdmin={isAdmin} now={now} onDelete={remove} deleting={deleting} />
          </div>
        )}
      </section>

      {status.data && <MappingCard instances={instances} isAdmin={isAdmin} />}
    </div>
  );
}
