package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/testpki"
)

func tlsServer(t *testing.T, p *testpki.PKI, clientCAs bool, h http.Handler) *httptest.Server {
	srv := httptest.NewUnstartedServer(h)
	cert, _, _ := p.Issue(t, "server", "127.0.0.1")
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	if clientCAs {
		srv.TLS.ClientCAs = p.Pool()
		srv.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func instance(url string, tc config.TLS) *config.Instance {
	return &config.Instance{BaseURL: url, TLS: tc, Proxy: config.Proxy{Disabled: true}}
}

func get(t *testing.T, c *http.Client, url string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	return c.Do(req)
}

func TestCustomCA(t *testing.T) {
	p := testpki.New(t)
	srv := tlsServer(t, p, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }))

	// Without the CA: unknown authority, classified as a certificate error.
	c, err := New(instance(srv.URL, config.TLS{}), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = get(t, c, srv.URL)
	if err == nil || clierr.Classify(err).Layer != clierr.LayerCertificate {
		t.Fatalf("expected certificate error, got %v", err)
	}

	ca := testpki.WriteFile(t, t.TempDir(), "ca.pem", p.CAPEM)
	c, _ = New(instance(srv.URL, config.TLS{CABundle: ca}), 0)
	resp, err := get(t, c, srv.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("with CA: %v", err)
	}
}

func TestMutualTLS(t *testing.T) {
	p := testpki.New(t)
	srv := tlsServer(t, p, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	dir := t.TempDir()
	ca := testpki.WriteFile(t, dir, "ca.pem", p.CAPEM)

	c, _ := New(instance(srv.URL, config.TLS{CABundle: ca}), 0)
	if _, err := get(t, c, srv.URL); err == nil {
		t.Fatal("expected failure without client certificate")
	}

	_, certPEM, keyPEM := p.Issue(t, "cli-client")
	cert := testpki.WriteFile(t, dir, "c.pem", certPEM)
	key := testpki.WriteFile(t, dir, "k.pem", keyPEM)
	c, err := New(instance(srv.URL, config.TLS{CABundle: ca, ClientCert: cert, ClientKey: key}), 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := get(t, c, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "cli-client" {
		t.Fatalf("server saw %q", b)
	}
}

func TestInsecureIsOptIn(t *testing.T) {
	tc, err := TLSConfig(config.TLS{})
	if err != nil || tc.InsecureSkipVerify || tc.MinVersion < tls.VersionTLS12 {
		t.Fatalf("unsafe defaults: %+v %v", tc, err)
	}
	f := false
	tc, _ = TLSConfig(config.TLS{Verify: &f})
	if !tc.InsecureSkipVerify {
		t.Fatal("explicit verify:false must disable verification")
	}
}

func TestRedirectStripsCredentialsAcrossOrigins(t *testing.T) {
	var gotAuth string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotAuth = r.Header.Get("Authorization") }))
	defer other.Close()
	var sameAuth string
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, other.URL+"/x", http.StatusFound)
		case "/same":
			http.Redirect(w, r, origin.URL+"/final", http.StatusFound)
		case "/final":
			sameAuth = r.Header.Get("Authorization")
		}
	}))
	defer origin.Close()
	c := &http.Client{CheckRedirect: CheckRedirect}
	for _, path := range []string{"/cross", "/same"} {
		req, _ := http.NewRequest("GET", origin.URL+path, nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		if _, err := c.Do(req); err != nil {
			t.Fatal(err)
		}
	}
	if gotAuth != "" {
		t.Fatalf("Authorization forwarded to other origin: %q", gotAuth)
	}
	if sameAuth != "Bearer secret-token" {
		t.Fatalf("Authorization should be kept on same origin, got %q", sameAuth)
	}
}

func TestRedirectRefusesDowngrade(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer plain.Close()
	p := testpki.New(t)
	srv := tlsServer(t, p, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	ca := testpki.WriteFile(t, t.TempDir(), "ca.pem", p.CAPEM)
	c, _ := New(instance(srv.URL, config.TLS{CABundle: ca}), 0)
	_, err := get(t, c, srv.URL)
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("expected downgrade refusal, got %v", err)
	}
}

func TestRetryPolicy(t *testing.T) {
	resp := func(code int, ra string) *http.Response {
		h := http.Header{}
		if ra != "" {
			h.Set("Retry-After", ra)
		}
		return &http.Response{StatusCode: code, Header: h}
	}
	idem := RetryPolicy{Idempotent: true}
	post := RetryPolicy{Idempotent: false}
	if ok, _ := post.Next(1, resp(503, ""), nil); ok {
		t.Error("non-idempotent must not retry 503")
	}
	if ok, _ := post.Next(1, nil, errors.New("connection reset")); ok {
		t.Error("non-idempotent must not retry network errors")
	}
	if ok, d := post.Next(1, resp(429, "2"), nil); !ok || d.Seconds() != 2 {
		t.Errorf("429 should retry after Retry-After: %v %v", ok, d)
	}
	if ok, _ := idem.Next(1, resp(429, "3600"), nil); ok {
		t.Error("long Retry-After should not block")
	}
	if ok, _ := idem.Next(1, resp(503, ""), nil); !ok {
		t.Error("idempotent 503 should retry")
	}
	if ok, _ := idem.Next(3, resp(503, ""), nil); ok {
		t.Error("must stop at MaxAttempts")
	}
	if ok, _ := idem.Next(1, resp(500, ""), nil); ok {
		t.Error("500 is not retried")
	}
	if ok, _ := idem.Next(1, nil, context.Canceled); ok {
		t.Error("cancellation is not retried")
	}
	if !strings.Contains(ErrDowngrade.Error(), "https") {
		t.Fail()
	}
}

func TestProxyConnectRefusedIsClassified(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer proxy.Close()
	in := &config.Instance{BaseURL: "https://jira.example.internal", Proxy: config.Proxy{URL: proxy.URL}}
	c, err := New(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = get(t, c, "https://jira.example.internal/rest/api/2/serverInfo")
	e := clierr.Classify(err)
	if e.Layer != clierr.LayerProxy || e.Code != "proxy_connect_refused" || e.HTTPStatus != 403 {
		t.Fatalf("got %+v (%v)", e, err)
	}
}
