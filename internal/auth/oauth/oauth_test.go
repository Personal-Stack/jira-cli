package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/personal-stack/jira-cli/internal/credentials"
)

type memStore struct {
	mu  sync.Mutex
	m   map[string]string
	src string
}

func (s *memStore) Get(ref string) (*credentials.Found, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[ref]
	if !ok {
		return nil, credentials.ErrNotFound
	}
	return &credentials.Found{Secret: credentials.NewSecret([]byte(v)), Source: s.src}, nil
}
func (s *memStore) Writer() (credentials.Writer, error) { return memWriter{s}, nil }

type memWriter struct{ s *memStore }

func (w memWriter) Name() string { return "keyring" }
func (w memWriter) Get(ref string) (credentials.Secret, bool, error) {
	v, ok := w.s.m[ref]
	return credentials.NewSecret([]byte(v)), ok, nil
}
func (w memWriter) Delete(ref string) error { delete(w.s.m, ref); return nil }
func (w memWriter) Set(ref string, v credentials.Secret) error {
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[ref] = v.Reveal()
	return nil
}

// fakeAS is a minimal OAuth 2.0 authorization server token endpoint.
type fakeAS struct {
	challenge string
	refreshes int
	lastGrant string
}

func (f *fakeAS) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.lastGrant = r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		switch f.lastGrant {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "token_type": "bearer", "expires_in": 3600})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "rt-1" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			f.refreshes++
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-refreshed", "refresh_token": "rt-1", "token_type": "bearer", "expires_in": 3600})
		}
	})
}

func freePort(t *testing.T) string {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	_, p, _ := net.SplitHostPort(l.Addr().String())
	return p
}

func TestLoginWithPKCEAndLoopback(t *testing.T) {
	as := &fakeAS{}
	srv := httptest.NewServer(as.handler(t))
	defer srv.Close()
	redirect := "http://127.0.0.1:" + freePort(t) + "/callback"
	conf := &oauth2.Config{ClientID: "cli", RedirectURL: redirect, Endpoint: oauth2.Endpoint{AuthURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token"}}

	opts := LoginOptions{PKCE: true, Out: io.Discard, Timeout: 10 * time.Second, OnAuthURL: func(u string) {
		pu, _ := url.Parse(u)
		q := pu.Query()
		as.challenge = q.Get("code_challenge")
		if q.Get("code_challenge_method") != "S256" {
			t.Error("PKCE method must be S256")
		}
		go func() {
			time.Sleep(50 * time.Millisecond)
			// Wrong state first: must be rejected.
			resp, err := http.Get(redirect + "?code=the-code&state=forged")
			if err == nil {
				resp.Body.Close()
			}
		}()
	}}
	_, err := Login(context.Background(), conf, nil, opts)
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("forged state must be rejected, got %v", err)
	}

	opts.OnAuthURL = func(u string) {
		pu, _ := url.Parse(u)
		as.challenge = pu.Query().Get("code_challenge")
		go func() {
			time.Sleep(50 * time.Millisecond)
			resp, err := http.Get(redirect + "?code=the-code&state=" + url.QueryEscape(pu.Query().Get("state")))
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	tok, err := Login(context.Background(), conf, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" {
		t.Fatalf("%+v", tok)
	}
}

func TestProviderRefreshAndPersist(t *testing.T) {
	as := &fakeAS{}
	srv := httptest.NewServer(as.handler(t))
	defer srv.Close()
	conf := &oauth2.Config{ClientID: "cli", Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token"}}
	expired, _ := EncodeToken(&oauth2.Token{AccessToken: "old", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Minute)})
	store := &memStore{m: map[string]string{"ref": expired.Reveal()}, src: "keyring"}
	p := New("ref", conf, srv.Client(), store)

	req, _ := http.NewRequest("GET", "https://jira", nil)
	if err := p.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "Bearer at-refreshed" || as.refreshes != 1 {
		t.Fatalf("expected refresh, got %q (%d)", req.Header.Get("Authorization"), as.refreshes)
	}
	if !strings.Contains(store.m["ref"], "at-refreshed") {
		t.Fatal("refreshed token not persisted to keyring")
	}
	// Forced refresh after a 401.
	if err := p.Refresh(context.Background()); err != nil || as.refreshes != 2 {
		t.Fatalf("forced refresh: %v %d", err, as.refreshes)
	}
}

func TestBareRefreshTokenFromFileIsNotPersisted(t *testing.T) {
	as := &fakeAS{}
	srv := httptest.NewServer(as.handler(t))
	defer srv.Close()
	conf := &oauth2.Config{ClientID: "cli", Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token"}}
	store := &memStore{m: map[string]string{"ref": "rt-1\n"}, src: "file:/run/secrets/ref"}
	p := New("ref", conf, srv.Client(), store)
	req, _ := http.NewRequest("GET", "https://jira", nil)
	if err := p.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if store.m["ref"] != "rt-1\n" {
		t.Fatal("must not write tokens back to file-sourced credentials")
	}
}

func TestRevokedRefreshToken(t *testing.T) {
	as := &fakeAS{}
	srv := httptest.NewServer(as.handler(t))
	defer srv.Close()
	conf := &oauth2.Config{ClientID: "cli", Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token"}}
	store := &memStore{m: map[string]string{"ref": "revoked"}, src: "env:X"}
	p := New("ref", conf, srv.Client(), store)
	req, _ := http.NewRequest("GET", "https://jira", nil)
	err := p.Apply(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "refresh") {
		t.Fatalf("expected refresh failure, got %v", err)
	}
	if strings.Contains(err.Error(), "revoked") {
		t.Fatal("refresh token value leaked into error")
	}
}
