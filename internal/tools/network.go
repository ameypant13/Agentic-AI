package tools

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProbePolicy limits which hosts the TLS and HTTP probes may contact, so the
// model cannot use them to reach internal services from inside the cluster.
type ProbePolicy struct {
	AllowedSuffixes []string
}

func DefaultProbePolicy() ProbePolicy {
	return ProbePolicy{AllowedSuffixes: []string{".prophecy.ai", ".prophecy.io"}}
}

func (p ProbePolicy) check(host string) error {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, s := range p.AllowedSuffixes {
		if strings.HasSuffix(h, s) {
			return nil
		}
	}
	return fmt.Errorf("host %q is not allowed; probes are limited to %s", host, strings.Join(p.AllowedSuffixes, ", "))
}

func networkTools() []Tool {
	return []Tool{
		{
			Name:        "resolve_dns",
			Description: "Resolve a hostname from inside the cluster: CNAME and A/AAAA records. Use it to check whether a control plane's DNS record exists and where it points.",
			Properties:  map[string]any{"host": str("Hostname, e.g. capgroup.app.prophecy.ai.")},
			Required:    []string{"host"},
			Label:       "Checked DNS",
			Run:         runResolveDNS,
		},
		{
			Name:        "check_tls_certificate",
			Description: "Connect to host:443 and report the served certificate: subject, SANs, issuer, NotBefore/NotAfter and days left. Limited to Prophecy hostnames.",
			Properties:  map[string]any{"host": str("Hostname.")},
			Required:    []string{"host"},
			Label:       "Checked the TLS certificate",
			Run:         runCheckTLS,
		},
		{
			Name:        "probe_https",
			Description: "HTTPS GET a Prophecy URL and report the status code, latency and the first 500 bytes of the body. Use it to see whether a control plane answers.",
			Properties:  map[string]any{"url": str("https:// URL on a Prophecy hostname.")},
			Required:    []string{"url"},
			Label:       "Checked that the endpoint responds",
			Run:         runProbeHTTPS,
		},
	}
}

func runResolveDNS(ctx context.Context, _ *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Host string `json:"host"`
	}](input)
	if err != nil {
		return "", err
	}
	if in.Host == "" {
		return "", fmt.Errorf("host is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := map[string]any{"host": in.Host}
	if cname, err := net.DefaultResolver.LookupCNAME(ctx, in.Host); err == nil {
		out["cname"] = cname
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, in.Host)
	if err != nil {
		out["error"] = err.Error()
	} else {
		out["addresses"] = addrs
	}
	return marshal(out)
}

// CertInfo is the served certificate of a host. The monitors use it too.
type CertInfo struct {
	Host     string    `json:"host"`
	Subject  string    `json:"subject"`
	SANs     []string  `json:"sans"`
	Issuer   string    `json:"issuer"`
	NotAfter time.Time `json:"notAfter"`
	DaysLeft int       `json:"daysLeft"`
}

// FetchCert reads the certificate a host serves on :443. Verification is
// skipped on purpose: an expired or mismatched certificate is exactly what
// the caller wants to see.
func FetchCert(ctx context.Context, host string) (*CertInfo, error) {
	d := &tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}} //nolint:gosec // inspection only
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificate served")
	}
	leaf := certs[0]
	return &CertInfo{
		Host:     host,
		Subject:  leaf.Subject.CommonName,
		SANs:     leaf.DNSNames,
		Issuer:   leaf.Issuer.CommonName,
		NotAfter: leaf.NotAfter,
		DaysLeft: int(time.Until(leaf.NotAfter).Hours() / 24),
	}, nil
}

func runCheckTLS(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		Host string `json:"host"`
	}](input)
	if err != nil {
		return "", err
	}
	if err := env.Probe.check(in.Host); err != nil {
		return "", err
	}
	info, err := FetchCert(ctx, in.Host)
	if err != nil {
		return "", err
	}
	return marshal(info)
}

func runProbeHTTPS(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
	in, err := decode[struct {
		URL string `json:"url"`
	}](input)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "https" {
		return "", fmt.Errorf("url must be an https:// URL")
	}
	if err := env.Probe.check(u.Hostname()); err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		// Do not follow redirects off the allowed hosts.
		CheckRedirect: func(req *http.Request, _ []*http.Request) error { return env.Probe.check(req.URL.Hostname()) },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return marshal(map[string]any{"url": in.URL, "error": err.Error()})
	}
	defer resp.Body.Close()
	buf := make([]byte, 500)
	n, _ := resp.Body.Read(buf)
	return marshal(map[string]any{
		"url": in.URL, "status": resp.StatusCode, "latencyMs": time.Since(start).Milliseconds(),
		"bodyStart": string(buf[:n]),
	})
}
