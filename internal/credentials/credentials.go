// Package credentials resolves secrets referenced by name (credential_ref)
// from, in order: an explicitly mounted file, a secrets directory (Docker /
// Kubernetes secrets), environment variables, and the OS keyring.
//
// Secrets are never accepted from command-line arguments, never written to
// temporary files and are re-read on every invocation, so rotating a mounted
// secret takes effect on the next command.
package credentials

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// Environment variables.
const (
	EnvCredentialFile = "JIRA_CREDENTIAL_FILE" // file holding the selected instance's credential
	EnvCredentialsDir = "JIRA_CREDENTIALS_DIR" // directory of <credential_ref> files (default /run/secrets)
	EnvNoKeyring      = "JIRA_NO_KEYRING"      // "1" disables the OS keyring
	envPrefix         = "JIRA_CREDENTIAL_"     // JIRA_CREDENTIAL_<REF>, e.g. JIRA_CREDENTIAL_JIRA_PRODUCTION
	keyringService    = "jira-cli"
	defaultSecretsDir = "/run/secrets"
	maxSecretSize     = 64 << 10
)

// ErrNotFound means no provider holds the reference.
var ErrNotFound = errors.New("credential not found")

// Secret wraps sensitive bytes. Every formatting path prints a placeholder.
type Secret struct{ b []byte }

// NewSecret wraps b.
func NewSecret(b []byte) Secret { return Secret{b: b} }

// Bytes returns the secret value. Call sites must not log it.
func (s Secret) Bytes() []byte { return s.b }

// Reveal returns the secret as a string. Call sites must not log it.
func (s Secret) Reveal() string { return string(s.b) }

// Empty reports whether the secret has no content.
func (s Secret) Empty() bool { return len(s.b) == 0 }

func (s Secret) String() string                { return "[REDACTED]" }
func (s Secret) GoString() string              { return "credentials.Secret{[REDACTED]}" }
func (s Secret) MarshalJSON() ([]byte, error)  { return []byte(`"[REDACTED]"`), nil }
func (s Secret) MarshalText() ([]byte, error)  { return []byte("[REDACTED]"), nil }
func (s Secret) Format(f fmt.State, verb rune) { _, _ = io.WriteString(f, "[REDACTED]") }

var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidRef reports whether ref is safe to use as a file name / env suffix.
func ValidRef(ref string) bool { return refRe.MatchString(ref) && !strings.Contains(ref, "..") }

// Provider is one secret backend.
type Provider interface {
	Name() string
	// Get returns (secret, true, nil) when found, (_, false, nil) when the
	// backend does not hold ref, and an error when the backend failed.
	Get(ref string) (Secret, bool, error)
}

// Writer is a backend that can store secrets (only the keyring).
type Writer interface {
	Provider
	Set(ref string, s Secret) error
	Delete(ref string) error
}

// Found describes where a secret came from (never its value).
type Found struct {
	Secret   Secret
	Source   string   // e.g. "keyring", "file:/run/secrets/jira-prod", "env:JIRA_CREDENTIAL_JIRA_PROD"
	Warnings []string // e.g. permissive file mode
}

// Chain queries providers in order.
type Chain struct{ Providers []Provider }

// Options configures the default chain.
type Options struct {
	// PrimaryRef is the selected instance's credential_ref; JIRA_CREDENTIAL_FILE
	// applies only to it.
	PrimaryRef string
	Getenv     func(string) string
}

// Default builds the standard chain from the environment.
func Default(o Options) *Chain {
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var ps []Provider
	if f := getenv(EnvCredentialFile); f != "" && o.PrimaryRef != "" {
		ps = append(ps, &FileProvider{Ref: o.PrimaryRef, Path: f})
	}
	dir := getenv(EnvCredentialsDir)
	if dir == "" {
		dir = defaultSecretsDir
	}
	ps = append(ps, &DirProvider{Dir: dir}, &EnvProvider{Getenv: getenv})
	if getenv(EnvNoKeyring) != "1" {
		ps = append(ps, &Keyring{})
	}
	return &Chain{Providers: ps}
}

// Get resolves ref.
func (c *Chain) Get(ref string) (*Found, error) {
	if !ValidRef(ref) {
		return nil, fmt.Errorf("invalid credential reference %q", ref)
	}
	var errs []error
	for _, p := range c.Providers {
		s, ok, err := p.Get(ref)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		if ok {
			f := &Found{Secret: s, Source: p.Name()}
			if fp, ok := p.(sourced); ok {
				f.Source, f.Warnings = fp.source(ref)
			}
			return f, nil
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: %q (%v)", ErrNotFound, ref, errors.Join(errs...))
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
}

// Writer returns the first writable provider.
func (c *Chain) Writer() (Writer, error) {
	for _, p := range c.Providers {
		if w, ok := p.(Writer); ok {
			return w, nil
		}
	}
	return nil, errors.New("no writable credential store (OS keyring disabled or unavailable); provide the credential via a mounted file or environment variable")
}

type sourced interface {
	source(ref string) (string, []string)
}

// FileProvider serves exactly one ref from one file (JIRA_CREDENTIAL_FILE).
type FileProvider struct {
	Ref, Path string
	warn      []string
}

func (f *FileProvider) Name() string { return "file" }

func (f *FileProvider) Get(ref string) (Secret, bool, error) {
	if ref != f.Ref {
		return Secret{}, false, nil
	}
	s, warn, err := ReadSecretFile(f.Path)
	f.warn = warn
	return s, err == nil, err
}

func (f *FileProvider) source(string) (string, []string) { return "file:" + f.Path, f.warn }

// DirProvider serves <Dir>/<ref> (Docker secrets: /run/secrets/<name>).
type DirProvider struct {
	Dir  string
	warn []string
}

func (d *DirProvider) Name() string { return "secrets-dir" }

func (d *DirProvider) Get(ref string) (Secret, bool, error) {
	p := filepath.Join(d.Dir, ref)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return Secret{}, false, nil
	}
	s, warn, err := ReadSecretFile(p)
	d.warn = warn
	return s, err == nil, err
}

func (d *DirProvider) source(ref string) (string, []string) {
	return "file:" + filepath.Join(d.Dir, ref), d.warn
}

// EnvProvider serves JIRA_CREDENTIAL_<REF>. Environment variables are visible
// to same-user processes; prefer mounted files where possible.
type EnvProvider struct{ Getenv func(string) string }

func (e *EnvProvider) Name() string { return "env" }

// EnvName returns the variable name for ref.
func EnvName(ref string) string {
	return envPrefix + strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'a' && r <= 'z' {
			return r - 32
		}
		return '_'
	}, ref)
}

func (e *EnvProvider) Get(ref string) (Secret, bool, error) {
	v := strings.TrimSpace(e.Getenv(EnvName(ref)))
	if v == "" {
		return Secret{}, false, nil
	}
	return NewSecret([]byte(v)), true, nil
}

func (e *EnvProvider) source(ref string) (string, []string) { return "env:" + EnvName(ref), nil }

// Keyring stores secrets in macOS Keychain, Windows Credential Manager or the
// Linux Secret Service (via go-keyring).
type Keyring struct{}

func (Keyring) Name() string { return "keyring" }

// keyring calls go over D-Bus/OS APIs that can block when no session bus is
// running (containers); bound them.
const keyringTimeout = 5 * time.Second

func withTimeout[T any](f func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() { v, err := f(); ch <- res{v, err} }()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(keyringTimeout):
		var zero T
		return zero, errors.New("keyring did not respond (no session keyring available?)")
	}
}

func (Keyring) Get(ref string) (Secret, bool, error) {
	v, err := withTimeout(func() (string, error) { return keyring.Get(keyringService, ref) })
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return Secret{}, false, nil
	case err != nil:
		return Secret{}, false, err
	}
	return NewSecret([]byte(v)), true, nil
}

func (Keyring) Set(ref string, s Secret) error {
	if !ValidRef(ref) {
		return fmt.Errorf("invalid credential reference %q", ref)
	}
	_, err := withTimeout(func() (struct{}, error) { return struct{}{}, keyring.Set(keyringService, ref, s.Reveal()) })
	return err
}

func (Keyring) Delete(ref string) error {
	_, err := withTimeout(func() (struct{}, error) { return struct{}{}, keyring.Delete(keyringService, ref) })
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// ReadSecretFile reads a mounted secret. It refuses non-regular files,
// world-writable files and oversized files, and warns about group/world
// readable files (Docker secrets are 0444 by default, so that is a warning,
// not an error). Trailing whitespace/newlines are trimmed.
func ReadSecretFile(path string) (Secret, []string, error) {
	st, err := os.Stat(path) // follows symlinks: Kubernetes secrets are symlinks
	if err != nil {
		return Secret{}, nil, err
	}
	if !st.Mode().IsRegular() {
		return Secret{}, nil, fmt.Errorf("%s is not a regular file", path)
	}
	if st.Size() > maxSecretSize {
		return Secret{}, nil, fmt.Errorf("%s is larger than %d bytes", path, maxSecretSize)
	}
	var warn []string
	if perm := st.Mode().Perm(); perm&0o002 != 0 {
		return Secret{}, nil, fmt.Errorf("%s is world-writable (mode %o); refusing to use it", path, perm)
	} else if perm&0o044 != 0 {
		warn = append(warn, fmt.Sprintf("%s is readable by group/others (mode %o)", path, perm))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Secret{}, nil, err
	}
	b = []byte(strings.TrimRight(string(b), " \t\r\n"))
	if len(b) == 0 {
		return Secret{}, nil, fmt.Errorf("%s is empty", path)
	}
	return NewSecret(b), warn, nil
}
