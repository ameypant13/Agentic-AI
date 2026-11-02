# API overview and auth

The console's HTTP API is what its own UI uses. It is documented in `API.md` in the module; these pages organise
the same contract by area.

## Conventions

- Every route is under `/api`. JSON in and out (except the chat stream, which is server-sent events).
- Requests are authenticated by the httpOnly cookie **`aic_session`**, set by a sign-in route. There are no API keys:
  sign in like the UI does.
- An unauthenticated call returns `401 {"error": "..."}`. Errors are always `{"error": "<message>"}` with a 4xx or 5xx
  status.
- Admin-only routes return `403 {"error": "admins only"}` to everyone else.
- Times are RFC 3339 strings. IDs are strings.
- Request bodies are limited to 1 MiB.
- An unknown `/api/...` path returns `404 {"error": "no such API route"}`. Any other path serves the UI.

| Area | Page |
|---|---|
| Sign-in, session, Claude key | this page |
| Fleet, findings, monitors | [Fleet, findings and monitors](fleet.md) |
| Chats, streaming, actions, investigations | [Copilot, actions and investigations](copilot.md) |
| Clusters and the resource explorer | [Clusters and explorer](clusters.md) |
| Log rules, log scans, error patterns | [Log rules and patterns](log-rules.md) |
| Grafana, metric issues, charts | [Grafana, metrics and issues](grafana.md) |
| Certificates | [Certificates](certificates.md) |
| Costs, right-sizing, review | [Costs](costs.md) |
| Learnings | [Learnings](learnings.md) |
| Readiness, digest, policy, audit, Slack events | [Readiness, digest and guardrails](other.md) |

## Unauthenticated routes

| Method | Path | Auth | Response |
|---|---|---|---|
| GET | `/api/healthz` | none | `{"status": "ok"}`. Used by the readiness and liveness probes. |
| POST | `/api/slack/events` | Slack request signature | The Slack Events API endpoint; see [Slack events](other.md#slack-events). |
| GET | `/docs/…` | session cookie, else a redirect | The documentation site (not JSON). Without a valid session it redirects to `/login?next=<url>`. `/docs` redirects to `/docs/`. |

## Auth

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/api/auth/login` | `{email, password}` | `Me`. Logs in through control-hub's `/api/auth/login`. `401` "invalid email or password"; `502` "could not reach control-hub". |
| POST | `/api/auth/token` | `{token}` | `Me`. Sign in with an existing control-hub token (SSO users). A leading `Bearer ` is accepted. `400` if it is not a control-hub JWT; `401` if control-hub rejects it. |
| POST | `/api/auth/logout` | — | `{}`. Deletes the session and clears the cookie. |
| GET | `/api/me` | — | `Me` |
| PUT | `/api/me/claude-key` | `{apiKey}` | `ClaudeKey`. The key must start with `sk-ant-` and is validated against the Claude API before it is stored (`400` otherwise). |
| DELETE | `/api/me/claude-key` | — | `ClaudeKey` |

Sessions last 7 days, or until the control-hub token expires if that is sooner.

```ts
type Me = {
  email: string
  username: string
  isAdmin: boolean            // control-hub admin, or listed in CONSOLE_ADMIN_EMAILS
  claudeKey: ClaudeKey
  readOnly: boolean           // READ_ONLY: Copilot actions are off and execute returns 403
  controlHub: string          // CONTROLHUB_LABEL, e.g. "control-hub-prod"
}
type ClaudeKey = {
  configured: boolean          // the user stored a personal key
  last4: string                // "" when not configured
  source: "personal" | "shared" | "none"   // which key the next chat will use
}
```

## Example: sign in with curl

```bash
# Sign in and keep the cookie
curl -sS -c cookies.txt -H 'Content-Type: application/json' \
  -d '{"email":"<you@example.com>","password":"<password>"}' \
  https://<AI_CONSOLE_HOST>/api/auth/login

# Call an authenticated route
curl -sS -b cookies.txt https://<AI_CONSOLE_HOST>/api/fleet
```

!!! warning
    The cookie file holds a live session. Delete it when you are done.
