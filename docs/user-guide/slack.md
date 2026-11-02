# Copilot in Slack

You can ask the Copilot from Slack: mention the bot in a channel, or send it a direct message. It answers in the
thread, **as you**: with your control-hub access and your Claude key, exactly as in the console. Setup for operators is
in [Slack app setup](../operations/slack.md).

## Before you start

The bot links your Slack account to the console **by email**:

1. Your Slack profile email must be the email of your control-hub account.
2. You need a **current console session**: sign in to the console in your browser. The bot uses your most recent
   unexpired session (sessions last 7 days), so sign in again when it asks.
3. You need a Claude key: your own (add it in [Settings](settings.md)) or the team's shared key.

If any of these is missing, the bot replies with what to do: "Sign in to the console at … first, then ask again", or
"Add your Claude API key in the console's Settings …", or that it couldn't read your email from Slack.

## Ask a question

=== "In a channel"

    1. Mention the bot with your question, for example:

        ```text
        @copilot why is capgroup's post-deploy failing?
        ```

    2. The bot posts "_Looking into it…_" in the thread and updates it with its current step every few seconds.
    3. The answer replaces that message (long answers continue in more messages).

=== "In a direct message"

    Send the bot your question in its Messages tab. No mention is needed.

A mention with no question gets a short hint with an example.

## What you see

| Where you ask | View |
|---|---|
| A channel | The **non-admin view** for everyone, admins included, because everyone in the channel reads the answer: plain-terms outcome, the next step and a reference ID; no internal root cause, raw errors, resource names or internal hostnames. |
| A direct message | **Your own view**: full technical detail if you are a console admin, the non-admin view otherwise. |

The full root cause still goes into the recorded investigation, which admins read in the console.

## Threads are chats

Each Slack thread is one Copilot chat, per person. Reply in the same thread (mentioning the bot in a channel) to ask a
follow-up; the Copilot keeps the whole conversation. Every answer ends with **Open this conversation in the console**,
which opens the chat (titled "Slack: …") on the Copilot page, with its steps, investigation link and actions.

While the bot is still answering in a thread, another question in that thread gets "I'm still working on the previous
question in this thread."

## Actions

The Copilot can propose actions in Slack, but Slack has no buttons. Each proposal is listed under the answer, for
example:

```text
Prepared: Restart pod on `metagraph-0` (deletes the pod; StatefulSet/metagraph creates a replacement with the same spec). Review the dry run in the console.
```

To run it, open the conversation in the console, review the [dry run](../concepts/actions.md) and click the button
there. On a read-only console it stays a dry run.

## Limits

- A turn has 10 minutes. If the Copilot cannot finish, the answer says it stopped and why.
- Answers are converted from Markdown to Slack formatting.
- Every Slack answer is audited (`slack_copilot`).
