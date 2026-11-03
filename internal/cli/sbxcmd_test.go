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
	if code != 0 || !strings.Contains(errOut, "defaults from the previous setup") {
		t.Fatalf("exit %d:\n%s", code, errOut)
	}
	env, _ = os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	if strings.Contains(string(env), "name:") || !strings.Contains(string(env), "agent: devex-opencode") {
		t.Fatalf("second run:\n%s", env)
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
