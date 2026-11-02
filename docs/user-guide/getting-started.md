# Signing in and finding your way

## Sign in

The console uses your **Control Hub account**. There is no separate console account.

=== "Email and password"

    1. Open the console's address (for example `https://controlhub.app.prophecy.ai`).
    2. Enter your Control Hub **Email** and **Password**.
    3. Click **Sign in**.

=== "Control-hub token (SSO users)"

    If you sign in to control-hub with Google SSO you have no password. Use a token from an existing Control Hub
    session instead:

    1. Copy your control-hub token from a signed-in Control Hub session.
    2. On the console's sign-in page, expand **Sign in with a control-hub token**.
    3. Paste it into **Control Hub token** and click **Sign in with token**.

    The console accepts the token only after control-hub does. Your session ends when the token expires, or after
    7 days, whichever is sooner.

After signing in you return to the page you were trying to open, or to the Fleet overview. Sessions last 7 days.
**Log out** is at the top right.

If sign-in fails with "invalid email or password", check your Control Hub credentials. "could not reach control-hub"
means the console cannot reach control-hub; tell the console's operators.

## The layout

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Control Hub  AI Console  [control-hub-prod]  Search or run… ⌘K  Live  Dark   │
│                                                     Copilot  you@…  Admin   │
├──────────────┬───────────────────────────────────────────────┬──────────────┤
│ Overview     │                                               │              │
│  Fleet       │                 page content                  │   Copilot    │
│  Issues  (3) │                                               │   panel      │
│  Digest      │                                               │              │
│ Explore      │                                               │              │
│  …           │                                               │              │
│ Ask          │                                               │              │
│ Configure    │                                               │              │
└──────────────┴───────────────────────────────────────────────┴──────────────┘
```

**Header**, left to right:

- **Control Hub · AI Console** brand.
- The **connected control-hub** label (for example `control-hub-prod`), red when its name contains "prod".
- **Search or run…** opens the command palette (<kbd>Cmd</kbd>+<kbd>K</kbd> on a Mac, <kbd>Ctrl</kbd>+<kbd>K</kbd>
  elsewhere).
- **Live · updated … ago**: the time since this page was opened. Pages refresh their own data in the background; this
  is not a live stream.
- **Dark / Light** theme toggle. Without a saved choice the console follows your system setting.
- **Copilot** opens and closes the Copilot panel (hidden on the Copilot page itself).
- Your email, and an **Admin** pill if you are a console admin.
- **Log out**.

**Read-only banner.** When the console runs read-only, every page shows "**Read-only:** connected to
&lt;control-hub&gt;. Nothing in the console can change clusters or control-hub."

**Sidebar**:

| Group | Pages |
|---|---|
| Overview | [Fleet overview](fleet.md), [Issues](issues.md) (with a red count of active errors), [Weekly digest](digest.md) |
| Explore | [Clusters](clusters.md), [Certificates](certificates.md), [Costs](costs.md), [Upgrade readiness](readiness.md), [Investigations](investigations.md), [Learnings](learnings.md) |
| Ask | [Copilot](copilot.md) |
| Configure | [Monitors](monitors.md), [Log rules](log-rules.md), [Guardrails](guardrails.md), [Settings](settings.md) |

Below the groups, **Documentation** opens this manual (`/docs/`) in a new tab. The command palette also lists it.
The manual is only served to signed-in users; opening a docs link without a session sends you to sign in first.

Creating and changing tenants, and billing, stay in the existing Control Hub UI.

## The command palette

Press <kbd>Cmd</kbd>+<kbd>K</kbd> / <kbd>Ctrl</kbd>+<kbd>K</kbd> anywhere, or click **Search or run…**.

- Type to fuzzy-search **clusters** (by name, provider, region and tenants), **pages** and **certificates** (by host).
- **Ask Copilot about this page** opens the Copilot panel. With text typed, the first entry becomes
  **Ask Copilot: "&lt;your text&gt;"** and sends it.
- Keys: <kbd>↑</kbd> <kbd>↓</kbd> move, <kbd>Enter</kbd> opens, <kbd>Esc</kbd> closes.

Clusters and certificates are loaded the first time you open the palette and refreshed after 5 minutes.

## The Copilot panel

The Copilot panel is a docked chat on the right of every page except the Copilot page. Open it with the **Copilot**
button in the header, the thin **Open Copilot** rail on wide screens, or the command palette. On narrow screens it is
a drawer; <kbd>Esc</kbd> closes it. The panel remembers whether it was open.

It uses the same chats as the [Copilot page](copilot.md): **Open in Copilot** opens the current chat there, and **New**
starts a new one. Some pages also send questions to it directly, such as **Ask Copilot why** on Costs or
**Ask Copilot** on Upgrade readiness.

## Asking from Slack

You can also ask the Copilot from Slack, by mentioning the bot in a channel or sending it a direct message. See
[Copilot in Slack](slack.md).

## Your Claude key

The Copilot needs a Claude API key. If your team has not configured a shared key, add your own in
[Settings](settings.md) before you ask anything; until then the Copilot shows "No Claude API key available" and an
**Add your key** button.
