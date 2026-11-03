package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Validate checks structural rules that do not touch the filesystem.
func (c *Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("config: unsupported version %d (expected 1)", c.Version)
	}
	if len(c.Instances) == 0 {
		return errors.New("config: no instances defined")
	}
	if c.DefaultInstance != "" {
		if _, ok := c.Instances[c.DefaultInstance]; !ok {
			return fmt.Errorf("config: default_instance %q is not defined", c.DefaultInstance)
		}
	}
	var errs []error
	for _, n := range c.Names() {
		if err := c.Instances[n].Validate(); err != nil {
			errs = append(errs, fmt.Errorf("instance %q: %w", n, err))
		}
	}
	return errors.Join(errs...)
}

// Validate checks one instance.
func (in *Instance) Validate() error {
	var errs []error
	add := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	if in.Deployment != "datacenter" {
		add("deployment: %q is not supported (only datacenter)", in.Deployment)
	}
	u, err := url.Parse(in.BaseURL)
	switch {
	case err != nil || u.Host == "":
		add("base_url: invalid URL %q", in.BaseURL)
	case u.Scheme == "http" && !in.AllowHTTP:
		add("base_url: http is not allowed (credentials would be sent in clear text); use https or set allow_insecure_http: true for local development")
	case u.Scheme != "https" && u.Scheme != "http":
		add("base_url: unsupported scheme %q", u.Scheme)
	case u.User != nil:
		add("base_url: must not contain credentials")
	case u.RawQuery != "" || u.Fragment != "":
		add("base_url: must not contain a query or fragment")
	}
	switch in.Auth.Type {
	case "pat":
	case "oauth":
		if in.Auth.OAuth == nil {
			add("auth.oauth: required for auth.type oauth")
		} else if err := in.Auth.OAuth.validate(); err != nil {
			errs = append(errs, err)
		}
	case "basic":
		add("auth.type: basic authentication is not supported; use pat or oauth")
	default:
		add("auth.type: must be pat or oauth, got %q", in.Auth.Type)
	}
	if !refRe.MatchString(in.Auth.CredentialRef) {
		add("auth.credential_ref: must match %s", refRe)
	}
	if (in.TLS.ClientCert == "") != (in.TLS.ClientKey == "") {
		add("tls: client_cert and client_key must be set together")
	}
	switch in.TLS.MinVersion {
	case "", "1.2", "1.3":
	default:
		add("tls.min_version: must be 1.2 or 1.3")
	}
	if in.Proxy.URL != "" {
		p, err := url.Parse(in.Proxy.URL)
		if err != nil || p.Host == "" || (p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5") {
			add("proxy.url: invalid proxy URL")
		}
		if err == nil && p.User != nil {
			if _, hasPw := p.User.Password(); hasPw {
				add("proxy.url: must not contain a password; use HTTPS_PROXY from a secret instead")
			}
		}
	}
	if _, _, err := in.Timeouts.Durations(); err != nil {
		errs = append(errs, err)
	}
	if in.Retry.MaxAttempts < 0 || in.Retry.MaxAttempts > 10 {
		add("retry.max_attempts: must be between 0 and 10")
	}
	return errors.Join(errs...)
}

func (o *OAuth) validate() error {
	var errs []error
	for name, v := range map[string]string{"authorization_url": o.AuthorizationURL, "token_url": o.TokenURL} {
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			errs = append(errs, fmt.Errorf("auth.oauth.%s: must be an https URL", name))
		}
	}
	if o.ClientID == "" {
		errs = append(errs, errors.New("auth.oauth.client_id: required"))
	}
	if o.ClientSecretRef != "" && !refRe.MatchString(o.ClientSecretRef) {
		errs = append(errs, fmt.Errorf("auth.oauth.client_secret_ref: must match %s", refRe))
	}
	r, err := url.Parse(o.RedirectURI)
	if err != nil || r.Scheme != "http" || (r.Hostname() != "127.0.0.1" && r.Hostname() != "localhost" && r.Hostname() != "::1") || r.Port() == "" {
		errs = append(errs, errors.New("auth.oauth.redirect_uri: must be a loopback URL with an explicit port, e.g. http://127.0.0.1:8765/callback"))
	}
	return errors.Join(errs...)
}

// CheckFiles validates referenced files (CA bundle, client certificate).
// It is separate from Validate because it touches the filesystem.
func (in *Instance) CheckFiles() error {
	var errs []error
	if in.TLS.CABundle != "" {
		b, err := os.ReadFile(in.TLS.CABundle)
		if err != nil {
			errs = append(errs, fmt.Errorf("tls.ca_bundle: %w", err))
		} else if _, _, err := ParseCABundle(b, in.TLS.CABundle); err != nil {
			errs = append(errs, fmt.Errorf("tls.ca_bundle: %w", err))
		}
	}
	if in.TLS.ClientCert != "" {
		if _, err := tls.LoadX509KeyPair(in.TLS.ClientCert, in.TLS.ClientKey); err != nil {
			errs = append(errs, fmt.Errorf("tls.client_cert/client_key: %w", err))
		}
		if st, err := os.Stat(in.TLS.ClientKey); err == nil && st.Mode().Perm()&0o077 != 0 {
			errs = append(errs, fmt.Errorf("tls.client_key: %s is accessible by group/others (mode %o); chmod 600", in.TLS.ClientKey, st.Mode().Perm()))
		}
	}
	return errors.Join(errs...)
}

// Redacted returns a copy safe to print. The config holds no secrets by
// design; this strips proxy userinfo defensively.
func (in *Instance) Redacted() Instance {
	cp := *in
	if u, err := url.Parse(cp.Proxy.URL); err == nil && u.User != nil {
		u.User = url.User("REDACTED")
		cp.Proxy.URL = u.String()
	}
	if cp.Auth.OAuth != nil {
		o := *cp.Auth.OAuth
		cp.Auth.OAuth = &o
	}
	return cp
}

// IsHTTPS reports whether base_url uses https.
func (in *Instance) IsHTTPS() bool { return strings.HasPrefix(strings.ToLower(in.BaseURL), "https://") }
