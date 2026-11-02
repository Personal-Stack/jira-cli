package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/logging"
	"github.com/personal-stack/jira-cli/internal/output"
)

// NewRoot builds the command tree. args are the process arguments (without
// the program name); they are pre-scanned for --instance/--config so that
// the command tree of the selected instance's profile can be registered.
func (a *App) NewRoot(args []string) *cobra.Command {
	root := &cobra.Command{
		Use:   "jira",
		Short: "Spec-driven command-line interface for Jira Data Center",
		Long: `jira is a command-line interface for Jira Data Center whose API commands are
generated at build time from OpenAPI specifications.

Start with:
  jira config validate        check the configuration
  jira doctor                 diagnose connectivity, TLS and authentication
  jira commands list          list available API commands (machine-readable)
  jira capabilities           describe this binary and the selected instance`,
		Version:           a.Version,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	}
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err.Error()) })
	root.AddGroup(&cobra.Group{ID: "core", Title: "CLI commands:"}, &cobra.Group{ID: "api", Title: "Jira API commands (generated):"})

	pf := root.PersistentFlags()
	pf.StringVar(&a.instance, "instance", "", "Jira instance to use (default: $"+config.EnvInstance+", then default_instance)")
	pf.StringVar(&a.configPath, "config", "", "config file (default: $"+config.EnvConfig+", then "+config.DefaultPath()+")")
	pf.StringVarP(&a.outputFlag, "output", "o", string(output.JSON), "output format: json, yaml, table")
	pf.BoolVarP(&a.verbose, "verbose", "v", false, "debug logging to stderr (never includes secrets)")
	pf.BoolVarP(&a.quiet, "quiet", "q", false, "only log errors")
	pf.BoolVar(&a.noColor, "no-color", false, "disable colored output (output is never colored; accepted for compatibility)")
	pf.DurationVar(&a.timeout, "timeout", 0, "request timeout, e.g. 30s (default: instance timeouts.request or 60s)")
	pf.BoolVar(&a.dryRun, "dry-run", false, "print the request that would be sent and exit without sending it")
	pf.BoolVar(&a.readOnly, "read-only", false, "refuse every operation that is not classified as read (also: $"+EnvReadOnly+"=1)")
	pf.BoolVarP(&a.yes, "yes", "y", false, "confirm destructive/administrative operations non-interactively")
	pf.StringVar(&a.logLevel, "log-level", "", "log level: debug, info, warn, error (default: $"+logging.EnvLevel+", -v=debug, -q=error, else warn)")
	pf.StringVar(&a.logFormat, "log-format", "", "log format: text, json (default: $"+logging.EnvFormat+", else text)")
	pf.StringVar(&a.logFile, "log-file", "", "append logs to this file (mode 0600) instead of stderr (default: $"+logging.EnvFile+")")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := a.initLog(); err != nil {
			return err
		}
		a.Log().Info("command started", "command", cmd.CommandPath(), "version", a.Version)
		return nil
	}

	for _, c := range []*cobra.Command{a.configCmd(), a.authCmd(), a.specCmd(), a.commandsCmd(), a.capabilitiesCmd(), a.doctorCmd()} {
		c.GroupID = "core"
		root.AddCommand(c)
	}

	// Register the generated commands of the profile used by the selected
	// instance (falls back to the default profile if config is unavailable).
	pre := prescan(args)
	a.instance, a.configPath = pre["instance"], pre["config"]
	prof := a.Profiles[a.Default]
	if in, _, err := a.Instance(); err == nil {
		if p, err := a.ProfileFor(in); err == nil {
			prof = p
		}
	}
	a.cfgLoaded, a.cfg, a.cfgErr = false, nil, nil // reload after flag parsing
	if prof != nil {
		a.registerProfile(root, prof)
	}
	return root
}

// prescan extracts --instance and --config before Cobra parses flags.
func prescan(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		for _, name := range []string{"instance", "config"} {
			switch {
			case arg == "--"+name && i+1 < len(args):
				out[name] = args[i+1]
			case strings.HasPrefix(arg, "--"+name+"="):
				out[name] = strings.TrimPrefix(arg, "--"+name+"=")
			}
		}
	}
	return out
}

// Execute runs the CLI and returns the process exit code.
func (a *App) Execute(ctx context.Context, args []string) int {
	start := time.Now()
	root := a.NewRoot(args)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	defer a.closeLog()
	if err == nil {
		a.Log().Info("command finished", "exit_code", 0, "duration_ms", time.Since(start).Milliseconds())
		return 0
	}
	e := clierr.As(err)
	if e.Layer == clierr.LayerInternal && isCobraUsage(err) {
		e = clierr.New(clierr.LayerUsage, "invalid_usage", err.Error())
	}
	a.Log().Info("command finished", "exit_code", e.ExitCode(), "duration_ms", time.Since(start).Milliseconds(),
		"error_layer", string(e.Layer), "error_code", e.Code, "http_status", e.HTTPStatus)
	a.printError(e)
	return e.ExitCode()
}

func isCobraUsage(err error) bool {
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "required flag", "accepts ", "invalid argument", "flag needs an argument"} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// printError writes the error to stderr: JSON when the output format is
// json (the default, for agents) and stderr is not a terminal; text otherwise.
func (a *App) printError(e *clierr.Error) {
	f, ferr := output.Parse(a.outputFlag)
	if ferr == nil && f == output.JSON && !a.IsTerminal() {
		payload := map[string]any{"error": e}
		if e.Err != nil {
			payload["cause"] = e.Err.Error()
		}
		_ = output.Write(a.Err, payload, output.JSON)
		return
	}
	fmt.Fprintf(a.Err, "Error: %s\n", e.Error())
	if e.Hint != "" {
		fmt.Fprintf(a.Err, "Hint: %s\n", e.Hint)
	}
	if len(e.Details) > 0 {
		fmt.Fprintf(a.Err, "Details: %v\n", e.Details)
	}
}

// flagNames lists global flags for discovery output.
func globalFlags(root *cobra.Command) []map[string]string {
	var out []map[string]string
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		out = append(out, map[string]string{"name": "--" + f.Name, "type": f.Value.Type(), "usage": f.Usage})
	})
	return out
}

func ctxOrBackground(cmd *cobra.Command) context.Context {
	if cmd.Context() != nil {
		return cmd.Context()
	}
	return context.Background()
}

func durationOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
