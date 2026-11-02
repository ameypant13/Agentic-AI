# Slack app setup

The [Copilot in Slack](../user-guide/slack.md) is a Slack app that sends events to the console at
`POST /api/slack/events`. It is separate from the incoming webhook (`SLACK_WEBHOOK_URL`) that posts findings and the
digest. It is on only when **both** `SLACK_BOT_TOKEN` and `SLACK_SIGNING_SECRET` are set; otherwise the endpoint
returns `404`.

## What the app needs

The manifest is in `deploy/slack-app-manifest.yaml`:

| Setting | Value | Why |
|---|---|---|
| Bot scopes | `app_mentions:read` | Mentions in channels |
| | `chat:write` | Reply in threads |
| | `im:history` | Direct messages to the bot |
| | `users:read`, `users:read.email` | Look up who asked, by email, to act with their console access |
| Bot events | `app_mention`, `message.im` | |
| Request URL | `https://controlhub.app.prophecy.ai/api/slack/events` | Change the host if the console runs elsewhere |
| App Home | Messages tab enabled | So people can DM the bot |
| Socket mode | off | Slack calls the console over HTTPS |

## Create and install the app

1. Make sure the console is reachable from the internet at its public host (Slack calls the request URL).
2. Open https://api.slack.com/apps → **Create New App** → **From an app manifest**, pick the workspace, and paste
   `deploy/slack-app-manifest.yaml`. Change the request URL host first if needed.
3. **Install** the app to the workspace.
4. Copy the **Bot User OAuth Token** (`xoxb-…`) and the **Signing Secret** (Basic Information).
5. Add them to the console's secret and restart:

    ```bash
    kubectl -n ai-console patch secret ai-console-secrets --type merge -p \
      '{"stringData":{"SLACK_BOT_TOKEN":"<xoxb-token>","SLACK_SIGNING_SECRET":"<signing-secret>"}}'
    kubectl -n ai-console rollout restart deploy/ai-console
    ```

6. In the app's **Event Subscriptions**, let Slack verify the request URL. The console answers Slack's
   `url_verification` challenge once the secrets are set.
7. Invite the bot to the channels where people should mention it (`/invite @copilot`).
8. Check `PUBLIC_URL` is the console's address: the bot's links ("Open this conversation in the console", sign-in and
   Settings links) use it.

## How requests are handled

- Every request's `X-Slack-Signature` is verified with the signing secret, and requests older than 5 minutes are
  rejected (`401`).
- The console answers within Slack's 3-second limit and runs the Copilot turn in the background.
- Slack retries are recognised by event ID and handled once. Bot messages, message edits and other subtypes are
  ignored.
- The person is looked up by Slack user ID → email (`users.info`) → their most recent unexpired console session. No
  session, no answer.
- In channels the turn runs with the non-admin view; in DMs with the person's own admin flag.
- Each thread and person maps to one chat (kept in the `KV` table).

## Troubleshooting

| Symptom | Cause |
|---|---|
| Slack says the request URL did not respond / failed verification | `SLACK_BOT_TOKEN` or `SLACK_SIGNING_SECRET` missing (the endpoint returns `404`), wrong signing secret (`401`), or the host is not reachable. |
| The bot never answers a mention | It is not in the channel, or the `app_mention` event is not subscribed. |
| "I couldn't read your email from Slack" | The app lacks `users:read.email`, or the user has no email. |
| "Sign in to the console … first" | The person has no current console session under the same email. |
| Answers lack technical detail | Expected in channels; ask in a DM for your own view. |
