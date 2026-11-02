# Audit: who changed what

Admins can ask the Copilot questions like *who changed the data PVC to 100Gi?*, *who deleted the metagraph
deployment?* or *who created this snapshot?*. The Copilot answers with the `audit_search` tool, which reads audit logs
and never changes them or any logging setting.

!!! note "Admins only"
    Audit answers name people, so `audit_search` is offered only to console admins (`CONSOLE_ADMIN_EMAILS` or a
    control-hub admin). In Slack it works in direct messages to admins, not in channels.

## Sources

| Cluster or resource | Source | Tells you |
|---|---|---|
| GKE clusters in `AUDIT_GCP_PROJECTS` (default `prophecy-cloud`) | Cloud Audit Logs, Admin Activity: the Kubernetes API audit, always on, kept 400 days | Who (person or service account), when, the tool (kubectl, helm, argocd, Cloud Console…), the source IP, and the change as `field: before → after` |
| Google Cloud resources in those projects (IAM, disks, snapshots, node pools, DNS, buckets) | Cloud Audit Logs, Admin Activity | Who, when, the method (for example `v1.compute.snapshots.insert`), and whether it failed |
| Any other cluster: EKS, AKS, GKE elsewhere | The object's `managedFields` | Which tool (field manager) last set each field, and when. **Not who**: these clusters collect no audit log. |

### How a change is worked out

Each audit entry carries the object as it was *after* the request. The tool compares an entry with the previous entry
for the same object to show what changed (`spec.resources.requests.storage: 10Gi → 100Gi`). For merge patches it also
shows the patch the person sent. Secret contents are never shown, and every value passes through the same redaction as
other tool output.

### People and controllers

A single `kubectl patch` is followed by changes from Kubernetes itself: the volume binder, the CSI driver, nodes. These
are left out unless `include_system` is set, so answers name the person who acted. Service accounts outside
`kube-system` (ArgoCD, control-hub) are kept: they are how automation shows up.

### What it cannot tell yet

- **EKS and AKS**: their API audit logs are off, so only the field owners are available. Turning them on is phase 2 and
  needs a control-hub change.
- **AWS and Azure resources**: CloudTrail and the Azure Activity Log are not read yet.
- **Changes made through control-hub** appear under control-hub's own identity (its service account), because
  control-hub does not record which person asked for them.

## Access

The console reads Cloud Audit Logs with its Google identity (`control-hub-admin@prophecy-cloud.iam.gserviceaccount.com`,
through Workload Identity). It needs `roles/logging.viewer` on each project in `AUDIT_GCP_PROJECTS`; without it the tool
answers that it cannot read the logs.

## API

`GET /api/audit/changes` (admins) runs the same search with query parameters `scope` (`kubernetes` or `gcp`),
`cluster`, `kind`, `namespace`, `name`, `actor`, `since` (`24h`, `30d` or an RFC 3339 time), `until`, `service` and
`include_system`.

```text
/api/audit/changes?cluster=prophecy-cloud-mgmt&kind=pvc&namespace=ai-console&name=ai-console-data&since=30d
/api/audit/changes?scope=gcp&name=ai-console-data-before-ns-move&since=7d
```
