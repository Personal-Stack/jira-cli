package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/sbx"
)

type configInitFlags struct {
	baseURL, credRef, caBundle string
	allowWrites, noDefault     bool
	skipChecks, interactive    bool
}

// configInitCmd is `jiracli config setup`.
func (a *App) configInitCmd(use, short string) *cobra.Command {
	f := &configInitFlags{}
	c := &cobra.Command{
		Use:   use,
		Short: short,
		Long: `Creates the configuration file, or adds or updates one instance in it.
Without flags in a terminal it asks for each value, checks it when given
(the Jira URL must be reachable, a CA file must hold certificates) and
offers the current values as defaults.

The file is the one jiracli uses: --config, else $` + config.EnvConfig + `, else
` + config.DefaultPath() + `; the folder is created if needed. Only the
values asked for change: comments, other instances and settings such as
timeouts or proxy are kept, and the result is validated before it is
written. --instance selects the instance (default: the default instance,
or "prod").`,
		Example: `  jiracli config setup
  jiracli config setup --instance prod --base-url https://jira.company.internal`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			guided := f.interactive || (cmd.LocalFlags().NFlag() == 0 && a.IsTerminal())
			return a.runConfigInit(cmd.Context(), f, guided, cmd.Flags().Changed)
		},
	}
	fl := c.Flags()
	fl.StringVar(&f.baseURL, "base-url", "", "Jira base URL, with the context path if any")
	fl.StringVar(&f.credRef, "credential-ref", "", "name of the stored token (default: jira-<instance>, or the current one)")
	fl.StringVar(&f.caBundle, "ca-bundle", "", "company CA file (PEM or DER); empty: none (see 'jiracli certs fetch')")
	fl.BoolVar(&f.allowWrites, "allow-writes", false, "do not set read_only (default for a new instance: read-only)")
	fl.BoolVar(&f.noDefault, "no-default", false, "do not make this the default instance")
	fl.BoolVar(&f.skipChecks, "skip-checks", false, "do not check that the Jira host is reachable")
	fl.BoolVarP(&f.interactive, "interactive", "i", false, "ask for every value (default without flags in a terminal)")
	return c
}

func (a *App) runConfigInit(ctx context.Context, f *configInitFlags, guided bool, set func(string) bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	format, err := a.Format()
	if err != nil {
		return err
	}
	path := a.ConfigPath()
	ui := a.newTermUI()

	// Start from the file as it is (if any) and the selected instance.
	cfg, err := config.Load(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return clierr.Wrap(clierr.LayerConfig, "config_invalid", "cannot read "+path, err).
			WithHint("fix the file (jiracli config validate shows what is wrong), or move it away to start over")
	}
	u := config.InstanceUpdate{Name: firstNonEmpty(a.instance, a.Getenv(config.EnvInstance)), ReadOnly: !f.allowWrites}
	current := func() *config.Instance {
		if cfg == nil {
			return nil
		}
		return cfg.Instances[u.Name]
	}
	if u.Name == "" && cfg != nil {
		u.Name = cfg.DefaultInstance
	}
	if u.Name == "" {
		u.Name = "prod"
	}
	fill := func() { // values of the current instance, then the flags
		u.BaseURL, u.CredentialRef, u.CABundle = "", "jira-"+u.Name, ""
		if in := current(); in != nil {
			u.BaseURL, u.CredentialRef, u.CABundle = in.BaseURL, in.Auth.CredentialRef, in.TLS.CABundle
			if !set("allow-writes") {
				u.ReadOnly = in.ReadOnly
			}
		}
		for _, v := range []struct {
			flag string
			dst  *string
			val  string
		}{{"base-url", &u.BaseURL, f.baseURL}, {"credential-ref", &u.CredentialRef, f.credRef}, {"ca-bundle", &u.CABundle, absIfSet(f.caBundle)}} {
			if set(v.flag) {
				*v.dst = v.val
			}
		}
		u.MakeDefault = !f.noDefault && (cfg == nil || cfg.DefaultInstance == "" || cfg.DefaultInstance == u.Name || set("instance"))
	}
	fill()

	checkURL := func(v string) error {
		if _, _, err := sbx.ParseBaseURL(v); err != nil || f.skipChecks {
			return err
		}
		return sbx.CheckReachable(ctx, v)
	}
	checkCA := func(v string) error {
		b, err := os.ReadFile(absPath(v))
		if err != nil {
			return fmt.Errorf("cannot read %s (to get the CA from the server: jiracli certs fetch)", v)
		}
		if _, _, err := config.ParseCABundle(b, v); err != nil {
			return err
		}
		return nil
	}

	ui.title("jiracli config setup", "Jira configuration")
	ui.kv("file", path)
	if guided {
		q := a.form(ctx, ui)
		ui.note("Enter keeps the value in [brackets], '-' clears an optional one, Ctrl+C quits (nothing is written before you confirm).")
		if cfg != nil && len(cfg.Instances) > 0 {
			ui.note("Instances: " + strings.Join(cfg.Names(), ", "))
		}
		before := u.Name
		q.text(&u.Name, "Instance (an existing one is updated)", sbx.ValidName)
		if q.err == nil && u.Name != before {
			fill()
		}
		q.text(&u.BaseURL, "Jira base URL (https://, with the context path if any)", checkURL)
		q.text(&u.CredentialRef, "Name of the stored token (credential_ref)", sbx.ValidName)
		q.yesno(&u.ReadOnly, "Read-only (refuse every command that is not a read)")
		q.text(&u.CABundle, "Company CA file (empty = none; most setups need none)", optional(checkCA))
		q.yesno(&u.MakeDefault, "Make it the default instance")
		if q.err != nil {
			return q.err
		}
		u.CABundle = absIfSet(u.CABundle)
		ui.section("Summary")
		ui.kv("instance", u.Name+map[bool]string{true: ui.dim(" (updated)"), false: ui.dim(" (new)")}[current() != nil])
		ui.kv("jira", u.BaseURL)
		ui.kv("token", u.CredentialRef)
		ui.kv("read-only", map[bool]string{true: "yes", false: "no"}[u.ReadOnly])
		ui.kv("CA", or(u.CABundle, "none (system trust)"))
		ui.kv("default", map[bool]string{true: "yes", false: "no"}[u.MakeDefault])
		ok := true
		if !a.yes {
			q.yesno(&ok, "Write it")
		}
		if q.err != nil {
			return q.err
		}
		if !ok {
			return clierr.New(clierr.LayerSafety, "confirmation_declined", "aborted; nothing was written")
		}
	} else {
		if u.BaseURL == "" {
			return usageErr("--base-url is required for a new instance (" + u.Name + ")")
		}
		for _, check := range []error{checkURL(u.BaseURL), sbx.ValidName(u.Name), sbx.ValidName(u.CredentialRef)} {
			if check != nil {
				return clierr.Wrap(clierr.LayerUsage, "invalid_value", check.Error(), nil)
			}
		}
		if u.CABundle != "" {
			if err := checkCA(u.CABundle); err != nil {
				return clierr.Wrap(clierr.LayerUsage, "invalid_value", err.Error(), nil)
			}
		}
	}

	existed := current() != nil
	if err := config.UpsertInstance(path, u); err != nil {
		return clierr.Wrap(clierr.LayerConfig, "config_write_failed", "the configuration was not written", err)
	}
	a.cfgLoaded = false // later reads see the new file
	ui.section("Done")
	ui.ok(map[bool]string{true: "updated", false: "added"}[existed] + " instance " + u.Name + " in " + path)
	next := []string{"jiracli auth login" + map[bool]string{true: "", false: " --instance " + u.Name}[u.MakeDefault], "jiracli doctor" + map[bool]string{true: "", false: " --instance " + u.Name}[u.MakeDefault]}
	ui.section("Next steps")
	ui.println("  " + ui.cyan("1.") + " Store the token (a Personal Access Token from your Jira profile), unless it is already stored:")
	ui.println("       " + ui.cmd(next[0]))
	ui.println("  " + ui.cyan("2.") + " Check the connection, TLS and the token:")
	ui.println("       " + ui.cmd(next[1]))
	ui.println("")
	if !guided || set("output") {
		return output.Write(a.Out, map[string]any{"path": path, "instance": u.Name, "updated": existed, "default": u.MakeDefault, "next": next}, format)
	}
	return nil
}

// offerConfigInit runs after a command failed for lack of a configuration
// file: in a terminal it offers to create it now. It reports whether the
// configuration was written.
func (a *App) offerConfigInit(ctx context.Context) bool {
	ui := a.newTermUI()
	q := a.form(ctx, ui)
	create := true
	ui.println("")
	q.yesno(&create, "No configuration yet. Create it now")
	if q.err != nil || !create {
		return false
	}
	if err := a.runConfigInit(ctx, &configInitFlags{}, true, func(string) bool { return false }); err != nil {
		a.printError(clierr.As(err))
		return false
	}
	ui.note("Configuration written; run your command again.")
	return true
}
