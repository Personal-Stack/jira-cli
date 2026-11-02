// Package config loads, validates and edits the CLI configuration file.
// The file never contains secrets: credentials are referenced by name and
// resolved by internal/credentials.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// Environment variables.
const (
	EnvConfig   = "JIRA_CONFIG"
	EnvInstance = "JIRA_INSTANCE"
)

// Config is the root of config.yaml.
type Config struct {
	Version         int                  `yaml:"version" json:"version"`
	DefaultInstance string               `yaml:"default_instance,omitempty" json:"default_instance,omitempty"`
	Instances       map[string]*Instance `yaml:"instances" json:"instances"`

	path string
}

// Instance is one Jira deployment.
type Instance struct {
	Name          string        `yaml:"-" json:"name"`
	Deployment    string        `yaml:"deployment" json:"deployment"`
	BaseURL       string        `yaml:"base_url" json:"base_url"`
	Specification Specification `yaml:"specification,omitempty" json:"specification"`
	Auth          Auth          `yaml:"auth" json:"auth"`
	TLS           TLS           `yaml:"tls,omitempty" json:"tls"`
	Proxy         Proxy         `yaml:"proxy,omitempty" json:"proxy"`
	Timeouts      Timeouts      `yaml:"timeouts,omitempty" json:"timeouts"`
	Retry         Retry         `yaml:"retry,omitempty" json:"retry"`
	// ReadOnly blocks every operation not classified as read before any
	// network call. Recommended for first contact with a production instance.
	ReadOnly bool `yaml:"read_only,omitempty" json:"read_only"`
	// AllowHTTP permits a plain-http base_url. Unsafe; development only.
	AllowHTTP bool `yaml:"allow_insecure_http,omitempty" json:"allow_insecure_http,omitempty"`
}

// Specification selects a compiled-in profile.
type Specification struct {
	Profile string `yaml:"profile,omitempty" json:"profile,omitempty"`
}

// Auth configures the authentication provider.
type Auth struct {
	Type          string `yaml:"type" json:"type"` // pat | oauth
	Username      string `yaml:"username,omitempty" json:"username,omitempty"`
	CredentialRef string `yaml:"credential_ref" json:"credential_ref"`
	OAuth         *OAuth `yaml:"oauth,omitempty" json:"oauth,omitempty"`
}

// OAuth configures an OAuth 2.0 authorization-code client.
type OAuth struct {
	AuthorizationURL string   `yaml:"authorization_url" json:"authorization_url"`
	TokenURL         string   `yaml:"token_url" json:"token_url"`
	ClientID         string   `yaml:"client_id" json:"client_id"`
	ClientSecretRef  string   `yaml:"client_secret_ref,omitempty" json:"client_secret_ref,omitempty"`
	RedirectURI      string   `yaml:"redirect_uri" json:"redirect_uri"`
	Scopes           []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	PKCE             *bool    `yaml:"pkce,omitempty" json:"pkce,omitempty"` // default true
}

// UsePKCE reports whether PKCE is enabled (default true).
func (o *OAuth) UsePKCE() bool { return o.PKCE == nil || *o.PKCE }

// TLS configures transport security.
type TLS struct {
	// Verify defaults to true; false must be set explicitly and is unsafe.
	Verify     *bool  `yaml:"verify,omitempty" json:"verify,omitempty"`
	CABundle   string `yaml:"ca_bundle,omitempty" json:"ca_bundle,omitempty"`
	ClientCert string `yaml:"client_cert,omitempty" json:"client_cert,omitempty"`
	ClientKey  string `yaml:"client_key,omitempty" json:"client_key,omitempty"`
	MinVersion string `yaml:"min_version,omitempty" json:"min_version,omitempty"` // "1.2" (default) or "1.3"
	ServerName string `yaml:"server_name,omitempty" json:"server_name,omitempty"`
}

// VerifyEnabled reports whether certificate verification is on.
func (t TLS) VerifyEnabled() bool { return t.Verify == nil || *t.Verify }

// Proxy configures an HTTP(S) proxy. Empty URL means use HTTPS_PROXY/NO_PROXY.
type Proxy struct {
	URL      string `yaml:"url,omitempty" json:"url,omitempty"`
	Disabled bool   `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// Timeouts are Go duration strings.
type Timeouts struct {
	Connect string `yaml:"connect,omitempty" json:"connect,omitempty"`
	Request string `yaml:"request,omitempty" json:"request,omitempty"`
}

// Retry configures retries of idempotent requests.
type Retry struct {
	MaxAttempts int `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty"`
}

// Durations returns parsed timeouts with defaults (10s connect, 60s request).
func (t Timeouts) Durations() (connect, request time.Duration, err error) {
	connect, request = 10*time.Second, 60*time.Second
	if t.Connect != "" {
		if connect, err = time.ParseDuration(t.Connect); err != nil || connect <= 0 {
			return 0, 0, fmt.Errorf("timeouts.connect: invalid duration %q", t.Connect)
		}
	}
	if t.Request != "" {
		if request, err = time.ParseDuration(t.Request); err != nil || request <= 0 {
			return 0, 0, fmt.Errorf("timeouts.request: invalid duration %q", t.Request)
		}
	}
	return connect, request, nil
}

// DefaultPath returns the default config location.
func DefaultPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "jira-cli", "config.yaml")
	}
	return "config.yaml"
}

// ResolvePath applies flag > JIRA_CONFIG > default.
func ResolvePath(flag string) string {
	if flag != "" {
		return flag
	}
	if p := os.Getenv(EnvConfig); p != "" {
		return p
	}
	return DefaultPath()
}

// Load reads and validates the config file. Unknown keys are errors so that
// typos (and attempts to add e.g. a `token:` key) fail loudly.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b, path)
}

// Parse decodes and validates config bytes.
func Parse(b []byte, path string) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.path = path
	for name, in := range c.Instances {
		if in == nil {
			return nil, fmt.Errorf("instance %q is empty", name)
		}
		in.Name = name
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// Names returns instance names sorted.
func (c *Config) Names() []string {
	out := make([]string, 0, len(c.Instances))
	for n := range c.Instances {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Select applies the instance selection order: explicit flag, then
// JIRA_INSTANCE, then default_instance (or the only instance).
func (c *Config) Select(flag string) (*Instance, string, error) {
	name, source := flag, "flag"
	if name == "" {
		name, source = os.Getenv(EnvInstance), "env"
	}
	if name == "" {
		name, source = c.DefaultInstance, "default"
	}
	if name == "" && len(c.Instances) == 1 {
		name, source = c.Names()[0], "only-instance"
	}
	if name == "" {
		return nil, "", fmt.Errorf("no instance selected: use --instance, %s or default_instance", EnvInstance)
	}
	in, ok := c.Instances[name]
	if !ok {
		return nil, "", fmt.Errorf("instance %q not found (configured: %v)", name, c.Names())
	}
	return in, source, nil
}

// SetDefaultInstance edits default_instance in place, preserving comments
// and formatting of the rest of the file.
func SetDefaultInstance(path, name string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c, err := Parse(b, path)
	if err != nil {
		return err
	}
	if _, ok := c.Instances[name]; !ok {
		return fmt.Errorf("instance %q not found (configured: %v)", name, c.Names())
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	root := doc.Content[0]
	set := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "default_instance" {
			root.Content[i+1].Value = name
			set = true
		}
	}
	if !set {
		root.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "default_instance"},
			{Kind: yaml.ScalarNode, Value: name},
		}, root.Content...)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), st.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
