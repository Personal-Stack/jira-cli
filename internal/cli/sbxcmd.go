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
	// sbxHandOver ends jira by handing the terminal to `sbx args...` in dir.
	sbxHandOver = execSbx
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
	projectSet                bool
	changed                   func(flag string) bool
	nameDefaulted             bool // Name was computed by DefaultName
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
			f.projectSet = cmd.Flags().Changed("project")
			f.changed = cmd.Flags().Changed
			return a.runSetupSbx(cmd.Context(), f, interactive)
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

// prompter asks questions on Err and reads answers from In. Reading happens
// in a goroutine so that Ctrl+C (a cancelled ctx) ends a prompt at once
// instead of waiting for Enter.
type prompter struct {
	ctx   context.Context
	lines chan promptLine
	w     io.Writer
	ui    *sbxUI
}

type promptLine struct {
	s   string
	err error
}

func newPrompter(ctx context.Context, r io.Reader, ui *sbxUI) *prompter {
	p := &prompter{ctx: ctx, lines: make(chan promptLine), w: ui.w, ui: ui}
	go func() {
		defer close(p.lines) // later prompts see end of input instead of waiting
		br := bufio.NewReader(r)
		for {
			s, err := br.ReadString('\n')
			select {
			case p.lines <- promptLine{s, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return p
}

// ask shows cur as the default: Enter keeps it, "-" clears it.
func (p *prompter) ask(q, cur string) (string, error) {
	fmt.Fprint(p.w, p.ui.question(q, cur))
	var line string
	select {
	case <-p.ctx.Done():
		fmt.Fprintln(p.w)
		return "", clierr.Interrupted()
	case l, ok := <-p.lines:
		if !ok || l.err != nil && (l.err != io.EOF || l.s == "") {
			return "", clierr.New(clierr.LayerUsage, "no_answer", "no answer (end of input)")
		}
		line = l.s
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
		v, err := p.ask(q+" "+p.ui.dim("["+hint+"]"), "")
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

func (a *App) runSetupSbx(ctx context.Context, f *sbxFlags, interactive bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if f.changed == nil {
		f.changed = func(string) bool { return false }
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
	if f.binaryFile != "" {
		f.binary = sbx.SourceFile
	}

	ui := a.newSbxUI()
	if interactive {
		if err := a.askSetupSbx(ctx, f, ui); err != nil {
			return err
		}
	} else {
		ui.title("Docker Sandbox for jira-cli")
		a.applySbxDefaults(f, ui)
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

	ui.section("Setting up " + plan.Project)
	tmp, err := os.MkdirTemp("", "jira-setup-sbx-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bin, source, err := sbx.GetBinary(ctx, sbx.BinaryRequest{
		Source: f.binary, Arch: f.arch, Version: a.Version, Clone: f.sourceDir,
		Image: f.image, File: absIfSet(f.binaryFile), Dir: tmp, Log: ui.note,
	})
	if err != nil {
		return clierr.Wrap(clierr.LayerUsage, "no_linux_binary", err.Error(), nil).
			WithHint("run it from a jira-cli clone with Go installed, use a released jira, or pass --binary-file / --binary image")
	}
	ui.ok(fmt.Sprintf("jira for linux/%s %s", f.arch, ui.dim("(source: "+source+")")))
	if err := plan.WriteKit(map[string]string{f.arch: bin}, a.Version); err != nil {
		return err
	}
	ui.ok("kit: " + plan.KitDir)
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
	if err := plan.SaveRemembered(); err != nil {
		notes = append(notes, "warning: could not save the defaults for the next project: "+err.Error())
	}
	for _, n := range notes {
		ui.result(n)
	}

	result := map[string]any{
		"project": plan.Project, "env_file": plan.EnvFile, "kit": plan.KitDir, "settings": plan.Settings,
		"config": plan.ConfigFile, "agent": plan.Agent, "name": plan.Name, "binary_source": source,
		"jira_host": plan.Host, "secret": plan.Service, "credential_env": plan.CredEnv, "notes": notes,
	}
	_, lookErr := sbxLookPath("sbx")
	haveSbx := lookErr == nil
	secretKnown := false
	if haveSbx && !f.skipSecret {
		if err := a.ensureSbxSecret(ctx, plan.Service, interactive || a.IsTerminal(), ui); err != nil {
			return err
		}
		secretKnown = true
	}
	if haveSbx && !plan.KitOnly {
		ui.section("sbx env plan " + ui.dim("(a preview; nothing is created yet)"))
		out, err := sbxRun(ctx, plan.Project, false, "env", "plan")
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			ui.println("  " + l)
		}
		if err != nil {
			return clierr.Wrap(clierr.LayerUsage, "sbx_plan_failed", "sbx env plan failed (see above)", err)
		}
	}
	running := haveSbx && f.run && !plan.KitOnly
	steps := sbxNextSteps(plan, secretKnown, running)
	ui.section("Next steps")
	switch {
	case a.inContainer():
		result["sbx"] = "jira ran in a container: run the next steps on the host"
		ui.note("jira ran in a container, so run these on your machine:")
	case !haveSbx:
		result["sbx"] = "not found"
		ui.note("sbx was not found: install Docker Sandboxes first (https://docs.docker.com/ai/sandboxes/).")
	case running:
		ui.note("Starting the sandbox now. Once it is up:")
	}
	var cmds []string
	for i, st := range steps {
		ui.println(fmt.Sprintf("  %s %s", ui.cyan(fmt.Sprintf("%d.", i+1)), st.what))
		for _, c := range st.cmds {
			ui.println("       " + ui.cmd(c))
			cmds = append(cmds, c)
		}
	}
	ui.println("")
	result["next"] = cmds
	// The guided run is for people: the styled output above is the result.
	// With flags (scripts) or an explicit -o, the structured result follows.
	if !interactive || f.changed("output") {
		if err := output.Write(a.Out, result, format); err != nil {
			return err
		}
	}
	if running {
		if err := sbxHandOver(plan.Project, "env", "run"); err != nil {
			return clierr.Wrap(clierr.LayerUsage, "sbx_run_failed", "sbx env run failed", err)
		}
	}
	return nil
}

type sbxStep struct {
	what string
	cmds []string
}

// sbxNextSteps is what the user runs after setup-sbx, on the host.
func sbxNextSteps(p *sbx.Plan, secretKnown, running bool) []sbxStep {
	cd := "cd " + shellQuote(p.Project) + " && "
	var steps []sbxStep
	if !secretKnown {
		steps = append(steps, sbxStep{"Store the Jira PAT in sbx, if 'sbx secret ls' does not list " + p.Service + " (sbx prompts for it):",
			[]string{"sbx secret ls", "sbx secret set " + p.Service}})
	}
	if p.KitOnly {
		steps = append(steps, sbxStep{"Merge the kit, the settings mount, the env and the binding into " + p.EnvFile + " (docs/sandbox.md), then start it:",
			[]string{cd + "sbx env run"}})
	} else if !running {
		what := "Start the sandbox (the first start builds the kit):"
		if p.Name != "" {
			what = "Start the sandbox " + p.Name + " (the first start builds the kit):"
		}
		steps = append(steps, sbxStep{what, []string{cd + "sbx env run"}})
	}
	steps = append(steps,
		sbxStep{"Check jira inside it, from another terminal: expect credentials " + "env:" + p.CredEnv + " and authentication ok:",
			[]string{cd + "sbx env exec -- bash -lc 'jira doctor'"}},
		sbxStep{"Ask the agent, e.g. \"list my 5 most recently updated Jira issues\". It should use jira and the skill on its own.", nil},
		sbxStep{"If the sandbox does not start ('failed to run sandbox container'): look at the plan and at existing sandboxes;" +
			" a sandbox with the same name from an earlier run must be removed (sbx rm NAME) or the setup re-run with another --name:",
			[]string{cd + "sbx env plan", "sbx ls", "sbx diagnose"}},
	)
	return steps
}

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._/-~+:@%=,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// applySbxDefaults fills what no flag set: from this project's previous
// setup if there is one, otherwise from the last setup of any project
// (<settings>/setup.yaml), so an agent kit's name carries over to new
// projects. A new project gets its own sandbox name (DefaultName).
func (a *App) applySbxDefaults(f *sbxFlags, ui *sbxUI) {
	o, set := &f.opts, f.changed
	use := func(flag string, dst *string, v string) {
		if !set(flag) && v != "" {
			*dst = v
		}
	}
	var from []string
	if prev, ok := sbx.ReadPrevious(filepath.Join(o.Project, "sbxenv.yaml")); ok {
		if !set("agent-kit") {
			use("agent", &o.Agent, prev.Agent)
		}
		use("agent-kit", &o.AgentKit, prev.AgentKit)
		use("template", &o.Template, prev.Template)
		if !set("name") {
			o.Name = prev.Name // keep the existing sandbox's name, even sbx's default
		}
		if !set("allow-writes") {
			o.ReadOnly = prev.ReadOnly
		}
		from = append(from, "the previous setup of this project")
	} else {
		if r, ok := sbx.LoadRemembered(o.Settings); ok {
			if !set("agent-kit") {
				use("agent", &o.Agent, r.Agent)
			}
			use("agent-kit", &o.AgentKit, r.AgentKit)
			use("template", &o.Template, r.Template)
			if r.ReadOnly != nil && !set("allow-writes") {
				o.ReadOnly = *r.ReadOnly
			}
			if r.Skill != nil && !set("no-skill") {
				o.Skill = *r.Skill
			}
			from = append(from, "your last setup ("+filepath.Join(o.Settings, sbx.RememberFile)+")")
		}
		if !set("name") {
			f.nameDefaulted = true
		}
	}
	if set("agent-kit") && !set("agent") {
		o.Agent = sbx.AgentFromKit(o.AgentKit)
	}
	if f.nameDefaulted {
		o.Name = sbx.DefaultName(o.Agent, o.Project)
	}
	if len(from) > 0 {
		ui.note(fmt.Sprintf("Defaults from %s: agent %s%s%s", from[0], o.Agent,
			map[bool]string{true: ", kit " + o.AgentKit, false: ""}[o.AgentKit != ""],
			map[bool]string{true: ", image " + o.Template, false: ""}[o.Template != ""]))
	}
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
func (a *App) ensureSbxSecret(ctx context.Context, service string, canPrompt bool, ui *sbxUI) error {
	out, err := sbxRun(ctx, "", false, "secret", "ls")
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == service {
				ui.ok(fmt.Sprintf("sbx secret %q exists %s", service, ui.dim("(replace it with: sbx secret set "+service+")")))
				return nil
			}
		}
	}
	if !canPrompt {
		return clierr.New(clierr.LayerCredentials, "sbx_secret_missing", "the sbx secret "+service+" does not exist").
			WithHint("run 'sbx secret set " + service + "' in a terminal (it prompts for the PAT), then re-run with --skip-secret")
	}
	ui.section("Jira personal access token")
	ui.note(fmt.Sprintf("Storing it as sbx secret %q: sbx prompts for it, and it stays on this machine.", service))
	if _, err := sbxRun(ctx, "", true, "secret", "set", service); err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "sbx_secret_failed", "sbx secret set "+service+" failed", err).
			WithHint("run it yourself (see 'sbx secret --help'), then re-run with --skip-secret")
	}
	ui.ok(fmt.Sprintf("sbx secret %q stored", service))
	return nil
}

func (a *App) askSetupSbx(ctx context.Context, f *sbxFlags, ui *sbxUI) error {
	o := &f.opts
	p := newPrompter(ctx, a.In, ui)
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
	ui.title("Docker Sandbox for jira-cli")
	ui.note("Enter keeps the value in [brackets], '-' clears an optional one, Ctrl+C quits (nothing is written before you confirm).")
	ui.section("Project")
	// In the wrapper's container only mounted folders exist: the wrapper asks
	// for the project on the host and passes --project.
	askProject := !(a.inContainer() && f.projectSet)
	if !askProject {
		ui.kv("project", absPath(o.Project))
	}
	for askProject {
		ask(&o.Project, "Project the agent works in (mounted read/write)")
		if err != nil {
			return err
		}
		o.Project = absPath(o.Project)
		if st, e := os.Stat(o.Project); e == nil && st.IsDir() {
			break
		}
		ui.warn("not a directory: " + o.Project)
		if a.inContainer() {
			ui.note("jira runs in a container and only sees the current directory: cd to the project and re-run")
		}
	}
	a.applySbxDefaults(f, ui)
	ui.section("Jira")
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
		ui.warn("must start with https://")
	}
	ui.section("Agent and sandbox")
	ui.note("A built-in sbx agent (e.g. opencode), or an agent kit (registry reference).")
	kitBefore := o.AgentKit
	ask(&o.AgentKit, "Agent kit reference (empty = built-in agent)")
	if err == nil && o.AgentKit != "" && o.AgentKit != kitBefore && !f.changed("agent") {
		o.Agent = sbx.AgentFromKit(o.AgentKit)
	}
	ask(&o.Agent, "Agent name (for a kit: the name sbx reports in 'agent kit name \"...\"')")
	ask(&o.Template, "Sandbox image (empty = the agent's default image)")
	if f.nameDefaulted {
		o.Name = sbx.DefaultName(o.Agent, o.Project) // follow the agent chosen above
	}
	if err == nil {
		ui.note("Sandbox name: '-' uses sbx's own default, <agent>-<project folder>, which is also the name")
		ui.note("of a sandbox you start with 'sbx run' in that folder.")
	}
	ask(&o.Name, "Sandbox name")
	if err == nil {
		ui.section("Options")
	}
	if a.inContainer() {
		if o.CABundle == "" && err == nil {
			ui.note("Company CA: none (in the container, pass --ca-bundle FILE so the wrapper can mount it)")
		}
	} else {
		ask(&o.CABundle, "Company CA certificate file (empty = none; usually not needed)")
	}
	yn(&o.ReadOnly, "Read-only (the agent cannot change anything in Jira)")
	yn(&o.Skill, "Copy the jira skill into <project>/.opencode/skills/jira")
	adv := false
	yn(&adv, "Change advanced settings (sbx secret name, credential ref, settings folder, binary source)")
	if adv {
		ask(&o.Service, "sbx secret name for the PAT")
		ask(&o.CredRef, "credential_ref in config.yaml")
		if a.inContainer() {
			ui.note("Settings folder: " + o.Settings + " (in the container, change it with --settings DIR)")
		} else {
			ask(&o.Settings, "Settings folder on this machine (config.yaml, bashrc)")
		}
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
		ui.warn(envFile + " exists and was not generated by jira-cli.")
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
	ui.section("Summary")
	ui.kv("project", o.Project)
	ui.kv("jira", o.BaseURL)
	ui.kv("agent", o.Agent+map[bool]string{true: ui.dim(" (kit " + o.AgentKit + ")"), false: ""}[o.AgentKit != ""])
	ui.kv("image", or(o.Template, "agent default"))
	ui.kv("name", or(o.Name, "sbx default"))
	ui.kv("CA", or(o.CABundle, "none"))
	ui.kv("read-only", yesno[o.ReadOnly])
	ui.kv("skill", map[bool]string{true: ".opencode/skills/jira", false: "no"}[o.Skill])
	ui.kv("token", fmt.Sprintf("sbx secret %q", o.Service)+ui.dim(" (sbx asks for it if it does not exist yet)"))
	ui.println("")
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
	return err
}
