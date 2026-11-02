package tools

import (
	"context"
	"encoding/json"
)

func certTool() Tool {
	return Tool{
		Name: "list_certificates",
		Description: "List TLS certificates from the console's certificate inventory (rescanned every few hours): the certificate each " +
			"tenant's control plane host serves, control-hub's wildcard TLS secrets, ACM and GCP Certificate Manager certificates. " +
			"Each has days left, whether something renews it automatically, and problems that will stop renewal: missing host DNS " +
			"record, missing or wrong ACM validation / Certificate Manager DNS authorization CNAME, failed renewal, a stale copy of a " +
			"wildcard, a certificate that does not cover its host. Sorted by expiry, soonest first.",
		Properties: map[string]any{
			"query":         str("Optional text matched against host, tenant name and source (ARN, secret, resource)."),
			"problems_only": map[string]any{"type": "boolean", "description": "Only certificates that are expiring or have a problem."},
		},
		Label: "Checked certificates",
		Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				Query        string `json:"query"`
				ProblemsOnly bool   `json:"problems_only"`
			}](input)
			if err != nil {
				return "", err
			}
			return env.Certs(ctx, in.Query, in.ProblemsOnly)
		},
	}
}

// CertRepairTool restores the DNS records a certificate's automatic renewal
// needs, through control-hub's admin-only POST /api/certificates/repair. It is
// the one state-changing tool; it is offered by the MCP server (where the
// client asks the person before each call), not by the console's Copilot,
// whose Certificates page has its own Repair button.
func CertRepairTool() Tool {
	return Tool{
		Name: "repair_certificate",
		Description: "Fix a certificate whose automatic renewal is failing because its DNS validation record is missing or wrong. " +
			"ACM and GCP Certificate Manager renew by themselves once that record exists again, so this recreates: the ACM " +
			"DNS-validation CNAMEs (kind acm-validation, give certificate_arn) or the Certificate Manager DNS-authorization " +
			"CNAMEs (kind gcp-dns-authorization, give certificate as projects/{p}/locations/{l}/certificates/{c}). Find the " +
			"certificate with list_certificates (its repair field). control-hub takes record names and values from the cloud " +
			"API, writes only in its own DNS zones and never deletes. Always call with dry_run true first and show the plan; " +
			"call with dry_run false only after the person agrees. Requires a control-hub admin token.",
		Properties: map[string]any{
			"kind":            enum("What to repair.", "acm-validation", "gcp-dns-authorization"),
			"certificate_arn": str("ACM certificate ARN (acm-validation)."),
			"certificate":     str("Certificate Manager certificate resource name (gcp-dns-authorization)."),
			"dry_run":         map[string]any{"type": "boolean", "description": "true (default) returns the plan without changing DNS."},
		},
		Required: []string{"kind"},
		Label:    "Repaired certificate DNS records",
		Writes:   true,
		Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				Kind           string `json:"kind"`
				CertificateARN string `json:"certificate_arn"`
				Certificate    string `json:"certificate"`
				DryRun         *bool  `json:"dry_run"`
			}](input)
			if err != nil {
				return "", err
			}
			dry := in.DryRun == nil || *in.DryRun
			raw, err := env.CH.Post(ctx, env.Token, "/api/certificates/repair", map[string]any{
				"kind": in.Kind, "certificateArn": in.CertificateARN, "certificate": in.Certificate, "dryRun": dry})
			if err != nil {
				return "", err
			}
			return string(raw), nil
		},
	}
}

// WildcardRenewTool renews control-hub's wildcard certificate and syncs it to
// tenant clusters, through control-hub's admin-only
// POST /api/certificates/wildcards/renew. MCP only, like repair_certificate.
func WildcardRenewTool() Tool {
	return Tool{
		Name: "renew_wildcard_certificate",
		Description: "Renew control-hub's wildcard certificate (*.app.prophecy.ai, an exported ACM certificate renewed by hand today) " +
			"and copy it to every tenant cluster that uses it. Each call does the next step: if ACM has no newer certificate it asks " +
			"ACM to renew (only inside ACM's 60-day window unless force, since early renewal is charged); once ACM holds a newer one " +
			"it exports it, updates control-hub's secret and each v2 control plane's {customer}-{cluster}-tls-secret that holds an " +
			"older copy of the same domain. The result lists every target with current and new expiry. Always call with dry_run " +
			"true first and show the plan; call with dry_run false only after the person agrees. Requires a control-hub admin token.",
		Properties: map[string]any{
			"secret_name": str("control-hub's wildcard secret; defaults to the *.app.prophecy.ai secret."),
			"dry_run":     map[string]any{"type": "boolean", "description": "true (default) returns the plan without changing anything."},
			"force":       map[string]any{"type": "boolean", "description": "Renew in ACM even more than 60 days before expiry (an extra, charged renewal)."},
		},
		Label:  "Renewed wildcard certificate",
		Writes: true,
		Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				SecretName string `json:"secret_name"`
				DryRun     *bool  `json:"dry_run"`
				Force      bool   `json:"force"`
			}](input)
			if err != nil {
				return "", err
			}
			dry := in.DryRun == nil || *in.DryRun
			raw, err := env.CH.Post(ctx, env.Token, "/api/certificates/wildcards/renew",
				map[string]any{"secretName": in.SecretName, "dryRun": dry, "force": in.Force})
			if err != nil {
				return "", err
			}
			return string(raw), nil
		},
	}
}
