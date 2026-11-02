package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	certmanager "google.golang.org/api/certificatemanager/v1"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

// Options configure the real sources.
type Options struct {
	CH     *controlhub.Client
	Token  string // read-only service token
	Realms []string
	// SecretNamespaces hold control-hub's wildcard TLS secrets.
	SecretNamespaces []string
	ACMRegions       []string
	AWSKeyID         string
	AWSSecret        string
	// GCPProjectFilter selects the projects whose Certificate Manager
	// certificates are read (Resource Manager filter, e.g. "id:sap-*"); empty disables.
	GCPProjectFilter string
}

// RealSources wires the scanner to control-hub, the cluster, AWS, GCP and DNS.
func RealSources(o Options) Sources {
	res := &net.Resolver{}
	s := Sources{
		Dial: dialChain,
		LookupHost: func(ctx context.Context, host string) ([]string, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return res.LookupHost(ctx, host)
		},
		LookupCNAME: func(ctx context.Context, name string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return res.LookupCNAME(ctx, name)
		},
	}
	if o.CH != nil && o.Token != "" {
		s.Tenants = func(ctx context.Context) ([]Tenant, error) { return tenants(ctx, o) }
	}
	if len(o.SecretNamespaces) > 0 {
		if cfg, err := rest.InClusterConfig(); err == nil {
			if cs, err := kubernetes.NewForConfig(cfg); err == nil {
				s.Secrets = func(ctx context.Context) ([]SecretCert, error) { return secrets(ctx, cs, o.SecretNamespaces) }
			}
		}
	}
	if o.AWSKeyID != "" && len(o.ACMRegions) > 0 {
		s.ACM = func(ctx context.Context) ([]ACMCert, []string) { return acmCerts(ctx, o) }
	}
	if o.GCPProjectFilter != "" {
		s.GCP = func(ctx context.Context) ([]GCPCert, []string) { return gcpCerts(ctx, o.GCPProjectFilter) }
	}
	return s
}

func dialChain(ctx context.Context, host string) ([]*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}} //nolint:gosec // inspection only; verified separately
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.(*tls.Conn).ConnectionState().PeerCertificates, nil
}

// skipStatus: control planes that are not expected to serve yet, or any more.
func skipStatus(status string) bool {
	s := strings.ToUpper(status)
	return strings.Contains(s, "FAIL") || strings.Contains(s, "DELET") || s == "CREATING" || s == "PENDING" || s == ""
}

func tenants(ctx context.Context, o Options) ([]Tenant, error) {
	var out []Tenant
	var errs []string
	for _, realm := range o.Realms {
		cps, err := tools.ListControlPlanes(ctx, o.CH, o.Token, realm, "")
		if err != nil {
			errs = append(errs, realm+": "+err.Error())
			continue
		}
		for _, cp := range cps {
			host := hostOf(cp.URL)
			if host == "" || skipStatus(cp.Status) {
				continue
			}
			out = append(out, Tenant{ID: cp.ID, Name: firstNonEmpty(cp.CustomerName, cp.TenantName, cp.ClusterName, cp.ID),
				Realm: realm, Provider: cp.Provider, Region: cp.Region, Host: host})
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return out, nil
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// secrets reads only tls.crt; the key is dropped with the rest of the object.
func secrets(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]SecretCert, error) {
	var out []SecretCert
	var errs []string
	for _, ns := range namespaces {
		list, err := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{FieldSelector: "type=kubernetes.io/tls"})
		if err != nil {
			errs = append(errs, ns+": "+err.Error())
			continue
		}
		for _, sec := range list.Items {
			block, _ := pem.Decode(sec.Data["tls.crt"])
			if block == nil {
				continue
			}
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				continue
			}
			out = append(out, SecretCert{Namespace: ns, Name: sec.Name, Cert: c})
		}
	}
	if len(errs) > 0 {
		return out, errors.New(strings.Join(errs, "; "))
	}
	return out, nil
}

var allKeyTypes = []acmtypes.KeyAlgorithm{acmtypes.KeyAlgorithmRsa1024, acmtypes.KeyAlgorithmRsa2048, acmtypes.KeyAlgorithmRsa3072,
	acmtypes.KeyAlgorithmRsa4096, acmtypes.KeyAlgorithmEcPrime256v1, acmtypes.KeyAlgorithmEcSecp384r1, acmtypes.KeyAlgorithmEcSecp521r1}

func acmCerts(ctx context.Context, o Options) ([]ACMCert, []string) {
	var mu sync.Mutex
	var out []ACMCert
	var errs []string
	var wg sync.WaitGroup
	for _, region := range o.ACMRegions {
		wg.Add(1)
		go func(region string) {
			defer wg.Done()
			certs, err := acmRegion(ctx, o, region)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, "ACM "+region+": "+err.Error())
			}
			out = append(out, certs...)
		}(region)
	}
	wg.Wait()
	sort.Strings(errs)
	return out, errs
}

func acmRegion(ctx context.Context, o Options, region string) ([]ACMCert, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(o.AWSKeyID, o.AWSSecret, "")))
	if err != nil {
		return nil, err
	}
	cl := acm.NewFromConfig(cfg)
	var arns []string
	p := acm.NewListCertificatesPaginator(cl, &acm.ListCertificatesInput{Includes: &acmtypes.Filters{KeyTypes: allKeyTypes}})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.CertificateSummaryList {
			arns = append(arns, aws.ToString(s.CertificateArn))
		}
	}
	var out []ACMCert
	for _, arn := range arns {
		d, err := cl.DescribeCertificate(ctx, &acm.DescribeCertificateInput{CertificateArn: aws.String(arn)})
		if err != nil || d.Certificate == nil {
			continue
		}
		c := d.Certificate
		a := ACMCert{ARN: arn, Region: region, Domain: aws.ToString(c.DomainName), SANs: c.SubjectAlternativeNames,
			Status: string(c.Status), Serial: aws.ToString(c.Serial), RenewalEligibility: string(c.RenewalEligibility),
			InUseBy: len(c.InUseBy)}
		if c.NotAfter != nil {
			a.NotAfter = *c.NotAfter
		}
		if c.Options != nil && c.Options.Export == acmtypes.CertificateExportEnabled {
			a.Exportable = true
		}
		if r := c.RenewalSummary; r != nil {
			a.RenewalStatus, a.RenewalReason = string(r.RenewalStatus), string(r.RenewalStatusReason)
			if a.RenewalReason == "" && len(r.DomainValidationOptions) > 0 {
				a.RenewalReason = string(r.DomainValidationOptions[0].ValidationStatus)
			}
		}
		for _, v := range c.DomainValidationOptions {
			if v.ResourceRecord == nil {
				continue
			}
			a.Validations = append(a.Validations, Record{Domain: aws.ToString(v.DomainName),
				Name: aws.ToString(v.ResourceRecord.Name), Value: aws.ToString(v.ResourceRecord.Value)})
		}
		out = append(out, a)
	}
	return out, nil
}

func gcpCerts(ctx context.Context, filter string) ([]GCPCert, []string) {
	rm, err := crm.NewService(ctx)
	if err != nil {
		return nil, []string{"GCP: " + err.Error()}
	}
	cm, err := certmanager.NewService(ctx)
	if err != nil {
		return nil, []string{"GCP: " + err.Error()}
	}
	var projects []string
	err = rm.Projects.List().Filter(filter+" lifecycleState:ACTIVE").Pages(ctx, func(r *crm.ListProjectsResponse) error {
		for _, p := range r.Projects {
			projects = append(projects, p.ProjectId)
		}
		return nil
	})
	if err != nil {
		return nil, []string{"GCP projects: " + err.Error()}
	}
	var mu sync.Mutex
	var out []GCPCert
	var errs []string
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, p := range projects {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			certs, err := gcpProject(ctx, cm, p)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, "Certificate Manager "+p+": "+err.Error())
			}
			out = append(out, certs...)
		}(p)
	}
	wg.Wait()
	sort.Strings(errs)
	return out, errs
}

// gcpProject reads one project. Projects the console cannot read or where the
// API is off hold no certificates of ours, so those errors are dropped.
func gcpProject(ctx context.Context, cm *certmanager.Service, project string) ([]GCPCert, error) {
	parent := "projects/" + project + "/locations/-"
	auths := map[string]Record{}
	err := cm.Projects.Locations.DnsAuthorizations.List(parent).Pages(ctx, func(r *certmanager.ListDnsAuthorizationsResponse) error {
		for _, a := range r.DnsAuthorizations {
			if a.DnsResourceRecord != nil {
				auths[authKey(a.Name)] = Record{Domain: a.Domain, Name: a.DnsResourceRecord.Name, Value: a.DnsResourceRecord.Data}
			}
		}
		return nil
	})
	if ignorable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []GCPCert
	err = cm.Projects.Locations.Certificates.List(parent).Pages(ctx, func(r *certmanager.ListCertificatesResponse) error {
		for _, c := range r.Certificates {
			if c.Managed == nil {
				continue // self-managed certificates are tracked through the hosts that serve them
			}
			g := GCPCert{Name: c.Name, Project: project, Location: locationOf(c.Name), Hosts: c.SanDnsnames, State: c.Managed.State}
			if len(g.Hosts) == 0 {
				g.Hosts = c.Managed.Domains
			}
			if t, err := time.Parse(time.RFC3339, c.ExpireTime); err == nil {
				g.NotAfter = t
			}
			if pi := c.Managed.ProvisioningIssue; pi != nil && (pi.Reason != "" || pi.Details != "") {
				g.Issue = strings.TrimSpace(pi.Reason + " " + pi.Details)
			}
			for _, a := range c.Managed.AuthorizationAttemptInfo {
				if a.State == "FAILED" {
					g.AuthFailures = append(g.AuthFailures, fmt.Sprintf("%s: %s %s", a.Domain, a.FailureReason, a.Details))
				}
			}
			for _, name := range c.Managed.DnsAuthorizations {
				if r, ok := auths[authKey(name)]; ok {
					g.DNSAuth = append(g.DNSAuth, r)
				}
			}
			out = append(out, g)
		}
		return nil
	})
	if ignorable(err) {
		return nil, nil
	}
	return out, err
}

// authKey drops the project from a DNS authorization name: certificates refer
// to it by project number, the list returns it by project ID.
func authKey(name string) string {
	if i := strings.Index(name, "/locations/"); i >= 0 {
		return name[i:]
	}
	return name
}

func ignorable(err error) bool {
	var ge *googleapi.Error
	return errors.As(err, &ge) && (ge.Code == 403 || ge.Code == 404)
}

func locationOf(name string) string {
	parts := strings.Split(name, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "locations" {
			return parts[i+1]
		}
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
