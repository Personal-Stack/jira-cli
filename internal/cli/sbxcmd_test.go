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

// runSbx runs `jira setup-sbx` with stdin answers, a fake sbx and HOME in
// a temp dir. It returns the exit code, stdout, stderr and the sbx calls.
// isolateHome gives the test its own HOME (the settings folder lives there).
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
}

func runSbx(t *testing.T, haveSbx bool, secretList, stdin string, args ...string) (int, string, string, []sbxCall) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("uses this linux test binary as the sandbox binary")
	}
	var calls []sbxCall
	oldLook, oldRun := sbxLookPath, sbxRun
	t.Cleanup(func() { sbxLookPath, sbxRun = oldLook, oldRun })
	sbxLookPath = func(string) (string, error) {
		if haveSbx {
			return "/usr/bin/sbx", nil
		}
		return "", errors.New("not found")
	}
	sbxRun = func(_ context.Context, dir string, attach bool, a ...string) ([]byte, error) {
		calls = append(calls, sbxCall{dir, attach, strings.Join(a, " ")})
		if strings.Join(a, " ") == "secret ls" {
			return []byte(secretList), nil
		}
		return []byte("plan ok\n"), nil
	}
	self, _ := os.Executable()
	var out, errb bytes.Buffer
	app := NewApp("v9.9.9", generated.Profiles, generated.DefaultProfile)
	app.In, app.Out, app.Err = strings.NewReader(stdin), &out, &errb
	app.IsTerminal = func() bool { return false }
	full := append([]string{"setup-sbx", "--binary-file", self, "--arch", runtime.GOARCH}, args...)
	code := app.Execute(context.Background(), full)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("stderr:\n%s", errb.String())
		}
	})
	return code, out.String(), errb.String(), calls
}

func TestSetupSbxFlags(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	code, out, _, calls := runSbx(t, true, "NAME  TYPE\njira  apiKey\n", "",
		"--project", project, "--base-url", "https://jira.example.com/jira", "--agent", "devex-opencode", "--name", "jira-sbx")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output: %v\n%s", err, out)
	}
	if res["binary_source"] != "file" || res["credential_env"] != "JIRA_CREDENTIAL_JIRA_PROD" || res["name"] != "jira-sbx" {
		t.Fatalf("result: %v", res)
	}
	env, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	for _, want := range []string{"name: jira-sbx", "agent: devex-opencode", "  - ./.sbx/jira-cli", `JIRA_READ_ONLY: "1"`} {
		if !strings.Contains(string(env), want) {
			t.Errorf("sbxenv.yaml lacks %q:\n%s", want, env)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".opencode", "skills", "jira", "SKILL.md")); err != nil {
		t.Fatal("skill not installed from the embedded copy")
	}
	if _, err := os.Stat(filepath.Join(project, ".sbx", "jira-cli", "bin", "jira-"+runtime.GOARCH)); err != nil {
		t.Fatal("binary not in the kit")
	}
	// The secret exists, so sbx is only asked to list it, then to plan.
	got := []string{}
	for _, c := range calls {
		got = append(got, c.args)
	}
	if strings.Join(got, ",") != "secret ls,env plan" || calls[1].dir != project {
		t.Fatalf("sbx calls: %+v", calls)
	}
}

func TestSetupSbxMissingSecretWithoutTerminal(t *testing.T) {
	isolateHome(t)
	code, _, errOut, calls := runSbx(t, true, "NAME  TYPE\n", "",
		"--project", t.TempDir(), "--base-url", "https://jira.example.com")
	if code == 0 || !strings.Contains(errOut, "sbx secret set jira") {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	for _, c := range calls {
		if c.attach {
			t.Fatal("must not try an interactive sbx prompt without a terminal")
		}
	}
}

func TestSetupSbxInteractive(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	answers := strings.Join([]string{
		project + "/nope",                   // not a directory: asked again
		project,                             // project
		"http://x",                          // rejected
		"https://jira.example.com",          // URL
		"reg.example.com/team/opencode:2.1", // agent kit
		"devex-opencode",                    // agent name (sbx's name for the kit)
		"reg.example.com/team/image:1",      // image
		"my-sbx",                            // custom sandbox name
		"",                                  // CA: none
		"",                                  // read-only: keep yes
		"n",                                 // skill: no
		"",                                  // advanced: no
		"",                                  // proceed: yes
		"",                                  // start: no
	}, "\n") + "\n"
	code, _, errOut, calls := runSbx(t, false, "", answers, "-i")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut, "not a directory") || !strings.Contains(errOut, "must start with https://") {
		t.Fatalf("invalid answers were not re-asked:\n%s", errOut)
	}
	env, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	for _, want := range []string{"name: my-sbx", "agent: devex-opencode", "  - reg.example.com/team/opencode:2.1", "template: reg.example.com/team/image:1"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("sbxenv.yaml lacks %q:\n%s", want, env)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".opencode")); err == nil {
		t.Fatal("skill copied although declined")
	}
	if len(calls) != 0 {
		t.Fatalf("sbx is not installed, yet called: %v", calls)
	}

	// A second run offers the previous answers: Enter everywhere, '-' clears the name.
	again := strings.Join([]string{project, "", "", "", "", "-", "", "", "", "", "", ""}, "\n") + "\n"
	code, _, errOut, _ = runSbx(t, false, "", again, "-i")
	if code != 0 || !strings.Contains(errOut, "Defaults from the previous setup of this project") {
		t.Fatalf("exit %d:\n%s", code, errOut)
	}
	env, _ = os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	if strings.Contains(string(env), "name:") || !strings.Contains(string(env), "agent: devex-opencode") {
		t.Fatalf("second run:\n%s", env)
	}
}

// The bug from real use: the agent kit's name was only remembered per
// project, so every new project folder fell back to "opencode", which sbx
// rejects for a kit ("agent ... does not match agent kit name ...").
func TestSetupSbxNewProjectInheritsAgentFromLastSetup(t *testing.T) {
	isolateHome(t)
	first, second := t.TempDir(), filepath.Join(t.TempDir(), "My Work")
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, _, _ := runSbx(t, false, "", "", "--project", first, "--base-url", "https://jira.example.com",
		"--agent-kit", "reg.example.com/team/opencode:2.1", "--agent", "devex-opencode", "--template", "reg.example.com/img:1")
	if code != 0 {
		t.Fatalf("first: exit %d", code)
	}
	// Second project, no agent flags: everything comes from the last setup.
	code, _, errOut, _ := runSbx(t, false, "", "", "--project", second)
	if code != 0 {
		t.Fatalf("second: exit %d", code)
	}
	env, _ := os.ReadFile(filepath.Join(second, "sbxenv.yaml"))
	for _, want := range []string{"agent: devex-opencode", "  - reg.example.com/team/opencode:2.1", "template: reg.example.com/img:1",
		"name: devex-opencode-my-work-jira"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("second project lacks %q:\n%s", want, env)
		}
	}
	if !strings.Contains(errOut, "Defaults from your last setup") {
		t.Errorf("no notice:\n%s", errOut)
	}

	// Flags still win, and a new kit without --agent derives its own name.
	third := t.TempDir()
	code, _, _, _ = runSbx(t, false, "", "", "--project", third, "--agent-kit", "reg.example.com/other/claude-kit:1", "--name", "x")
	if code != 0 {
		t.Fatalf("third: exit %d", code)
	}
	env, _ = os.ReadFile(filepath.Join(third, "sbxenv.yaml"))
	if !strings.Contains(string(env), "agent: claude-kit") || !strings.Contains(string(env), "name: x") || strings.Contains(string(env), "opencode:2.1") {
		t.Fatalf("flags did not win:\n%s", env)
	}
}

func TestSetupSbxExistingSetupKeepsItsSandboxName(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	// A setup made before names were defaulted: no name: (sbx's default).
	if _, _, _, _ = runSbx(t, false, "", "", "--project", project, "--base-url", "https://jira.example.com", "--name", "tmp"); true {
		b, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
		_ = os.WriteFile(filepath.Join(project, "sbxenv.yaml"), []byte(strings.Replace(string(b), "name: tmp\n", "", 1)), 0o644)
	}
	code, _, _, _ := runSbx(t, false, "", "", "--project", project)
	env, _ := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	if code != 0 || strings.Contains(string(env), "name:") {
		t.Fatalf("exit %d; the existing sandbox would be renamed:\n%s", code, env)
	}
}

func TestSetupSbxDeclined(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	answers := strings.Join([]string{project, "https://jira.example.com", "", "", "", "", "", "", "", "", "n"}, "\n") + "\n"
	code, _, errOut, _ := runSbx(t, false, "", answers, "-i")
	if code == 0 || !strings.Contains(errOut, "nothing was written") {
		t.Fatalf("declining: exit %d\n%s", code, errOut)
	}
	if entries, _ := os.ReadDir(project); len(entries) != 0 {
		t.Fatalf("files written after declining: %v", entries)
	}
}

func TestSetupSbxDryRunWritesNothing(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	code, out, _, calls := runSbx(t, true, "", "", "--dry-run", "--project", project, "--base-url", "https://jira.example.com")
	if code != 0 || !strings.Contains(out, `"dry_run": true`) || !strings.Contains(out, sbx.GeneratedMarker) {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if entries, _ := os.ReadDir(project); len(entries) != 0 || len(calls) != 0 {
		t.Fatalf("dry run wrote %v / called %v", entries, calls)
	}
}

func TestSetupSbxInContainerPrintsHostSteps(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	t.Setenv("JIRA_CLI_CONTAINER", "1")
	code, out, errOut, calls := runSbx(t, false, "", "", "--project", project, "--base-url", "https://jira.example.com")
	if code != 0 || len(calls) != 0 {
		t.Fatalf("exit %d calls %v", code, calls)
	}
	if !strings.Contains(errOut, "run these on your machine") || !strings.Contains(errOut, "cd "+project+" && sbx env run") ||
		!strings.Contains(out, "sbx secret set jira") {
		t.Fatalf("stderr:\n%s\nstdout:\n%s", errOut, out)
	}
}

func TestSetupSbxInContainerDoesNotAskForPaths(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	t.Setenv("JIRA_CLI_CONTAINER", "1")
	// The wrapper asked for the project on the host and passed --project:
	// the first question is the URL, and there is no CA question.
	answers := strings.Join([]string{"https://jira.example.com", "", "", "", "", "", "", "y", "", "", "", "", ""}, "\n") + "\n"
	code, _, errOut, _ := runSbx(t, false, "", answers, "-i", "--project", project)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, errOut)
	}
	if strings.Contains(errOut, "Project the agent works in") || strings.Contains(errOut, "Company CA certificate file") ||
		!strings.Contains(errOut, "project    "+project) || !strings.Contains(errOut, "pass --ca-bundle FILE") ||
		!strings.Contains(errOut, "change it with --settings DIR") {
		t.Fatalf("prompts:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(project, "sbxenv.yaml")); err != nil {
		t.Fatal("not written")
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
	code, _, _, calls := runSbx(t, true, "jira apiKey\n", "", "--project", project, "--base-url", "https://jira.example.com", "--run")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// `sbx env run` is handed the terminal (exec), not run as a child that
	// jira's Ctrl+C handling would interrupt.
	if strings.Join(handed, " ") != project+" env run" {
		t.Fatalf("handed over: %v", handed)
	}
	for _, c := range calls {
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
	code, _, errOut, _ := runSbx(t, false, "", "", "--project", project, "--base-url", "https://jira.example.com")
	if code == 0 || !strings.Contains(errOut, "--kit-only") {
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
	code, out, errOut, _ := runSbx(t, true, "jira apiKey\n", "", "--project", project, "--base-url", "https://jira.example.com", "--name", "jira-sbx")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	q := "'" + project + "'" // the path has a space: quoted for the shell
	for _, want := range []string{"Next steps", "Start the sandbox jira-sbx", "$ cd " + q + " && sbx env run",
		"$ cd " + q + " && sbx env exec -- bash -lc 'jira doctor'", "env:JIRA_CREDENTIAL_JIRA_PROD", "$ sbx ls", "$ sbx diagnose"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("next steps lack %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "Store the Jira PAT") {
		t.Error("the secret exists: no step to set it")
	}
	if strings.Contains(errOut, "\x1b[") {
		t.Error("colors without a terminal")
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
