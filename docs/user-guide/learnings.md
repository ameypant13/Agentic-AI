# Learnings

**Route:** `/learnings` · **Admins only**

"Failure patterns distilled from investigations. The Copilot searches approved ones before it investigates, drafts new
ones after, and you decide what it may rely on." How learnings work is in [Learnings](../concepts/learnings.md).

Non-admins see "Admins only: Learnings hold root causes from investigations, so only console admins can review them."

## Reading the page

Three tabs:

| Tab | Shows |
|---|---|
| To review | Drafts, with a count badge. The Copilot drafts a learning after an investigation that no existing learning covered. |
| Approved | Learnings the Copilot searches. |
| Retired | Learnings no longer used. |

Search with "Search symptoms, causes…"; results are ranked by relevance. **Search by meaning** asks Claude (a small
model, with your Claude key) to reword your search with synonyms, likely causes and related errors, and searches for
all of them; the page lists what it also searched. Each row shows the title, pills
(**contradicted**, **ready for a rule** for learnings confirmed by three or more investigations, and the category),
the symptoms and "seen N× · confirmed N · contradicted N · last …".

## Common tasks

### Review a draft

1. Open **To review** and click a draft.
2. Check that it is a **general pattern**, that the symptoms are what someone would search for (exact error strings,
   failed steps, statuses, alert names), and that it holds **no customer details or secrets**.
3. Edit **Title**, **Category**, **Symptoms**, **Root cause**, **How to confirm**, **Fix** and
   **Knowledge-graph nodes** as needed.
4. Click **Approve**. From now on the Copilot can find it with `search_learnings`.

Or click **Delete draft** if it is not worth keeping. Only drafts can be deleted.

### Add a pattern you already know

1. Click **+ New learning**.
2. Fill in at least **Title**, **Symptoms** and **Root cause**.
3. Click **Save as draft**, or **Save and approve** to make it searchable at once.

### Deal with a contradicted learning

When an investigation found that an approved learning looked similar but the cause was different, the learning gets a
**contradicted** pill and a note "An investigation contradicted this learning", with the investigation IDs.

1. Open the learning and read the note; follow the investigation links.
2. Edit the learning so its symptoms or how-to-confirm checks tell the two cases apart, or retire it.
3. Click **Mark as dealt with** to clear the note.

### Retire or restore

- **Retire** an approved learning that no longer applies. It stays on record but the Copilot stops seeing it.
- **Restore** a retired learning to make it approved again.

### Turn a learning into a log rule

For an approved learning (especially one marked **ready for a rule**):

1. Click **Draft log rule**. Claude drafts a log rule from the symptoms.
2. If it proposes one, the Log rules page opens with the draft in the rule form. Review it and save.
3. If not, the page says why ("No log rule proposed: …").

The monitors then catch the problem without AI. See [Log rules](log-rules.md).
