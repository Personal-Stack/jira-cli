package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/generated"
	"github.com/personal-stack/jira-cli/internal/sbx"
)

type sbxCall struct {
	dir    string
	attach bool
	args   string
}

// fakeSbx stands in for the sbx CLI.
type fakeSbx struct {
	missing   bool   // sbx not installed
	secrets   string // `sbx secret ls` output
	reject    string // env plan fails with this output
	baseKit   string // an agent kit from ~/.sbxenv.yaml: env plan requires it as the agent
	unreach   string // host the Jira reachability probe cannot reach
	calls     []sbxCall
	planDirs  []string
	setSecret bool
}

// isolateHome gives the test its own HOME (the settings folder lives there).
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
}

// runSbx runs `jira setup-sbx` with stdin answers against fs.
func runSbx(t *testing.T, fs *fakeSbx, stdin string, args ...string) (int, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("uses this linux test binary as the sandbox binary")
	}
	oldLook, oldRun, oldDial := sbxLookPath, sbxRun, sbx.Dial
	t.Cleanup(func() { sbxLookPath, sbxRun, sbx.Dial = oldLook, oldRun, oldDial })
	sbxLookPath = func(string) (string, error) {
		if fs.missing {
			return "", errors.New("not found")
		}
		return "/usr/bin/sbx", nil
	}
	sbx.Dial = func(_ context.Context, addr string) error {
		if fs.unreach != "" && strings.HasPrefix(addr, fs.unreach+":") {
			return errors.New("connection refused")
		}
		return nil
	}
	sbxRun = func(_ context.Context, dir string, attach bool, a ...string) ([]byte, error) {
		cmd := strings.Join(a, " ")
		fs.calls = append(fs.calls, sbxCall{dir, attach, cmd})
		switch cmd {
		case "secret ls":
			return []byte(fs.secrets), nil
		case "env plan":
			fs.planDirs = append(fs.planDirs, dir)
			if fs.reject != "" {
				return []byte(fs.reject), errors.New("exit status 1")
			}
			env, _ := os.ReadFile(filepath.Join(dir, "sbxenv.yaml"))
			if a := agentOf(string(env)); fs.baseKit != "" && a != fs.baseKit {
				return []byte(`error: agent "` + a + `" does not match agent kit name "` + fs.baseKit + `" (set agent: "` + fs.baseKit + `" in sbxenv.yaml)` + "\n"), errors.New("exit status 1")
			}
			return []byte("plan ok\n"), nil
		case "secret set jira":
			fs.setSecret = true
		}
		return nil, nil
	}
	self, _ := os.Executable()
	var out, errb bytes.Buffer
	app := NewApp("v9.9.9", generated.Profiles, generated.DefaultProfile)
	app.In, app.Out, app.Err = strings.NewReader(stdin), &out, &errb
	app.IsTerminal = func() bool { return false }
	full := append([]string{"setup-sbx", "--binary-file", self}, args...)
	code := app.Execute(context.Background(), full)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("stderr:\n%s", errb.String())
		}
	})
	return code, out.String(), errb.String()
}

func agentOf(env string) string {
	for _, l := range strings.Split(env, "\n") {
		if a, ok := strings.CutPrefix(l, "agent: "); ok {
			return a
		}
	}
	return ""
}

func empty(t *testing.T, dir string) {
	t.Helper()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files were written to %s: %v", dir, entries)
	}
}

func answers(a ...string) string { return strings.Join(a, "\n") + "\n" }

func TestSetupSbxFlags(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "NAME  TYPE\njira  apiKey\n"}
	code, out, _ := runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com/jira", "--agent", "devex-opencode", "--name", "jira-sbx")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output: %v\n%s", err, out)
	}
	if res["binary_source"] != "file" || res["credential_env"] != "JIRA_CREDENTIAL_JIRA_PROD" || res["name"] != "jira-sbx" || res["checked_with_sbx"] != true {
		t.Fatalf("result: %v", res)
	}
	env, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	for _, want := range []string{"name: jira-sbx", "agent: devex-opencode", "  - ./tmp/.sbx/jira-cli", `JIRA_READ_ONLY: "1"`} {
		if !strings.Contains(string(env), want) {
			t.Errorf("sbxenv.yaml lacks %q:\n%s", want, env)
		}
	}
	for _, f := range []string{".opencode/skills/jira/SKILL.md", "tmp/.sbx/jira-cli/bin/jira"} {
		if _, err := os.Stat(filepath.Join(project, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	// sbx validated a scratch copy, not the project (which it never touched).
	if len(fs.planDirs) != 1 || strings.HasPrefix(fs.planDirs[0], project) {
		t.Fatalf("plan dirs: %v", fs.planDirs)
	}
}

func TestSetupSbxFailsFastWithoutSbx(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	code, _, errOut := runSbx(t, &fakeSbx{missing: true}, answers(project, "https://jira.example.com"), "-i")
	if code != 3 || !strings.Contains(errOut, "not installed") || strings.Contains(errOut, "Project the agent works in") {
		t.Fatalf("must stop before the first question: exit %d\n%s", code, errOut)
	}
	empty(t, project)
}

func TestSetupSbxFailsFastWhenJiraUnreachable(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira apiKey\n", unreach: "jira.example.com"}
	code, _, errOut := runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com")
	if code != 8 || !strings.Contains(errOut, "cannot reach jira.example.com:443") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if len(fs.planDirs) != 0 {
		t.Fatal("went on to sbx after the failed check")
	}
	empty(t, project)
	// --skip-checks lets it through.
	if code, _, _ = runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com", "--skip-checks"); code != 0 {
		t.Fatalf("--skip-checks: exit %d", code)
	}
}

func TestSetupSbxInteractiveValidatesEachAnswer(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira apiKey\n", unreach: "down.example.com"}
	code, _, errOut := runSbx(t, fs, answers(
		project+"/nope",                        // not a directory: asked again
		project,                                //
		"http://x",                             // not https: asked again
		"https://down.example.com",             // unreachable: asked again
		"https://jira.example.com",             //
		"company.jfrog.io/team/opencode-sbx:1", // an image, not an agent name: asked again
		"devex-opencode",                       //
		"",                                     // sandbox name: the default
		"",                                     // read-only: yes
		"",                                     // proceed
		"",                                     // start: no
	), "-i")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"not a directory", "must be https://", "cannot reach down.example.com:443", "looks like an image or kit reference"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q", want)
		}
	}
	env, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	if !strings.Contains(string(env), "agent: devex-opencode\nkits:\n  - ./tmp/.sbx/jira-cli\nworkspace:") {
		t.Fatalf("sbxenv.yaml:\n%s", env)
	}

	// The same project again: one confirmation instead of every question.
	code, _, errOut = runSbx(t, fs, answers(project, "", ""), "-i")
	if code != 0 || !strings.Contains(errOut, "Use these settings") || strings.Contains(errOut, "Jira base URL (https://") {
		t.Fatalf("exit %d:\n%s", code, errOut)
	}
}

func TestSetupSbxNewProjectInheritsAgentFromLastSetup(t *testing.T) {
	isolateHome(t)
	first, second := t.TempDir(), filepath.Join(t.TempDir(), "My Work")
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSbx{secrets: "jira apiKey\n"}
	if code, _, _ := runSbx(t, fs, "", "--project", first, "--base-url", "https://jira.example.com", "--agent", "devex-opencode"); code != 0 {
		t.Fatalf("first: exit %d", code)
	}
	code, _, errOut := runSbx(t, fs, "", "--project", second)
	if code != 0 {
		t.Fatalf("second: exit %d", code)
	}
	env, _ := os.ReadFile(filepath.Join(second, "sbxenv.yaml"))
	if !strings.Contains(string(env), "name: devex-opencode-my-work-jira\nagent: devex-opencode\nkits:\n  - ./tmp/.sbx/jira-cli\n") ||
		!strings.Contains(errOut, "Defaults from your last setup") {
		t.Fatalf("second project:\n%s\n%s", env, errOut)
	}
	// Flags still win.
	third := t.TempDir()
	if code, _, _ = runSbx(t, fs, "", "--project", third, "--agent", "claude", "--name", "x"); code != 0 {
		t.Fatalf("third: exit %d", code)
	}
	if env, _ = os.ReadFile(filepath.Join(third, "sbxenv.yaml")); !strings.Contains(string(env), "name: x\nagent: claude\n") {
		t.Fatalf("flags did not win:\n%s", env)
	}
}

// The case from real use: an image given as the agent ended up as a kit,
// and saved defaults kept bringing it back. A re-run keeps only jira-cli's
// kit, and the kit moves to tmp/ (or --kit-dir), removing the old one.
func TestSetupSbxDropsOldKitsAndMovesTheKit(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira apiKey\n"}
	if code, _, _ := runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com", "--agent", "devex-opencode", "--kit-dir", "temp/.sbx/jira-cli"); code != 0 {
		t.Fatalf("setup: exit %d", code)
	}
	// What an earlier version left: the image as a kit, and as the saved agent.
	envPath := filepath.Join(project, "sbxenv.yaml")
	env, _ := os.ReadFile(envPath)
	old := strings.Replace(string(env), "kits:\n", "kits:\n  - company.jfrog.io/team/opencode-sbx:1.2\n", 1)
	if err := os.WriteFile(envPath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(os.Getenv("HOME"), ".config", "jira-cli-sbx", "setup.yaml")
	if err := os.WriteFile(settings, []byte("agent: company.jfrog.io/team/opencode-sbx:1.2\nagent_name: devex-opencode\nkit_path: temp/.sbx/jira-cli\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runSbx(t, fs, "", "--project", project, "--kit-dir", "tmp/.sbx/jira-cli"); code != 0 {
		t.Fatalf("re-run: exit %d", code)
	}
	env, _ = os.ReadFile(envPath)
	if !strings.Contains(string(env), "agent: devex-opencode\nkits:\n  - ./tmp/.sbx/jira-cli\nworkspace:") {
		t.Fatalf("kits:\n%s", env)
	}
	if _, err := os.Stat(filepath.Join(project, "tmp/.sbx/jira-cli/bin/jira")); err != nil {
		t.Fatal("kit not in tmp/")
	}
	if _, err := os.Stat(filepath.Join(project, "temp")); err == nil {
		if _, err := os.Stat(filepath.Join(project, "temp/.sbx/jira-cli")); err == nil {
			t.Fatal("the old kit was left behind")
		}
	}
	for _, bad := range []string{"../outside", "/abs", "a/../b"} {
		if code, _, _ := runSbx(t, fs, "", "--project", project, "--kit-dir", bad); code == 0 {
			t.Errorf("--kit-dir %s accepted", bad)
		}
	}
	if code, _, errOut := runSbx(t, fs, "", "--project", project, "--agent", "company.jfrog.io/team/opencode-sbx:1.2"); code == 0 ||
		!strings.Contains(errOut, "looks like an image or kit reference") {
		t.Errorf("an image as --agent: exit %d", code)
	}
}

// The case from real use: ~/.sbxenv.yaml brings the agent kit
// "devex-opencode", so sbx rejects any other agent name. setup-sbx takes
// the name sbx gives, saves it, and the next project starts with it.
func TestSetupSbxTakesAgentNameFromSbx(t *testing.T) {
	isolateHome(t)
	project, next := t.TempDir(), t.TempDir()
	fs := &fakeSbx{secrets: "jira apiKey\n", baseKit: "devex-opencode"}
	code, _, errOut := runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com", "--agent", "opencode-xxxx")
	if code != 0 || !strings.Contains(errOut, `the agent kit in this environment is "devex-opencode"`) || len(fs.planDirs) != 2 {
		t.Fatalf("exit %d, plans %d:\n%s", code, len(fs.planDirs), errOut)
	}
	env, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	if agentOf(string(env)) != "devex-opencode" || !strings.Contains(string(env), "name: devex-opencode-") {
		t.Fatalf("sbxenv.yaml:\n%s", env)
	}
	fs.planDirs = nil
	if code, _, _ := runSbx(t, fs, "", "--project", next); code != 0 || len(fs.planDirs) != 1 {
		t.Fatalf("next project: exit %d, plans %d", code, len(fs.planDirs))
	}
}

func TestSetupSbxRejectedBySbxWritesNothing(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira apiKey\n", reject: "sbxenv.yaml:10:5: unknown field \"target\" in additionalWorkspaces[0]\n"}
	code, _, errOut := runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com")
	if code != 3 || !strings.Contains(errOut, `unknown field \"target\"`) && !strings.Contains(errOut, `unknown field "target"`) {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	empty(t, project)
}

func TestSetupSbxMissingSecret(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	// Without a terminal: an error before anything is written.
	fs := &fakeSbx{secrets: "NAME  TYPE\n"}
	code, _, errOut := runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com")
	if code == 0 || !strings.Contains(errOut, "sbx secret set jira") || fs.setSecret {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	empty(t, project)
	// Guided: sbx stores it (it prompts), before the files are written.
	fs = &fakeSbx{secrets: "NAME  TYPE\n"}
	code, _, _ = runSbx(t, fs, answers(project, "https://jira.example.com", "", "", "", "", ""), "-i")
	if code != 0 || !fs.setSecret {
		t.Fatalf("exit %d, secret set %v", code, fs.setSecret)
	}
}

func TestSetupSbxExistingSetupKeepsItsSandboxName(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira apiKey\n"}
	// A setup made before names were defaulted: no name: (sbx's default).
	runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com", "--name", "tmp")
	b, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	_ = os.WriteFile(filepath.Join(project, "sbxenv.yaml"), []byte(strings.Replace(string(b), "name: tmp\n", "", 1)), 0o644)
	code, _, _ := runSbx(t, fs, "", "--project", project)
	env, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	if code != 0 || strings.Contains(string(env), "name:") {
		t.Fatalf("exit %d; the existing sandbox would be renamed:\n%s", code, env)
	}
}

func TestSetupSbxDeclined(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	code, _, errOut := runSbx(t, &fakeSbx{secrets: "jira apiKey\n"}, answers(project, "https://jira.example.com", "", "", "", "n"), "-i")
	if code == 0 || !strings.Contains(errOut, "nothing was written") {
		t.Fatalf("declining: exit %d\n%s", code, errOut)
	}
	empty(t, project)
}

func TestSetupSbxDryRunWritesNothing(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira apiKey\n"}
	code, out, _ := runSbx(t, fs, "", "--dry-run", "--project", project, "--base-url", "https://jira.example.com")
	if code != 0 || !strings.Contains(out, `"dry_run": true`) || !strings.Contains(out, sbx.GeneratedMarker) {
		t.Fatalf("exit %d\n%s", code, out)
	}
	empty(t, project)
	if len(fs.planDirs) != 0 || fs.setSecret {
		t.Fatal("dry run went on to sbx")
	}
}

func TestSetupSbxInContainer(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	t.Setenv("JIRA_CLI_CONTAINER", "1")
	fs := &fakeSbx{}
	code, _, errOut := runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com")
	if code != 0 || len(fs.calls) != 0 || !strings.Contains(errOut, "checks the sbx secret and the environment on your machine") {
		t.Fatalf("exit %d calls %v\n%s", code, fs.calls, errOut)
	}
	// Guided, with the project from the wrapper: no project question.
	// (A fresh HOME: no saved defaults, so every question is asked.)
	isolateHome(t)
	project2 := t.TempDir()
	code, _, errOut = runSbx(t, &fakeSbx{}, answers("https://jira.example.com", "", "", "", "", ""), "-i", "--project", project2)
	if code != 0 || strings.Contains(errOut, "Project the agent works in") || !strings.Contains(errOut, "project    "+project2) {
		t.Fatalf("exit %d:\n%s", code, errOut)
	}
}

func TestSetupSbxRunHandsOverToSbx(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	var handed []string
	old := sbxHandOver
	t.Cleanup(func() { sbxHandOver = old })
	sbxHandOver = func(dir string, args ...string) error {
		handed = append([]string{dir}, args...)
		return nil
	}
	fs := &fakeSbx{secrets: "jira apiKey\n"}
	if code, _, _ := runSbx(t, fs, "", "--project", project, "--base-url", "https://jira.example.com", "--run"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	// `sbx env run` is handed the terminal (exec), not run as a child that
	// jira's Ctrl+C handling would interrupt.
	if strings.Join(handed, " ") != project+" env run" {
		t.Fatalf("handed over: %v", handed)
	}
	for _, c := range fs.calls {
		if c.args == "env run" {
			t.Fatal("env run must not run as a child process")
		}
	}
}

func TestSetupSbxRefusesForeignEnvFile(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sbxenv.yaml"), []byte("schemaVersion: \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runSbx(t, &fakeSbx{secrets: "jira apiKey\n"}, "", "--project", project, "--base-url", "https://jira.example.com")
	if code == 0 || !strings.Contains(errOut, "--force") {
		t.Fatalf("exit %d:\n%s", code, errOut)
	}
	if b, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml")); string(b) != "schemaVersion: \"1\"\n" {
		t.Fatal("foreign file changed")
	}
}

func TestSetupSbxPrintsNextSteps(t *testing.T) {
	isolateHome(t)
	project := filepath.Join(t.TempDir(), "my project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSbx(t, &fakeSbx{secrets: "jira apiKey\n"}, "", "--project", project, "--base-url", "https://jira.example.com", "--name", "jira-sbx")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	q := "'" + project + "'" // the path has a space: quoted for the shell
	for _, want := range []string{"Next steps", "Start the sandbox jira-sbx", "$ cd " + q + " && sbx env run",
		"$ cd " + q + " && sbx env exec -- '" + project + "/tmp/.sbx/jira-cli/bin/jira' doctor", "env:JIRA_CREDENTIAL_JIRA_PROD", "$ sbx ls", "$ sbx diagnose"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("next steps lack %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "Store the Jira PAT") || strings.Contains(errOut, "\x1b[") {
		t.Error("secret step although it exists, or colors without a terminal")
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res["next"].([]any)) != 5 {
		t.Fatalf("next in the JSON result: %v %v", res["next"], err)
	}
}

func TestSbxUIColorOnlyOnTerminal(t *testing.T) {
	for _, c := range []struct {
		terminal, noColorFlag bool
		env                   string
		want                  bool
	}{{true, false, "", true}, {false, false, "", false}, {true, true, "", false}, {true, false, "1", false}} {
		app := NewApp("test", nil, "")
		app.IsTerminal = func() bool { return c.terminal }
		app.noColor = c.noColorFlag
		app.Getenv = func(k string) string {
			if k == "NO_COLOR" {
				return c.env
			}
			return ""
		}
		var b bytes.Buffer
		app.Err = &b
		ui := app.newSbxUI()
		ui.ok("done")
		if got := strings.Contains(b.String(), "\x1b["); got != c.want {
			t.Errorf("%+v: colored=%v\n%q", c, got, b.String())
		}
		if !strings.Contains(b.String(), "✓") || !strings.Contains(b.String(), "done") {
			t.Errorf("%+v: %q", c, b.String())
		}
	}
}
