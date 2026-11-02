package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/personal-stack/jira-cli/internal/api"
	"github.com/personal-stack/jira-cli/internal/auth"
	"github.com/personal-stack/jira-cli/internal/auth/oauth"
	"github.com/personal-stack/jira-cli/internal/auth/pat"
	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/credentials"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/transport"
)

func (a *App) authCmd() *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Log in, inspect and test authentication"}

	var method string
	var manual, noVerify bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Store credentials for an instance in the OS keyring",
		Long: `Store credentials for the selected instance in the OS keyring.

PAT: the token is read from standard input (hidden prompt on a terminal, or
piped: ` + "`printf %s \"$TOKEN\" | jira auth login --method pat`" + `). It is never
accepted as a command-line argument. The token is verified against
/rest/api/2/myself before it is stored (disable with --no-verify).

OAuth: runs the authorization-code flow (PKCE by default) using a loopback
redirect listener, or --manual to paste the redirect URL.

Headless/container environments do not need login: mount the credential and
point JIRA_CREDENTIAL_FILE at it (see docs/credentials.md).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, _, err := a.Instance()
			if err != nil {
				return err
			}
			if method == "" {
				method = in.Auth.Type
			}
			if method != in.Auth.Type {
				return usageErr(fmt.Sprintf("instance %s is configured for auth.type %s, not %s", in.Name, in.Auth.Type, method))
			}
			switch method {
			case "pat":
				return a.loginPAT(cmd, in, noVerify)
			case "oauth":
				return a.loginOAuth(cmd, in, manual)
			}
			return usageErr("--method must be pat or oauth")
		},
	}
	login.Flags().StringVar(&method, "method", "", "pat or oauth (default: the instance's auth.type)")
	login.Flags().BoolVar(&manual, "manual", false, "oauth: paste the redirect URL instead of running a loopback listener")
	login.Flags().BoolVar(&noVerify, "no-verify", false, "pat: store the token without verifying it against Jira")

	logout := &cobra.Command{
		Use: "logout", Short: "Remove the instance's credential from the OS keyring", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, _, err := a.Instance()
			if err != nil {
				return err
			}
			w, err := a.Store(in).Writer()
			if err != nil {
				return clierr.Wrap(clierr.LayerCredentials, "no_keyring", err.Error(), nil)
			}
			if err := w.Delete(in.Auth.CredentialRef); err != nil {
				if errors.Is(err, credentials.ErrNotFound) {
					return clierr.New(clierr.LayerCredentials, "not_found", "no keyring entry for "+in.Auth.CredentialRef).
						WithHint("credentials from files or environment variables must be removed where they are defined")
				}
				return clierr.Wrap(clierr.LayerCredentials, "keyring_error", "cannot delete keyring entry", err)
			}
			f, _ := a.Format()
			return output.Write(a.Out, map[string]any{"ok": true, "removed": in.Auth.CredentialRef, "store": "keyring"}, f)
		},
	}

	status := &cobra.Command{
		Use: "status", Short: "Show credential availability for the instance (no network access)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			in, src, err := a.Instance()
			if err != nil {
				return err
			}
			hc, err := transport.New(in, a.timeout)
			if err != nil {
				return clierr.Wrap(clierr.LayerConfig, "transport_config", "invalid transport configuration", err)
			}
			ap, err := a.AuthProvider(in, hc)
			if err != nil {
				return err
			}
			st := ap.Status(ctxOrBackground(cmd))
			if st.Username == "" {
				st.Username = in.Auth.Username
			}
			if err := output.Write(a.Out, map[string]any{"instance": in.Name, "selected_by": src, "auth": st}, f); err != nil {
				return err
			}
			if !st.Present {
				return clierr.New(clierr.LayerCredentials, "credential_unavailable", "credential "+in.Auth.CredentialRef+" is not available").
					WithHint("run `jira auth login` or provide it via JIRA_CREDENTIAL_FILE / " + credentials.EnvName(in.Auth.CredentialRef))
			}
			return nil
		},
	}

	test := &cobra.Command{
		Use: "test", Short: "Verify the credential against Jira (GET /rest/api/2/myself)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			in, _, err := a.Instance()
			if err != nil {
				return err
			}
			p, err := a.ProfileFor(in)
			if err != nil {
				return err
			}
			client, err := a.Client(in, p)
			if err != nil {
				return err
			}
			me, err := whoami(ctxOrBackground(cmd), client)
			if err != nil {
				return err
			}
			return output.Write(a.Out, map[string]any{"ok": true, "instance": in.Name, "auth_method": in.Auth.Type, "user": me}, f)
		},
	}
	c.AddCommand(login, logout, status, test)
	return c
}

// whoami calls /rest/api/2/myself and returns non-sensitive identity fields.
func whoami(ctx context.Context, c *api.Client) (map[string]any, error) {
	resp, err := c.Do(ctx, api.Request{Method: http.MethodGet, Path: "/api/2/myself", Idempotent: true})
	if err != nil {
		return nil, err
	}
	var me map[string]any
	if err := json.Unmarshal(resp.Body, &me); err != nil {
		return nil, clierr.Wrap(clierr.LayerResponse, "invalid_json", "cannot parse /myself response", err)
	}
	out := map[string]any{}
	for _, k := range []string{"name", "key", "displayName", "emailAddress", "active", "timeZone"} {
		if v, ok := me[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (a *App) readToken() (credentials.Secret, error) {
	if f, ok := a.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(a.Err, "Personal access token (input hidden): ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.Err)
		if err != nil {
			return credentials.Secret{}, err
		}
		return credentials.NewSecret([]byte(strings.TrimSpace(string(b)))), nil
	}
	b, err := io.ReadAll(io.LimitReader(a.In, 64<<10))
	if err != nil {
		return credentials.Secret{}, err
	}
	return credentials.NewSecret([]byte(strings.TrimSpace(string(b)))), nil
}

// staticSecretStore serves exactly one in-memory secret (login verification).
type staticSecretStore struct {
	ref string
	s   credentials.Secret
}

func (s staticSecretStore) Get(ref string) (*credentials.Found, error) {
	if ref != s.ref {
		return nil, credentials.ErrNotFound
	}
	return &credentials.Found{Secret: s.s, Source: "stdin"}, nil
}

func (a *App) loginPAT(cmd *cobra.Command, in *config.Instance, noVerify bool) error {
	tok, err := a.readToken()
	if err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "read_failed", "cannot read token from stdin", err)
	}
	if err := pat.Validate(tok); err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "invalid_token", "invalid token", err)
	}
	result := map[string]any{"ok": true, "instance": in.Name, "credential_ref": in.Auth.CredentialRef, "store": "keyring", "verified": false}
	if !noVerify {
		p, err := a.ProfileFor(in)
		if err != nil {
			return err
		}
		client, err := a.Client(in, p)
		if err != nil {
			return err
		}
		client.Auth = pat.New(in.Auth.CredentialRef, in.Auth.Username, staticSecretStore{in.Auth.CredentialRef, tok})
		me, err := whoami(ctxOrBackground(cmd), client)
		if err != nil {
			return err
		}
		result["verified"], result["user"] = true, me
	}
	w, err := a.Store(in).Writer()
	if err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "no_keyring", err.Error(), nil)
	}
	if err := w.Set(in.Auth.CredentialRef, tok); err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "keyring_error", "cannot store token in the OS keyring", err).
			WithHint("in headless environments use JIRA_CREDENTIAL_FILE instead of `auth login`")
	}
	f, _ := a.Format()
	return output.Write(a.Out, result, f)
}

func (a *App) loginOAuth(cmd *cobra.Command, in *config.Instance, manual bool) error {
	store := a.Store(in)
	conf, err := oauth.Config(in.Auth.OAuth, store)
	if err != nil {
		return err
	}
	hc, err := transport.New(in, a.timeout)
	if err != nil {
		return clierr.Wrap(clierr.LayerConfig, "transport_config", "invalid transport configuration", err)
	}
	tok, err := oauth.Login(ctxOrBackground(cmd), conf, hc, oauth.LoginOptions{
		PKCE: in.Auth.OAuth.UsePKCE(), Manual: manual, In: a.In, Out: a.Err, Timeout: durationOr(a.timeout, 5*time.Minute),
	})
	if err != nil {
		return clierr.Wrap(clierr.LayerAuth, "oauth_login_failed", "OAuth login failed", err).
			WithHint("check authorization_url/token_url/client_id/redirect_uri against the Jira application link; if the server rejects PKCE set auth.oauth.pkce: false")
	}
	s, err := oauth.EncodeToken(tok)
	if err != nil {
		return err
	}
	w, err := store.Writer()
	if err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "no_keyring", err.Error(), nil)
	}
	if err := w.Set(in.Auth.CredentialRef, s); err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "keyring_error", "cannot store token in the OS keyring", err)
	}
	f, _ := a.Format()
	res := map[string]any{"ok": true, "instance": in.Name, "credential_ref": in.Auth.CredentialRef, "store": "keyring", "refreshable": tok.RefreshToken != ""}
	if !tok.Expiry.IsZero() {
		res["expiry"] = tok.Expiry
	}
	return output.Write(a.Out, res, f)
}

var _ auth.Provider = (*pat.Provider)(nil)
