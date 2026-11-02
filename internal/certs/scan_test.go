package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

type testCA struct {
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	serial int64
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root", Organization: []string{"Test"}},
		NotBefore: testNow.Add(-24 * time.Hour), NotAfter: testNow.Add(10 * 365 * 24 * time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &testCA{cert: c, key: key, serial: 100}
}

func (ca *testCA) issue(t *testing.T, name string, days int) *x509.Certificate {
	t.Helper()
	ca.serial++
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(ca.serial), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: testNow.Add(-24 * time.Hour), NotAfter: testNow.Add(time.Duration(days)*24*time.Hour + time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func serialOf(c *x509.Certificate) string {
	h := c.SerialNumber.Text(16)
	if len(h)%2 == 1 {
		h = "0" + h
	}
	var parts []string
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

func TestScan(t *testing.T) {
	ca := newTestCA(t)
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)

	wildcard := ca.issue(t, "*.app.example.com", 90)    // the current wildcard, exported from ACM
	oldWildcard := ca.issue(t, "*.app.example.com", 10) // an older copy still on one cluster
	dCert := ca.issue(t, "d.example.com", 20)           // ACM per-host cert whose renewal failed
	eCert := ca.issue(t, "e.example.com", 25)           // Certificate Manager cert, DNS auth mismatched
	other := ca.issue(t, "other.example.com", 200)      // served on the wrong host
	oldOther := ca.issue(t, "*.other.example.com", 40)  // a copy one cluster never updated...
	newOther := ca.issue(t, "*.other.example.com", 300) // ...while another cluster serves the renewed one
	albWild := ca.issue(t, "*.app.example.com", 200)    // a separate ACM cert, served straight from an ALB

	served := map[string][]*x509.Certificate{
		"b.app.example.com":   {oldWildcard},
		"c.app.example.com":   {wildcard},
		"d.example.com":       {dCert},
		"e.example.com":       {eCert},
		"f.example.com":       {other},
		"g.other.example.com": {oldOther},
		"h.other.example.com": {newOther},
		"j.app.example.com":   {albWild},
	}
	cnames := map[string]string{
		"_ok.d.example.com":   "_tok.acm-validations.aws",
		"_auth.e.example.com": "wrong.authorize.certificatemanager.goog",
	}
	src := Sources{
		Now:   func() time.Time { return testNow },
		Roots: roots,
		Tenants: func(context.Context) ([]Tenant, error) {
			return []Tenant{
				{ID: "a", Name: "Acme", Realm: "dedicated-saas", Host: "a.app.example.com"},
				{ID: "b", Name: "Bravo", Realm: "dedicated-saas", Host: "b.app.example.com"},
				{ID: "c", Name: "Charlie", Realm: "dedicated-saas", Host: "c.app.example.com"},
				{ID: "d", Name: "Delta", Realm: "public-saas", Host: "d.example.com"},
				{ID: "e", Name: "Echo", Realm: "sap", Host: "e.example.com"},
				{ID: "f", Name: "Foxtrot", Realm: "sap", Host: "f.example.com"},
				{ID: "g", Name: "Golf", Realm: "dedicated-saas", Host: "g.other.example.com"},
				{ID: "h", Name: "Hotel", Realm: "dedicated-saas", Host: "h.other.example.com"},
				{ID: "i", Name: "India", Realm: "dedicated-saas", Host: "i.example.com"}, // allow-listed: no handshake
				{ID: "j", Name: "Juliet", Realm: "dedicated-saas", Host: "j.app.example.com"},
			}, nil
		},
		Secrets: func(context.Context) ([]SecretCert, error) {
			return []SecretCert{
				{Namespace: "control-hub-prod", Name: "wildcard-app-tls", Cert: wildcard},
				{Namespace: "control-hub-dev", Name: "wildcard-app-tls", Cert: wildcard},
			}, nil
		},
		ACM: func(context.Context) ([]ACMCert, []string) {
			return []ACMCert{
				{ARN: "arn:wild", Region: "us-east-1", Domain: "*.app.example.com", Status: "ISSUED", NotAfter: wildcard.NotAfter,
					Serial: serialOf(wildcard), RenewalEligibility: "ELIGIBLE", Exportable: true, InUseBy: 0,
					Validations: []Record{{Domain: "*.app.example.com", Name: "_gone.app.example.com.", Value: "_x.acm-validations.aws."}}},
				{ARN: "arn:d", Region: "us-east-1", Domain: "d.example.com", Status: "ISSUED", NotAfter: dCert.NotAfter,
					Serial: serialOf(dCert), RenewalEligibility: "ELIGIBLE", InUseBy: 1, RenewalStatus: "FAILED", RenewalReason: "DOMAIN_VALIDATION_DENIED",
					Validations: []Record{{Domain: "d.example.com", Name: "_ok.d.example.com.", Value: "_tok.acm-validations.aws."}}},
				// A separate, non-exportable certificate for the same wildcard name on an ALB.
				// It can never be copied into a secret, so it must not make the secret look stale.
				{ARN: "arn:alb-wild", Region: "us-west-2", Domain: "*.app.example.com", Status: "ISSUED", NotAfter: testNow.Add(200 * 24 * time.Hour),
					Serial: serialOf(albWild), RenewalEligibility: "ELIGIBLE", InUseBy: 1,
					Validations: []Record{{Domain: "*.app.example.com", Name: "_ok.d.example.com.", Value: "_tok.acm-validations.aws."}}},
			}, []string{"ACM eu-west-1: access denied"}
		},
		GCP: func(context.Context) ([]GCPCert, []string) {
			return []GCPCert{
				{Name: "projects/p/locations/us-east4/certificates/e", Project: "p", Location: "us-east4", Hosts: []string{"e.example.com"},
					State: "ACTIVE", NotAfter: eCert.NotAfter,
					DNSAuth: []Record{{Domain: "e.example.com", Name: "_auth.e.example.com.", Value: "right.authorize.certificatemanager.goog."}}},
				{Name: "projects/p/locations/us-east4/certificates/ep", Project: "p", Location: "us-east4", Hosts: []string{"ep.example.com"},
					State: "ACTIVE", NotAfter: testNow.Add(50 * 24 * time.Hour)},
			}, nil
		},
		Dial: func(_ context.Context, host string) ([]*x509.Certificate, error) {
			if c, ok := served[host]; ok {
				return c, nil
			}
			return nil, errors.New("connection refused")
		},
		LookupHost: func(_ context.Context, host string) ([]string, error) {
			if host == "a.app.example.com" {
				return nil, errors.New("no such host")
			}
			return []string{"192.0.2.1"}, nil
		},
		LookupCNAME: func(_ context.Context, name string) (string, error) {
			if v, ok := cnames[normDNS(name)]; ok {
				return v + ".", nil
			}
			return "", errors.New("no such host")
		},
	}

	inv := Scan(context.Background(), src)
	if len(inv.Errors) != 1 || !strings.Contains(inv.Errors[0], "eu-west-1") {
		t.Errorf("errors = %v", inv.Errors)
	}
	byKey := map[string]Item{}
	for _, it := range inv.Items {
		byKey[it.Key] = it
	}
	get := func(key string) Item {
		t.Helper()
		it, ok := byKey[key]
		if !ok {
			var keys []string
			for k := range byKey {
				keys = append(keys, k)
			}
			t.Fatalf("no item %s; have %v", key, keys)
		}
		return it
	}
	hasProblem := func(it Item, code, sev string) {
		t.Helper()
		for _, p := range it.Problems {
			if p.Code == code && p.Severity == sev {
				return
			}
		}
		t.Errorf("%s: want %s problem %s, have %+v", it.Key, sev, code, it.Problems)
	}

	a := get("tenant:dedicated-saas:a")
	hasProblem(a, "dns_missing", SevCritical)
	if a.Status != StatusCritical || a.DaysLeft != nil {
		t.Errorf("a: status %s daysLeft %v", a.Status, a.DaysLeft)
	}

	b := get("tenant:dedicated-saas:b")
	hasProblem(b, "stale_copy", SevCritical)
	if b.Status != StatusCritical || *b.DaysLeft != 10 {
		t.Errorf("b: status %s daysLeft %d", b.Status, *b.DaysLeft)
	}

	c := get("tenant:dedicated-saas:c")
	if c.Source != SourceWildcard || !strings.Contains(c.AutoRenew, "re-export") || len(c.Problems) != 0 {
		t.Errorf("c: source %s autoRenew %q problems %+v", c.Source, c.AutoRenew, c.Problems)
	}

	w := get("secret:" + fingerprint(wildcard))
	if w.Kind != KindWildcard || w.SourceRef != "control-hub-prod/wildcard-app-tls, control-hub-dev/wildcard-app-tls (exported from arn:wild)" {
		t.Errorf("wildcard: kind %s ref %q", w.Kind, w.SourceRef)
	}
	if len(w.UsedBy) != 1 || w.UsedBy[0] != "c.app.example.com" {
		t.Errorf("wildcard usedBy = %v", w.UsedBy)
	}
	hasProblem(w, "validation_cname_missing", SevWarning) // inherited from the ACM cert it was exported from
	if _, ok := byKey["acm:arn:wild"]; ok {
		t.Errorf("exported ACM cert listed separately from its wildcard secret")
	}

	d := get("tenant:public-saas:d")
	if d.Source != SourceACM || d.SourceRef != "arn:d" {
		t.Errorf("d: source %s ref %s", d.Source, d.SourceRef)
	}
	hasProblem(d, "renewal_failed", SevCritical)
	if d.Repair == nil || d.Repair.Kind != "acm-validation" || d.Repair.CertificateARN != "arn:d" || !d.Repair.Broken {
		t.Errorf("d repair = %+v", d.Repair)
	}
	// arn:d has its own problem, so it is listed too, with the tenant using it.
	if ad := get("acm:arn:d"); len(ad.UsedBy) != 1 || ad.UsedBy[0] != "d.example.com" {
		t.Errorf("acm d usedBy = %v", ad.UsedBy)
	}

	e := get("tenant:sap:e")
	if e.Source != SourceGCP {
		t.Errorf("e: source %s", e.Source)
	}
	hasProblem(e, "validation_cname_mismatch", SevCritical) // 25 days left: renewal is due
	if e.Repair == nil || e.Repair.Kind != "gcp-dns-authorization" || e.Repair.Certificate != "projects/p/locations/us-east4/certificates/e" {
		t.Errorf("e repair = %+v", e.Repair)
	}
	if w.Repair == nil || w.Repair.Kind != "wildcard-renew" || w.Repair.SecretName != "wildcard-app-tls" {
		t.Errorf("wildcard renew target = %+v", w.Repair)
	}
	if c.Repair != nil || b.Repair != nil {
		t.Errorf("repair offered for copies: c %+v b %+v", c.Repair, b.Repair)
	}
	if _, ok := byKey["gcp:projects/p/locations/us-east4/certificates/e"]; ok {
		t.Errorf("Certificate Manager cert listed separately from the tenant serving it")
	}
	if ep := get("gcp:projects/p/locations/us-east4/certificates/ep"); ep.Kind != KindGCP || ep.Status != StatusOK {
		t.Errorf("unmatched GCP cert = %+v", ep)
	}

	hasProblem(get("tenant:sap:f"), "hostname_mismatch", SevCritical)

	for _, p := range w.Problems {
		if p.Code == "stale_secret" {
			t.Errorf("wildcard secret flagged stale against a non-exportable ACM cert: %s", p.Message)
		}
	}
	g := get("tenant:dedicated-saas:g")
	hasProblem(g, "stale_copy", SevWarning) // h serves a newer copy of the same wildcard
	if !strings.Contains(g.Problems[0].Message, "h.other.example.com") {
		t.Errorf("g stale message = %q", g.Problems[0].Message)
	}
	if h := get("tenant:dedicated-saas:h"); len(h.Problems) != 0 || h.Status != StatusOK {
		t.Errorf("h: status %s problems %+v", h.Status, h.Problems)
	}
	if j := get("tenant:dedicated-saas:j"); j.Source != SourceACM || len(j.Problems) != 0 {
		t.Errorf("j: source %s problems %+v", j.Source, j.Problems)
	} else if j.Repair == nil || j.Repair.CertificateARN != "arn:alb-wild" || j.Repair.Broken {
		t.Errorf("healthy ACM cert should offer renew (not broken): %+v", j.Repair)
	}
	if i := get("tenant:dedicated-saas:i"); i.Status != StatusUnknown {
		t.Errorf("unreachable host status = %s, want unknown", i.Status)
	}

	// Sorted by days left, unknown expiry last.
	var prev = -1 << 31
	for i, it := range inv.Items {
		if it.DaysLeft == nil {
			for _, rest := range inv.Items[i:] {
				if rest.DaysLeft != nil {
					t.Fatalf("item with unknown expiry before %s", rest.Key)
				}
			}
			break
		}
		if *it.DaysLeft < prev {
			t.Fatalf("not sorted: %s (%d) after %d", it.Key, *it.DaysLeft, prev)
		}
		prev = *it.DaysLeft
	}
	if inv.Items[0].Key != "tenant:dedicated-saas:b" {
		t.Errorf("first item = %s, want the 10-day stale copy", inv.Items[0].Key)
	}
}

func TestDaysLeft(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want int
	}{{36 * time.Hour, 1}, {time.Hour, 0}, {-time.Hour, -1}, {-25 * time.Hour, -2}} {
		if got := daysLeft(testNow.Add(tc.in), testNow); got != tc.want {
			t.Errorf("daysLeft(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestNormSerial(t *testing.T) {
	if normSerial("0A:1b:00") != normSerial("a1b00") {
		t.Error("serial forms differ")
	}
}

func TestAuthKeyIgnoresProjectForm(t *testing.T) {
	byNumber := "projects/510821219829/locations/us-east1/dnsAuthorizations/sap-450009-dnsauth-gateway-sbox"
	byID := "projects/sap-450009/locations/us-east1/dnsAuthorizations/sap-450009-dnsauth-gateway-sbox"
	if authKey(byNumber) != authKey(byID) {
		t.Errorf("%q != %q", authKey(byNumber), authKey(byID))
	}
}
