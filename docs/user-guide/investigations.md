# Investigations

**Route:** `/investigations`

An investigation is the Copilot's write-up of a diagnosis of one control plane or execution plane. The Copilot records
one (with an `INC-…` reference) when it has diagnosed a specific problem; general questions record nothing. See
[The Copilot agent loop](../concepts/copilot-agent.md#recording-investigations).

## The list

Admins see "Every Copilot investigation, newest first". Non-admins see "Investigations Copilot opened from your chats".

| Column | Shows |
|---|---|
| Investigation | Title and `INC-…` ID; opens the investigation |
| Tenant | The entity's customer or tenant name |
| Category | `networking-dns`, `certificates`, `cloud-quota`, `cloud-permissions`, `cloud-capacity`, `helm-kubernetes`, `athena-postdeploy`, `control-hub-bug`, `configuration`, `transient` or `unknown` |
| Requester | Who asked |
| Opened | When |
| *(link)* | **Open chat** opens the conversation |

## One investigation

**Route:** `/investigations/<id>`

The header shows the title, confidence (high, medium, low), category, the entity, who opened it and when, and **Open
chat**.

=== "Admins"

    - **Root cause** (admins only): the full technical root cause with the evidence, plus category, confidence and
      entity.
    - **Recommended fix**: what should be done, including any permanent fix in control-hub.
    - **What the requester was told**: the plain summary the requester saw.
    - **Evidence · tool calls**: every tool call in the chat with time, ✓/✗, tool, input and a summary of the result.
      **Full transcript** opens the chat.
    - **Actions**: actions the Copilot proposed.

=== "Non-admins"

    - **Summary**: the outcome and next step in plain terms, without internal details.
    - **Actions**: actions the Copilot proposed.

    Quote the `INC-…` reference to the cloud team; they can read the root cause.

## Actions

An action card shows what the Copilot proposed (for example **Retry post-deploy · capgroup**), why, its
[dry run](../concepts/actions.md) while it is proposed, and its status:

| Status | Meaning |
|---|---|
| proposed | Waiting for you. Click the button to run it, or **Dismiss**. |
| running | It is running. |
| succeeded / failed | Finished, with the result. |
| dismissed | You dismissed it. |

Actions run with **your** access, so control-hub decides whether you may do them. On a read-only console the card says
"Dry run only: the console is read-only, so this can't be executed here. An admin can apply the steps by hand."
