# Copilot, actions and investigations

## Types

```ts
type ChatSummary = { id: string; title: string; createdAt: string; updatedAt: string }
type Step = { id: string; label: string; ok: boolean | null; tool?: string; input?: string; summary?: string }
// tool / input / summary are present only for admins
type Action = {
  id: string
  kind: "retry_cp_postdeploy" | "retry_ep_postdeploy" | "restart_pod" | "delete_orphaned_sandboxes"
  title: string                 // button text, e.g. "Retry post-deploy"
  description: string           // why, in one sentence
  targetName: string
  status: "proposed" | "running" | "succeeded" | "failed" | "dismissed"
  result: string                // "" until executed
  plan: Plan | null             // the dry run shown on the card
}
type Plan = {
  summary: string
  steps: string[]               // the exact operations, as commands or API calls
  checks: string[]              // what the dry run read and found
  risks?: string[]
  blocked?: string              // set only on refused dry runs, which are never stored as actions
  targets: string[]             // objects the steps touch, compared at execution time
}
type ChatItem = {
  id: string
  role: "user" | "assistant"
  text: string                  // markdown
  steps: Step[]                 // tool calls made while producing this assistant turn
  actions: Action[]
  investigationId: string | null
  createdAt: string
  keySource: "personal" | "shared" | ""   // which Claude key produced an assistant reply
}
type Chat = { id: string; title: string; items: ChatItem[] }
```

## Chats

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/chats` | — | `ChatSummary[]` (the caller's own, newest first) |
| POST | `/api/chats` | `{title?}` | `ChatSummary` |
| GET | `/api/chats/{id}` | — | `Chat`. Only the owner can read a chat (`404` otherwise). |
| POST | `/api/chats/{id}/messages` | `{text}` | **Server-sent events** stream (below). `400` without text; `412` when the caller has no Claude key and there is no shared key. |

A chat without a title takes the first 60 characters of its first message.

### Stream events

`POST /api/chats/{id}/messages` answers `Content-Type: text/event-stream` (with `X-Accel-Buffering: no`). Each event is
`event: <name>\ndata: <json>\n\n`. Use `fetch` and a `ReadableStream` reader, not `EventSource`, which cannot POST.

| event | data |
|---|---|
| `step` | `Step` with `ok: null`: a tool call started |
| `step_done` | `{id, ok, summary?}` |
| `text` | `{delta}`: append to the current assistant text |
| `action` | `Action`: render as a button under the reply |
| `investigation` | `{id, title}`: link to the investigation |
| `done` | `{itemId, keySource: "personal" | "shared", usage: {inputTokens, outputTokens}}` |
| `error` | `{message}`: the turn ended; show inline |

`inputTokens` includes cache reads and cache writes.

Example:

```bash
curl -sS -N -b cookies.txt -H 'Content-Type: application/json' \
  -d '{"text":"Which control planes failed to provision this week?"}' \
  https://<AI_CONSOLE_HOST>/api/chats/<chat id>/messages
```

## Actions

| Method | Path | Response |
|---|---|---|
| POST | `/api/actions/{id}/execute` | `Action` (status `succeeded` or `failed`, `result` filled). Re-runs the dry run first, then runs with the caller's access. `403` when the console is read-only; `409` when the dry run is now blocked or its targets differ from the stored plan ("the state changed since the dry run; ask the Copilot to propose it again"), or when the action is no longer pending; `404` unless the action is in one of the caller's chats. |
| POST | `/api/actions/{id}/dismiss` | `Action`. Dismisses a proposed action. |

See [Dry-run actions](../concepts/actions.md) for each kind's plan and refusals.

## Investigations

```ts
type InvestigationSummary = {
  id: string; title: string; entityName: string; category: string
  requester: string; createdAt: string; chatId: string
}
type Evidence = { at: string; tool: string; input: string; summary: string; ok: boolean }
type Investigation = InvestigationSummary & {
  entityId: string
  summaryForRequester: string
  confidence: "high" | "medium" | "low"
  rootCause?: string            // admins only
  recommendedFix?: string       // admins only
  evidence?: Evidence[]         // admins only
  actions: Action[]
}
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/investigations` | `InvestigationSummary[]`: admins see all, others their own |
| GET | `/api/investigations/{id}` | `Investigation`. Non-admins get `404` for other people's investigations. Evidence leaves out `record_investigation` and `propose_action` calls. |
