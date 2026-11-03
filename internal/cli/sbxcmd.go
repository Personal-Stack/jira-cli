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
	"sort"
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
	skipChecks                bool
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

How it works, so that anything wrong is reported as soon as it is known and
nothing is written before everything passed:
  1. checks: sbx is installed and signed in, a linux jira binary is available,
     which sbx secrets exist;
  2. answers (prompts without flags in a terminal, or flags), each checked when
     given (project exists, Jira reachable, names and references valid);
     defaults come from this project's previous setup, else from your last
     setup of any project, so a known setup is one confirmation;
  3. sbx validates the complete environment ('sbx env plan' in a scratch
     folder); when sbx knows the agent kit under another name, that name is
     taken over;
  4. the PAT: the sbx secret exists, or sbx stores it now (it prompts);
  5. the files are written and the next steps printed.
Through the Docker wrapper, steps 1, 3 and 4 that need sbx run on the host
right after the container.

It writes:
  <project>/.sbx/jira-cli/          a local sbx kit: linux jira binary, egress rule
                                    for the Jira host, the credential, shell hooks
  <project>/sbxenv.yaml             the environment 'sbx env run' reads
  <project>/.opencode/skills/jira   the agent skill
  <settings>/config.yaml, bashrc    created once, never overwritten; mounted
                                    read-only at the same path in the sandbox

--run starts the sandbox at the end. --dry-run shows the plan and the
sbxenv.yaml without writing anything. See docs/sandbox.md.`,
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
	fl.BoolVar(&f.skipChecks, "skip-checks", false, "skip the checks (sbx installed, Jira reachable) and only write the files")
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

// sbxState is what the checks found out before any question is asked.
type sbxState struct {
	haveSbx   bool            // sbx usable from this process (not in the wrapper's container)
	secrets   map[string]bool // sbx secrets that exist
	bin, src  string          // the linux jira binary for the kit, and where it came from
	planOut   string          // sbx env plan output of the validated environment
	tmp       string
	agentSbx  bool // the agent name was corrected from sbx's answer
	canPrompt bool
}

// runSetupSbx works in this order, so that anything wrong is reported as
// soon as it can be known and nothing is written before everything passed:
//
//  1. checks: sbx is installed, a linux jira binary is available, which sbx
//     secrets exist;
//  2. answers (prompts or flags), each validated when given; defaults from
//     this project's previous setup, else from the last setup of any project;
//  3. sbx validates the complete environment (sbx env plan in a scratch
//     folder); an agent kit name sbx reports is taken over automatically;
//  4. the PAT: the sbx secret exists, or sbx stores it now;
//  5. files are written, then the next steps are printed.
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
	format, err := a.Format()
	if err != nil {
		return err
	}
	ui := a.newSbxUI()
	ui.title("Docker Sandbox for jira-cli")

	// Flags are complete up front: validate them before any work.
	if !interactive {
		a.applySbxDefaults(f, ui)
		if _, err := sbx.NewPlan(*o); err != nil {
			return clierr.Wrap(clierr.LayerUsage, "invalid_setup", err.Error(), nil)
		}
	}

	// 1. Checks.
	st, err := a.sbxChecks(ctx, f, ui, interactive)
	if st.tmp != "" {
		defer os.RemoveAll(st.tmp)
	}
	if err != nil {
		return err
	}

	// 2. Answers.
	if interactive {
		if err := a.askSetupSbx(ctx, f, ui, st); err != nil {
			return err
		}
	} else if !f.skipChecks {
		if err := sbx.CheckReachable(ctx, o.BaseURLOrConfig()); err != nil {
			return clierr.Wrap(clierr.LayerTCP, "jira_unreachable", err.Error(), nil)
		}
	}
	plan, err := sbx.NewPlan(*o)
	if err != nil {
		return clierr.Wrap(clierr.LayerUsage, "invalid_setup", err.Error(), nil)
	}
	if err := plan.CheckEnvFile(); err != nil {
		return clierr.Wrap(clierr.LayerUsage, "foreign_sbxenv", err.Error(), nil)
	}
	if a.dryRun {
		env, _ := plan.EnvFileContent()
		return output.Write(a.Out, map[string]any{
			"dry_run": true, "project": plan.Project, "env_file": plan.EnvFile, "kit": plan.KitDir,
			"settings": plan.Settings, "skill": plan.Skill, "jira_host": plan.Host, "secret": plan.Service,
			"agent": plan.Agent, "name": plan.Name, "credential_env": plan.CredEnv, "sbxenv_yaml": env,
		}, format)
	}

	// 3. sbx validates the whole environment before anything is written.
	if st.haveSbx && !plan.KitOnly {
		ui.section("Validating with sbx")
		if err := a.sbxValidate(ctx, plan, f, st, ui); err != nil {
			return err
		}
	}

	// 4. The PAT.
	secretKnown := false
	if st.haveSbx && !f.skipSecret {
		if !st.secrets[plan.Service] {
			if err := a.sbxSetSecret(ctx, plan.Service, st, ui); err != nil {
				return err
			}
		}
		secretKnown = true
	}

	// 5. Write.
	ui.section("Writing " + plan.Project)
	if err := plan.WriteKit(map[string]string{f.arch: st.bin}, a.Version); err != nil {
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
		"config": plan.ConfigFile, "agent": plan.Agent, "name": plan.Name, "binary_source": st.src,
		"jira_host": plan.Host, "secret": plan.Service, "credential_env": plan.CredEnv, "notes": notes,
		"validated": st.haveSbx && !plan.KitOnly,
	}
	running := st.haveSbx && f.run && !plan.KitOnly
	steps := sbxNextSteps(plan, secretKnown, running)
	ui.section("Next steps")
	switch {
	case a.inContainer():
		result["sbx"] = "jira ran in a container: the wrapper checks the secret and runs sbx env plan on the host"
		ui.note("jira ran in a container: the jira wrapper now checks the sbx secret and runs 'sbx env plan' on your machine.")
	case !st.haveSbx:
		result["sbx"] = "not found"
		ui.note("sbx was not found: install Docker Sandboxes first (https://docs.docker.com/ai/sandboxes/).")
	case running:
		ui.note("Starting the sandbox now. Once it is up:")
	}
	var cmds []string
	for i, step := range steps {
		ui.println(fmt.Sprintf("  %s %s", ui.cyan(fmt.Sprintf("%d.", i+1)), step.what))
		for _, c := range step.cmds {
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

// sbxChecks runs before any question: everything that can fail without
// knowing the answers fails here, immediately.
func (a *App) sbxChecks(ctx context.Context, f *sbxFlags, ui *sbxUI, interactive bool) (*sbxState, error) {
	st := &sbxState{secrets: map[string]bool{}, canPrompt: interactive || a.IsTerminal()}
	ui.section("Checks")
	switch {
	case a.inContainer():
		ui.note("jira runs in the Docker wrapper's container: the sbx checks run on your machine afterwards.")
	case f.skipChecks:
		if _, err := sbxLookPath("sbx"); err == nil {
			st.haveSbx = true
		}
		ui.warn("checks skipped (--skip-checks)")
	default:
		if _, err := sbxLookPath("sbx"); err != nil {
			return st, clierr.New(clierr.LayerConfig, "sbx_not_found", "sbx (Docker Sandboxes) is not installed").
				WithHint("install it (https://docs.docker.com/ai/sandboxes/), or pass --skip-checks to only write the files")
		}
		st.haveSbx = true
		ui.ok("sbx is installed")
	}
	if st.haveSbx {
		out, err := sbxRun(ctx, "", false, "secret", "ls")
		if err != nil {
			return st, clierr.Wrap(clierr.LayerConfig, "sbx_failed", "sbx secret ls failed: "+strings.TrimSpace(string(out)), err).
				WithHint("is sbx signed in? run 'sbx login'")
		}
		for _, line := range strings.Split(string(out), "\n") {
			if fl := strings.Fields(line); len(fl) > 0 && sbx.ValidSecretName(fl[0]) == nil {
				st.secrets[fl[0]] = true
			}
		}
		names := make([]string, 0, len(st.secrets))
		for n := range st.secrets {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) == 0 {
			ui.ok("sbx secrets: none yet")
		} else {
			ui.ok("sbx secrets: " + strings.Join(names, ", "))
		}
	}
	if a.dryRun {
		return st, nil
	}
	tmp, err := os.MkdirTemp("", "jira-setup-sbx-")
	if err != nil {
		return st, err
	}
	st.tmp = tmp
	st.bin, st.src, err = sbx.GetBinary(ctx, sbx.BinaryRequest{
		Source: f.binary, Arch: f.arch, Version: a.Version, Clone: f.sourceDir,
		Image: f.image, File: absIfSet(f.binaryFile), Dir: tmp, Log: ui.note,
	})
	if err != nil {
		return st, clierr.Wrap(clierr.LayerUsage, "no_linux_binary", err.Error(), nil).
			WithHint("run it from a jira-cli clone with Go installed, use a released jira, or pass --binary-file / --binary image")
	}
	ui.ok(fmt.Sprintf("jira for linux/%s %s", f.arch, ui.dim("(source: "+st.src+")")))
	return st, nil
}

// sbxValidate lets sbx check the complete environment in a scratch folder
// before the project is touched. When sbx names the agent kit differently,
// its name is taken over and the check repeated once.
func (a *App) sbxValidate(ctx context.Context, plan *sbx.Plan, f *sbxFlags, st *sbxState, ui *sbxUI) error {
	if err := os.MkdirAll(plan.Settings, 0o755); err != nil { // the env mounts it
		return err
	}
	for attempt := 0; ; attempt++ {
		dir := filepath.Join(st.tmp, fmt.Sprintf("plan-%d", attempt))
		scratch := *plan
		scratch.Project, scratch.KitDir, scratch.EnvFile = dir, filepath.Join(dir, ".sbx", "jira-cli"), filepath.Join(dir, "sbxenv.yaml")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := scratch.WriteKit(map[string]string{f.arch: st.bin}, a.Version); err != nil {
			return err
		}
		if err := scratch.WriteEnvFile(); err != nil {
			return err
		}
		out, err := sbxRun(ctx, dir, false, "env", "plan")
		if err == nil {
			st.planOut = string(out)
			ui.ok("sbx accepts the environment (agent " + plan.Agent + map[bool]string{true: ", sandbox " + plan.Name, false: ""}[plan.Name != ""] + ")")
			return nil
		}
		if name, ok := sbx.AgentNameFromSbx(out); ok && name != plan.Agent && attempt == 0 {
			ui.warn(fmt.Sprintf("sbx names the agent kit %q, not %q: using %q", name, plan.Agent, name))
			plan.Agent, f.opts.Agent, st.agentSbx = name, name, true
			if f.nameDefaulted {
				plan.Name = sbx.DefaultName(name, plan.Project)
				f.opts.Name = plan.Name
			}
			continue
		}
		ui.warn("sbx rejected the environment; nothing was written. sbx said:")
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			ui.println("    " + l)
		}
		first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
		return clierr.Wrap(clierr.LayerConfig, "sbx_rejected", "sbx rejected the environment: "+first, err).
			WithHint("fix the answer it refers to and run jira setup-sbx again; your answers are offered as defaults")
	}
}

// sbxSetSecret stores the PAT with sbx (sbx prompts for it, so it never
// passes through jira-cli).
func (a *App) sbxSetSecret(ctx context.Context, service string, st *sbxState, ui *sbxUI) error {
	if !st.canPrompt {
		return clierr.New(clierr.LayerCredentials, "sbx_secret_missing", "the sbx secret "+service+" does not exist").
			WithHint("run 'sbx secret set " + service + "' in a terminal (it prompts for the PAT), then run this again")
	}
	ui.section("Jira personal access token")
	ui.note(fmt.Sprintf("Storing it as sbx secret %q: sbx prompts for it, and it stays on this machine.", service))
	if _, err := sbxRun(ctx, "", true, "secret", "set", service); err != nil {
		return clierr.Wrap(clierr.LayerCredentials, "sbx_secret_failed", "sbx secret set "+service+" failed", err).
			WithHint("run it yourself (see 'sbx secret --help'), then run this again")
	}
	ui.ok(fmt.Sprintf("sbx secret %q stored", service))
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
		name := ""
		if p.Name != "" {
			name = " " + p.Name
		}
		what := "Start the sandbox" + name + ". The first start builds the kit, and sbx asks you to approve what the" +
			" jira-cli kit needs (it records the approval and asks again only if that changes): egress to " +
			p.Host + ", the sbx secret " + p.Service + " injected for that host, and a read-only mount of " + p.Settings + ":"
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
func (a *App) applySbxDefaults(f *sbxFlags, ui *sbxUI) (known bool) {
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
	if r, ok := sbx.LoadRemembered(o.Settings); ok && r.Service != "" && !set("secret") {
		o.Service = r.Service // the secret name is per user, not per project
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
	return len(from) > 0
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

func (a *App) askSetupSbx(ctx context.Context, f *sbxFlags, ui *sbxUI, st *sbxState) error {
	o := &f.opts
	p := newPrompter(ctx, a.In, ui)
	var err error
	// askValid re-asks until validate accepts the answer (nil: any answer).
	askValid := func(dst *string, q string, validate func(string) error) {
		for err == nil {
			var v string
			if v, err = p.ask(q, *dst); err != nil {
				return
			}
			if validate != nil {
				if verr := validate(v); verr != nil {
					ui.warn(verr.Error())
					continue
				}
			}
			*dst = v
			return
		}
	}
	optional := func(check func(string) error) func(string) error {
		return func(v string) error {
			if v == "" {
				return nil
			}
			return check(v)
		}
	}
	yn := func(dst *bool, q string) {
		if err == nil {
			*dst, err = p.yn(q, *dst)
		}
	}
	ui.note("Enter keeps the value in [brackets], '-' clears an optional one, Ctrl+C quits (nothing is written before you confirm).")

	// Project: must exist; a foreign sbxenv.yaml is decided right away.
	ui.section("Project")
	if a.inContainer() && f.projectSet {
		// The wrapper asked for it on the host (only mounted folders exist here).
		ui.kv("project", o.Project)
	} else {
		askValid(&o.Project, "Project the agent works in (mounted read/write)", func(v string) error {
			v = absPath(v)
			if st, e := os.Stat(v); e != nil || !st.IsDir() {
				if a.inContainer() {
					return errors.New("not a directory: " + v + " (jira runs in a container and only sees mounted folders: cd to the project and re-run)")
				}
				return errors.New("not a directory: " + v)
			}
			return nil
		})
		o.Project = absPath(o.Project)
	}
	if err != nil {
		return err
	}
	envFile := filepath.Join(o.Project, "sbxenv.yaml")
	if b, e := os.ReadFile(envFile); e == nil && !sbx.IsGenerated(b) && !o.Force && !o.KitOnly {
		ui.warn(envFile + " exists and was not generated by jira-cli.")
		choice := "k"
		askValid(&choice, "k = keep it and only write the kit, settings and skill (you merge), o = overwrite, a = abort", func(v string) error {
			if v != "k" && v != "o" && v != "a" {
				return errors.New("answer k, o or a")
			}
			return nil
		})
		if err != nil {
			return err
		}
		switch choice {
		case "o":
			o.Force = true
		case "a":
			return clierr.New(clierr.LayerSafety, "confirmation_declined", "aborted; nothing was written")
		default:
			o.KitOnly = true
		}
	}
	known := a.applySbxDefaults(f, ui)
	if o.BaseURL == "" {
		o.BaseURL = sbx.BaseURLFromConfig(filepath.Join(o.Settings, "config.yaml"))
	}
	checkURL := func(v string) error {
		if _, _, e := sbx.ParseBaseURL(v); e != nil {
			return e
		}
		if f.skipChecks {
			return nil
		}
		return sbx.CheckReachable(ctx, v)
	}

	// Known setup: confirm it in one step instead of answering everything again.
	if known && o.BaseURL != "" && checkURL(o.BaseURL) == nil {
		a.sbxSummary(ui, o)
		same := true
		yn(&same, "Use these settings")
		if err != nil {
			return err
		}
		if same {
			yn(&f.run, "Start the sandbox when done (sbx env run)")
			return err
		}
	}

	ui.section("Jira")
	askValid(&o.BaseURL, "Jira base URL (https://, with the context path if any)", checkURL)
	if err == nil && !f.skipChecks {
		ui.ok("reachable: " + o.BaseURL)
	}

	ui.section("Agent and sandbox")
	ui.note("A built-in sbx agent (e.g. opencode), or an agent kit (registry reference). If sbx knows the")
	ui.note("agent kit under another name, that name is taken over automatically when sbx checks the setup.")
	kitBefore := o.AgentKit
	askValid(&o.AgentKit, "Agent kit reference (empty = built-in agent)", optional(sbx.ValidRef))
	if err == nil && o.AgentKit != "" && o.AgentKit != kitBefore && !f.changed("agent") {
		o.Agent = sbx.AgentFromKit(o.AgentKit)
	}
	askValid(&o.Agent, "Agent name", sbx.ValidName)
	askValid(&o.Template, "Sandbox image (empty = the agent's default image)", optional(sbx.ValidRef))
	if f.nameDefaulted {
		o.Name = sbx.DefaultName(o.Agent, o.Project) // follow the agent chosen above
	}
	if err == nil {
		ui.note("'-' uses sbx's own default, <agent>-<project folder>: also the name 'sbx run' gives a sandbox there.")
	}
	askValid(&o.Name, "Sandbox name", optional(sbx.ValidName))

	ui.section("Options")
	if a.inContainer() {
		if o.CABundle == "" && err == nil {
			ui.note("Company CA: none (in the container, pass --ca-bundle FILE so the wrapper can mount it)")
		}
	} else {
		askValid(&o.CABundle, "Company CA certificate file (empty = none; usually not needed)", optional(func(v string) error { return sbx.ValidFile(absPath(v)) }))
	}
	yn(&o.ReadOnly, "Read-only (the agent cannot change anything in Jira)")
	yn(&o.Skill, "Copy the jira skill into <project>/.opencode/skills/jira")
	adv := false
	yn(&adv, "Change advanced settings (sbx secret name, credential ref, settings folder)")
	if adv {
		if err == nil && len(st.secrets) > 0 {
			names := make([]string, 0, len(st.secrets))
			for n := range st.secrets {
				names = append(names, n)
			}
			sort.Strings(names)
			ui.note("existing sbx secrets: " + strings.Join(names, ", "))
		}
		askValid(&o.Service, "sbx secret name for the PAT", sbx.ValidSecretName)
		askValid(&o.CredRef, "credential_ref in config.yaml", sbx.ValidName)
		if a.inContainer() {
			ui.note("Settings folder: " + o.Settings + " (in the container, change it with --settings DIR)")
		} else {
			askValid(&o.Settings, "Settings folder on this machine (config.yaml, bashrc)", func(v string) error { return sbx.ValidSettingsPath(absPath(v)) })
		}
	}
	if err != nil {
		return err
	}
	o.Settings = absPath(o.Settings)
	if o.CABundle != "" {
		o.CABundle = absPath(o.CABundle)
	}
	a.sbxSummary(ui, o)
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

// sbxSummary prints the choices.
func (a *App) sbxSummary(ui *sbxUI, o *sbx.Options) {
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
	ui.kv("token", fmt.Sprintf("sbx secret %q", o.Service))
	ui.println("")
}
