package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/generated"
)

// noToken removes the mounted token: the credential comes from the 0600
// file store, which starts empty.
func (e *env) noToken() string {
	delete(e.vars, "JIRA_CREDENTIAL_FILE")
	store := filepath.Join(e.dir, "creds")
	e.vars["JIRA_CREDENTIAL_STORE"], e.vars["JIRA_CREDENTIALS_DIR"] = "file", store
	return store
}

// runTerminal runs like a terminal session (prompts and offers enabled).
func (e *env) runTerminal(stdin string, args ...string) (int, string, string) {
	e.t.Helper()
	var out, errb bytes.Buffer
	app := NewApp("test", generated.Profiles, generated.DefaultProfile)
	app.In, app.Out, app.Err = strings.NewReader(stdin), &out, &errb
	app.Getenv = func(k string) string { return e.vars[k] }
	app.IsTerminal = func() bool { return true }
	code := app.Execute(context.Background(), append([]string{"--no-color"}, args...))
	if strings.Contains(out.String()+errb.String(), token) {
		e.t.Fatalf("token leaked in output of %v", args)
	}
	return code, out.String(), errb.String()
}

func TestConfigSetupStoresTheTokenAndChecksTheConnection(t *testing.T) {
	e := setup(t, "")
	store := e.noToken()
	answers := strings.Join([]string{
		"", "", "", "", "", "", // instance test, URL, credential_ref, read-only, CA, default: unchanged
		"",                // write it
		"",                // store the token now: yes
		"WRONG-TOKEN-123", // refused by Jira: asked again
		token,             // accepted
		"",                // check the connection: yes
	}, "\n") + "\n"
	code, _, errOut := e.runTerminal(answers, "config", "setup")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	for _, want := range []string{"Store the token now", "token verified as Jane Doe and stored", "Check the connection now", "✓ authentication", "Ready"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("missing %q:\n%s", want, errOut)
		}
	}
	if strings.Count(errOut, "Personal access token") != 2 {
		t.Fatalf("the refused token was not asked again:\n%s", errOut)
	}
	if b, err := os.ReadFile(filepath.Join(store, "test-pat")); err != nil || strings.TrimSpace(string(b)) != token {
		t.Fatalf("token not stored: %v", err)
	}

	// Run again: the stored token is found, nothing is asked about it.
	code, _, errOut = e.runTerminal(strings.Repeat("\n", 7)+"n\n", "config", "setup")
	if code != 0 || !strings.Contains(errOut, "token test-pat is available") || strings.Contains(errOut, "Store the token now") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
}

func TestConfigSetupFollowUpsAreOptional(t *testing.T) {
	e := setup(t, "")
	e.noToken()
	// Answers end after "Write it": the configuration is written and the
	// remaining steps are printed, without an error.
	code, _, errOut := e.runTerminal(strings.Repeat("\n", 7), "config", "setup")
	if code != 0 || !strings.Contains(errOut, "Later: $ jiracli auth login") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
}

func TestMissingTokenOffersToStoreIt(t *testing.T) {
	e := setup(t, "")
	e.noToken()
	code, _, errOut := e.runTerminal("y\n"+token+"\n", "issue", "get", "ABC-1")
	if code != 0 || !strings.Contains(errOut, "No token stored for instance test") || !strings.Contains(errOut, "run your command again") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if code, _, errOut = e.run("", "issue", "get", "ABC-1"); code != 0 {
		t.Fatalf("with the stored token: exit %d\n%s", code, errOut)
	}
	// auth commands report the missing token instead of offering.
	e.noToken()
	_ = os.RemoveAll(e.vars["JIRA_CREDENTIALS_DIR"])
	if code, _, errOut = e.runTerminal("y\n", "auth", "status"); code == 0 || strings.Contains(errOut, "Store it now") {
		t.Fatalf("auth status: exit %d\n%s", code, errOut)
	}
}
