package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/sbx"
	"github.com/personal-stack/jira-cli/skills"
)

// Seams for tests: running sbx and finding it.
var (
	sbxLookPath = exec.LookPath
	// sbxRun runs sbx in dir. attach connects the terminal (for prompts and
	// `sbx env run`); otherwise the combined output is returned.
	sbxRun = func(ctx context.Context, dir string, attach bool, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "sbx", args...)
		cmd.Dir = dir
		if attach {
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return nil, cmd.Run()
		}
		return cmd.CombinedOutput()
	}
)

type sbxFlags struct {
	opts                      sbx.Options
	interactive, skipSecret   bool
	run, allowWrites, noSkill bool
	binary, sourceDir, image  string
	binaryFile, arch          string
}

func (a *App) setupSbxCmd() *cobra.Command {
	f := &sbxFlags{}
	c := &cobra.Command{
		Use:   "setup-sbx",
		Short: "Set up a Docker Sandbox (sbx) in which an AI agent can use jira-cli",
		Long: `Sets up a Docker Sandboxes (sbx) environment in which an AI agent (OpenCode by
default) can use jira-cli, while the Jira personal access token stays on this
machine: it is the sbx secret "jira", and inside the sandbox
JIRA_CREDENTIAL_<REF> is only a sentinel that the sandbox proxy replaces with
the real Authorization header on requests to your Jira host.

Run without flags in a terminal, it asks for everything (project, Jira URL,
agent, image, sandbox name, ...), shows a summary and asks before writing.
Re-running it for the same project offers the previous answers.

It writes:
  <project>/.sbx/jira-cli/          a local sbx kit: linux jira binary, egress rule
                                    for the Jira host, the credential, shell hooks
  <project>/sbxenv.yaml             the environment 'sbx env run' reads
  <project>/.opencode/skills/jira   the agent skill
  <settings>/config.yaml, bashrc    created once, never overwritten; mounted
                                    read-only at the same path in the sandbox

Then it stores the PAT with 'sbx secret set jira' if needed (sbx prompts for
it) and runs 'sbx env plan' (and 'sbx env run' with --run). --dry-run shows
the plan and the sbxenv.yaml without writing anything. See docs/sandbox.md.`,
		Example: `  jira setup-sbx
  jira setup-sbx --project ~/Projects/my-work --base-url https://jira.company.internal \
    --agent devex-opencode --name jira-sbx --template company.jfrog.io/team/opencode-sbx:1.2`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			interactive := f.interactive || (cmd.Flags().NFlag() == 0 && a.IsTerminal())
			return a.runSetupSbx(cmd.Context(), f, interactive, cmd.Flags().Changed("agent"))
		},
	}
	home, _ := os.UserHomeDir()
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	if cfgHome == "" {
		cfgHome = filepath.Join(home, ".config")
	}
	fl := c.Flags()
	fl.StringVar(&f.opts.Project, "project", "", "workspace the agent works in; sbxenv.yaml goes here (default: current directory)")
	fl.StringVar(&f.opts.BaseURL, "base-url", "", "Jira base URL (default: base_url from <settings>/config.yaml)")
	fl.StringVar(&f.opts.Agent, "agent", "opencode", "sbx agent: a built-in name, or the agent kit's name as sbx reports it (\"agent kit name ...\")")
	fl.StringVar(&f.opts.AgentKit, "agent-kit", "", "agent kit to compose (e.g. sbx/opencode:<tag>); --agent defaults to its last path segment")
	fl.StringVar(&f.opts.Template, "template", "", "sandbox image instead of the agent's default (e.g. your configured image)")
	fl.StringVar(&f.opts.Name, "name", "", "custom sandbox name (default: sbx's own, <agent>-<project folder>)")
	fl.StringVar(&f.opts.CABundle, "ca-bundle", "", "company CA (PEM or DER) for config.yaml; copied into the settings folder")
	fl.BoolVar(&f.allowWrites, "allow-writes", false, "do not set read_only / JIRA_READ_ONLY (default: read-only)")
	fl.BoolVar(&f.noSkill, "no-skill", false, "do not copy the jira skill into <project>/.opencode/skills/jira")
	fl.StringVar(&f.opts.Settings, "settings", filepath.Join(cfgHome, "jira-cli-sbx"), "host folder with config.yaml and bashrc")
	fl.StringVar(&f.opts.Instance, "instance-name", "prod", "instance name in a new config.yaml")
	fl.StringVar(&f.opts.CredRef, "credential-ref", "jira-prod", "credential_ref in config.yaml")
	fl.StringVar(&f.opts.Service, "secret", "jira", "sbx secret (credential service) name holding the PAT")
	fl.StringVar(&f.binary, "binary", "auto", "linux binary source: auto, self, go, release, image, file")
	fl.StringVar(&f.binaryFile, "binary-file", "", "linux jira binary to use (implies --binary file)")
	fl.StringVar(&f.sourceDir, "source-dir", "", "jira-cli clone for --binary go (default: the clone containing the current directory)")
	fl.StringVar(&f.image, "image", "", "image for --binary image (default: "+sbx.DefaultImage+":<this version or latest>)")
	fl.StringVar(&f.arch, "arch", runtime.GOARCH, "sandbox architecture: amd64 or arm64 (sandboxes run on the host's)")
	fl.BoolVar(&f.opts.KitOnly, "kit-only", false, "write the kit, settings and skill, not sbxenv.yaml (merge it by hand)")
	fl.BoolVar(&f.opts.Force, "force", false, "overwrite a foreign sbxenv.yaml and an edited skill")
	fl.BoolVar(&f.skipSecret, "skip-secret", false, "do not check or create the sbx secret")
	fl.BoolVar(&f.run, "run", false, "run 'sbx env run' at the end")
	fl.BoolVarP(&f.interactive, "interactive", "i", false, "ask for every setting (default when run without flags in a terminal)")
	return c
}

// prompter asks questions on Err and reads answers from In.
type prompter struct {
	r *bufio.Reader
	w io.Writer
}

// ask shows cur as the default: Enter keeps it, "-" clears it.
func (p *prompter) ask(q, cur string) (string, error) {
	if cur != "" {
		fmt.Fprintf(p.w, "%s [%s]: ", q, cur)
	} else {
		fmt.Fprintf(p.w, "%s: ", q)
	}
	line, err := p.r.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", clierr.New(clierr.LayerUsage, "no_answer", "no answer (end of input)")
	}
	switch v := strings.TrimSpace(line); v {
	case "":
		return cur, nil
	case "-":
		return "", nil
	default:
		return v, nil
	}
}

func (p *prompter) yn(q string, cur bool) (bool, error) {
	hint := "y/N"
	if cur {
		hint = "Y/n"
	}
	for {
		v, err := p.ask(q+" ["+hint+"]", "")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(v) {
		case "":
			return cur, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func absPath(p string) string {
	p = expandHome(p)
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func (a *App) runSetupSbx(ctx context.Context, f *sbxFlags, interactive, agentSet bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	o := &f.opts
	o.ReadOnly, o.Skill = !f.allowWrites, !f.noSkill
	if o.Project == "" {
		o.Project, _ = os.Getwd()
	}
	o.Project, o.Settings = absPath(o.Project), absPath(o.Settings)
	if o.CABundle != "" {
		o.CABundle = absPath(o.CABundle)
	}
	if o.AgentKit != "" && !agentSet {
		o.Agent = sbx.AgentFromKit(o.AgentKit)
	}
	if f.binaryFile != "" {
		f.binary = sbx.SourceFile
	}

	if interactive {
		if err := a.askSetupSbx(f, agentSet); err != nil {
			return err
		}
	}
	plan, err := sbx.NewPlan(*o)
	if err != nil {
		return clierr.Wrap(clierr.LayerUsage, "invalid_setup", err.Error(), nil)
	}
	if err := plan.CheckEnvFile(); err != nil {
		return clierr.Wrap(clierr.LayerUsage, "foreign_sbxenv", err.Error(), nil)
	}
	format, err := a.Format()
	if err != nil {
		return err
	}
	if a.dryRun {
		env, _ := plan.EnvFileContent()
		return output.Write(a.Out, map[string]any{
			"dry_run": true, "project": plan.Project, "env_file": plan.EnvFile, "kit": plan.KitDir,
			"settings": plan.Settings, "skill": plan.Skill, "jira_host": plan.Host, "secret": plan.Service,
			"credential_env": plan.CredEnv, "sbxenv_yaml": env,
		}, format)
	}

	logf := func(s string) { fmt.Fprintln(a.Err, s) }
	tmp, err := os.MkdirTemp("", "jira-setup-sbx-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bin, source, err := sbx.GetBinary(ctx, sbx.BinaryRequest{
		Source: f.binary, Arch: f.arch, Version: a.Version, Clone: f.sourceDir,
		Image: f.image, File: absIfSet(f.binaryFile), Dir: tmp, Log: logf,
	})
	if err != nil {
		return clierr.Wrap(clierr.LayerUsage, "no_linux_binary", err.Error(), nil).
			WithHint("run it from a jira-cli clone with Go installed, use a released jira, or pass --binary-file / --binary image")
	}
	logf(fmt.Sprintf("linux/%s binary: %s", f.arch, source))
	if err := plan.WriteKit(map[string]string{f.arch: bin}, a.Version); err != nil {
		return err
	}
	logf("kit: " + plan.KitDir)
	notes, err := plan.WriteSettings()
	if err != nil {
		return err
	}
	if plan.Skill {
		sub, err := fs.Sub(skills.FS, "jira")
		if err != nil {
			return err
		}
		note, err := plan.WriteSkill(sub)
		if err != nil {
			return err
		}
		notes = append(notes, note)
	}
	if !plan.KitOnly {
		if err := plan.WriteEnvFile(); err != nil {
			return err
		}
		notes = append(notes, "env file: "+plan.EnvFile)
	} else {
		notes = append(notes, "env file not written (--kit-only): merge the kit, mount, env and binding into "+plan.EnvFile+" (docs/sandbox.md)")
	}
	for _, n := range notes {
		logf(n)
	}

	result := map[string]any{
		"project": plan.Project, "env_file": plan.EnvFile, "kit": plan.KitDir, "settings": plan.Settings,
		"config": plan.ConfigFile, "agent": plan.Agent, "name": plan.Name, "binary_source": source,
		"jira_host": plan.Host, "secret": plan.Service, "credential_env": plan.CredEnv, "notes": notes,
	}
	if _, err := sbxLookPath("sbx"); err != nil {
		next := []string{"sbx secret ls   # if " + plan.Service + " is missing: sbx secret set " + plan.Service, "cd " + plan.Project + " && sbx env run"}
		if a.inContainer() {
			result["sbx"] = "jira ran in a container: run the next steps on the host"
			logf("\nDone. jira ran in a container, so run these on the host:")
			for _, n := range next {
				logf("  " + n)
			}
		} else {
			result["sbx"] = "not found: install Docker Sandboxes (https://docs.docker.com/ai/sandboxes/), then run the next steps"
		}
		result["next"] = next
		return output.Write(a.Out, result, format)
	}
	if !f.skipSecret {
		if err := a.ensureSbxSecret(ctx, plan.Service, interactive || a.IsTerminal()); err != nil {
			return err
		}
	}
	if !plan.KitOnly {
		logf("sbx env plan (nothing is created yet):")
		out, err := sbxRun(ctx, plan.Project, false, "env", "plan")
		fmt.Fprint(a.Err, string(out))
		if err != nil {
			return clierr.Wrap(clierr.LayerUsage, "sbx_plan_failed", "sbx env plan failed (see above)", err)
		}
		result["next"] = []string{
			"cd " + plan.Project + " && sbx env run",
			"sbx env exec -- bash -lc 'jira doctor'",
		}
		if f.run {
			if err := output.Write(a.Out, result, format); err != nil {
				return err
			}
			if _, err := sbxRun(ctx, plan.Project, true, "env", "run"); err != nil {
				return clierr.Wrap(clierr.LayerUsage, "sbx_run_failed", "sbx env run failed", err)
			}
			return nil
		}
	}
	return output.Write(a.Out, result, format)
}

// inContainer reports whether jira runs in the Docker wrapper's container,
// where sbx is not available and only mounted host paths exist.
func (a *App) inContainer() bool { return a.Getenv("JIRA_CLI_CONTAINER") == "1" }

func absIfSet(p string) string {
	if p == "" {
		return ""
	}
	return absPath(p)
}

// ensureSbxSecret creates the sbx secret if it is missing; sbx itself
// prompts for the value, so the PAT never passes through jira-cli.
func (a *App) ensureSbxSecret(ctx context.Context, service string, canPrompt bool) error {
	out, err := sbxRun(ctx, "", false, "secret", "ls")
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == service {
				fmt.Fprintf(a.Err, "sbx secret %q exists (replace it with: sbx secret set %s)\n", service, service)
				return nil
			}
		}
	}
	if !canPrompt {
		return clierr.New(clierr.LayerCredentials, "sbx_secret_missing", "the sbx secret "+service+" does not exist").
			WithHint("run 'sbx secret set " + service + "' in a terminal (it prompts for the PAT), then re-run with --skip-secret")
	}
	fmt.Fprintf(a.Err, "Storing the Jira PAT as sbx secret %q (sbx prompts for it; it stays on this machine)\n", service)
	if _, err := sbxRun(ctx, "", true, "secret", "set", service); err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "sbx_secret_failed", "sbx secret set "+service+" failed", err).
			WithHint("run it yourself (see 'sbx secret --help'), then re-run with --skip-secret")
	}
	return nil
}

func (a *App) askSetupSbx(f *sbxFlags, agentSet bool) error {
	o := &f.opts
	p := &prompter{r: bufio.NewReader(a.In), w: a.Err}
	var err error
	ask := func(dst *string, q string) {
		if err == nil {
			*dst, err = p.ask(q, *dst)
		}
	}
	yn := func(dst *bool, q string) {
		if err == nil {
			*dst, err = p.yn(q, *dst)
		}
	}
	fmt.Fprintln(a.Err, "jira-cli sandbox setup. Enter keeps the value in [brackets]; '-' clears an optional one.")
	fmt.Fprintln(a.Err)
	for {
		ask(&o.Project, "Project the agent works in (mounted read/write)")
		if err != nil {
			return err
		}
		o.Project = absPath(o.Project)
		if st, e := os.Stat(o.Project); e == nil && st.IsDir() {
			break
		}
		fmt.Fprintln(a.Err, "  not a directory: "+o.Project)
		if a.inContainer() {
			fmt.Fprintln(a.Err, "  (jira runs in a container and only sees the current directory: cd to the project and re-run)")
		}
	}
	if prev, ok := sbx.ReadPrevious(filepath.Join(o.Project, "sbxenv.yaml")); ok {
		if !agentSet && prev.Agent != "" {
			o.Agent = prev.Agent
		}
		for _, c := range []struct {
			dst *string
			v   string
		}{{&o.Name, prev.Name}, {&o.Template, prev.Template}, {&o.AgentKit, prev.AgentKit}} {
			if *c.dst == "" {
				*c.dst = c.v
			}
		}
		o.ReadOnly = prev.ReadOnly
		fmt.Fprintln(a.Err, "  (defaults from the previous setup of this project)")
	}
	if o.BaseURL == "" {
		o.BaseURL = sbx.BaseURLFromConfig(filepath.Join(o.Settings, "config.yaml"))
	}
	for {
		ask(&o.BaseURL, "Jira base URL (https://, with the context path if any)")
		if err != nil {
			return err
		}
		if strings.HasPrefix(o.BaseURL, "https://") && len(o.BaseURL) > len("https://") {
			break
		}
		fmt.Fprintln(a.Err, "  must start with https://")
	}
	fmt.Fprintln(a.Err)
	fmt.Fprintln(a.Err, "Agent: a built-in sbx agent (e.g. opencode), or an agent kit (registry reference).")
	kitBefore := o.AgentKit
	ask(&o.AgentKit, "Agent kit reference (empty = built-in agent)")
	if err == nil && o.AgentKit != "" && o.AgentKit != kitBefore && !agentSet {
		o.Agent = sbx.AgentFromKit(o.AgentKit)
	}
	ask(&o.Agent, "Agent name (for a kit: the name sbx reports in 'agent kit name \"...\"')")
	ask(&o.Template, "Sandbox image (empty = the agent's default image)")
	if err == nil {
		fmt.Fprintln(a.Err, "Sandbox name: empty uses sbx's default (<agent>-<project folder>).")
	}
	ask(&o.Name, "Custom sandbox name")
	if err == nil {
		fmt.Fprintln(a.Err)
	}
	ask(&o.CABundle, "Company CA certificate file (empty = none; usually not needed)")
	yn(&o.ReadOnly, "Read-only (the agent cannot change anything in Jira)")
	yn(&o.Skill, "Copy the jira skill into <project>/.opencode/skills/jira")
	adv := false
	yn(&adv, "Change advanced settings (sbx secret name, credential ref, settings folder, binary source)")
	if adv {
		ask(&o.Service, "sbx secret name for the PAT")
		ask(&o.CredRef, "credential_ref in config.yaml")
		ask(&o.Settings, "Settings folder on this machine (config.yaml, bashrc)")
		ask(&f.binary, "Linux binary source: auto, self, go, release, image")
	}
	if err != nil {
		return err
	}
	o.Settings = absPath(o.Settings)
	if o.CABundle != "" {
		o.CABundle = absPath(o.CABundle)
	}
	envFile := filepath.Join(o.Project, "sbxenv.yaml")
	if b, e := os.ReadFile(envFile); e == nil && !sbx.IsGenerated(b) && !o.Force && !o.KitOnly {
		fmt.Fprintln(a.Err, envFile+" exists and was not generated by jira-cli.")
		choice := "k"
		ask(&choice, "k = keep it and only write the kit, settings and skill (you merge), o = overwrite, a = abort")
		if err != nil {
			return err
		}
		switch strings.ToLower(choice) {
		case "o":
			o.Force = true
		case "a":
			return errors.New("aborted")
		default:
			o.KitOnly = true
		}
	}
	or := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	yesno := map[bool]string{true: "yes", false: "no"}
	fmt.Fprintf(a.Err, "\nSummary\n  project:    %s\n  jira:       %s\n  agent:      %s%s\n  image:      %s\n  name:       %s\n  CA:         %s\n  read-only:  %s\n  skill:      %s\n  token:      sbx secret %q (sbx asks for it if it does not exist yet)\n",
		o.Project, o.BaseURL, o.Agent, map[bool]string{true: " (kit " + o.AgentKit + ")", false: ""}[o.AgentKit != ""],
		or(o.Template, "agent default"), or(o.Name, "sbx default"), or(o.CABundle, "none"),
		yesno[o.ReadOnly], map[bool]string{true: ".opencode/skills/jira", false: "no"}[o.Skill], o.Service)
	if !a.yes {
		ok := true
		yn(&ok, "Proceed")
		if err != nil {
			return err
		}
		if !ok {
			return clierr.New(clierr.LayerSafety, "confirmation_declined", "aborted; nothing was written")
		}
	}
	yn(&f.run, "Start the sandbox when done (sbx env run)")
	if err == nil {
		fmt.Fprintln(a.Err)
	}
	return err
}
