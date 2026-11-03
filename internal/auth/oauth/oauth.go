// Package oauth implements an OAuth 2.0 authorization-code provider (with
// PKCE) for Jira Data Center's OAuth 2.0 provider ("incoming application
// links"). Endpoints, client ID and scopes are fully configurable because
// Data Center installations differ; nothing here assumes Jira Cloud.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/personal-stack/jira-cli/internal/auth"
	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/credentials"
)

// Store resolves and (optionally) persists secrets.
type Store interface {
	Get(ref string) (*credentials.Found, error)
	Writer() (credentials.Writer, error)
}

// Provider applies OAuth access tokens, refreshing them when expired.
type Provider struct {
	Ref  string
	Conf *oauth2.Config
	// HTTP is the centrally configured client (TLS, proxy) used for the
	// token endpoint.
	HTTP  *http.Client
	Store Store

	mu       sync.Mutex
	loaded   bool
	loadErr  error
	tok      *oauth2.Token
	source   string
	store    string
	warnings []string
}

// Config builds the oauth2 configuration. The client secret, if any, is
// read from the credential store, never from the config file.
func Config(c *config.OAuth, store Store) (*oauth2.Config, error) {
	conf := &oauth2.Config{
		ClientID:    c.ClientID,
		RedirectURL: c.RedirectURI,
		Scopes:      c.Scopes,
		Endpoint:    oauth2.Endpoint{AuthURL: c.AuthorizationURL, TokenURL: c.TokenURL, AuthStyle: oauth2.AuthStyleAutoDetect},
	}
	if c.ClientSecretRef != "" {
		f, err := store.Get(c.ClientSecretRef)
		if err != nil {
			return nil, clierr.Wrap(clierr.LayerCredentials, "client_secret_unavailable", "OAuth client secret is not available", err)
		}
		conf.ClientSecret = f.Secret.Reveal()
	}
	return conf, nil
}

// New returns a provider.
func New(ref string, conf *oauth2.Config, httpClient *http.Client, store Store) *Provider {
	return &Provider{Ref: ref, Conf: conf, HTTP: httpClient, Store: store}
}

func (p *Provider) Method() string { return "oauth" }

// ParseToken accepts either a stored token JSON document or a bare refresh
// token (convenient for mounting into headless containers).
func ParseToken(s credentials.Secret) (*oauth2.Token, error) {
	raw := strings.TrimSpace(s.Reveal())
	if strings.HasPrefix(raw, "{") {
		var t oauth2.Token
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			return nil, errors.New("stored OAuth token is not valid JSON")
		}
		if t.AccessToken == "" && t.RefreshToken == "" {
			return nil, errors.New("stored OAuth token has neither access_token nor refresh_token")
		}
		return &t, nil
	}
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return nil, errors.New("invalid refresh token")
	}
	return &oauth2.Token{RefreshToken: raw}, nil
}

// EncodeToken serialises a token for storage.
func EncodeToken(t *oauth2.Token) (credentials.Secret, error) {
	b, err := json.Marshal(struct {
		AccessToken  string    `json:"access_token"`
		TokenType    string    `json:"token_type,omitempty"`
		RefreshToken string    `json:"refresh_token,omitempty"`
		Expiry       time.Time `json:"expiry,omitzero"`
	}{t.AccessToken, t.TokenType, t.RefreshToken, t.Expiry})
	return credentials.NewSecret(b), err
}

func (p *Provider) load() error {
	if p.loaded {
		return p.loadErr
	}
	p.loaded = true
	f, err := p.Store.Get(p.Ref)
	if err != nil {
		p.loadErr = err
		return err
	}
	p.source, p.store, p.warnings = f.Source, f.Store, f.Warnings
	p.tok, p.loadErr = ParseToken(f.Secret)
	return p.loadErr
}

func (p *Provider) ctx(ctx context.Context) context.Context {
	if p.HTTP != nil {
		return context.WithValue(ctx, oauth2.HTTPClient, p.HTTP)
	}
	return ctx
}

// token returns a valid access token, refreshing it if needed (or forced).
func (p *Provider) token(ctx context.Context, force bool) (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return nil, clierr.Wrap(clierr.LayerCredentials, "credential_unavailable", fmt.Sprintf("OAuth token %q is not available", p.Ref), err).
			WithHint("run `jira auth login --method oauth` (or mount a refresh token via JIRA_CREDENTIAL_FILE)")
	}
	cur := p.tok
	if force {
		if cur.RefreshToken == "" {
			return nil, clierr.New(clierr.LayerAuth, "no_refresh_token", "access token rejected and no refresh token is available").
				WithHint("run `jira auth login --method oauth` again")
		}
		cp := *cur
		cp.AccessToken, cp.Expiry = "", time.Unix(1, 0)
		cur = &cp
	}
	nt, err := p.Conf.TokenSource(p.ctx(ctx), cur).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			return nil, clierr.Wrap(clierr.LayerAuth, "token_refresh_failed", "OAuth token refresh was rejected by the token endpoint", errors.New(re.ErrorCode)).
				WithHint("the refresh token may be expired or revoked; run `jira auth login --method oauth`")
		}
		return nil, clierr.Classify(err)
	}
	if nt.AccessToken != p.tok.AccessToken {
		p.tok = nt
		p.persist(nt)
	}
	return nt, nil
}

// persist writes refreshed tokens back to the store they came from, if that
// store is writable (keyring, or the opt-in file store). Tokens from mounted
// secret files or env vars are kept in memory only.
func (p *Provider) persist(t *oauth2.Token) {
	if p.store != "keyring" && p.store != "file-store" {
		return
	}
	w, err := p.Store.Writer()
	if err != nil {
		return
	}
	if s, err := EncodeToken(t); err == nil {
		_ = w.Set(p.Ref, s)
	}
}

func (p *Provider) Apply(ctx context.Context, req *http.Request) error {
	t, err := p.token(ctx, false)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	return nil
}

// Refresh forces a refresh (called by the API client after a 401).
func (p *Provider) Refresh(ctx context.Context) error {
	_, err := p.token(ctx, true)
	return err
}

func (p *Provider) Status(context.Context) auth.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := auth.Status{Method: "oauth", CredentialRef: p.Ref}
	if err := p.load(); err != nil {
		st.Error = err.Error()
		return st
	}
	st.Present, st.Source, st.Warnings = true, p.source, p.warnings
	st.Refreshable = p.tok.RefreshToken != ""
	if !p.tok.Expiry.IsZero() {
		e := p.tok.Expiry
		st.Expiry = &e
	}
	return st
}
