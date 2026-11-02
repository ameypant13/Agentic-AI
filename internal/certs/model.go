// Package certs keeps an inventory of every TLS certificate the fleet depends
// on: the certificate each tenant's control plane host serves, control-hub's
// wildcard TLS secrets, ACM certificates and GCP Certificate Manager
// certificates. For each one it checks expiry and the things auto-renewal
// needs (the host's DNS record, ACM's validation CNAME, Certificate Manager's
// DNS authorization CNAME), so a renewal that is going to fail shows up before
// the certificate expires. Everything here is read-only.
package certs

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// Item kinds.
const (
	KindTenant   = "tenant"   // the certificate a control plane host serves
	KindWildcard = "wildcard" // a control-hub wildcard TLS secret
	KindACM      = "acm"      // an ACM certificate no tenant host was matched to
	KindGCP      = "gcp"      // a Certificate Manager certificate no tenant host was matched to
)

// Sources: where a certificate comes from.
const (
	SourceACM      = "acm"
	SourceGCP      = "gcp-certificate-manager"
	SourceWildcard = "wildcard-secret"
	SourceUnknown  = "unknown"
)

// Statuses, worst last.
const (
	StatusOK       = "ok"
	StatusUnknown  = "unknown" // the certificate could not be read
	StatusWarning  = "warning"
	StatusCritical = "critical"
	StatusExpired  = "expired"
)

// Problem severities.
const (
	SevInfo     = "info"
	SevWarning  = "warning"
	SevCritical = "critical"
)

// Item is one certificate in the inventory.
type Item struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Tenant   string `json:"tenant,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
	Realm    string `json:"realm,omitempty"`
	Provider string `json:"provider,omitempty"`
	Region   string `json:"region,omitempty"`
	// Host is the tenant host, or the certificate's main name for the other kinds.
	Host string `json:"host"`

	Source string `json:"source"`
	// SourceRef names the source: an ACM ARN, a Certificate Manager resource,
	// or the namespace/name of the wildcard secrets.
	SourceRef string `json:"sourceRef,omitempty"`
	// AutoRenew says whether something renews this certificate without a person:
	// "yes", "no", or a short explanation such as "ACM renews; re-export needed".
	AutoRenew string `json:"autoRenew"`

	Subject  string     `json:"subject,omitempty"`
	Issuer   string     `json:"issuer,omitempty"`
	SANs     []string   `json:"sans,omitempty"`
	NotAfter *time.Time `json:"notAfter,omitempty"`
	DaysLeft *int       `json:"daysLeft,omitempty"`

	Status   string    `json:"status"`
	Problems []Problem `json:"problems"`
	Checks   []Check   `json:"checks"`
	// UsedBy lists the tenant hosts serving this certificate (wildcards, ACM, GCP).
	UsedBy []string `json:"usedBy,omitempty"`
	// Repair is set when control-hub can restore the DNS records renewal needs.
	Repair *RepairTarget `json:"repair,omitempty"`
}

// RepairTarget is what control-hub's POST /api/certificates/repair takes.
type RepairTarget struct {
	Kind           string `json:"kind"` // acm-validation | gcp-dns-authorization | wildcard-renew
	CertificateARN string `json:"certificateArn,omitempty"`
	Certificate    string `json:"certificate,omitempty"`
	// SecretName is control-hub's wildcard secret (wildcard-renew).
	SecretName string `json:"secretName,omitempty"`
	// Broken is true when renewal is failing (vs. a healthy certificate whose
	// records can still be re-checked and restored).
	Broken bool `json:"broken"`
}

// repairCodes are the problems restoring validation records can fix.
var repairCodes = map[string]bool{"validation_cname_missing": true, "validation_cname_mismatch": true,
	"renewal_failed": true, "renewal_pending_validation": true, "gcp_authorization_failed": true, "gcp_state": true}

func needsRepair(problems []Problem) bool {
	for _, p := range problems {
		if repairCodes[p.Code] {
			return true
		}
	}
	return false
}

// Problem is something that will break the certificate or its renewal.
type Problem struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Check is one thing the scanner verified, for the detail view.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// ScannerVersion changes when scan results change meaning; a stored inventory
// from another version is rescanned at startup instead of waiting an interval.
const ScannerVersion = 7

// Inventory is the result of one scan.
type Inventory struct {
	Version    int       `json:"version"`
	ScannedAt  time.Time `json:"scannedAt"`
	DurationMs int64     `json:"durationMs"`
	Items      []Item    `json:"items"`
	// Errors are sources that could not be read; their certificates are missing.
	Errors []string `json:"errors"`
}

// Thresholds for the expiry status.
const (
	CriticalDays = 7
	WarningDays  = 30
)

func (it *Item) setCert(c *x509.Certificate, now time.Time) {
	na := c.NotAfter.UTC()
	days := daysLeft(na, now)
	it.Subject, it.Issuer, it.SANs, it.NotAfter, it.DaysLeft = c.Subject.CommonName, issuerName(c), c.DNSNames, &na, &days
}

func (it *Item) problem(sev, code, msg string) {
	it.Problems = append(it.Problems, Problem{Severity: sev, Code: code, Message: msg})
}

func (it *Item) check(name string, ok bool, detail string) {
	it.Checks = append(it.Checks, Check{Name: name, OK: ok, Detail: detail})
}

// finish sets Status from the expiry and the problems.
func (it *Item) finish() {
	st := StatusOK
	if it.DaysLeft != nil {
		switch d := *it.DaysLeft; {
		case d < 0:
			st = StatusExpired
		case d <= CriticalDays:
			st = StatusCritical
		case d <= WarningDays:
			st = StatusWarning
		}
	}
	for _, p := range it.Problems {
		switch p.Severity {
		case SevCritical:
			st = worse(st, StatusCritical)
		case SevWarning:
			st = worse(st, StatusWarning)
		}
	}
	if st == StatusOK && it.DaysLeft == nil {
		st = StatusUnknown
	}
	it.Status = st
	if it.Problems == nil {
		it.Problems = []Problem{}
	}
	if it.Checks == nil {
		it.Checks = []Check{}
	}
	sort.Strings(it.UsedBy)
}

var statusRank = map[string]int{StatusOK: 0, StatusUnknown: 1, StatusWarning: 2, StatusCritical: 3, StatusExpired: 4}

func worse(a, b string) string {
	if statusRank[b] > statusRank[a] {
		return b
	}
	return a
}

// sortItems orders by days left (unknown last), then worst status, then host.
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if (a.DaysLeft == nil) != (b.DaysLeft == nil) {
			return a.DaysLeft != nil
		}
		if a.DaysLeft != nil && *a.DaysLeft != *b.DaysLeft {
			return *a.DaysLeft < *b.DaysLeft
		}
		if statusRank[a.Status] != statusRank[b.Status] {
			return statusRank[a.Status] > statusRank[b.Status]
		}
		return a.Host < b.Host
	})
}

func daysLeft(notAfter, now time.Time) int {
	d := notAfter.Sub(now)
	if d < 0 {
		// Round toward minus infinity so an expired certificate is never "0 days left".
		return int(d.Hours()/24) - 1
	}
	return int(d.Hours() / 24)
}

func issuerName(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0] + " " + c.Issuer.CommonName
	}
	return c.Issuer.CommonName
}

func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// normSerial makes ACM's "0a:1b:..." and x509's serial comparable.
func normSerial(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, ":", ""))
	return strings.TrimLeft(s, "0")
}

func certSerial(c *x509.Certificate) string { return normSerial(c.SerialNumber.Text(16)) }

func day(t time.Time) string { return t.UTC().Format("2 Jan 2006") }

// normDNS lower-cases a DNS name and drops the trailing dot.
func normDNS(s string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".") }
