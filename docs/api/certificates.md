# Certificates

A background worker (`CERT_SCAN_INTERVAL`, default 6 h; result in `DATA_DIR/certs/inventory.json`) inventories the
certificate each control plane host serves (`kind: tenant`), control-hub's wildcard TLS secrets (`wildcard`), and ACM
and GCP Certificate Manager certificates not already shown through a tenant (`acm`, `gcp`). Read-only. See
[Certificate inventory](../concepts/certificates.md).

## Routes

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/api/certificates?kind=` | — | `{scannedAt, durationMs, running, intervalMinutes, repairEnabled, counts, errors[], items: CertItem[]}`, soonest expiry first (unknown expiry last). `503` when certificate tracking is not configured. |
| POST | `/api/certificates/scan` | — | `202 {queued}`. Admins only; rescans now. |
| POST | `/api/certificates/repair` | `{key, dryRun, force?}` | Admins only; needs `CERT_REPAIR_ENABLED`. See below. Audited. |

## CertItem

```ts
type CertItem = {
  key: string
  kind: "tenant" | "wildcard" | "acm" | "gcp"
  tenant?: string; tenantId?: string; realm?: string; provider?: string; region?: string
  host: string                // the tenant host, or the certificate's main name for other kinds
  source: "acm" | "gcp-certificate-manager" | "wildcard-secret" | "unknown"
  sourceRef?: string          // an ACM ARN, a Certificate Manager resource, or the wildcard secrets' namespace/name
  autoRenew: string           // "yes", "no", or a short explanation such as "ACM renews; re-export needed"
  subject?: string; issuer?: string; sans?: string[]
  notAfter?: string; daysLeft?: number
  status: "ok" | "unknown" | "warning" | "critical" | "expired"
  problems: { severity: "info" | "warning" | "critical"; code: string; message: string }[]
  checks: { name: string; ok: boolean; detail: string }[]
  usedBy?: string[]           // tenant hosts serving this certificate (wildcards, ACM, GCP)
  repair?: {                  // set when control-hub can restore what renewal needs
    kind: "acm-validation" | "gcp-dns-authorization" | "wildcard-renew"
    certificateArn?: string; certificate?: string; secretName?: string
    broken: boolean           // renewal is failing (vs. a healthy certificate whose records can be re-checked)
  }
}
```

Status: `expired` below 0 days, `critical` at 7 or fewer, `warning` at 30 or fewer, raised by any warning or critical
problem; `unknown` when the host could not be reached.

Problem codes: `dns_missing`, `tls_unreachable` (info), `hostname_mismatch`, `untrusted_chain`,
`validation_cname_missing`, `validation_cname_mismatch`, `renewal_failed`, `renewal_pending_validation`, `acm_status`,
`not_in_use` (info), `gcp_state`, `gcp_provisioning_issue`, `gcp_authorization_failed`, `stale_copy` (a cluster serves
an older copy of a wildcard than a secret, an exportable ACM certificate or another host has), `stale_secret`,
`manual_renewal` (info).

## Repair

The target comes from the inventory item's `repair` field, and the call runs with the admin's own control-hub token.

- For ACM validation and Certificate Manager DNS-authorization targets, the console calls control-hub's
  `POST /api/certificates/repair`, which recreates the validation CNAMEs. It returns control-hub's
  `RepairResult {kind, target, dryRun, changes[{name, type, current, desired, action, applied, error}], next}`.
- For a wildcard row (`repair.kind = wildcard-renew`) it calls control-hub's `POST /api/certificates/wildcards/renew`:
  renew in ACM, then export and sync to control-hub's secret and every tenant copy. It returns
  `WildcardResult {secret, domain, step: renew-in-acm|sync|up-to-date|manual, source, changes[{target, secret, currentExpiry, newExpiry, action, reason, applied, error}], next}`.
  `force` renews early.

control-hub's own error status and body are passed through (`403` for non-admins, `502` with partial results). After
an applied (non-dry-run) repair the console queues a rescan.

## Copilot

`list_certificates` reads the stored inventory (optionally only certificates with problems).
