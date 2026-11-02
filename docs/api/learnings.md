# Learnings

Reusable failure patterns distilled from investigations; see [Learnings](../concepts/learnings.md). **Every route is
admins only.**

```ts
type Learning = {
  id: string; title: string; category: string; status: "draft" | "approved" | "retired"
  symptoms: string; rootCause: string; howToConfirm: string; fix: string; kgNodes: string
  entities: string[]; investigations: string[]; timesSeen: number; confirmed: number; contradicted: number
  reviewNote: string; createdBy: string; approvedBy: string; approvedAt: string | null
  lastSeen: string; createdAt: string; updatedAt: string
}
```

| Method | Path | Notes |
|---|---|---|
| GET | `/api/learnings?status=&q=&alt=` | Without `q`: the learnings with that status (all when empty). With `q`: ranked by relevance (BM25), across all statuses unless `status` is set. Each `alt` (repeatable) is a rewording searched together with `q` and merged by reciprocal rank fusion; at most six queries in all. |
| GET | `/api/learnings/expand?q=` | `{alternatives: string[]}`: Claude (a small model, with the caller's key) rewords the search. `412` without a Claude key. |
| POST | `/api/learnings` | Body: `title, category, symptoms, rootCause, howToConfirm, fix, kgNodes, status?`. Title, symptoms and root cause are required. Created as a draft unless `status` says otherwise. `201` with the learning. |
| GET | `/api/learnings/{id}` | The learning. |
| PUT | `/api/learnings/{id}` | Edits fields; `status` approves, retires or restores; `clearReview: true` drops the contradiction note. |
| DELETE | `/api/learnings/{id}` | `204`. Drafts only (`409` otherwise: retire instead). |
| POST | `/api/learnings/{id}/draft-rule` | Claude drafts a log rule from an approved learning's symptoms; see [Log rules and patterns](log-rules.md#new-error-patterns-and-drafted-rules-admins). |

Console admins are control-hub admins plus the emails in `CONSOLE_ADMIN_EMAILS` (comma separated).

## Copilot tools

- `search_learnings(query, alternatives?, category?, entity?)`: BM25 over title, symptoms, cause, checks and fix,
  **approved learnings only**, with up to five rewordings merged by reciprocal rank fusion. Returns up to five matches.
- `record_learning`, after `record_investigation`: either `learning_id + outcome` (`confirmed` adds a sighting;
  `contradicted` leaves the text alone and sets `reviewNote` for an admin) or a new **draft** (secrets masked). Drafts
  are never searchable, because the Copilot writes them from logs and pages that may hold planted text; an admin
  approves, edits or deletes them.
