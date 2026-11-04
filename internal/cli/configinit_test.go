package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/generated"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/sbx"
	"github.com/personal-stack/jira-cli/internal/testpki"
)

// runInit runs jira with args; terminal makes it interactive. Hosts in
// unreachable fail the reachability check.
func runInit(t *testing.T, terminal bool, stdin string, unreachable string, args ...string) (int, string, string) {
	t.Helper()
	old := sbx.Dial
	t.Cleanup(func() { sbx.Dial = old })
	sbx.Dial = func(_ context.Context, addr string) error {
		if unreachable != "" && strings.HasPrefix(addr, unreachable+":") {
			return errors.New("connection refused")
		}
		return nil
	}
	var out, errb bytes.Buffer
	app := NewApp("test", generated.Profiles, generated.DefaultProfile)
	app.In, app.Out, app.Err = strings.NewReader(stdin), &out, &errb
	app.IsTerminal = func() bool { return terminal }
	app.Getenv = func(string) string { return "" }
	code := app.Execute(context.Background(), args)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("stderr:\n%s", errb.String())
		}
	})
	return code, out.String(), errb.String()
}

func TestConfigInitFlagsCreatesInTheExpectedFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jira-cli", "config.yaml") // folder created
	code, out, _ := runInit(t, false, "", "", "config", "setup", "--config", path, "--base-url", "https://jira.example.com/jira")
	if code != 0 || !strings.Contains(out, `"instance": "prod"`) || !strings.Contains(out, `"jiracli auth login"`) {
		t.Fatalf("exit %d\n%s", code, out)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	in := c.Instances["prod"]
	if c.DefaultInstance != "prod" || in.BaseURL != "https://jira.example.com/jira" || in.Auth.CredentialRef != "jira-prod" || !in.ReadOnly {
		t.Fatalf("%+v %+v", c, in)
	}
	// Missing --base-url for a new instance, an unreachable host: refused, nothing written.
	other := filepath.Join(t.TempDir(), "c.yaml")
	if code, _, _ := runInit(t, false, "", "", "config", "setup", "--config", other); code != 2 {
		t.Fatalf("no --base-url: exit %d", code)
	}
	if code, _, errOut := runInit(t, false, "", "jira.example.com", "config", "setup", "--config", other, "--base-url", "https://jira.example.com"); code == 0 || !strings.Contains(errOut, "cannot reach") {
		t.Fatalf("unreachable: exit %d", code)
	}
	if _, err := os.Stat(other); err == nil {
		t.Fatal("written although refused")
	}
}

func TestConfigInitGuidedChecksEachAnswerAndUpdates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	ca := testpki.WriteFile(t, dir, "ca.pem", testpki.New(t).CAPEM)
	notCA := testpki.WriteFile(t, dir, "notes.txt", []byte("not a certificate"))
	code, _, errOut := runInit(t, true, answers(
		"",                         // instance: prod
		"https://down.example.com", // unreachable: asked again
		"https://jira.example.com", //
		"",                         // token name: jira-prod
		"",                         // read-only: yes
		dir+"/missing.pem",         // no such file: asked again
		notCA,                      // not a certificate: asked again
		ca,                         //
		"",                         // default: yes
		"",                         // write it
	), "down.example.com", "config", "setup", "--config", path)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"cannot reach down.example.com:443", "cannot read " + dir + "/missing.pem", "jiracli certs fetch", "jiracli auth login"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q", want)
		}
	}
	if in := mustLoad(t, path).Instances["prod"]; in.TLS.CABundle != ca || in.BaseURL != "https://jira.example.com" {
		t.Fatalf("%+v", in)
	}

	// A setting it does not manage survives an update; current values are the defaults.
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "    read_only: true\n", "    read_only: true\n    timeouts:\n      request: 30s\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runInit(t, true, answers("", "", "team-pat", "n", "-", "", ""), "", "config", "setup", "--config", path)
	if code != 0 || !strings.Contains(errOut, "[https://jira.example.com]") {
		t.Fatalf("update: exit %d\n%s", code, errOut)
	}
	in := mustLoad(t, path).Instances["prod"]
	if in.Auth.CredentialRef != "team-pat" || in.ReadOnly || in.TLS.CABundle != "" || in.Timeouts.Request != "30s" {
		t.Fatalf("update: %+v", in)
	}
}

func TestMissingConfigOffersToCreateIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jira-cli", "config.yaml")
	// In a terminal: offered, created, exit 0.
	code, _, errOut := runInit(t, true, answers("", "", "https://jira.example.com", "", "", "", "", ""), "", "--config", path, "myself", "get")
	if code != 0 || !strings.Contains(errOut, "No configuration yet") || !strings.Contains(errOut, "run your command again") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if mustLoad(t, path).Instances["prod"] == nil {
		t.Fatal("not created")
	}
	// Without a terminal: the error, with the command to run.
	code, _, errOut = runInit(t, false, "", "", "--config", path+".missing", "myself", "get")
	if code != 3 || !strings.Contains(errOut, "jiracli config setup") || strings.Contains(errOut, "Create it now") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
}

func mustLoad(t *testing.T, path string) *config.Config {
	t.Helper()
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
