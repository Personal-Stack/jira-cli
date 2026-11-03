package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/personal-stack/jira-cli/internal/config"
)

// ChainCert is one certificate of a server's chain and where it came from.
type ChainCert struct {
	Cert   *x509.Certificate
	Source string // "server" or "aia:<url>"
}

// maxAIASize bounds certificates downloaded from AIA URLs.
const maxAIASize = 1 << 20

// FetchChain returns the certificate chain a server presents, completed via
// AIA "CA Issuers" URLs when the server omits its issuers (common: servers
// rarely send the root). Certificate verification is deliberately disabled
// for this one handshake: its only purpose is to *read* the chain so the
// user can decide whether to trust it. No HTTP credentials are sent; the
// proxy and client-certificate settings of the instance are honoured so the
// chain is the one the CLI will see.
func FetchChain(ctx context.Context, rawURL string, in *config.Instance, timeout time.Duration) ([]ChainCert, []string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, nil, fmt.Errorf("need an https:// URL, got %q", rawURL)
	}
	tc, err := TLSConfig(in.TLS)
	if err != nil {
		return nil, nil, err
	}
	var presented []*x509.Certificate
	tc.InsecureSkipVerify = true //nolint:gosec // read-only chain inspection; nothing is trusted here
	tc.VerifyConnection = func(cs tls.ConnectionState) error {
		presented = cs.PeerCertificates
		return nil
	}
	proxy, err := ProxyFunc(in.Proxy)
	if err != nil {
		return nil, nil, err
	}
	hc := &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{Proxy: proxy, TLSClientConfig: tc, ForceAttemptHTTP2: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.Scheme+"://"+u.Host+"/", nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "jira-cli certs fetch")
	resp, err := hc.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if len(presented) == 0 {
		if err == nil {
			err = fmt.Errorf("server presented no certificates")
		}
		return nil, nil, err
	}
	chain := make([]ChainCert, 0, len(presented)+2)
	for _, c := range presented {
		chain = append(chain, ChainCert{Cert: c, Source: "server"})
	}
	aia := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: proxy}}
	return completeChain(ctx, chain, aia)
}

// completeChain follows AIA CA-Issuers URLs from the top of the chain until a
// self-signed root is reached (at most 3 hops). A downloaded certificate is
// accepted only if it verifies the signature of the certificate below it.
func completeChain(ctx context.Context, chain []ChainCert, hc *http.Client) ([]ChainCert, []string, error) {
	var notes []string
	for hop := 0; hop < 3; hop++ {
		top := chain[len(chain)-1].Cert
		if SelfSigned(top) {
			return chain, notes, nil
		}
		if len(top.IssuingCertificateURL) == 0 {
			notes = append(notes, fmt.Sprintf("%q has no AIA issuer URL; the root is not available from the network (ask IT, or export it from the browser)", top.Subject.CommonName))
			return chain, notes, nil
		}
		var found *x509.Certificate
		var from string
		for _, link := range top.IssuingCertificateURL {
			certs, err := fetchCerts(ctx, hc, link)
			if err != nil {
				notes = append(notes, fmt.Sprintf("AIA %s: %v", link, err))
				continue
			}
			for _, c := range certs {
				if top.CheckSignatureFrom(c) == nil {
					found, from = c, link
					break
				}
			}
			if found != nil {
				break
			}
			notes = append(notes, fmt.Sprintf("AIA %s: no certificate there signs %q; ignored", link, top.Subject.CommonName))
		}
		if found == nil {
			return chain, notes, nil
		}
		chain = append(chain, ChainCert{Cert: found, Source: "aia:" + from})
	}
	return chain, notes, nil
}

func fetchCerts(ctx context.Context, hc *http.Client, link string) ([]*x509.Certificate, error) {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("unsupported URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAIASize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxAIASize {
		return nil, fmt.Errorf("larger than %d bytes", maxAIASize)
	}
	certs, _, err := config.ParseCABundle(b, link)
	return certs, err
}

// SelfSigned reports whether c is a self-signed (root) certificate.
func SelfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawIssuer, c.RawSubject) && c.CheckSignatureFrom(c) == nil
}
