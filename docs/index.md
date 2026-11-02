# Control Hub AI Console

The Control Hub AI Console is an AI-assisted operations console for Prophecy's control-hub fleet. It sits next to
control-hub, watches every control plane, execution plane and cluster that control-hub manages, finds problems before
anyone reports them, and has a Copilot (Claude) that investigates them through control-hub's API, the clusters
themselves, Grafana and Athena.

It is its own Go module (`ai-console/` in the control-hub repository) with its own image. It never touches
control-hub's database: every read and every action goes through control-hub's REST API **with the signed-in user's
control-hub token**, so control-hub's permission checks apply unchanged.

## Who it is for

- **The cloud and SRE teams**, who run the fleet day to day: triage findings, explore clusters, investigate failures,
  plan upgrades and keep an eye on certificates and cost.
- **Console admins** (control-hub admins plus the emails in `CONSOLE_ADMIN_EMAILS`), who also see root causes, curate
  learnings and log rules, map Grafana instances and send the weekly digest.
- **Anyone else with a control-hub account**, who can ask the Copilot about the tenants they can see and get a plain
  summary of what went wrong, without internal details.

Creating and changing tenants, and billing, stay in the existing Control Hub UI.

## Feature tour

| Feature | What it does | Read more |
|---|---|---|
| Fleet overview | Open findings, control plane counts, top issues and certificates expiring, on one page. | [Fleet overview](user-guide/fleet.md) |
| Monitors and findings | Scheduled checks (every 5 minutes by default) that open findings and post to Slack. | [Findings and monitors](concepts/monitors.md) |
| Issues | Active problems the metrics parser (Grafana) and the log parser see on every cluster you can access. Rules, not AI. | [Issues](user-guide/issues.md) |
| Clusters | A read-only, Lens-style explorer over every Kubernetes cluster control-hub manages. | [Clusters](user-guide/clusters.md) |
| Certificates | An inventory of every certificate tenants depend on, with the checks renewal needs. | [Certificates](user-guide/certificates.md) |
| Costs | Estimated compute cost per cluster and workload, with right-sizing from actual use. | [Costs](user-guide/costs.md) |
| Upgrade readiness | Twelve rule-based checks that say whether a tenant is safe to upgrade. | [Upgrade readiness](user-guide/readiness.md) |
| Copilot | A Claude agent with read-only tools that investigates problems and records what it found. | [Copilot](user-guide/copilot.md) |
| Copilot in Slack | Mention the bot or DM it; it answers in the thread with your access. | [Copilot in Slack](user-guide/slack.md) |
| Dry-run actions | Fixes the Copilot proposes (retry post-deploy, restart a pod, delete orphaned go-sandboxes), planned against the live state before anyone clicks. | [Dry-run actions](concepts/actions.md) |
| Investigations | The Copilot's write-ups: root cause, recommended fix and the tool calls behind them. | [Investigations](user-guide/investigations.md) |
| Learnings | Reusable failure patterns from past investigations that the Copilot checks first. | [Learnings](concepts/learnings.md) |
| Log rules | A library of known log messages matched without AI, plus new error patterns waiting to become rules. | [Log rules](user-guide/log-rules.md) |
| Metric rules | PromQL rules evaluated through Grafana every minute. | [Metric rules](user-guide/metric-rules.md) |
| Weekly digest | The week across the fleet, posted to Slack. No AI. | [Weekly digest](user-guide/digest.md) |
| Guardrails and audit | What the Copilot may do, and a log of every sign-in, key change, investigation and action. | [Guardrails](user-guide/guardrails.md) |
| Settings | Your personal Claude API key, and Grafana instances and mappings. | [Settings](user-guide/settings.md) |

## A 5 minute tour

1. **Sign in** at the console's address with your Control Hub email and password. If you use Google SSO with
   control-hub, paste a control-hub token under **Sign in with a control-hub token** instead.
   See [Signing in](user-guide/getting-started.md).
2. **Open Settings** and add your Claude API key from console.anthropic.com, unless your team has a shared key
   configured. **Next chat uses** tells you which key the Copilot will use. See [Settings](user-guide/settings.md).
3. **Read the Fleet overview.** The tiles show control planes, findings that need attention, active issues, Copilot
   investigations in the last 7 days and when the monitors last ran. Below them are the top five issues, the open
   findings and the certificates expiring soonest.
4. **Investigate a finding.** Click **Investigate** on any finding. The console opens a Copilot chat with a prompt that
   describes the finding, and the Copilot starts working: you see each check it runs as a step.
5. **Read the answer.** The Copilot leads with the cause and the next step. If it diagnosed a specific control plane
   or execution plane, it records an investigation and links it under the reply.
6. **Open a cluster.** Go to **Clusters**, pick one, and look at its Overview: metric issues, charts, pods with
   problems, log parser hits and Warning events.
7. **Ask from anywhere.** Press <kbd>Cmd</kbd>+<kbd>K</kbd> (or <kbd>Ctrl</kbd>+<kbd>K</kbd>), type a question and
   choose **Ask Copilot**. The answer appears in the Copilot panel on the right, without leaving the page.

!!! note "Read-only deployments"
    When the console runs with `READ_ONLY=true` (required against a production control-hub), every page shows a
    **Read-only** banner and nothing in the console can change clusters or control-hub. The Copilot still
    investigates; it just cannot propose actions.

## Where to go next

- New to the console: [Architecture](concepts/architecture.md) and [Security model](concepts/security.md).
- Using it: the [User guide](user-guide/getting-started.md).
- Running it: [Installation & operations](operations/prerequisites.md) and the
  [configuration reference](reference/configuration.md).
- Changing it: [Development](development/local.md) and the [API reference](api/index.md).
