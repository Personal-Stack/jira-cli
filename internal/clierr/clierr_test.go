package clierr

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"
)

type proxyErr struct{ code int }

func (p proxyErr) Error() string    { return "proxy refused" }
func (p proxyErr) ProxyStatus() int { return p.code }

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }

func TestClassify(t *testing.T) {
	wrap := func(e error) error { return &url.Error{Op: "Get", URL: "https://jira", Err: e} }
	cases := []struct {
		err   error
		layer Layer
		code  string
	}{
		{wrap(&net.DNSError{Name: "jira.invalid", Err: "no such host"}), LayerDNS, "dns_resolution_failed"},
		{wrap(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}), LayerTCP, "connection_failed"},
		{wrap(&net.OpError{Op: "proxyconnect", Net: "tcp", Err: errors.New("refused")}), LayerProxy, "proxy_connect_failed"},
		{wrap(proxyErr{403}), LayerProxy, "proxy_connect_refused"},
		{wrap(x509.UnknownAuthorityError{}), LayerCertificate, "unknown_certificate_authority"},
		{wrap(x509.HostnameError{Certificate: &x509.Certificate{}, Host: "x"}), LayerCertificate, "certificate_hostname_mismatch"},
		{wrap(timeoutErr{}), LayerTimeout, "timeout"},
		{wrap(os.ErrDeadlineExceeded), LayerTimeout, "timeout"},
		{wrap(errors.New("tls: handshake failure")), LayerTLS, "tls_handshake_failed"},
		{wrap(errors.New("EOF")), LayerTransport, "http_transport_error"},
		{context.Canceled, LayerInternal, "error"},
	}
	for _, c := range cases {
		e := Classify(c.err)
		if e.Layer != c.layer || e.Code != c.code {
			t.Errorf("%v: got %s/%s, want %s/%s", c.err, e.Layer, e.Code, c.layer, c.code)
		}
	}
	if e := Classify(wrap(proxyErr{407})); e.HTTPStatus != 407 || e.Hint == "" {
		t.Errorf("407: %+v", e)
	}
}

func TestExitCodesAndAs(t *testing.T) {
	want := map[Layer]int{LayerUsage: 2, LayerConfig: 3, LayerCredentials: 3, LayerAuth: 4, LayerAuthorization: 5,
		LayerDNS: 8, LayerProxy: 8, LayerTLS: 8, LayerSafety: 9, LayerInternal: 1}
	for l, code := range want {
		if got := New(l, "x", "y").ExitCode(); got != code {
			t.Errorf("%s: exit %d want %d", l, got, code)
		}
	}
	api := &Error{Layer: LayerAPI, HTTPStatus: 404}
	if api.ExitCode() != 6 {
		t.Error("404")
	}
	api.HTTPStatus = 429
	if api.ExitCode() != 10 {
		t.Error("429")
	}
	wrapped := fmt.Errorf("context: %w", New(LayerSafety, "read_only_mode", "no"))
	if As(wrapped).Code != "read_only_mode" {
		t.Error("As must unwrap")
	}
}
