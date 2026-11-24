package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSetup(t *testing.T, fs *fakeSbx, stdin string, args ...string) (int, string, string) {
	t.Helper()
	self, _ := os.Executable()
	return runJiraSbx(t, fs, stdin, append([]string{"sbx", "setup", "--binary-file", self}, args...)...)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

// A local agent kit in the v2 format, listed in ~/.sbxenv.yaml.
func v2Agent(t *testing.T, home string) {
	t.Helper()
	writeFile(t, filepath.Join(home, "kits", "devex-opencode", "spec.yaml"),
		"schemaVersion: \"2\"\nkind: sandbox\nname: devex-opencode\nextends: opencode\n")
	writeFile(t, filepath.Join(home, ".sbxenv.yaml"),
		"schemaVersion: \"1\"\nagent: devex-opencode\nkits:\n  - ./kits/devex-opencode\n")
}

func setupStatus(t *testing.T, fs *fakeSbx, project string) map[string]any {
	t.Helper()
	code, out, errOut := runSetup(t, fs, "", "--action", "status", "--project", project)
	if code != 0 {
		t.Fatalf("status: exit %d\n%s", code, errOut)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return res
}

func TestSbxSetupStatusFindsFormatMismatch(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	v2Agent(t, home)
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	project := t.TempDir()
	// A v3 kit next to the v2 agent kit: sbx refuses that.
	if code, _, errOut := runKit(t, fs, "--base-url", "jira.example.com", "--yes", "--kit-schema", "3"); code != 0 {
		t.Fatalf("kit: %s", errOut)
	}
	res := setupStatus(t, fs, project)
	in := res["inspection"].(map[string]any)
	if in["agent_schema"] != float64(2) {
		t.Fatalf("agent_schema %v (%v)", in["agent_schema"], in["agent_schema_why"])
	}
	issues, _ := json.Marshal(in["issues"])
	if !strings.Contains(string(issues), "the jira-cli kit is v3 but the agent's kits are v1/v2") {
		t.Fatalf("issues: %s", issues)
	}

	// update writes the kit in the agent's format.
	code, _, errOut := runSetup(t, fs, "", "--action", "update", "--project", project, "--yes")
	if code != 0 {
		t.Fatalf("update: exit %d\n%s", code, errOut)
	}
	kit := filepath.Join(home, ".config", "jira-cli-sbx", "kit")
	if _, err := os.Stat(filepath.Join(kit, "spec.yaml")); err != nil {
		t.Fatalf("no v2 kit after update:\n%s", errOut)
	}
	res = setupStatus(t, fs, project)
	issues, _ = json.Marshal(res["inspection"].(map[string]any)["issues"])
	if strings.Contains(string(issues), `"error"`) {
		t.Fatalf("errors left: %s", issues)
	}
}

func TestSbxSetupInitProjectTarget(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".sbxenv.yaml"), "schemaVersion: \"1\"\nagent: opencode\n")
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	code, _, errOut := runSetup(t, fs, "", "--action", "init", "--target", "project", "--project", project,
		"--base-url", "jira.example.com", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	env := readFile(t, filepath.Join(project, "sbxenv.yaml"))
	for _, want := range []string{"workspace: .", `host: "jira.example.com"`, "domains: [jira.example.com]"} {
		if !strings.Contains(env, want) {
			t.Errorf("project sbxenv.yaml lacks %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "agent:") {
		t.Errorf("agent set although ~/.sbxenv.yaml sets it:\n%s", env)
	}
	if strings.Contains(readFile(t, filepath.Join(home, ".sbxenv.yaml")), "jira") {
		t.Error("~/.sbxenv.yaml changed")
	}
	// sbx checked the two files in merge order: the user's copy, then the project's.
	var plan string
	for _, c := range fs.calls {
		if strings.HasPrefix(c.args, "env plan ") {
			plan = c.args
		}
	}
	if !strings.Contains(plan, "user.sbxenv.yaml") || !strings.HasSuffix(plan, "/sbxenv.yaml") {
		t.Fatalf("plan call: %q", plan)
	}
}

func TestSbxSetupDryRunWritesNothing(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	orig := "schemaVersion: \"1\"\nagent: opencode\n"
	writeFile(t, filepath.Join(home, ".sbxenv.yaml"), orig)
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	code, out, errOut := runSetup(t, fs, "", "--action", "init", "--target", "user", "--project", t.TempDir(),
		"--base-url", "jira.example.com", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if readFile(t, filepath.Join(home, ".sbxenv.yaml")) != orig {
		t.Fatal("--dry-run changed ~/.sbxenv.yaml")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "jira-cli-sbx", "kit")); err == nil {
		t.Fatal("--dry-run wrote the kit")
	}
	if !strings.Contains(errOut, "+ kits:") || !strings.Contains(out, `"dry_run": true`) {
		t.Fatalf("no diff or result:\n%s\n%s", errOut, out)
	}
}

func TestSbxSetupMigrateLegacyProject(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	if code, _, errOut := runSbx(t, fs, "", "--project", project, "--base-url", "jira.example.com"); code != 0 {
		t.Fatalf("setup-sbx: %s", errOut)
	}
	if code, _, errOut := runKit(t, fs, "--base-url", "jira.example.com", "--yes"); code != 0 {
		t.Fatalf("kit: %s", errOut)
	}
	res := setupStatus(t, fs, project)
	issues, _ := json.Marshal(res["inspection"].(map[string]any)["issues"])
	if !strings.Contains(string(issues), "sbx refuses two kits providing jira-cli") {
		t.Fatalf("conflict not found: %s", issues)
	}
	code, _, errOut := runSetup(t, fs, "", "--action", "migrate", "--project", project, "--yes")
	if code != 0 {
		t.Fatalf("migrate: exit %d\n%s", code, errOut)
	}
	env := readFile(t, filepath.Join(project, "sbxenv.yaml"))
	if strings.Contains(env, "tmp/.sbx/jira-cli") || strings.Contains(env, "JIRA_CONFIG") || strings.HasPrefix(env, "# generated by jiracli setup-sbx") {
		t.Fatalf("after migrate:\n%s", env)
	}
	if !strings.Contains(env, "agent:") {
		t.Fatalf("migrate lost the agent:\n%s", env)
	}
	res = setupStatus(t, fs, project)
	issues, _ = json.Marshal(res["inspection"].(map[string]any)["issues"])
	if strings.Contains(string(issues), `"error"`) {
		t.Fatalf("errors left: %s", issues)
	}
	// setup-sbx no longer treats the file as its own.
	if code, _, _ := runSbx(t, fs, "", "--project", project, "--base-url", "jira.example.com"); code == 0 {
		t.Fatal("setup-sbx overwrote the migrated file")
	}
}

func TestSbxSetupVerify(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".sbxenv.yaml"), "schemaVersion: \"1\"\nagent: opencode\n")
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	if code, _, errOut := runKit(t, fs, "--base-url", "jira.example.com", "--yes"); code != 0 {
		t.Fatalf("kit: %s", errOut)
	}
	// No sandbox: the live checks are skipped, the static ones pass.
	code, out, errOut := runSetup(t, fs, "", "--action", "verify", "--project", project)
	if code != 0 || !strings.Contains(errOut, "none for this project") {
		t.Fatalf("exit %d\n%s\n%s", code, errOut, out)
	}
	// A running sandbox with the kit applied.
	fs.sandbox = "bin=yes\npath=/usr/bin/jiracli\nconfig=/x/config.yaml\nread_only=1\ncred_name=JIRA_CREDENTIAL_JIRA_PROD\ncred=yes\n"
	code, out, errOut = runSetup(t, fs, "", "--action", "verify", "--project", project)
	var res struct {
		Failed int `json:"failed"`
		Checks []struct{ Name, Status string }
	}
	_ = json.Unmarshal([]byte(out), &res)
	if code != 0 || res.Failed != 0 || len(res.Checks) < 7 {
		t.Fatalf("exit %d %+v\n%s", code, res, errOut)
	}
	// The kit not applied, doctor failing: verify fails.
	fs.sandbox, fs.doctorErr = "bin=no\npath=none\nconfig=unset\nread_only=unset\ncred_name=\ncred=no\n", true
	code, _, errOut = runSetup(t, fs, "", "--action", "verify", "--project", project)
	if code == 0 || !strings.Contains(errOut, "check auth failed: 401") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
}

func TestSbxSetupWizardInit(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".sbxenv.yaml"), "schemaVersion: \"1\"\nagent: opencode\n")
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira  apiKey\n", terminal: true}
	// Action: init (default when nothing is set up); where: user; URL; read-only:
	// yes; secret: keep; format: keep (v3, the agent's); apply; keep the stored
	// token; confirm the file.
	stdin := answers("", "1", "jira.example.com", "", "", "", "", "", "y")
	code, _, errOut := runSetup(t, fs, stdin, "--project", project)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	for _, want := range []string{"What is set up", "Action", "Changes to", "updated"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("output lacks %q:\n%s", want, errOut)
		}
	}
	if env := readFile(t, filepath.Join(home, ".sbxenv.yaml")); !strings.Contains(env, `host: "jira.example.com"`) || !strings.Contains(env, `readOnly: "1"`) {
		t.Fatalf("~/.sbxenv.yaml:\n%s", env)
	}
	// Run again: now update is offered first, and nothing to fix leaves verify.
	code, _, errOut = runSetup(t, fs, answers("5"), "--project", project)
	if code != 0 || !strings.Contains(errOut, "update: refresh the kit") || !strings.Contains(errOut, "verify: check the setup") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
}

func TestLineDiff(t *testing.T) {
	got := strings.Join(lineDiff("a\nb\nc\nd\ne\nf\n", "a\nb\nX\nd\ne\nf\ng\n", 1), "|")
	want := "@@ line 2|  b|- c|+ X|  d|@@ line 6|  f|+ g"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// The kit format question defaults to the agent's format (v2 here); Enter
// keeps it, although "3" is both the default's position and another key.
func TestSbxSetupWizardKeepsAgentFormat(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	v2Agent(t, home)
	fs := &fakeSbx{secrets: "jira  apiKey\n", terminal: true}
	code, _, errOut := runSetup(t, fs, answers("", "1", "jira.example.com", "", "", "", "", "", "y"), "--project", t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "jira-cli-sbx", "kit", "spec.yaml")); err != nil {
		t.Fatalf("not a v2 kit:\n%s", errOut)
	}
}

// Saying no to one step skips that step, not the whole run.

func TestSbxKitNoToOptionalSteps(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	orig := "schemaVersion: \"1\"\nagent: opencode\n"
	writeFile(t, filepath.Join(home, ".sbxenv.yaml"), orig)
	// The token is stored: keep it (Enter); do not update ~/.sbxenv.yaml.
	fs := &fakeSbx{secrets: "jira  apiKey\n", terminal: true}
	self, _ := os.Executable()
	code, _, errOut := runJiraSbx(t, fs, answers("", "n"), "sbx", "kit", "--binary-file", self, "--base-url", "jira.example.com")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if fs.setSecret {
		t.Error("the stored token was replaced")
	}
	if readFile(t, filepath.Join(home, ".sbxenv.yaml")) != orig {
		t.Error("~/.sbxenv.yaml changed after no")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "jira-cli-sbx", "kit", "jira-cli.yaml")); err != nil {
		t.Error("the kit was not written")
	}
	if !strings.Contains(errOut, "kept as it is (you said no)") || !strings.Contains(errOut, "Add to") {
		t.Errorf("no note or entries to add:\n%s", errOut)
	}

	// Replace the token: yes; update the file: yes.
	fs = &fakeSbx{secrets: "jira  apiKey\n", terminal: true}
	if code, _, errOut = runJiraSbx(t, fs, answers("y", "y"), "sbx", "kit", "--binary-file", self, "--base-url", "jira.example.com"); code != 0 || !fs.setSecret {
		t.Fatalf("exit %d, set %v\n%s", code, fs.setSecret, errOut)
	}

	// No token stored, and not now: the setup still completes.
	isolateHome(t)
	fs = &fakeSbx{secrets: "other  apiKey\n", terminal: true}
	code, _, errOut = runJiraSbx(t, fs, answers("n", "y"), "sbx", "kit", "--binary-file", self, "--base-url", "jira.example.com")
	if code != 0 || fs.setSecret || !strings.Contains(errOut, "No token stored") || !strings.Contains(errOut, "sbx secret set jira") {
		t.Fatalf("exit %d, set %v\n%s", code, fs.setSecret, errOut)
	}
	if !strings.Contains(readFile(t, filepath.Join(os.Getenv("HOME"), ".sbxenv.yaml")), "jira-cli-sbx/kit") {
		t.Fatal("~/.sbxenv.yaml not written")
	}
}

func TestSbxSetupRemoveNoToOneFile(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".sbxenv.yaml"), "schemaVersion: \"1\"\nagent: opencode\n")
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	for _, target := range []string{"user", "project"} {
		if code, _, errOut := runSetup(t, fs, "", "--action", "init", "--target", target, "--project", project, "--base-url", "jira.example.com", "--yes"); code != 0 {
			t.Fatalf("init %s: %s", target, errOut)
		}
	}
	// ~/.sbxenv.yaml: no; the project file: yes.
	fs.terminal = true
	code, _, errOut := runSetup(t, fs, answers("n", "y"), "--action", "remove", "--project", project)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".sbxenv.yaml")), "jira-cli-sbx/kit") {
		t.Error("removed from ~/.sbxenv.yaml after no")
	}
	if strings.Contains(readFile(t, filepath.Join(project, "sbxenv.yaml")), "jira-cli-sbx/kit") {
		t.Error("not removed from the project file after yes")
	}
}

func TestSbxSetupNoToMigration(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	if code, _, errOut := runSbx(t, fs, "", "--project", project, "--base-url", "jira.example.com"); code != 0 {
		t.Fatalf("setup-sbx: %s", errOut)
	}
	legacy := readFile(t, filepath.Join(project, "sbxenv.yaml"))
	fs.terminal = true
	// For every sandbox: no to the migration goes on (keep the token, update the file).
	code, _, errOut := runSetup(t, fs, answers("n", "", "y"), "--action", "init", "--target", "user", "--project", project, "--base-url", "jira.example.com")
	if code != 0 || !strings.Contains(errOut, "kept setup-sbx's kit") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if readFile(t, filepath.Join(project, "sbxenv.yaml")) != legacy {
		t.Error("the project file changed after no")
	}
	if !strings.Contains(readFile(t, filepath.Join(os.Getenv("HOME"), ".sbxenv.yaml")), "jira-cli-sbx/kit") {
		t.Error("~/.sbxenv.yaml not set up")
	}
	// For this project: no to the migration stops (sbx would refuse the file).
	code, _, errOut = runSetup(t, fs, answers("n"), "--action", "init", "--target", "project", "--project", project, "--base-url", "jira.example.com")
	if code == 0 || !strings.Contains(errOut, "keeps setup-sbx's kit, and sbx would refuse") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if readFile(t, filepath.Join(project, "sbxenv.yaml")) != legacy {
		t.Error("the project file changed")
	}
}

func TestSetupSbxNoToReplaceOffersMerge(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	own := "schemaVersion: \"1\"\nagent: opencode\nworkspace: .\nenv:\n  MINE: \"1\"\n"
	writeFile(t, filepath.Join(project, "sbxenv.yaml"), own)
	fs := &fakeSbx{secrets: "jira  apiKey\n", terminal: true}
	// setup-sbx: project; replace it: no; add jira-cli to it: yes. Then the
	// wizard (target project): where, URL, read-only, secret, format, apply:
	// Enter; keep the token; update the file: yes.
	code, _, errOut := runSbx(t, fs, answers(project, "n", "y", "", "", "", "", "", "", "", "y"), "-i", "--base-url", "jira.example.com")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	env := readFile(t, filepath.Join(project, "sbxenv.yaml"))
	if !strings.Contains(env, `MINE: "1"`) || !strings.Contains(env, "jira-cli-sbx/kit") {
		t.Fatalf("project file:\n%s", env)
	}
	// No to both: nothing is written.
	writeFile(t, filepath.Join(project, "sbxenv.yaml"), own)
	code, _, _ = runSbx(t, fs, answers(project, "n", "n"), "-i", "--base-url", "jira.example.com")
	if code == 0 || readFile(t, filepath.Join(project, "sbxenv.yaml")) != own {
		t.Fatalf("exit %d", code)
	}
}
