package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/internal/sbx"
)

func TestCompletePath(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"company-ca.pem", "company-root.crt", "notes.txt", ".hidden.pem", "certs/inner.pem"} {
		p := filepath.Join(d, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, nil, 0o644)
	}
	_ = os.Mkdir(filepath.Join(d, "configs"), 0o755)
	_ = os.WriteFile(filepath.Join(d, "configs", "a.yaml"), nil, 0o644)
	dir := d + "/"
	for _, c := range []struct {
		line, want string
		choices    []string
		exts       []string
	}{
		{dir + "company-c", dir + "company-ca.pem", []string{"company-ca.pem"}, certExts},                      // unique: completed
		{dir + "comp", dir + "company-", []string{"company-ca.pem", "company-root.crt"}, certExts},             // common prefix
		{dir + "c", dir + "c", []string{"certs/", "company-ca.pem", "company-root.crt", "configs/"}, certExts}, // ambiguous: listed
		{dir + "ce", dir + "certs/", []string{"certs/"}, certExts},                                             // folder gets a slash
		{dir + "certs/", dir + "certs/inner.pem", []string{"inner.pem"}, certExts},                             // inside a folder
		{dir + "n", dir + "notes.txt", []string{"notes.txt"}, certExts},                                        // no certificate matches: any file
		{dir + ".h", dir + ".hidden.pem", []string{".hidden.pem"}, certExts},                                   // hidden only when asked
		{dir + "co", dir + "co", []string{"company-ca.pem", "company-root.crt", "configs/"}, certExts},         // no dotfiles listed
		{dir + "c", dir + "c", []string{"certs/", "configs/"}, nil},                                            // folders only
		{dir + "zzz", dir + "zzz", nil, certExts},                                                              // nothing
		{filepath.Join(d, "missing") + "/x", filepath.Join(d, "missing") + "/x", nil, certExts},                // unreadable folder
	} {
		got, choices := completePath(c.line, c.exts)
		if got != c.want || !reflect.DeepEqual(choices, c.choices) {
			t.Errorf("completePath(%q) = %q %v, want %q %v", strings.TrimPrefix(c.line, d), strings.TrimPrefix(got, d), choices, strings.TrimPrefix(c.want, d), c.choices)
		}
	}
	home, _ := os.UserHomeDir()
	if got, _ := completePath("~/", nil); home != "" && got != "~/" && !strings.HasPrefix(got, "~/") {
		t.Errorf("~ not kept: %q", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"jira.company.internal":         "https://jira.company.internal",
		" jira.company.internal/jira ":  "https://jira.company.internal/jira",
		"https://jira.company.internal": "https://jira.company.internal",
		"http://jira.company.internal":  "http://jira.company.internal", // refused later, not rewritten
		"":                              "",
	} {
		if got := sbx.NormalizeBaseURL(in); got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q", in, got)
		}
	}
}

func TestConfigSetupAddsHTTPS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if code, _, errOut := runInit(t, false, "", "", "config", "setup", "--config", path, "--base-url", "jira.example.com/jira"); code != 0 {
		t.Fatalf("flag: exit %d\n%s", code, errOut)
	}
	if got := mustLoad(t, path).Instances["prod"].BaseURL; got != "https://jira.example.com/jira" {
		t.Fatalf("flag: base_url %q", got)
	}
	// Guided: the answer without a scheme, then the default instance kept.
	code, _, errOut := runInit(t, true, answers("staging", "jira-staging.example.com", "", "", "", "n", ""), "", "config", "setup", "--config", path)
	if code != 0 {
		t.Fatalf("guided: exit %d\n%s", code, errOut)
	}
	if got := mustLoad(t, path).Instances["staging"].BaseURL; got != "https://jira-staging.example.com" {
		t.Fatalf("guided: base_url %q", got)
	}
	// http:// stays http:// and is refused.
	if code, _, errOut := runInit(t, false, "", "", "config", "setup", "--config", path, "--base-url", "http://jira.example.com"); code != 2 || !strings.Contains(errOut, "https://") {
		t.Fatalf("http: exit %d\n%s", code, errOut)
	}
}
