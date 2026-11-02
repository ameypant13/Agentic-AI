package grafana

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Some servers send only their leaf certificate and leave out the
// intermediate (azure.monitoring.prophecy.io does). Browsers fetch the
// missing intermediate from the certificate's Authority Information Access
// URL; Go's TLS client does not, and fails with "unknown authority". aiaTLS
// does what browsers do: it verifies the chain itself and, when an issuer is
// missing, downloads it from the AIA URL. The result must still chain to a
// trusted root, so this only fills a gap and never weakens verification.

type aiaVerifier struct {
	roots *x509.CertPool // nil = system roots
	fetch func(url string) (*x509.Certificate, error)

	mu    sync.Mutex
	cache map[string]*x509.Certificate
}

func newAIAVerifier(roots *x509.CertPool) *aiaVerifier {
	cl := &http.Client{Timeout: 10 * time.Second}
	v := &aiaVerifier{roots: roots, cache: map[string]*x509.Certificate{}}
	v.fetch = func(url string) (*x509.Certificate, error) { return fetchCert(cl, url) }
	return v
}

// tlsConfig skips Go's built-in verification only to replace it with verify.
func (v *aiaVerifier) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // VerifyConnection below does the full verification
		VerifyConnection:   v.verify,
	}
}

func (v *aiaVerifier) verify(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("tls: server sent no certificate")
	}
	leaf := cs.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	opts := x509.VerifyOptions{DNSName: cs.ServerName, Roots: v.roots, Intermediates: inter}
	_, err := leaf.Verify(opts)
	// Follow AIA up to three issuers deep when the chain is incomplete.
	cur := leaf
	for hop := 0; hop < 3 && isUnknownAuthority(err) && len(cur.IssuingCertificateURL) > 0; hop++ {
		issuer, ferr := v.issuer(cur.IssuingCertificateURL[0])
		if ferr != nil {
			return fmt.Errorf("%w (fetching the missing intermediate failed: %v)", err, ferr)
		}
		inter.AddCert(issuer)
		_, err = leaf.Verify(opts)
		cur = issuer
	}
	return err
}

func (v *aiaVerifier) issuer(url string) (*x509.Certificate, error) {
	v.mu.Lock()
	c, ok := v.cache[url]
	v.mu.Unlock()
	if ok {
		return c, nil
	}
	c, err := v.fetch(url)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.cache[url] = c
	v.mu.Unlock()
	return c, nil
}

func isUnknownAuthority(err error) bool {
	var ua x509.UnknownAuthorityError
	return errors.As(err, &ua)
}

func fetchCert(cl *http.Client, url string) (*x509.Certificate, error) {
	resp, err := cl.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if b, _ := pem.Decode(raw); b != nil {
		raw = b.Bytes
	}
	return x509.ParseCertificate(raw)
}
