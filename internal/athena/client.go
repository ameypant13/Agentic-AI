// Package athena reads a control plane's Athena API, the in-cluster service
// that reports Prophecy's health, upgrades, sandboxes, features and metering.
// control-hub reaches every control plane's Athena at <cp-url>/athena/api/v1
// with one shared credential, so this client is deliberately narrow: it only
// sends GET requests, and only to the read views listed below. Athena's
// write endpoints (restore, backup delete, sandbox delete, feature updates)
// cannot be reached through it.
package athena

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// View is one read-only Athena endpoint.
type View struct {
	Path        string
	Description string
	// Query lists the query parameters the view accepts.
	Query []string
}

// Views are the only endpoints the client calls.
var Views = map[string]View{
	"cluster":            {Path: "/cluster", Description: "cluster UUID, deployment and upgrade status, current and desired version, per-execution-plane upgrade status"},
	"update_info":        {Path: "/cluster/updateInfo", Description: "whether an update is available, scheduled update time, upgrade status"},
	"available_versions": {Path: "/cluster/availableVersions", Description: "Prophecy versions this cluster can upgrade to"},
	"maintenance":        {Path: "/cluster/scheduledMaintenance", Description: "scheduled maintenance windows"},
	"insights":           {Path: "/cluster/prophecyInsights", Description: "current and peak sandbox users, allocated sandboxes, user counts, scheduled pipelines, 30-day active users"},
	"prophecy_status":    {Path: "/prophecy/status", Description: "whether Prophecy or any service is down, initial admin user state, ETA and message"},
	"service_status":     {Path: "/service/status", Description: "per-component status and the running version"},
	"platform_report":    {Path: "/report/platform/status", Description: "per-service availability (RED/YELLOW/GREEN) from Athena's e2e/SLA tests, failed scenarios, version mismatch"},
	"alerts":             {Path: "/alert/status", Description: "Athena's current alert status"},
	"tasks":              {Path: "/tasks/list", Description: "async tasks (upgrades, backups, restores) and their status"},
	"sandboxes":          {Path: "/sandbox", Description: "sandbox pool: free and used per type (Scala, Python, SQL, DBX serverless)"},
	"features":           {Path: "/features", Description: "cluster feature flags (expressEdition, lineage, SSO, transpiler, …)"},
	"feature_config":     {Path: "/featureConfig", Description: "feature configuration; config_type selects alert, audit, backup, e2etests, log, sandbox, telemetry or verticals", Query: []string{"configType"}},
	"metering":           {Path: "/metering", Description: "metering for the cluster", Query: []string{"metricsScope"}},
	"credits":            {Path: "/metering/creditsUsed", Description: "percentage of credits used and projected exhaustion date"},
}

const maxBody = 1 << 20

// ErrNotConfigured means the console has no Athena credential.
var ErrNotConfigured = errors.New("Athena access is not configured (ATHENA_AUTH_TOKEN)")

type Client struct {
	token string
	http  *http.Client
}

// New returns a client sending token as Athena's Authorization header, which
// is base64(email:password) without a scheme, as control-hub sends it.
func New(token string) *Client {
	return &Client{token: strings.TrimSpace(token), http: &http.Client{Timeout: 30 * time.Second}}
}

// WithHTTPClient replaces the HTTP client (tests use a TLS test server's).
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	c.http = h
	return c
}

// Get reads one view from the Athena of the control plane at cpURL.
func (c *Client) Get(ctx context.Context, cpURL, view string, query url.Values) ([]byte, error) {
	if c == nil || c.token == "" {
		return nil, ErrNotConfigured
	}
	v, ok := Views[view]
	if !ok {
		return nil, fmt.Errorf("unknown Athena view %q", view)
	}
	base, err := BaseURL(cpURL)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, k := range v.Query {
		if val := query.Get(k); val != "" {
			q.Set(k, val)
		}
	}
	u := base + v.Path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("athena %s: %w", view, scrub(err, c.token))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		body := string(raw)
		if len(body) > 300 {
			body = body[:300] + "…"
		}
		return nil, fmt.Errorf("athena %s: %s %s", view, resp.Status, strings.TrimSpace(body))
	}
	return raw, nil
}

// BaseURL turns a control plane URL (host, or https URL) into its Athena API
// base. Plain http is refused: the credential must not travel unencrypted.
func BaseURL(cpURL string) (string, error) {
	s := strings.TrimRight(strings.TrimSpace(cpURL), "/")
	if s == "" {
		return "", errors.New("the control plane has no URL yet")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", errors.New("the control plane URL is not a plain https URL") // not echoed: it may hold credentials
	}
	return "https://" + u.Host + "/athena/api/v1", nil
}

func scrub(err error, token string) error {
	if token != "" && strings.Contains(err.Error(), token) {
		return errors.New(strings.ReplaceAll(err.Error(), token, "***"))
	}
	return err
}
