import { Link } from "react-router-dom";
import { api } from "../api/client";
import { Page } from "../components/Layout";
import { Pill } from "../components/Pill";
import { Empty, ErrorCard, Loading, PageHeader } from "../components/States";
import { formatDateTime } from "../lib/format";
import { useMe } from "../lib/me";
import { useApi } from "../lib/useApi";

export default function InvestigationsPage() {
  const { me } = useMe();
  const list = useApi(() => api.investigations());

  return (
    <Page>
      <PageHeader
        title="Investigations"
        subtitle={
          me.isAdmin
            ? "Every Copilot investigation, newest first. Open one for the root cause and the evidence behind it."
            : "Investigations Copilot opened from your chats."
        }
      />
      <section className="card" aria-label="Investigations">
        {list.error ? (
          <div className="p-4">
            <ErrorCard title="Couldn't load investigations" error={list.error} onRetry={list.reload} />
          </div>
        ) : list.loading && !list.data ? (
          <Loading label="Loading investigations…" />
        ) : (list.data ?? []).length === 0 ? (
          <Empty title="No investigations yet">
            Copilot opens one when it diagnoses a failure. <Link to="/copilot">Start a chat</Link>.
          </Empty>
        ) : (
          <div className="overflow-x-auto">
            <table className="table min-w-[720px]">
              <thead>
                <tr>
                  <th scope="col">Investigation</th>
                  <th scope="col">Tenant</th>
                  <th scope="col">Category</th>
                  <th scope="col">Requester</th>
                  <th scope="col">Opened</th>
                  <th scope="col">
                    <span className="sr-only">Chat</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.data!.map((inv) => (
                  <tr key={inv.id}>
                    <td>
                      <Link to={`/investigations/${encodeURIComponent(inv.id)}`} className="font-medium">
                        {inv.title || inv.id}
                      </Link>
                      <div className="font-mono text-xs text-muted">{inv.id}</div>
                    </td>
                    <td className="font-medium">{inv.entityName}</td>
                    <td>{inv.category ? <Pill>{inv.category}</Pill> : <span className="text-muted">—</span>}</td>
                    <td>{inv.requester}</td>
                    <td className="whitespace-nowrap font-mono text-xs text-muted">{formatDateTime(inv.createdAt)}</td>
                    <td className="whitespace-nowrap text-right">
                      {inv.chatId && <Link to={`/copilot/${encodeURIComponent(inv.chatId)}`}>Open chat</Link>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </Page>
  );
}
