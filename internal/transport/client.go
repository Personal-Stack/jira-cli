// Package transport builds the single HTTP client used for all Jira traffic:
// TLS/mTLS, proxy, timeouts, connection pooling, redirect policy and retry
// policy live here and nowhere else.
package transport

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/personal-stack/jira-cli/internal/config"
)

// MaxRedirects bounds redirect chains.
const MaxRedirects = 5

// New builds an *http.Client for an instance. timeout overrides the
// configured request timeout when > 0.
func New(in *config.Instance, timeout time.Duration) (*http.Client, error) {
	connect, request, err := in.Timeouts.Durations()
	if err != nil {
		return nil, err
	}
	if timeout > 0 {
		request = timeout
	}
	tc, err := TLSConfig(in.TLS)
	if err != nil {
		return nil, err
	}
	proxy, err := ProxyFunc(in.Proxy)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: connect, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy:                 proxy,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tc,
		TLSHandshakeTimeout:   connect,
		ResponseHeaderTimeout: request,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: tr, Timeout: request, CheckRedirect: CheckRedirect}, nil
}

// ProxyFunc returns the proxy selector: explicit URL, disabled, or the
// standard HTTPS_PROXY/NO_PROXY environment variables.
func ProxyFunc(p config.Proxy) (func(*http.Request) (*url.URL, error), error) {
	switch {
	case p.Disabled:
		return nil, nil
	case p.URL != "":
		u, err := url.Parse(p.URL)
		if err != nil {
			return nil, fmt.Errorf("proxy.url: %w", err)
		}
		return http.ProxyURL(u), nil
	}
	return http.ProxyFromEnvironment, nil
}

// ErrDowngrade is returned for https -> http redirects.
var ErrDowngrade = errors.New("refusing redirect from https to http")

// CheckRedirect enforces the redirect policy:
//   - at most MaxRedirects hops;
//   - never downgrade https to http;
//   - credentials (Authorization, Cookie) are only forwarded to the exact
//     origin (scheme, host, port) of the original request. Go's default
//     forwards them to subdomains as well, which is too permissive.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxRedirects {
		return fmt.Errorf("stopped after %d redirects", MaxRedirects)
	}
	orig := via[0].URL
	if orig.Scheme == "https" && req.URL.Scheme != "https" {
		return ErrDowngrade
	}
	if !SameOrigin(orig, req.URL) {
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		req.Header.Del("Proxy-Authorization")
	}
	return nil
}

// SameOrigin compares scheme, host and effective port.
func SameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}
