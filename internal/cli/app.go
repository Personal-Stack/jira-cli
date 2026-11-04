// Package cli wires configuration, authentication, transport, generated
// operations and output into the Cobra command tree.
package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/personal-stack/jira-cli/internal/api"
	"github.com/personal-stack/jira-cli/internal/auth"
	"github.com/personal-stack/jira-cli/internal/auth/oauth"
	"github.com/personal-stack/jira-cli/internal/auth/pat"
	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/credentials"
	"github.com/personal-stack/jira-cli/internal/logging"
	"github.com/personal-stack/jira-cli/internal/operation"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/transport"
)

// EnvReadOnly forces read-only mode for every instance when set to "1".
const EnvReadOnly = "JIRA_READ_ONLY"

// App holds global flags and lazily initialised state for one invocation.
type App struct {
	Version  string
	Profiles map[string]*operation.Profile
	Default  string

	In       io.Reader
	Out, Err io.Writer
	Getenv   func(string) string
	// IsTerminal reports whether interactive prompts are possible.
	IsTerminal func() bool

	// global flags
	instance, configPath, outputFlag string
	verbose, quiet, noColor          bool
	dryRun, readOnly, yes            bool
	timeout                          time.Duration

	logLevel, logFormat, logFile string

	cfg       *config.Config
	cfgErr    error
	cfgLoaded bool
	log       *slog.Logger
	logCloser io.Closer
	runID     string
	prompts   *prompter // shared by every question of a run
}

// NewApp returns an App bound to the process environment.
func NewApp(version string, profiles map[string]*operation.Profile, def string) *App {
	return &App{
		Version: version, Profiles: profiles, Default: def,
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Getenv: os.Getenv,
		IsTerminal: func() bool {
			return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
		},
	}
}

// Format returns the selected output format.
func (a *App) Format() (output.Format, error) {
	f, err := output.Parse(a.outputFlag)
	if err != nil {
		return "", clierr.Wrap(clierr.LayerUsage, "invalid_output", err.Error(), nil)
	}
	return f, nil
}

// RunID identifies this invocation in every log line.
func (a *App) RunID() string {
	if a.runID == "" {
		a.runID = logging.NewID()
	}
	return a.runID
}

// logOptions resolves logging settings: flags > JIRA_LOG_* env >
// --verbose/--quiet > defaults (warn, text, stderr).
func (a *App) logOptions() logging.Options {
	o := logging.Options{
		Level:  firstNonEmpty(a.logLevel, a.Getenv(logging.EnvLevel)),
		Format: firstNonEmpty(a.logFormat, a.Getenv(logging.EnvFormat)),
		File:   firstNonEmpty(a.logFile, a.Getenv(logging.EnvFile)),
		Attrs:  []slog.Attr{slog.String("run_id", a.RunID())},
	}
	if o.Level == "" {
		switch {
		case a.verbose:
			o.Level = "debug"
		case a.quiet:
			o.Level = "error"
		}
	}
	return o
}

// initLog builds the logger after flags are parsed.
func (a *App) initLog() error {
	l, c, err := logging.New(a.Err, a.logOptions())
	if err != nil {
		return clierr.Wrap(clierr.LayerUsage, "invalid_logging", err.Error(), nil)
	}
	a.closeLog()
	a.log, a.logCloser = l, c
	return nil
}

func (a *App) closeLog() {
	if a.logCloser != nil {
		_ = a.logCloser.Close()
		a.logCloser = nil
	}
}

// Log returns the structured logger. All records pass through the
// redacting handler (internal/logging); call sites still must not log
// secrets deliberately.
func (a *App) Log() *slog.Logger {
	if a.log == nil {
		if err := a.initLog(); err != nil {
			a.log, _, _ = logging.New(a.Err, logging.Options{Attrs: []slog.Attr{slog.String("run_id", a.RunID())}})
		}
	}
	return a.log
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// ConfigPath returns the resolved config path.
func (a *App) ConfigPath() string {
	if a.configPath != "" {
		return a.configPath
	}
	if p := a.Getenv(config.EnvConfig); p != "" {
		return p
	}
	return config.DefaultPath()
}

// Config loads the configuration once.
func (a *App) Config() (*config.Config, error) {
	if !a.cfgLoaded {
		a.cfgLoaded = true
		p := a.ConfigPath()
		a.cfg, a.cfgErr = config.Load(p)
		if a.cfgErr == nil && a.log != nil {
			a.log.Debug("config loaded", "path", p, "instances", len(a.cfg.Instances))
		}
		if a.cfgErr != nil {
			e := clierr.Wrap(clierr.LayerConfig, "config_invalid", "cannot load configuration "+p, a.cfgErr)
			if errors.Is(a.cfgErr, os.ErrNotExist) {
				e.Code = "config_not_found"
				e.Hint = "create it with 'jira config init' (or 'jira setup'), or point " + config.EnvConfig + " / --config at an existing file"
			}
			a.cfgErr = e
		}
	}
	return a.cfg, a.cfgErr
}

// Instance returns the selected instance.
func (a *App) Instance() (*config.Instance, string, error) {
	c, err := a.Config()
	if err != nil {
		return nil, "", err
	}
	name := a.instance
	if name == "" {
		name = a.Getenv(config.EnvInstance)
	}
	// config.Select reads JIRA_INSTANCE via os.Getenv; pass the resolved
	// name explicitly so tests can inject the environment.
	in, src, err := c.Select(name)
	if err != nil {
		return nil, "", clierr.Wrap(clierr.LayerConfig, "instance_not_found", err.Error(), nil)
	}
	if a.instance == "" && name != "" {
		src = "env"
	}
	if a.log != nil {
		a.log.Debug("instance selected", "instance", in.Name, "selected_by", src, "deployment", in.Deployment, "auth_method", in.Auth.Type, "read_only", a.ReadOnly(in))
	}
	return in, src, nil
}

// ProfileFor returns the compiled-in profile for an instance (or the default).
func (a *App) ProfileFor(in *config.Instance) (*operation.Profile, error) {
	name := a.Default
	if in != nil && in.Specification.Profile != "" {
		name = in.Specification.Profile
	}
	p, ok := a.Profiles[name]
	if !ok {
		names := make([]string, 0, len(a.Profiles))
		for n := range a.Profiles {
			names = append(names, n)
		}
		return nil, clierr.New(clierr.LayerConfig, "unknown_profile", fmt.Sprintf("specification profile %q is not compiled into this binary (available: %v)", name, names)).
			WithHint("profiles are added at build time via specs/manifest.yaml and `make generate`")
	}
	return p, nil
}

// ReadOnly resolves read-only mode: flag, env or instance config.
func (a *App) ReadOnly(in *config.Instance) bool {
	return a.readOnly || a.Getenv(EnvReadOnly) == "1" || (in != nil && in.ReadOnly)
}

// Store returns the credential chain for an instance.
func (a *App) Store(in *config.Instance) *credentials.Chain {
	c := credentials.Default(credentials.Options{PrimaryRef: in.Auth.CredentialRef, Getenv: a.Getenv})
	c.Log = a.Log().With("instance", in.Name)
	return c
}

// Client builds the API client (auth provider + transport) for an instance.
func (a *App) Client(in *config.Instance, p *operation.Profile) (*api.Client, error) {
	if !in.TLS.VerifyEnabled() {
		a.Log().Warn("TLS certificate verification is DISABLED for this instance (tls.verify: false); traffic can be intercepted", "instance", in.Name)
	}
	if !in.IsHTTPS() {
		a.Log().Warn("instance uses plain HTTP (allow_insecure_http); credentials are sent unencrypted", "instance", in.Name)
	}
	hc, err := transport.New(in, a.timeout)
	if err != nil {
		return nil, clierr.Wrap(clierr.LayerConfig, "transport_config", "invalid transport configuration", err)
	}
	ap, err := a.AuthProvider(in, hc)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(strings.TrimRight(in.BaseURL, "/"))
	if err != nil {
		return nil, clierr.Wrap(clierr.LayerConfig, "invalid_base_url", "invalid base_url", err)
	}
	return &api.Client{
		HTTP: hc, BaseURL: base, BasePath: p.BasePath, Auth: ap, Log: a.Log().With("instance", in.Name),
		MaxAttempts: in.Retry.MaxAttempts, UserAgent: "jira-cli/" + a.Version,
	}, nil
}

// AuthProvider builds the configured provider. Credentials are loaded lazily.
func (a *App) AuthProvider(in *config.Instance, hc *http.Client) (auth.Provider, error) {
	store := a.Store(in)
	switch in.Auth.Type {
	case "pat":
		return pat.New(in.Auth.CredentialRef, in.Auth.Username, store), nil
	case "oauth":
		conf, err := oauth.Config(in.Auth.OAuth, store)
		if err != nil {
			return nil, err
		}
		return oauth.New(in.Auth.CredentialRef, conf, hc, store), nil
	}
	return nil, clierr.New(clierr.LayerConfig, "unsupported_auth", "unsupported auth type "+in.Auth.Type)
}
