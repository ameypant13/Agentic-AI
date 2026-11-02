# Certificates

**Route:** `/certificates` · "Every certificate tenants depend on, soonest expiry first. Each scan also checks what
renewal needs: the host's DNS record, ACM validation CNAMEs and Certificate Manager DNS authorizations."

This page shows the [certificate inventory](../concepts/certificates.md): the certificate each tenant host serves,
control-hub's wildcard TLS secrets, and ACM and GCP Certificate Manager certificates.

## Reading the page

The summary line says when the inventory was scanned and how often it rescans, and counts critical, warning, OK and
unreachable certificates. If some sources could not be read, a card lists them: their certificates are missing from
the list.

| Column | Shows |
|---|---|
| Status | Expired, Critical, Warning, Unknown or OK |
| Expires in | Days left, or "expired Nd ago" |
| Host | The tenant host, or the certificate's main name |
| Tenant | Tenant, with provider · realm · region |
| Type | Tenant, Wildcard, ACM or Cert Manager |
| Source | ACM, GCP Certificate Manager, Wildcard secret or Unknown, with its auto-renew state |
| Problem | The main problem |
| Action | Admins only: **Renew**, **Renew & sync** (wildcards) or "manual" |

Filter with **All**, **Needs attention**, **Tenants**, **Wildcards**, **ACM** or **Cert Manager**, and search by host
or tenant. Click a row to see every problem, the checks that ran (✓/✗), subject, issuer, expiry, names and which hosts
serve it.

## Common tasks

### See what expires soon

Click **Needs attention**. The list is already sorted soonest first.

### Find out why a certificate did not renew

1. Click the row.
2. Read **Problems**. A `validation_cname_missing` or `gcp_authorization_failed` problem means the DNS record renewal
   depends on is missing or wrong; `dns_missing` means the host itself has no record.
3. Ask the Copilot ("Why did the certificate for &lt;host&gt; not renew?"). It uses `list_certificates`, which already
   includes these checks.

### Rescan now (admins)

Click **Rescan now**. The label changes to "Scan queued" or "Scanning…". Results appear when the scan finishes.

### Renew a certificate (admins, when repair is enabled)

The **Action** buttons are enabled only when the console runs with `CERT_REPAIR_ENABLED=true`; otherwise their tooltip
says they are turned off. Both panels run with your own admin login.

=== "ACM / Certificate Manager"

    1. Click **Renew** on the row. The **Renew certificate** panel opens and runs a dry run (**Check renewal**).
    2. Read the table: each validation record (CNAME), what it is now ("missing"), what it should be, and the change
       (correct, create, update).
    3. Click **Renew: write N records** to apply. ("Records already correct" means there is nothing to do.)
    4. The result column shows written, unchanged or failed for each record. The list refreshes after the next scan.

=== "Wildcard"

    1. Click **Renew & sync**. The **Renew & sync wildcard** panel opens and checks what the next step is: **Renew in
       ACM**, **Sync renewed certificate**, **Up to date** or **Renew by hand**.
    2. Click the step's button (**Request renewal in ACM** or **Sync to N secrets**). Each click does the next step.
    3. When the wildcard is up to date you can tick **renew early (charged)** to force a renewal.
    4. The table shows each secret, its expiry now and after, and the result (updated, already current, skipped,
       failed).
