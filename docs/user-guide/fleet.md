# Fleet overview

**Route:** `/` · "What needs a human today. Findings come from scheduled monitors and from Copilot investigations."

The Fleet overview is the console's home page: a summary of the whole fleet and the things that need a person.

## What it shows

**Realm filter.** When there is more than one realm, buttons at the top (**All realms** plus one per realm) filter the
tiles and the findings.

**Tiles**

| Tile | Shows |
|---|---|
| Control planes | Total control planes, with the top statuses or the realm count. |
| Needs attention | Open critical + warning findings; highlighted when above 0. |
| Active issues | Errors + warnings from the metrics and log parsers. Links to [Issues](issues.md). |
| Investigations (7d) | Investigations the Copilot opened in the last 7 days. |
| Last monitor run | When the monitors last ran, or "Never" if they have not run yet. |

**Top issues.** The five most severe, newest active issues across every cluster you can see, from the metrics and log
parsers. Each row shows the severity, the source (metrics or logs), the title, `cluster · ns / entity`, the value and
how long it has been active. **Open** goes to the pod page, the node page or the cluster overview. **View all** opens
the Issues page.

**Needs attention.** Open findings, sorted by severity then newest. Each row shows the severity, an acknowledged pill if
someone acknowledged it, the tenant, `provider · realm`, the title and detail, and when it was first and last seen.

**Certificates expiring.** The six certificates expiring soonest (tenant and wildcard), with the main problem and the
days left. **View all** opens [Certificates](certificates.md).

## Common tasks

### Investigate a finding

1. In **Needs attention**, find the finding.
2. Click **Investigate**.
3. The console creates a Copilot chat titled after the finding and sends a prompt describing it ("The monitor flagged
   control plane … Investigate the root cause and tell me what to do.").
4. Follow the Copilot's steps on the [Copilot page](copilot.md). If it records an investigation, the finding gets a
   **View RCA** button.

### Acknowledge a finding (admins)

1. Click **Acknowledge** on an open finding.
2. The finding shows "Acknowledged" (or "Acknowledged by &lt;name&gt;" to others).

Acknowledging records who saw it. It does not hide or resolve the finding: it still counts as open and resolves on its
own when the monitor no longer sees the problem. See [Findings and monitors](../concepts/monitors.md).

### Read a root cause

Click **View RCA** on a finding with a linked investigation. Admins see the root cause and evidence. Non-admins can
only open investigations that came from their own chats, and see the summary they were given.
