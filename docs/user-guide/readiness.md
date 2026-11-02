# Upgrade readiness

**Route:** `/readiness` · "Twelve checks before you upgrade a tenant: control plane, findings, certificates, nodes,
pods, Helm, Postgres, disk, Athena, memory history, recent changes and orphaned go-sandboxes."

Upgrade readiness runs rule-based checks against control-hub, the tenant's cluster, Grafana and Athena, and gives a
verdict: **Go**, **Go with care**, **No-go** or **Can't tell**. The checks run with your access. What each check looks
at is in [Upgrade readiness checks](../concepts/readiness.md).

## Check a tenant

1. Open **Upgrade readiness**.
2. Type a tenant name or control plane ID (for example `capgroup`) and click **Check**.
3. Wait while the console runs the checks ("Running twelve checks against control-hub, the cluster, Grafana and
   Athena…"). They time out after 60 seconds.
4. Read the verdict card: the verdict, the tenant, `provider · region · Prophecy <version> · cluster`, and a summary
   naming the failing or warning checks.
5. Read the checks below it, worst first (fail, warn, unknown, pass). Each has a detail and, for warnings and failures,
   a hint about what to do.

The URL keeps the tenant (`/readiness?cp=<tenant>`), so you can share it. **Re-run** checks again.

## Ask the Copilot

Click **Ask Copilot**. The Copilot panel opens with:

```text
Explain the upgrade readiness of <tenant> (verdict <verdict>) and what to do first.
Run upgrade_readiness for it and investigate the failing checks.
```

The Copilot runs the same checks and digs into the failing ones with its other tools.

## Verdicts

| Verdict | Meaning |
|---|---|
| Go | Every check that ran passed. |
| Go with care | At least one check warned. Review them before upgrading. |
| No-go | At least one check failed. Fix them before upgrading. |
| Can't tell | More than half the checks could not run (no cluster access, no Grafana mapping, no Athena). |
