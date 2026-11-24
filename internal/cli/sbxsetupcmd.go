package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/sbx"
)

// The actions of `jiracli sbx setup`.
const (
	actStatus  = "status"
	actInit    = "init"
	actUpdate  = "update"
	actVerify  = "verify"
	actMigrate = "migrate"
	actRemove  = "remove"
)

var setupActions = []string{actStatus, actInit, actUpdate, actVerify, actMigrate, actRemove}

type sbxSetupFlags struct {
	action, target, project string
	kit                     sbxKitFlags
	readOnly                bool // --read-only (update: keep read-only even if the kit allowed writes)
}

func (a *App) sbxSetupCmd() *cobra.Command {
	f := &sbxSetupFlags{}
	c := &cobra.Command{
		Use:   "setup",
		Short: "Wizard: inspect, set up, update, verify or remove jira-cli in sbx sandboxes",
		Long: `Inspects what is set up for jira-cli in Docker Sandboxes on this machine and
in a project, then offers what fits:

  status   only the inspection (what is set up, problems found and their fixes)
  init     set up the jira-cli kit: for every sandbox (~/.sbxenv.yaml) or for
           this project only (<project>/sbxenv.yaml); your other entries stay
  update   refresh the kit to this jiracli and fix what the inspection found,
           keeping its settings (host, secret, read-only)
  verify   check the setup: files, the kit format, the sbx secret,
           'sbx env plan', and in a running sandbox the JIRA_* variables, the
           binary and 'jiracli doctor'
  migrate  move a project set up with 'jiracli setup-sbx' to the shared kit
  remove   take the kit out of the environment files

The inspection reads both environment files and the kits they list, so it
knows the agent's kit format: sbx does not mix v3 kits (the built-in agents)
with v1/v2 kits (spec.yaml), and the jira-cli kit is written in the format
that fits (--kit-schema to choose). Every change is shown as a diff first;
--dry-run (or 'dry run' in the wizard) shows it and writes nothing.

Without flags in a terminal it asks; otherwise give --action.`,
		Example: `  jiracli sbx setup
  jiracli sbx setup --action status -o json
  jiracli sbx setup --action init --target user --base-url https://jira.company.internal --yes
  jiracli sbx setup --action update --dry-run
  jiracli sbx setup --action verify --project ~/Projects/my-work`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := &sbxWizard{a: a, f: f, set: cmd.Flags().Changed, ui: a.newTermUI()}
			return w.run(cmd.Context())
		},
	}
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	if cfgHome == "" {
		home, _ := os.UserHomeDir()
		cfgHome = filepath.Join(home, ".config")
	}
	fl := c.Flags()
	fl.StringVar(&f.action, "action", "", "status, init, update, verify, migrate or remove (default: ask in a terminal, else status)")
	fl.StringVar(&f.target, "target", "", "init: user (~/.sbxenv.yaml, every sandbox) or project (<project>/sbxenv.yaml)")
	fl.StringVar(&f.project, "project", "", "project folder (default: current directory)")
	fl.StringVar(&f.kit.o.BaseURL, "base-url", "", "Jira base URL (default: the kit's host, or <settings>/config.yaml)")
	fl.StringVar(&f.kit.o.Secret, "secret", "", "sbx secret that holds the PAT (default: the kit's, else jira)")
	fl.BoolVar(&f.kit.allowWrites, "allow-writes", false, "let the agent write to Jira (default: read-only, or the kit's setting on update)")
	fl.BoolVar(&f.readOnly, "read-only", false, "update: make the kit read-only")
	fl.IntVar(&f.kit.kitSchema, "kit-schema", 0, "kit format: 3 or 2 (default: the agent's, else 3 with v2 if sbx refuses it)")
	fl.BoolVar(&f.kit.noSkill, "no-skill", false, "do not bundle the jira skill in the kit")
	fl.StringVar(&f.kit.o.Settings, "settings", filepath.Join(cfgHome, "jira-cli-sbx"), "folder for the kit, config.yaml and bashrc (mounted read-only)")
	fl.StringVar(&f.kit.binaryFile, "binary-file", "", "linux jiracli binary for the kit (default: this jiracli, a build from a clone, or the release)")
	fl.StringVar(&f.kit.sourceDir, "source-dir", "", "jira-cli clone to build the linux binary from")
	fl.StringVar(&f.kit.agent, "agent", "", "agent for a new project file, and for the 'sbx env plan' check when no file sets one (default: opencode)")
	fl.BoolVar(&f.kit.skipChecks, "skip-checks", false, "init/update: no sbx, reachability or secret checks")
	for _, d := range []string{"settings", "source-dir", "project"} {
		_ = c.MarkFlagDirname(d)
	}
	_ = c.MarkFlagFilename("binary-file")
	c.MarkFlagsMutuallyExclusive("allow-writes", "read-only")
	return c
}

// sbxWizard is one run of `jiracli sbx setup`.
type sbxWizard struct {
	a   *App
	f   *sbxSetupFlags
	set func(flag string) bool
	ui  *termUI
	q   *form

	home, project, settings string
	in                      *sbx.Inspection
	sbxPath                 string          // "" if sbx is not installed
	secrets                 map[string]bool // nil if not known
}

func (w *sbxWizard) interactive() bool { return w.a.IsTerminal() && !w.set("action") }

func (w *sbxWizard) run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	format, err := w.a.Format()
	if err != nil {
		return err
	}
	if w.f.action != "" && !slices.Contains(setupActions, w.f.action) {
		return clierr.New(clierr.LayerUsage, "invalid_usage", "--action: use one of "+strings.Join(setupActions, ", "))
	}
	if w.f.target != "" && w.f.target != "user" && w.f.target != "project" {
		return clierr.New(clierr.LayerUsage, "invalid_usage", "--target: use user or project")
	}
	if w.a.inContainer() {
		return clierr.New(clierr.LayerUsage, "unsupported_in_container",
			"jiracli sbx setup reads and edits your sbx environment files and needs sbx: run a native jiracli on the host (install.sh without --docker)")
	}
	if w.home, err = os.UserHomeDir(); err != nil {
		return err
	}
	w.project = w.f.project
	if w.project == "" {
		w.project, _ = os.Getwd()
	}
	w.project, w.settings = absPath(w.project), absPath(w.f.kit.o.Settings)
	w.ui.title("jiracli sbx setup", "jira-cli in Docker Sandboxes")
	w.inspect(ctx)
	w.report()

	action := w.f.action
	if action == "" && !w.interactive() {
		action = actStatus
	}
	if action == "" {
		w.q = w.a.form(ctx, w.ui)
		action = w.ask()
		if w.q.err != nil {
			return w.q.err
		}
	}
	if w.q == nil {
		w.q = w.a.form(ctx, w.ui)
	}
	switch action {
	case actStatus:
		if !w.a.IsTerminal() || w.set("output") {
			return output.Write(w.a.Out, w.statusJSON(), format)
		}
		return nil
	case actVerify:
		return w.verify(ctx, format)
	case actMigrate:
		return w.migrate(ctx)
	case actRemove:
		return w.remove(ctx)
	case actUpdate:
		return w.update(ctx)
	default:
		return w.initKit(ctx)
	}
}

// inspect reads the files, and asks sbx what only it knows.
func (w *sbxWizard) inspect(ctx context.Context) {
	w.in = sbx.Inspect(w.home, w.project, w.settings, w.a.Version)
	if p, err := sbxLookPath("sbx"); err == nil {
		w.sbxPath = p
		if out, err := sbxRun(ctx, "", false, "secret", "ls"); err == nil {
			w.secrets = map[string]bool{}
			for _, line := range strings.Split(string(out), "\n") {
				if f := strings.Fields(line); len(f) > 0 && sbx.ValidSecretName(f[0]) == nil {
					w.secrets[f[0]] = true
				}
			}
		}
	}
	add := func(sev, text, fix string) {
		w.in.Issues = append(w.in.Issues, sbx.Issue{Severity: sev, Text: text, Fix: fix})
	}
	switch {
	case w.sbxPath == "":
		add("error", "sbx (Docker Sandboxes) is not installed", "install it: https://docs.docker.com/ai/sandboxes/install/")
	case w.secrets == nil:
		add("warn", "'sbx secret ls' failed: is sbx signed in?", "run 'sbx login'")
	default:
		if k := w.jiraKit(); k != nil {
			if s := w.kitSecret(k); !w.secrets[s] {
				add("error", fmt.Sprintf("the sbx secret %q (the Jira PAT) does not exist", s), "run 'sbx secret set "+s+"' (it prompts for the token)")
			}
		}
	}
}

// jiraKit is the jira-cli kit entry in effect: the project's, else the user's.
func (w *sbxWizard) jiraKit() *sbx.KitInfo {
	if k := w.in.Proj.JiraKit(); k != nil {
		return k
	}
	return w.in.User.JiraKit()
}

func (w *sbxWizard) kitSecret(k *sbx.KitInfo) string {
	if s := k.Args["secret"]; s != "" {
		return s
	}
	return "jira"
}

func (w *sbxWizard) report() {
	ui, in := w.ui, w.in
	ui.section("What is set up")
	if w.sbxPath != "" {
		ui.kv("sbx", w.sbxPath)
	} else {
		ui.kv("sbx", "not installed")
	}
	for _, e := range []*sbx.EnvInfo{&in.User, &in.Proj} {
		ui.kv("file", e.Path)
		switch {
		case !e.Exists:
			ui.note("             does not exist")
			continue
		case e.Error != "":
			ui.note("             " + e.Error)
			continue
		}
		if e.Generated {
			ui.note("             generated by jiracli setup-sbx")
		}
		if e.Agent != "" {
			ui.note("             agent: " + e.Agent)
		}
		for _, k := range e.Kits {
			tag := k.Family()
			if tag == "" {
				tag = "format unknown"
			}
			switch {
			case k.Jira:
				tag += ", jira-cli kit" + argsText(k.Args)
			case k.Legacy:
				tag += ", setup-sbx's jira-cli kit"
			}
			ui.note(fmt.Sprintf("             kit %s (%s)", k.Source, tag))
		}
	}
	if in.StrayYml != "" {
		ui.kv("ignored", in.StrayYml+" (sbx reads only ~/"+sbx.UserEnvName+")")
	}
	kit := "not generated"
	if in.Kit.Exists {
		kit = fmt.Sprintf("v%d, jiracli %s, binary %s", in.Kit.Schema, or(in.Kit.Version, "?"), map[bool]string{true: "present", false: "missing"}[in.Kit.Binary])
	}
	ui.kv("kit", in.Kit.Dir+": "+kit)
	if in.Config.BaseURL != "" {
		ui.kv("config", fmt.Sprintf("%s: %s%s", in.Config.Path, in.Config.BaseURL, map[bool]string{true: ", read_only", false: ""}[in.Config.ReadOnly]))
	}
	agentFmt := "unknown"
	if in.AgentSchema == 3 {
		agentFmt = "v3"
	} else if in.AgentSchema == 2 {
		agentFmt = "v1/v2"
	}
	ui.kv("agent", fmt.Sprintf("%s, kits %s (%s)", or(in.Agent, "not set"), agentFmt, in.AgentSchemaWhy))

	ui.section("Findings")
	if len(in.Issues) == 0 {
		ui.ok("nothing to fix")
	}
	for _, is := range in.Issues {
		text := is.Text
		if is.Fix != "" {
			text += ui.dim(" → " + is.Fix)
		}
		switch is.Severity {
		case "error":
			ui.bad(text)
		case "warn":
			ui.warn(text)
		default:
			ui.note("i " + text)
		}
	}
}

func argsText(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}
	var parts []string
	for _, k := range []string{"host", "port", "secret", "readOnly"} {
		if v, ok := args[k]; ok {
			parts = append(parts, k+"="+v)
		}
	}
	return ": " + strings.Join(parts, " ")
}

func (w *sbxWizard) hasError() bool {
	for _, is := range w.in.Issues {
		if is.Severity == "error" {
			return true
		}
	}
	return false
}

// ask offers the actions that fit what was found.
func (w *sbxWizard) ask() string {
	in := w.in
	var opts []choice
	def := actInit
	shared := len(in.JiraTargets()) > 0
	if in.Proj.LegacyKit() != nil {
		opts = append(opts, choice{actMigrate, "migrate: move this project from setup-sbx's kit to the shared kit"})
		def = actMigrate
	}
	if shared {
		opts = append(opts, choice{actUpdate, "update: refresh the kit to this jiracli and fix the findings, keeping its settings"},
			choice{actVerify, "verify: check the setup, and jira-cli in a running sandbox"})
		if in.Proj.LegacyKit() == nil {
			def = map[bool]string{true: actUpdate, false: actVerify}[w.hasError() || hasInfoUpdate(in)]
		}
	}
	opts = append(opts, choice{actInit, map[bool]string{true: "init: set up again, with new settings or another target",
		false: "init: set up the jira-cli kit"}[shared]})
	if shared {
		opts = append(opts, choice{actRemove, "remove: take the kit out of the environment files"})
	}
	opts = append(opts, choice{actStatus, "nothing: only this report"})
	w.ui.section("What to do")
	w.q.choose(&def, "Action", opts)
	return def
}

func hasInfoUpdate(in *sbx.Inspection) bool {
	for _, is := range in.Issues {
		if strings.Contains(is.Fix, "action update") {
			return true
		}
	}
	return false
}

// applyMode asks whether to write or only show the changes; --dry-run, and
// --yes without a terminal, answer it.
func (w *sbxWizard) applyMode() bool {
	if w.a.dryRun {
		return false
	}
	if !w.interactive() {
		return true
	}
	mode := "apply"
	w.q.choose(&mode, "Write the changes, or only show them", []choice{
		{"apply", "apply: show the changes, confirm, write"},
		{"dry-run", "dry run: show the changes, write nothing"}})
	if mode == "dry-run" {
		w.a.dryRun = true
	}
	return mode == "apply"
}

// kitRun runs the kit engine (as `jiracli sbx kit`) with f.
func (w *sbxWizard) kitRun(ctx context.Context, f sbxKitFlags) error {
	f.o.Settings, f.embedded = w.settings, true
	if f.agent == "" {
		f.agent = or(w.f.kit.agent, "opencode")
	}
	set := func(flag string) bool { return w.set(flag) }
	k := &sbxKit{a: w.a, f: &f, set: set, ui: w.ui}
	return k.run(ctx)
}

func (w *sbxWizard) targetFile(target string) string {
	if target == "project" {
		return filepath.Join(w.project, "sbxenv.yaml")
	}
	return filepath.Join(w.home, sbx.UserEnvName)
}

// legacyFirst migrates the project away from setup-sbx's kit when it has
// one: sbx refuses it next to the shared kit.
func (w *sbxWizard) legacyFirst(ctx context.Context) error {
	if w.in.Proj.LegacyKit() == nil {
		return nil
	}
	w.ui.warn(w.in.Proj.Path + " lists setup-sbx's own jira-cli kit; sbx refuses it next to the shared kit, so it is removed first")
	return w.migrateFile(ctx)
}

func (w *sbxWizard) initKit(ctx context.Context) error {
	in, f := w.in, w.f.kit
	target := w.f.target
	if target == "" {
		target = "user"
		if in.User.JiraKit() == nil && in.Proj.JiraKit() != nil {
			target = "project"
		}
	}
	base := f.o.BaseURL
	if base == "" {
		base = in.Config.BaseURL
		if k := w.jiraKit(); k != nil && k.Args["host"] != "" {
			base = baseFromArgs(k.Args, in.Config.BaseURL)
		}
	}
	secret := f.o.Secret
	if secret == "" {
		secret = "jira"
		if k := w.jiraKit(); k != nil {
			secret = w.kitSecret(k)
		} else if d := sbx.LoadDefaults(w.settings, w.settings); d.Secret != "" {
			secret = d.Secret
		}
	}
	readOnly := !f.allowWrites
	if !w.set("allow-writes") {
		if k := w.jiraKit(); k != nil && k.Args["readOnly"] == "0" {
			readOnly = false
		}
	}
	schema := w.schemaChoice()
	agent := w.f.kit.agent
	if w.interactive() {
		w.ui.section("Settings")
		w.q.choose(&target, "Where", []choice{
			{"user", "every sandbox: ~/" + sbx.UserEnvName},
			{"project", "this project only: " + w.targetFile("project")}})
		w.q.url(&base, "Jira base URL", func(v string) error {
			if _, _, err := sbx.ParseBaseURL(v); err != nil || w.f.kit.skipChecks {
				return err
			}
			return sbx.CheckReachable(ctx, v)
		})
		w.q.yesno(&readOnly, "Read-only (the agent cannot change anything in Jira)")
		w.q.text(&secret, "sbx secret holding the Jira PAT", sbx.ValidSecretName)
		sch := fmt.Sprint(schema)
		w.q.choose(&sch, "Kit format", []choice{
			{"0", "auto: v3, and v2 if sbx refuses v3"},
			{"3", "v3: the built-in agents (opencode, claude, ...)"},
			{"2", "v2: agents whose kits are spec.yaml (v1/v2)"}})
		fmt.Sscan(sch, &schema)
		if target == "project" && !in.Proj.Exists && in.User.Agent == "" {
			agent = or(agent, "opencode")
			w.q.text(&agent, "Agent for the new "+w.targetFile("project"), sbx.ValidAgent)
		}
		if w.q.err != nil {
			return w.q.err
		}
	}
	if base == "" {
		return clierr.New(clierr.LayerUsage, "invalid_usage", "no Jira base URL: pass --base-url")
	}
	w.applyMode()
	if !w.a.dryRun {
		if err := w.legacyFirst(ctx); err != nil {
			return err
		}
	}
	kf := f
	kf.o.BaseURL, kf.o.Secret, kf.allowWrites, kf.kitSchema = sbx.NormalizeBaseURL(base), secret, !readOnly, schema
	kf.envFile = w.targetFile(target)
	if target == "project" && !in.Proj.Exists {
		if in.User.Agent != "" {
			agent = "" // the user's file sets it
		} else {
			agent = or(agent, "opencode")
		}
		kf.seed = sbx.NewProjectEnv(agent)
	}
	if !w.interactive() {
		kf.agent = agent
	}
	return w.kitRun(ctx, kf)
}

// schemaChoice is the kit format to ask sbx for: --kit-schema, else the
// agent's when known, else 0 (v3, v2 if refused).
func (w *sbxWizard) schemaChoice() int {
	if w.f.kit.kitSchema != 0 {
		return w.f.kit.kitSchema
	}
	return w.in.AgentSchema
}

func baseFromArgs(args map[string]string, configURL string) string {
	host := args["host"]
	if configURL != "" {
		if h, _, err := sbx.ParseBaseURL(sbx.NormalizeBaseURL(configURL)); err == nil && strings.EqualFold(h, host) {
			return sbx.NormalizeBaseURL(configURL) // keeps the context path
		}
	}
	if p := args["port"]; p != "" && p != "443" {
		return "https://" + host + ":" + p
	}
	return "https://" + host
}

func (w *sbxWizard) update(ctx context.Context) error {
	in := w.in
	targets := in.JiraTargets()
	if len(targets) == 0 {
		if in.Proj.LegacyKit() != nil {
			return clierr.New(clierr.LayerUsage, "nothing_to_update",
				"this project uses setup-sbx's own kit: run 'jiracli setup-sbx' to update it, or migrate it ('jiracli sbx setup --action migrate')")
		}
		return clierr.New(clierr.LayerUsage, "nothing_to_update", "no environment file lists the jira-cli kit: run 'jiracli sbx setup --action init'")
	}
	w.applyMode()
	if !w.a.dryRun {
		if err := w.legacyFirst(ctx); err != nil {
			return err
		}
	}
	for _, file := range targets {
		e := &in.User
		if file == in.Proj.Path {
			e = &in.Proj
		}
		k := e.JiraKit()
		w.ui.section("Updating " + file)
		kf := w.f.kit
		if kf.o.BaseURL == "" {
			kf.o.BaseURL = baseFromArgs(k.Args, in.Config.BaseURL)
		}
		if kf.o.Secret == "" {
			kf.o.Secret = w.kitSecret(k)
		}
		if !w.set("allow-writes") && !w.f.readOnly {
			kf.allowWrites = k.Args["readOnly"] == "0"
		}
		kf.kitSchema = w.schemaChoice()
		kf.envFile = file
		if err := w.kitRun(ctx, kf); err != nil {
			return err
		}
	}
	return nil
}

func (w *sbxWizard) remove(ctx context.Context) error {
	targets := w.in.JiraTargets()
	if len(targets) == 0 {
		w.ui.ok("no environment file lists the jira-cli kit; nothing to remove")
		return nil
	}
	if w.interactive() && len(targets) > 1 {
		which := "all"
		opts := []choice{{"all", "both files"}}
		for _, t := range targets {
			opts = append(opts, choice{t, t})
		}
		w.q.choose(&which, "Remove it from", opts)
		if w.q.err != nil {
			return w.q.err
		}
		if which != "all" {
			targets = []string{which}
		}
	}
	w.applyMode()
	for _, file := range targets {
		kf := w.f.kit
		kf.remove, kf.envFile = true, file
		kf.o.BaseURL = "https://" + or(w.jiraKit().Args["host"], "jira.invalid") // only for the plan's validation
		if err := w.kitRun(ctx, kf); err != nil {
			return err
		}
	}
	return nil
}

func (w *sbxWizard) migrate(ctx context.Context) error {
	if w.in.Proj.LegacyKit() == nil {
		w.ui.ok(w.in.Proj.Path + " does not list setup-sbx's kit; nothing to migrate")
		return nil
	}
	w.applyMode()
	if err := w.migrateFile(ctx); err != nil || w.a.dryRun {
		return err
	}
	if len(w.in.JiraTargets()) > 0 {
		w.ui.ok("the shared kit is already listed; recreate the sandbox: sbx env rm; sbx env run")
		return nil
	}
	setUp := true
	if w.interactive() {
		w.q.yesno(&setUp, "Set up the shared kit now")
		if w.q.err != nil {
			return w.q.err
		}
	}
	if !setUp {
		return nil
	}
	w.in = sbx.Inspect(w.home, w.project, w.settings, w.a.Version)
	return w.initKit(ctx)
}

// migrateFile removes setup-sbx's kit (and the JIRA_* variables it set) from
// the project file.
func (w *sbxWizard) migrateFile(ctx context.Context) error {
	path := w.in.Proj.Path
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, removed, err := sbx.MigrateLegacy(old, w.project)
	if err != nil {
		return clierr.Wrap(clierr.LayerConfig, "invalid_sbxenv", err.Error(), nil)
	}
	if !removed {
		return nil
	}
	k := &sbxKit{a: w.a, ui: w.ui, f: &sbxKitFlags{embedded: true}, set: w.set}
	k.showDiff(path, old, out)
	if w.a.dryRun {
		w.ui.note("Dry run: nothing was written.")
		return nil
	}
	if err := k.confirm(ctx, "Update "+path+" (the previous version is kept as sbxenv.yaml.bak)"); err != nil {
		return err
	}
	if err := writeWithBackup(path, old, out); err != nil {
		return err
	}
	w.ui.ok("removed setup-sbx's kit from " + path)
	if lk := w.in.Proj.LegacyKit(); lk != nil && lk.Dir != "" {
		w.ui.note(lk.Dir + " is no longer used; delete it when you like")
	}
	return nil
}

// verifyCheck is one line of verify's result.
type verifyCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, fail, skip
	Detail string `json:"detail,omitempty"`
}

func (w *sbxWizard) verify(ctx context.Context, format output.Format) error {
	in, ui := w.in, w.ui
	var checks []verifyCheck
	add := func(name, status, detail string) {
		checks = append(checks, verifyCheck{name, status, detail})
		text := name
		if detail != "" {
			text += ui.dim(": " + detail)
		}
		switch status {
		case "ok":
			ui.ok(text)
		case "fail":
			ui.bad(text)
		default:
			ui.note("- " + text)
		}
	}
	ui.section("Verify")
	kit := w.jiraKit()
	legacy := in.Proj.LegacyKit()
	if kit == nil && legacy == nil {
		add("jira-cli kit listed", "fail", "neither "+in.User.Path+" nor "+in.Proj.Path+" lists it; run init")
		return w.verifyResult(checks, format)
	}
	errs := 0
	for _, is := range in.Issues {
		if is.Severity == "error" {
			errs++
		}
	}
	if errs == 0 {
		add("files and kit format", "ok", "")
	} else {
		add("files and kit format", "fail", fmt.Sprintf("%d problem(s), see Findings", errs))
	}
	if w.sbxPath == "" {
		add("sbx", "fail", "not installed")
		return w.verifyResult(checks, format)
	}
	out, err := sbxRun(ctx, w.project, false, "env", "plan")
	if err != nil {
		first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		add("sbx env plan (this project)", "fail", first)
	} else {
		add("sbx env plan (this project)", "ok", "sbx accepts the merged environment")
	}

	bin := filepath.Join(in.Kit.Dir, "bin", "jiracli")
	if kit == nil {
		bin = filepath.Join(legacy.Dir, "bin", "jiracli")
	} else if kit.Dir != "" {
		bin = filepath.Join(kit.Dir, "bin", "jiracli")
	}
	probe, err := sbxRun(ctx, w.project, false, "env", "exec", "--no-input", "--", "sh", "-c", sandboxProbe(bin))
	if err != nil {
		add("running sandbox", "skip", "none for this project (or not reachable): start it with 'sbx env run' (recreate it if it predates the kit: 'sbx env rm' first), then verify again")
		return w.verifyResult(checks, format)
	}
	vals := map[string]string{}
	for _, l := range strings.Split(string(probe), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			vals[k] = v
		}
	}
	st := func(ok bool) string { return map[bool]string{true: "ok", false: "fail"}[ok] }
	add("kit applied (JIRA_CONFIG, JIRA_READ_ONLY set)", st(vals["config"] != "unset" && vals["read_only"] != "unset"),
		"JIRA_READ_ONLY="+vals["read_only"])
	add("credential sentinel "+or(vals["cred_name"], "JIRA_CREDENTIAL_*"), st(vals["cred"] == "yes"), "")
	add("binary at "+bin, st(vals["bin"] == "yes"), map[bool]string{true: "", false: "the settings folder is not mounted"}[vals["bin"] == "yes"])
	add("jiracli on PATH", st(vals["path"] != "none"), vals["path"])
	dout, derr := sbxRun(ctx, w.project, false, "env", "exec", "--no-input", "--", bin, "doctor")
	if derr != nil {
		add("jiracli doctor in the sandbox", "fail", lastFailingLayer(dout))
	} else {
		add("jiracli doctor in the sandbox", "ok", "token injected, authenticated")
	}
	return w.verifyResult(checks, format)
}

// sandboxProbe prints key=value lines about jira-cli in a sandbox.
func sandboxProbe(bin string) string {
	return `echo "bin=$(test -x '` + bin + `' && echo yes || echo no)"
echo "path=$(command -v jiracli || echo none)"
echo "config=${JIRA_CONFIG-unset}"
echo "read_only=${JIRA_READ_ONLY-unset}"
c=$(env | sed -n 's/^\(JIRA_CREDENTIAL_[A-Z0-9_]*\)=.*/\1/p' | head -1)
echo "cred_name=$c"
echo "cred=$( [ -n "$c" ] && echo yes || echo no)"`
}

// lastFailingLayer extracts the failing layer from doctor's JSON output.
func lastFailingLayer(out []byte) string {
	s := string(out)
	if i := strings.LastIndex(s, `"message": "`); i >= 0 {
		m := s[i+len(`"message": "`):]
		if j := strings.Index(m, `"`); j >= 0 {
			return m[:j]
		}
	}
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return first
}

func (w *sbxWizard) verifyResult(checks []verifyCheck, format output.Format) error {
	failed := 0
	for _, c := range checks {
		if c.Status == "fail" {
			failed++
		}
	}
	w.ui.println("")
	if !w.a.IsTerminal() || w.set("output") {
		if err := output.Write(w.a.Out, map[string]any{"checks": checks, "failed": failed, "issues": w.in.Issues}, format); err != nil {
			return err
		}
	}
	if failed > 0 {
		return clierr.New(clierr.LayerConfig, "verify_failed", fmt.Sprintf("%d check(s) failed", failed))
	}
	w.ui.ok("verified")
	return nil
}

func (w *sbxWizard) statusJSON() map[string]any {
	return map[string]any{"inspection": w.in, "sbx_installed": w.sbxPath != "", "secrets_known": w.secrets != nil,
		"set_up": w.in.SetUp(), "jira_targets": w.in.JiraTargets()}
}
