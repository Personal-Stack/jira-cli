// Package auth defines the authentication provider abstraction. Providers
// only decorate requests; they never perform transport themselves (except the
// OAuth token endpoint, which uses the same centrally built HTTP client).
package auth

import (
	"context"
	"net/http"
	"time"
)

// Provider authenticates outgoing requests.
type Provider interface {
	// Method is "pat" or "oauth".
	Method() string
	// Apply adds credentials to req. It must not log or return the secret.
	Apply(ctx context.Context, req *http.Request) error
	// Status reports credential availability without contacting Jira.
	Status(ctx context.Context) Status
}

// Refresher is implemented by providers whose credentials can be renewed
// (OAuth). The API client calls Refresh once after a 401.
type Refresher interface {
	Refresh(ctx context.Context) error
}

// Status is a secret-free description of the credential state.
type Status struct {
	Method        string     `json:"method"`
	CredentialRef string     `json:"credential_ref"`
	Present       bool       `json:"credential_present"`
	Source        string     `json:"source,omitempty"`
	Username      string     `json:"username,omitempty"`
	Expiry        *time.Time `json:"expiry,omitempty"`
	Refreshable   bool       `json:"refreshable,omitempty"`
	Warnings      []string   `json:"warnings,omitempty"`
	Error         string     `json:"error,omitempty"`
}
