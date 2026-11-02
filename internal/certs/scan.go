package certs

import (
	"context"
	"crypto/x509"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Tenant is a control plane whose host should serve a valid certificate.
type Tenant struct {
	ID, Name, Realm, Provider, Region, Host string
}

// SecretCert is the certificate in a TLS secret. The private key is never kept.
type SecretCert struct {
	Namespace, Name string
	Cert            *x509.Certificate
}

// Record is a DNS record a certificate authority checks before renewing.
type Record struct {
	Domain string // the domain it proves
	Name   string // where the CNAME must be
	Value  string // what it must point to
}

// ACMCert is an ACM certificate.
type ACMCert struct {
	ARN, Region, Domain string
	SANs                []string
	Status              string // ISSUED, PENDING_VALIDATION, EXPIRED, ...
	NotAfter            time.Time
	Serial              string
	RenewalEligibility  string // ELIGIBLE / INELIGIBLE
	RenewalStatus       string // PENDING_AUTO_RENEWAL, PENDING_VALIDATION, SUCCESS, FAILED, ""
	RenewalReason       string
	InUseBy             int
	Exportable          bool
	Validations         []Record
}

// GCPCert is a Google-managed Certificate Manager certificate.
type GCPCert struct {
	Name     string // full resource name
	Project  string
	Location string
	Hosts    []string
	State    string // ACTIVE, PROVISIONING, FAILED
	NotAfter time.Time
	Issue    string // provisioning issue, if any
	// AuthFailures are failed domain authorization attempts.
	AuthFailures []string
	// DNSAuth are the CNAMEs of the certificate's DNS authorizations.
	DNSAuth []Record
}

// Sources is everything the scanner reads. Nil sources are skipped.
type Sources struct {
	Tenants func(ctx context.Context) ([]Tenant, error)
	Secrets func(ctx context.Context) ([]SecretCert, error)
	// ACM and GCP return what they could read plus per-region/project errors.
	ACM func(ctx context.Context) ([]ACMCert, []string)
	GCP func(ctx context.Context) ([]GCPCert, []string)

	// Dial returns the chain a host serves on :443, leaf first.
	Dial        func(ctx context.Context, host string) ([]*x509.Certificate, error)
	LookupHost  func(ctx context.Context, host string) ([]string, error)
	LookupCNAME func(ctx context.Context, name string) (string, error)
	// Roots verifies served chains; nil uses the system pool.
	Roots *x509.CertPool
	Now   func() time.Time
}

// Scan builds the inventory.
func Scan(ctx context.Context, src Sources) *Inventory {
	now := time.Now
	if src.Now != nil {
		now = src.Now
	}
	start := now()
	inv := &Inventory{Version: ScannerVersion, ScannedAt: start.UTC(), Errors: []string{}}

	var (
		tenants []Tenant
		secrets []SecretCert
		acm     []ACMCert
		gcp     []GCPCert
		mu      sync.Mutex
		wg      sync.WaitGroup
	)
	addErr := func(errs ...string) {
		mu.Lock()
		inv.Errors = append(inv.Errors, errs...)
		mu.Unlock()
	}
	run := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	if src.Tenants != nil {
		run(func() {
			var err error
			if tenants, err = src.Tenants(ctx); err != nil {
				addErr("control planes: " + err.Error())
			}
		})
	}
	if src.Secrets != nil {
		run(func() {
			var err error
			if secrets, err = src.Secrets(ctx); err != nil {
				addErr("wildcard secrets: " + err.Error())
			}
		})
	}
	if src.ACM != nil {
		run(func() {
			var errs []string
			acm, errs = src.ACM(ctx)
			addErr(errs...)
		})
	}
	if src.GCP != nil {
		run(func() {
			var errs []string
			gcp, errs = src.GCP(ctx)
			addErr(errs...)
		})
	}
	wg.Wait()

	s := &scan{src: src, now: now(), cnames: map[string]cnameResult{}}
	inv.Items = s.build(ctx, tenants, secrets, acm, gcp)
	sortItems(inv.Items)
	inv.DurationMs = now().Sub(start).Milliseconds()
	return inv
}

type cnameResult struct {
	target string
	err    error
}

type scan struct {
	src    Sources
	now    time.Time
	mu     sync.Mutex
	cnames map[string]cnameResult
}

// lookupCNAME caches lookups; many certificates share a validation record.
func (s *scan) lookupCNAME(ctx context.Context, name string) (string, error) {
	name = normDNS(name)
	s.mu.Lock()
	r, ok := s.cnames[name]
	s.mu.Unlock()
	if ok {
		return r.target, r.err
	}
	t, err := s.src.LookupCNAME(ctx, name+".")
	r = cnameResult{normDNS(t), err}
	s.mu.Lock()
	s.cnames[name] = r
	s.mu.Unlock()
	return r.target, r.err
}

// checkRecords verifies validation CNAMEs. renewSoon makes a missing record
// critical: the CA is about to need it.
func (s *scan) checkRecords(ctx context.Context, it *Item, recs []Record, who string, renewSoon bool) bool {
	ok := true
	seen := map[string]bool{}
	sev := SevWarning
	if renewSoon {
		sev = SevCritical
	}
	for _, r := range recs {
		name, want := normDNS(r.Name), normDNS(r.Value)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		got, err := s.lookupCNAME(ctx, name)
		switch {
		case err != nil || got == "" || got == name:
			ok = false
			it.check("Validation CNAME", false, name+" is missing")
			it.problem(sev, "validation_cname_missing", fmt.Sprintf(
				"The validation CNAME %s is missing from DNS, so %s cannot renew this certificate. Recreate it pointing to %s.", name, who, want))
		case want != "" && got != want:
			ok = false
			it.check("Validation CNAME", false, fmt.Sprintf("%s points to %s, expected %s", name, got, want))
			it.problem(sev, "validation_cname_mismatch", fmt.Sprintf(
				"The validation CNAME %s points to %s instead of %s, so %s cannot renew this certificate.", name, got, want, who))
		default:
			it.check("Validation CNAME", true, name+" → "+got)
		}
	}
	return ok
}

// acmItem assesses an ACM certificate; tenants that serve it reuse the result.
func (s *scan) acmItem(ctx context.Context, c ACMCert) Item {
	it := Item{Key: "acm:" + c.ARN, Kind: KindACM, Host: c.Domain, Region: c.Region, Provider: "aws",
		Source: SourceACM, SourceRef: c.ARN, SANs: c.SANs, Issuer: "Amazon"}
	na := c.NotAfter.UTC()
	days := daysLeft(na, s.now)
	it.Subject, it.NotAfter, it.DaysLeft = c.Domain, &na, &days

	eligible := c.RenewalEligibility == "ELIGIBLE" || c.Exportable
	it.AutoRenew = "yes"
	if !eligible {
		it.AutoRenew = "no"
	}
	it.check("ACM status", c.Status == "ISSUED", c.Status)
	if c.Status != "ISSUED" && c.Status != "" {
		it.problem(SevWarning, "acm_status", "ACM reports the certificate as "+c.Status+".")
	}
	switch c.RenewalStatus {
	case "FAILED":
		it.problem(SevCritical, "renewal_failed", "ACM renewal failed: "+orDefault(c.RenewalReason, "no reason given")+".")
	case "PENDING_VALIDATION":
		sev := SevWarning
		if days <= 14 {
			sev = SevCritical
		}
		it.problem(sev, "renewal_pending_validation", "ACM is renewing but cannot validate the domain yet ("+
			orDefault(c.RenewalReason, "check the validation CNAME")+").")
	}
	if c.RenewalStatus != "" {
		it.check("ACM renewal", c.RenewalStatus != "FAILED", c.RenewalStatus)
	}
	if !eligible && c.InUseBy == 0 {
		it.problem(SevInfo, "not_in_use", "Not attached to any AWS resource, so ACM will not renew it.")
	}
	// ACM starts renewing 60 days before expiry; a missing record matters most then.
	if eligible {
		s.checkRecords(ctx, &it, c.Validations, "ACM", days <= 60)
	}
	// Every DNS-validated ACM certificate can be renewed through control-hub:
	// it restores the validation records and ACM revalidates.
	if len(c.Validations) > 0 {
		it.Repair = &RepairTarget{Kind: "acm-validation", CertificateARN: c.ARN, Broken: needsRepair(it.Problems)}
	}
	return it
}

func (s *scan) gcpItem(ctx context.Context, c GCPCert) Item {
	host := ""
	if len(c.Hosts) > 0 {
		host = c.Hosts[0]
	}
	it := Item{Key: "gcp:" + c.Name, Kind: KindGCP, Host: host, Region: c.Location, Provider: "gcp",
		Source: SourceGCP, SourceRef: c.Name, SANs: c.Hosts, Issuer: "Google Trust Services", AutoRenew: "yes", Tenant: c.Project}
	if !c.NotAfter.IsZero() {
		na := c.NotAfter.UTC()
		days := daysLeft(na, s.now)
		it.Subject, it.NotAfter, it.DaysLeft = host, &na, &days
	}
	it.check("Certificate Manager state", c.State == "ACTIVE", c.State)
	if c.State != "ACTIVE" {
		msg := "Certificate Manager reports the certificate as " + c.State
		if c.Issue != "" {
			msg += ": " + c.Issue
		}
		it.problem(SevCritical, "gcp_state", msg+".")
	} else if c.Issue != "" {
		it.problem(SevWarning, "gcp_provisioning_issue", "Certificate Manager reports a provisioning issue: "+c.Issue+".")
	}
	for _, f := range c.AuthFailures {
		it.problem(SevCritical, "gcp_authorization_failed", "Domain authorization failed: "+f+".")
	}
	renewSoon := it.DaysLeft != nil && *it.DaysLeft <= 30
	s.checkRecords(ctx, &it, c.DNSAuth, "Certificate Manager", renewSoon)
	if len(c.DNSAuth) > 0 {
		it.Repair = &RepairTarget{Kind: "gcp-dns-authorization", Certificate: c.Name, Broken: needsRepair(it.Problems)}
	}
	return it
}

func (s *scan) build(ctx context.Context, tenants []Tenant, secrets []SecretCert, acm []ACMCert, gcp []GCPCert) []Item {
	// Source items, keyed for matching.
	acmItems := make([]Item, len(acm))
	acmBySerial := map[string]int{}
	gcpItems := make([]Item, len(gcp))
	gcpByHost := map[string]int{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range acm {
		acmBySerial[normSerial(acm[i].Serial)] = i
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			acmItems[i] = s.acmItem(ctx, acm[i])
		}(i)
	}
	for i := range gcp {
		for _, h := range gcp[i].Hosts {
			gcpByHost[normDNS(h)] = i
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			gcpItems[i] = s.gcpItem(ctx, gcp[i])
		}(i)
	}
	wg.Wait()

	// Wildcard secrets, one item per distinct certificate.
	var wild []Item
	wildByFP := map[string]int{}
	exportedFrom := map[int]string{}
	secretName := map[int]string{}
	for _, sc := range secrets {
		fp := fingerprint(sc.Cert)
		ref := sc.Namespace + "/" + sc.Name
		if i, ok := wildByFP[fp]; ok {
			wild[i].SourceRef += ", " + ref
			continue
		}
		it := Item{Key: "secret:" + fp, Kind: KindWildcard, Source: SourceWildcard, SourceRef: ref, AutoRenew: "no"}
		it.setCert(sc.Cert, s.now)
		it.Host = it.Subject
		if it.Host == "" && len(it.SANs) > 0 {
			it.Host = it.SANs[0]
		}
		if i, ok := acmBySerial[certSerial(sc.Cert)]; ok {
			// An exported ACM certificate: ACM renews it, a person re-exports it.
			exportedFrom[len(wild)] = acm[i].ARN
			it.AutoRenew = "ACM renews; re-export needed"
			for _, p := range acmItems[i].Problems {
				if p.Severity != SevInfo {
					it.Problems = append(it.Problems, p)
				}
			}
			it.Checks = append(it.Checks, acmItems[i].Checks...)
			acmItems[i].Kind = "" // shown through the wildcard
		}
		wildByFP[fp] = len(wild)
		secretName[len(wild)] = sc.Name
		wild = append(wild, it)
	}
	// A wildcard with an exportable ACM certificate for its domain can be
	// renewed and synced by control-hub (POST /api/certificates/wildcards/renew).
	// Matched by domain, not serial: once ACM renews, the serials differ.
	for i := range wild {
		for _, c := range acm {
			if c.Exportable && c.Status == "ISSUED" && normDNS(c.Domain) == normDNS(wild[i].Subject) {
				wild[i].Repair = &RepairTarget{Kind: "wildcard-renew", SecretName: secretName[i]}
				break
			}
		}
	}
	for i, arn := range exportedFrom {
		wild[i].SourceRef += " (exported from " + arn + ")"
	}
	// newestByName finds the latest-expiring known certificate for a name, so
	// stale copies (a secret or a cluster still holding the old one) show up.
	newestByName := map[string]time.Time{}
	newestFrom := map[string]string{}
	note := func(name string, na time.Time, from string) {
		name = normDNS(name)
		if name == "" {
			return
		}
		if na.After(newestByName[name]) {
			newestByName[name], newestFrom[name] = na, from
		}
	}
	for _, w := range wild {
		note(w.Subject, *w.NotAfter, "secret "+w.SourceRef)
	}
	// Only exportable ACM certificates can end up in a secret; a non-exportable
	// one with the same name is a separate certificate, not a newer copy.
	for _, c := range acm {
		if c.Status == "ISSUED" && c.Exportable {
			note(c.Domain, c.NotAfter, "ACM "+c.ARN)
		}
	}
	for i := range wild {
		w := &wild[i]
		if t, ok := newestByName[normDNS(w.Subject)]; ok && t.Sub(*w.NotAfter) > 24*time.Hour {
			w.problem(staleSeverity(*w.DaysLeft), "stale_secret", fmt.Sprintf(
				"A renewed %s certificate valid until %s exists (%s). Update this secret with it.", w.Subject, day(t), newestFrom[normDNS(w.Subject)]))
		} else if w.AutoRenew == "no" {
			w.problem(SevInfo, "manual_renewal", "Nothing renews this secret automatically; renew it by hand before it expires.")
		}
		if w.Repair != nil && w.Repair.Kind == "wildcard-renew" {
			// Due when a newer copy waits in ACM or ACM's renewal window has started.
			w.Repair.Broken = hasProblem(w.Problems, "stale_secret") || (w.DaysLeft != nil && *w.DaysLeft <= 60)
		}
	}

	// Tenant hosts.
	tItems := make([]tenantItem, len(tenants))
	for i := range tenants {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tItems[i] = s.tenantItem(ctx, tenants[i])
		}(i)
	}
	wg.Wait()

	usedACM := map[int]bool{}
	usedGCP := map[int]bool{}
	for i := range tItems {
		it := &tItems[i]
		served := it.servedFP
		switch {
		case served != "" && hasKey(wildByFP, served):
			w := &wild[wildByFP[served]]
			it.Source, it.SourceRef, it.AutoRenew = SourceWildcard, w.SourceRef, "copied wildcard; "+w.AutoRenew
			w.UsedBy = append(w.UsedBy, it.Host)
		case it.servedSerial != "" && hasKey(acmBySerial, it.servedSerial):
			j := acmBySerial[it.servedSerial]
			usedACM[j] = true
			a := &acmItems[j]
			a.UsedBy = append(a.UsedBy, it.Host)
			it.Source, it.SourceRef, it.AutoRenew = SourceACM, a.SourceRef, a.AutoRenew
			it.inherit(*a)
		case hasKey(gcpByHost, normDNS(it.Host)):
			j := gcpByHost[normDNS(it.Host)]
			usedGCP[j] = true
			g := &gcpItems[j]
			g.UsedBy = append(g.UsedBy, it.Host)
			it.Source, it.SourceRef, it.AutoRenew = SourceGCP, g.SourceRef, g.AutoRenew
			it.inherit(*g)
		default:
			it.Source, it.AutoRenew = SourceUnknown, "unknown"
		}
	}

	// Stale copies, once every host's source is known.
	for i := range tItems {
		it := &tItems[i]
		// A stale copy of a wildcard: the cluster holds an older copy than the
		// newest one we know of. Hosts served straight from ACM or Certificate
		// Manager are renewed in place and are never copies.
		if it.NotAfter != nil && strings.HasPrefix(it.Subject, "*.") && it.Source != SourceACM && it.Source != SourceGCP {
			name := normDNS(it.Subject)
			if t, ok := newestByName[name]; ok && t.Sub(*it.NotAfter) > 24*time.Hour {
				it.problem(staleSeverity(*it.DaysLeft), "stale_copy", fmt.Sprintf(
					"Serves an older copy of %s that expires %s. A renewed one valid until %s exists (%s); update the TLS secret on this cluster.",
					it.Subject, day(*it.NotAfter), day(t), newestFrom[name]))
			} else if h, t := newestServed(tItems, name); h != "" && t.Sub(*it.NotAfter) > 24*time.Hour {
				// No source we can read has a newer one, but another host holding a
				// copy serves one. The source is not visible, so this is a hint.
				sev := SevInfo
				if *it.DaysLeft <= 60 {
					sev = SevWarning
				}
				it.problem(sev, "stale_copy", fmt.Sprintf(
					"Serves a copy of %s that expires %s, while %s serves one valid until %s. If both come from the same source, "+
						"this cluster's TLS secret was not updated; otherwise renew this copy before it expires.",
					it.Subject, day(*it.NotAfter), h, day(t)))
			}
		}
	}

	var out []Item
	for i := range tItems {
		tItems[i].finish()
		out = append(out, tItems[i].Item)
	}
	for i := range wild {
		wild[i].finish()
		out = append(out, wild[i])
	}
	for i := range acmItems {
		if acmItems[i].Kind == "" || usedACM[i] {
			// Exported into a wildcard secret, or covered by the tenants that serve it;
			// still listed when it has its own problems.
			if acmItems[i].Kind == "" || len(acmItems[i].Problems) == 0 {
				continue
			}
		}
		acmItems[i].finish()
		out = append(out, acmItems[i])
	}
	for i := range gcpItems {
		if usedGCP[i] {
			continue
		}
		gcpItems[i].finish()
		out = append(out, gcpItems[i])
	}
	return out
}

// newestServed returns the host serving the latest-expiring copy of a wildcard
// name. Hosts served straight from ACM or Certificate Manager hold separate
// certificates, not copies, so they are skipped.
func newestServed(items []tenantItem, name string) (string, time.Time) {
	var host string
	var best time.Time
	for _, t := range items {
		if t.Source == SourceACM || t.Source == SourceGCP {
			continue
		}
		if t.NotAfter != nil && normDNS(t.Subject) == name && t.NotAfter.After(best) {
			host, best = t.Host, *t.NotAfter
		}
	}
	return host, best
}

func hasProblem(ps []Problem, code string) bool {
	for _, p := range ps {
		if p.Code == code {
			return true
		}
	}
	return false
}

func staleSeverity(daysLeft int) string {
	if daysLeft <= 14 {
		return SevCritical
	}
	return SevWarning
}

func hasKey[K comparable, V any](m map[K]V, k K) bool {
	_, ok := m[k]
	return ok
}

// tenantItem is an Item plus what matching needs.
type tenantItem struct {
	Item
	servedFP, servedSerial string
}

// inherit copies a source's problems and checks onto the tenant that serves it.
func (t *tenantItem) inherit(src Item) {
	for _, p := range src.Problems {
		if p.Severity == SevInfo {
			continue
		}
		t.Problems = append(t.Problems, p)
	}
	t.Checks = append(t.Checks, src.Checks...)
	t.Repair = src.Repair
}

func (s *scan) tenantItem(ctx context.Context, t Tenant) tenantItem {
	it := tenantItem{Item: Item{Key: "tenant:" + t.Realm + ":" + t.ID, Kind: KindTenant, Tenant: t.Name, TenantID: t.ID,
		Realm: t.Realm, Provider: t.Provider, Region: t.Region, Host: t.Host}}
	addrs, err := s.src.LookupHost(ctx, t.Host)
	if err != nil || len(addrs) == 0 {
		it.check("DNS record", false, "no A/AAAA/CNAME record")
		it.problem(SevCritical, "dns_missing", "The host has no DNS record, so it cannot be reached and no certificate can be validated for it.")
		return it
	}
	it.check("DNS record", true, strings.Join(addrs, ", "))
	chain, err := s.src.Dial(ctx, t.Host)
	if err != nil || len(chain) == 0 {
		it.check("TLS handshake", false, errText(err, "no certificate served"))
		// Usually a host that only accepts allow-listed IPs; the certificate is
		// unknown rather than broken, so the item gets the "unknown" status.
		it.problem(SevInfo, "tls_unreachable", "The console could not complete a TLS handshake on port 443 ("+
			errText(err, "no certificate served")+"), so the certificate could not be checked. The host may only accept allow-listed IPs.")
		return it
	}
	leaf := chain[0]
	it.setCert(leaf, s.now)
	it.servedFP, it.servedSerial = fingerprint(leaf), certSerial(leaf)
	it.check("TLS handshake", true, "served "+leaf.Subject.CommonName)
	if err := leaf.VerifyHostname(t.Host); err != nil {
		it.check("Covers host", false, err.Error())
		it.problem(SevCritical, "hostname_mismatch", "The served certificate does not cover "+t.Host+".")
	} else {
		it.check("Covers host", true, "")
	}
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: s.src.Roots, Intermediates: inter, CurrentTime: s.now}); err != nil {
		if it.DaysLeft != nil && *it.DaysLeft >= 0 {
			it.check("Trusted chain", false, err.Error())
			it.problem(SevCritical, "untrusted_chain", "Browsers will not trust the served chain: "+err.Error()+".")
		}
	} else {
		it.check("Trusted chain", true, "")
	}
	return it
}

func errText(err error, def string) string {
	if err == nil {
		return def
	}
	return err.Error()
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
