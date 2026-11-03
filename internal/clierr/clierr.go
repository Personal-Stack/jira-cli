// Package clierr defines the structured, machine-readable error model. Every
// error that reaches the user identifies the failing layer.
package clierr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Layer identifies where a failure happened.
type Layer string

const (
	LayerUsage         Layer = "usage"
	LayerConfig        Layer = "configuration"
	LayerCredentials   Layer = "credentials"
	LayerDNS           Layer = "dns"
	LayerTCP           Layer = "tcp"
	LayerProxy         Layer = "proxy"
	LayerTLS           Layer = "tls"
	LayerCertificate   Layer = "tls_certificate"
	LayerTimeout       Layer = "timeout"
	LayerTransport     Layer = "http_transport"
	LayerAuth          Layer = "authentication"
	LayerAuthorization Layer = "authorization"
	LayerAPI           Layer = "jira_api"
	LayerResponse      Layer = "response_parsing"
	LayerSafety        Layer = "safety"
	LayerInternal      Layer = "internal"
	// LayerInterrupted: the user pressed Ctrl+C (or the process got SIGTERM).
	LayerInterrupted Layer = "interrupted"
)

// Error is the structured error type printed by the CLI.
type Error struct {
	Layer      Layer          `json:"layer"`
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	HTTPStatus int            `json:"http_status,omitempty"`
	Hint       string         `json:"hint,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
	Err        error          `json:"-"`
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", e.Layer, e.Message)
	if e.HTTPStatus != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.HTTPStatus)
	}
	if e.Err != nil && !strings.Contains(e.Message, e.Err.Error()) {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// ExitInterrupted is the conventional exit code after SIGINT (128 + 2).
const ExitInterrupted = 130

// Interrupted is the error for a run stopped by Ctrl+C.
func Interrupted() *Error {
	return New(LayerInterrupted, "interrupted", "interrupted")
}

// ExitCode maps layers to stable process exit codes.
func (e *Error) ExitCode() int {
	switch e.Layer {
	case LayerUsage:
		return 2
	case LayerConfig, LayerCredentials:
		return 3
	case LayerAuth:
		return 4
	case LayerAuthorization:
		return 5
	case LayerAPI, LayerResponse:
		if e.HTTPStatus == 404 {
			return 6
		}
		if e.HTTPStatus == 429 {
			return 10
		}
		return 7
	case LayerDNS, LayerTCP, LayerProxy, LayerTLS, LayerCertificate, LayerTimeout, LayerTransport:
		return 8
	case LayerSafety:
		return 9
	case LayerInterrupted:
		return ExitInterrupted
	}
	return 1
}

// New builds an Error.
func New(layer Layer, code, msg string) *Error { return &Error{Layer: layer, Code: code, Message: msg} }

// Wrap builds an Error around a cause.
func Wrap(layer Layer, code, msg string, err error) *Error {
	return &Error{Layer: layer, Code: code, Message: msg, Err: err}
}

// WithHint sets a remediation hint.
func (e *Error) WithHint(h string) *Error { e.Hint = h; return e }

// As returns err as *Error, classifying unknown errors.
func As(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Classify(err)
}

// Classify maps network/TLS errors to layers. The original error text is
// kept as the cause; it never contains credentials (requests carry them only
// in headers, which net/http does not include in errors).
func Classify(err error) *Error {
	var (
		dnsErr      *net.DNSError
		opErr       *net.OpError
		unknownCA   x509.UnknownAuthorityError
		hostErr     x509.HostnameError
		invalidCert x509.CertificateInvalidError
		verifyErr   *tls.CertificateVerificationError
		recordErr   tls.RecordHeaderError
		urlErr      *url.Error
	)
	var proxyRefused interface{ ProxyStatus() int }
	switch {
	case errors.Is(err, context.Canceled):
		return Wrap(LayerInterrupted, "interrupted", "interrupted", err)
	case errors.As(err, &proxyRefused):
		e := Wrap(LayerProxy, "proxy_connect_refused", "the proxy refused to open a tunnel to the Jira host", err)
		e.HTTPStatus = proxyRefused.ProxyStatus()
		if e.HTTPStatus == 407 {
			return e.WithHint("the proxy requires authentication: set credentials in HTTPS_PROXY (from a secret)")
		}
		return e.WithHint("the Jira host is probably not on the proxy allow-list; check the proxy policy or NO_PROXY")
	case errors.Is(err, os.ErrDeadlineExceeded) || isTimeout(err):
		return Wrap(LayerTimeout, "timeout", "request timed out", err).WithHint("increase --timeout or check network reachability")
	case errors.As(err, &dnsErr):
		return Wrap(LayerDNS, "dns_resolution_failed", "cannot resolve host "+dnsErr.Name, err).WithHint("check base_url, internal DNS or VPN")
	case errors.As(err, &unknownCA), errors.As(err, &verifyErr) && errors.As(verifyErr.Err, &unknownCA):
		return Wrap(LayerCertificate, "unknown_certificate_authority", "server certificate is signed by an unknown authority", err).WithHint("set tls.ca_bundle to your corporate CA bundle")
	case errors.As(err, &hostErr):
		return Wrap(LayerCertificate, "certificate_hostname_mismatch", "server certificate does not match the host name", err)
	case errors.As(err, &invalidCert):
		return Wrap(LayerCertificate, "certificate_invalid", "server certificate is invalid", err)
	case errors.As(err, &verifyErr):
		return Wrap(LayerCertificate, "certificate_verification_failed", "server certificate verification failed", err)
	case errors.As(err, &recordErr):
		return Wrap(LayerTLS, "not_tls", "server did not respond with TLS", err).WithHint("check the base_url scheme and port")
	case errors.As(err, &opErr) && opErr.Op == "proxyconnect":
		return Wrap(LayerProxy, "proxy_connect_failed", "cannot connect through the proxy", err)
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return Wrap(LayerTCP, "connection_failed", "cannot connect to "+addr(opErr), err).WithHint("check firewall, port and network access")
	case strings.Contains(err.Error(), "tls:"):
		return Wrap(LayerTLS, "tls_handshake_failed", "TLS handshake failed", err)
	case errors.As(err, &urlErr) && strings.Contains(urlErr.Err.Error(), "proxy"):
		return Wrap(LayerProxy, "proxy_error", "proxy error", err)
	case errors.As(err, &urlErr):
		return Wrap(LayerTransport, "http_transport_error", "HTTP request failed", err)
	}
	return Wrap(LayerInternal, "error", err.Error(), nil)
}

func addr(o *net.OpError) string {
	if o.Addr != nil {
		return o.Addr.String()
	}
	return "server"
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}
