# Certificate inventory

The certificate inventory (`internal/certs`) keeps a list of every TLS certificate the fleet depends on. For each one
it checks expiry **and the things automatic renewal needs**, so a renewal that is going to fail shows up before the
certificate expires. Scanning is read-only.

## What is inventoried

| Kind | What it is | Source of truth |
|---|---|---|
| `tenant` | The certificate each control plane host serves | A TLS connection to the host, matched to its ACM or Certificate Manager certificate where possible |
| `wildcard` | control-hub's wildcard TLS secrets | `kubernetes.io/tls` secrets in `CERT_SECRET_NAMESPACES` (default `control-hub-prod`); only `tls.crt` is parsed and the key is dropped |
| `acm` | ACM certificates not already shown through a tenant | AWS ACM in `CERT_ACM_REGIONS` (default the four US regions), read with `AWS_PROD_*` |
| `gcp` | GCP Certificate Manager certificates not already shown through a tenant | Projects matching `CERT_GCP_PROJECT_FILTER` (default `id:sap-*`), read with the pod's Google identity |

The tenant list comes from control-hub with `CONTROLHUB_SERVICE_TOKEN`, across every realm in `REALMS`. Control planes
that are not expected to serve yet, or any more, are skipped.

## Scanning

- A worker rescans every `CERT_SCAN_INTERVAL` (default 6 hours). At start-up it scans at once unless the stored
  inventory is still fresh.
- Admins can ask for a scan now (**Rescan now**, `POST /api/certificates/scan`); this is a no-op while one is queued.
- The result is saved to `DATA_DIR/certs/inventory.json`. Pages and the Copilot's `list_certificates` read the stored
  result, never a live scan.
- Sources that could not be read are listed as errors, so missing certificates are visible.

## Status

| Status | When |
|---|---|
| `expired` | Fewer than 0 days left. |
| `critical` | 7 days or fewer left, or any critical problem. |
| `warning` | 30 days or fewer left, or any warning problem. |
| `unknown` | The host could not be reached. |
| `ok` | Everything else. |

## Problems and checks

Each item carries `problems` (severity, code, message) and `checks` (name, ok, detail). Problem codes:

| Code | Meaning |
|---|---|
| `dns_missing` | The host has no DNS record, so it cannot be reached and no certificate can be validated for it. |
| `tls_unreachable` | The host did not answer on 443 (info). |
| `hostname_mismatch` | The served certificate does not cover the host. |
| `untrusted_chain` | Browsers will not trust the served chain. |
| `validation_cname_missing` | ACM's DNS validation CNAME is missing, so renewal will fail. |
| `validation_cname_mismatch` | The validation CNAME points somewhere else. |
| `renewal_failed` | ACM reports a failed renewal. |
| `renewal_pending_validation` | ACM is waiting for validation. |
| `acm_status` | ACM reports the certificate in a status other than issued. |
| `not_in_use` | Not attached to any AWS resource, so ACM will not renew it (info). |
| `gcp_state` | The Certificate Manager certificate is not active (critical). |
| `gcp_provisioning_issue` | Certificate Manager reports a provisioning issue. |
| `gcp_authorization_failed` | A DNS authorization failed. |
| `stale_copy` | A cluster serves an older copy of a wildcard than a secret, an exportable ACM certificate or another host has. |
| `stale_secret` | A renewed certificate for the same name exists elsewhere; the wildcard secret should be updated with it. |
| `manual_renewal` | Nothing renews this wildcard secret automatically; renew it by hand before it expires (info). |

## Repair and renewal

Some problems can be fixed by control-hub. When `CERT_REPAIR_ENABLED=true`, each inventory item that can be fixed has
a `repair` target, and admins can act on it from the Certificates page (or through the MCP server's
`repair_certificate` and `renew_wildcard_certificate` tools):

| Repair kind | What control-hub does | control-hub endpoint |
|---|---|---|
| `acm-validation` | Recreate the ACM validation CNAME | `POST /api/certificates/repair` |
| `gcp-dns-authorization` | Recreate the Certificate Manager DNS-authorization CNAME | `POST /api/certificates/repair` |
| `wildcard-renew` | Renew the wildcard in ACM, then export it and sync it to control-hub's secret and every tenant copy | `POST /api/certificates/wildcards/renew` |

Every repair runs as a **dry run first** that lists the changes, then applies them on request. It runs with the
admin's own control-hub token (control-hub returns `403` for non-admins), is audited, and triggers a rescan after an
apply. It is the one write allowed alongside `READ_ONLY`.

!!! warning
    Leave `CERT_REPAIR_ENABLED` off until the connected control-hub serves `POST /api/certificates/repair`.

## Related monitor

Separately from the inventory, the `cert_expiry` monitor checks the certificate each operational control plane
serves every `MONITOR_INTERVAL` and opens a finding under `CERT_WARN_DAYS`. See [Findings and monitors](monitors.md).
