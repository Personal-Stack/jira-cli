package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runKit(t *testing.T, fs *fakeSbx, args ...string) (int, string, string) {
	t.Helper()
	self, _ := os.Executable()
	return runJiraSbx(t, fs, "", append([]string{"sbx", "kit", "--binary-file", self}, args...)...)
}

func TestSbxKitWritesKitAndUserEnv(t *testing.T) {
	isolateHome(t)
	home := os.Getenv("HOME")
	envFile := filepath.Join(home, ".sbxenv.yaml")
	orig := "schemaVersion: \"1\"\nagent: opencode # mine\nkits: [other/kit]\n"
	if err := os.WriteFile(envFile, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	code, out, errOut := runKit(t, fs, "--base-url", "jira.example.com", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	settings := filepath.Join(home, ".config", "jira-cli-sbx")
	kitDir := filepath.Join(settings, "kit")
	if res["kit_dir"] != kitDir || res["env_file_changed"] != true || res["jira_host"] != "jira.example.com" || res["read_only"] != true {
		t.Fatalf("%v", res)
	}
	for _, f := range []string{"kit/jira-cli.yaml", "kit/jira-cli.dockerfile", "kit/bin/jiracli", "kit/skill/jira/SKILL.md", "config.yaml", "bashrc"} {
		if _, err := os.Stat(filepath.Join(settings, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	env, _ := os.ReadFile(envFile)
	for _, want := range []string{"agent: opencode # mine", "other/kit", "source: " + kitDir, `host: "jira.example.com"`, "path: " + settings, "domains: [jira.example.com]"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("~/.sbxenv.yaml lacks %q:\n%s", want, env)
		}
	}
	if bak, _ := os.ReadFile(envFile + ".bak"); string(bak) != orig {
		t.Errorf("backup:\n%s", bak)
	}
	// sbx checked the merged file in a scratch folder, by path.
	var planned bool
	for _, c := range fs.calls {
		if strings.HasPrefix(c.args, "env plan /") {
			planned = true
		}
	}
	if !planned || len(fs.planDirs) != 1 {
		t.Fatalf("calls %+v", fs.calls)
	}

	// setup-sbx now refuses: a project kit would be a second jira-cli.
	code, _, errOut = runSbx(t, fs, "", "--project", t.TempDir(), "--base-url", "jira.example.com")
	if code == 0 || !strings.Contains(errOut, "already lists jira-cli's kit") {
		t.Fatalf("setup-sbx: exit %d\n%s", code, errOut)
	}

	// --remove takes it out again.
	code, _, errOut = runKit(t, fs, "--remove", "--yes")
	if code != 0 {
		t.Fatalf("remove: exit %d\n%s", code, errOut)
	}
	env, _ = os.ReadFile(envFile)
	if strings.Contains(string(env), kitDir) || !strings.Contains(string(env), "other/kit") {
		t.Fatalf("after remove:\n%s", env)
	}
}

func TestSbxKitNeedsYesWithoutTerminal(t *testing.T) {
	isolateHome(t)
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	code, _, errOut := runKit(t, fs, "--base-url", "jira.example.com")
	if code == 0 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, ".sbxenv.yaml")); err == nil {
		t.Fatal("~/.sbxenv.yaml written")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "jira-cli-sbx", "kit")); err == nil {
		t.Fatal("kit written before confirmation")
	}
}

func TestSbxKitPrint(t *testing.T) {
	isolateHome(t)
	fs := &fakeSbx{secrets: "jira  apiKey\n"}
	code, out, errOut := runKit(t, fs, "--base-url", "jira.example.com", "--print", "--allow-writes")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".sbxenv.yaml")); err == nil {
		t.Fatal("--print wrote ~/.sbxenv.yaml")
	}
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	if s, _ := res["sbxenv_entries"].(string); !strings.Contains(s, `readOnly: "0"`) || res["env_file_changed"] != false {
		t.Fatalf("%v", res)
	}
}

func TestSbxKitRejectedWritesNothing(t *testing.T) {
	isolateHome(t)
	fs := &fakeSbx{secrets: "jira  apiKey\n", reject: "kits[1]: provides jira-cli twice\n"}
	code, _, errOut := runKit(t, fs, "--base-url", "jira.example.com", "--yes")
	if code == 0 || !strings.Contains(errOut, "provides jira-cli twice") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	// Only the (empty) settings folder the check mounts exists.
	empty(t, filepath.Join(os.Getenv("HOME"), ".config", "jira-cli-sbx"))
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".sbxenv.yaml")); err == nil {
		t.Fatal("~/.sbxenv.yaml written")
	}
}

func TestSbxKitStoresMissingSecret(t *testing.T) {
	isolateHome(t)
	fs := &fakeSbx{secrets: "other  apiKey\n"}
	app := func() int {
		code, _, _ := runKit(t, fs, "--base-url", "jira.example.com", "--yes")
		return code
	}
	// Without a terminal sbx cannot prompt: stop with the command to run.
	if code := app(); code == 0 || fs.setSecret {
		t.Fatalf("exit %d, set %v", code, fs.setSecret)
	}
}
