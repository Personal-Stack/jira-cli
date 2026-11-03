package transport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/personal-stack/jira-cli/internal/config"
)

// TLSConfig builds the TLS configuration. Verification is on unless the
// config explicitly sets tls.verify: false.
func TLSConfig(c config.TLS) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.ServerName}
	if c.MinVersion == "1.3" {
		tc.MinVersion = tls.VersionTLS13
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if c.CABundle != "" {
		b, err := os.ReadFile(c.CABundle)
		if err != nil {
			return nil, fmt.Errorf("tls.ca_bundle: %w", err)
		}
		certs, _, err := config.ParseCABundle(b, c.CABundle)
		if err != nil {
			return nil, fmt.Errorf("tls.ca_bundle: %w", err)
		}
		for _, cert := range certs {
			pool.AddCert(cert)
		}
	}
	tc.RootCAs = pool
	if c.ClientCert != "" {
		r := &certReloader{certPath: c.ClientCert, keyPath: c.ClientKey}
		if _, err := r.load(); err != nil {
			return nil, err
		}
		tc.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return r.load() }
	}
	if !c.VerifyEnabled() {
		tc.InsecureSkipVerify = true //nolint:gosec // explicit, warned opt-in (tls.verify: false)
	}
	return tc, nil
}

// certReloader re-reads the client certificate when either file changes, so
// rotated mTLS certificates are picked up without restarting long sessions.
type certReloader struct {
	certPath, keyPath string
	mu                sync.Mutex
	cert              *tls.Certificate
	certMod, keyMod   time.Time
}

func (r *certReloader) load() (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cs, err := os.Stat(r.certPath)
	if err != nil {
		return nil, fmt.Errorf("tls.client_cert: %w", err)
	}
	ks, err := os.Stat(r.keyPath)
	if err != nil {
		return nil, fmt.Errorf("tls.client_key: %w", err)
	}
	if r.cert != nil && cs.ModTime().Equal(r.certMod) && ks.ModTime().Equal(r.keyMod) {
		return r.cert, nil
	}
	c, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return nil, fmt.Errorf("tls client certificate: %w", err)
	}
	r.cert, r.certMod, r.keyMod = &c, cs.ModTime(), ks.ModTime()
	return r.cert, nil
}
