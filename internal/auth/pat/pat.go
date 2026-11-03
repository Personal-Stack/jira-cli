// Package pat implements Jira Data Center Personal Access Token
// authentication (Authorization: Bearer <token>). PATs are available in Jira
// Data Center 8.14 and later.
package pat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/personal-stack/jira-cli/internal/auth"
	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/credentials"
)

// Store resolves secrets by reference.
type Store interface {
	Get(ref string) (*credentials.Found, error)
}

// Provider is the PAT provider. The token is loaded lazily on first use so
// that offline commands (--dry-run, help, discovery) never touch secrets.
type Provider struct {
	Ref      string
	Username string
	Store    Store

	once  sync.Once
	found *credentials.Found
	err   error
}

// New returns a PAT provider.
func New(ref, username string, store Store) *Provider {
	return &Provider{Ref: ref, Username: username, Store: store}
}

func (p *Provider) Method() string { return "pat" }

func (p *Provider) load() (*credentials.Found, error) {
	p.once.Do(func() {
		p.found, p.err = p.Store.Get(p.Ref)
		if p.err == nil {
			p.err = Validate(p.found.Secret)
		}
	})
	return p.found, p.err
}

// Validate rejects values that would corrupt the Authorization header.
func Validate(s credentials.Secret) error {
	if s.Empty() {
		return errors.New("token is empty")
	}
	for _, c := range s.Bytes() {
		if c <= 0x20 || c == 0x7f {
			return errors.New("token contains whitespace or control characters")
		}
	}
	return nil
}

func (p *Provider) Apply(_ context.Context, req *http.Request) error {
	f, err := p.load()
	if err != nil {
		e := clierr.Wrap(clierr.LayerCredentials, "credential_unavailable", fmt.Sprintf("personal access token %q is not available", p.Ref), err)
		return e.WithHint("run `jira auth login --method pat`, mount the token file (JIRA_CREDENTIAL_FILE) or set " + credentials.EnvName(p.Ref))
	}
	req.Header.Set("Authorization", "Bearer "+f.Secret.Reveal())
	return nil
}

func (p *Provider) Status(context.Context) auth.Status {
	st := auth.Status{Method: "pat", CredentialRef: p.Ref, Username: p.Username}
	f, err := p.load()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Present, st.Source, st.Warnings = true, f.Source, f.Warnings
	return st
}
