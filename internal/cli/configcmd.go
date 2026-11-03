package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/output"
)

func (a *App) configCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "Inspect and validate the configuration"}

	list := &cobra.Command{
		Use: "list", Short: "List configured instances", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			cfg, err := a.Config()
			if err != nil {
				return err
			}
			var rows []map[string]any
			for _, n := range cfg.Names() {
				in := cfg.Instances[n]
				p, _ := a.ProfileFor(in)
				prof := ""
				if p != nil {
					prof = p.Name
				}
				rows = append(rows, map[string]any{
					"name": n, "default": n == cfg.DefaultInstance, "deployment": in.Deployment,
					"base_url": in.BaseURL, "auth": in.Auth.Type, "profile": prof, "read_only": a.ReadOnly(in),
				})
			}
			return output.Write(a.Out, rows, f)
		},
	}

	show := &cobra.Command{
		Use: "show [instance]", Short: "Show the effective configuration of an instance (redacted)", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				a.instance = args[0]
			}
			in, src, err := a.Instance()
			if err != nil {
				return err
			}
			r := in.Redacted()
			return output.Write(a.Out, map[string]any{
				"config_file": a.ConfigPath(), "selected_by": src, "instance": r, "effective_read_only": a.ReadOnly(in),
			}, f)
		},
	}

	validate := &cobra.Command{
		Use: "validate", Short: "Validate the configuration file and referenced TLS files", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			cfg, err := a.Config()
			if err != nil {
				_ = output.Write(a.Out, map[string]any{"valid": false, "config_file": a.ConfigPath(), "error": clierr.As(err).Error()}, f)
				return err
			}
			results := map[string]any{}
			var errs []error
			for _, n := range cfg.Names() {
				in := cfg.Instances[n]
				r := map[string]any{"valid": true}
				if _, perr := a.ProfileFor(in); perr != nil {
					r["valid"], r["error"] = false, clierr.As(perr).Message
					errs = append(errs, fmt.Errorf("instance %q: %w", n, perr))
				} else if ferr := in.CheckFiles(); ferr != nil {
					r["valid"], r["error"] = false, ferr.Error()
					errs = append(errs, fmt.Errorf("instance %q: %w", n, ferr))
				}
				if !in.TLS.VerifyEnabled() {
					r["warning"] = "tls.verify is false: certificate verification disabled"
				}
				results[n] = r
			}
			_ = output.Write(a.Out, map[string]any{"valid": len(errs) == 0, "config_file": cfg.Path(), "instances": results}, f)
			if len(errs) > 0 {
				var bad []string
				for _, n := range cfg.Names() {
					if r, _ := results[n].(map[string]any); r != nil && r["valid"] == false {
						bad = append(bad, n)
					}
				}
				e := clierr.Wrap(clierr.LayerConfig, "config_invalid", "configuration has errors in instance(s): "+strings.Join(bad, ", "), errors.Join(errs...))
				e.Details = map[string]any{"invalid_instances": bad}
				if sel, _, serr := a.Instance(); serr == nil && !contains(bad, sel.Name) {
					e.Hint = fmt.Sprintf("the selected instance %q is valid; fix or remove the other instance(s) in %s", sel.Name, cfg.Path())
				}
				return e
			}
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value (supported keys: default-instance)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "default-instance":
				if err := config.SetDefaultInstance(a.ConfigPath(), args[1]); err != nil {
					return clierr.Wrap(clierr.LayerConfig, "config_write_failed", "cannot set default-instance", err)
				}
				f, _ := a.Format()
				return output.Write(a.Out, map[string]any{"ok": true, "default_instance": args[1]}, f)
			}
			return usageErr("unsupported key " + args[0] + " (supported: default-instance); edit the YAML file for other settings")
		},
	}
	c.AddCommand(list, show, validate, set)
	return c
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
