package grafana

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type pki struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func mkCert(t *testing.T, tmpl *x509.Certificate, parent *pki) *pki {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	signer, signCert := key, tmpl
	if parent != nil {
		signer, signCert = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signCert, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &pki{cert: c, key: key}
}

func caTmpl(serial int64, name string) *x509.Certificate {
	return &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
}

// serveLeafOnly starts a TLS server that sends only the leaf, like azure.monitoring.prophecy.io.
func serveLeafOnly(t *testing.T, leaf *pki) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leaf.cert.Raw}, PrivateKey: leaf.key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func get(v *aiaVerifier, url string) error {
	tr := &http.Transport{TLSClientConfig: v.tlsConfig()}
	tr.TLSClientConfig.ServerName = "grafana.test"
	resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(url)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

func TestAIAFetchesMissingIntermediate(t *testing.T) {
	root := mkCert(t, caTmpl(1, "Root"), nil)
	inter := mkCert(t, caTmpl(2, "Intermediate"), root)

	aia := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(inter.cert.Raw) }))
	defer aia.Close()

	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "grafana.test"},
		DNSNames: []string{"grafana.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IssuingCertificateURL: []string{aia.URL + "/inter.cer"}}
	leaf := mkCert(t, leafTmpl, inter)
	srv := serveLeafOnly(t, leaf)

	roots := x509.NewCertPool()
	roots.AddCert(root.cert)

	if err := get(newAIAVerifier(roots), srv.URL); err != nil {
		t.Fatalf("leaf-only server with AIA: %v", err)
	}

	// Without the AIA fetch it fails the way Go's default client does.
	noAIA := newAIAVerifier(roots)
	noAIA.fetch = func(string) (*x509.Certificate, error) { return nil, errFetch }
	if err := get(noAIA, srv.URL); err == nil {
		t.Fatal("leaf-only server verified without the intermediate")
	}

	// A chain that does not reach a trusted root still fails.
	other := x509.NewCertPool()
	other.AddCert(mkCert(t, caTmpl(9, "Other root"), nil).cert)
	if err := get(newAIAVerifier(other), srv.URL); err == nil {
		t.Fatal("verified against an untrusted root")
	}

	// The wrong host name still fails.
	v := newAIAVerifier(roots)
	tr := &http.Transport{TLSClientConfig: v.tlsConfig()}
	tr.TLSClientConfig.ServerName = "evil.test"
	if _, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(srv.URL); err == nil {
		t.Fatal("verified a certificate for another host")
	}
}

var errFetch = &net.AddrError{Err: "fetch disabled", Addr: "aia"}
