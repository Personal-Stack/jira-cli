package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `version: 1
# keep me
default_instance: prod
instances:
  prod:
    deployment: datacenter
    base_url: https://jira.example.internal
    auth:
      type: pat
      credential_ref: jira-prod
  staging:
    deployment: datacenter
    base_url: https://jira-staging.example.internal
    auth:
      type: oauth
      credential_ref: jira-staging
      oauth:
        authorization_url: https://jira-staging.example.internal/rest/oauth2/latest/authorize
        token_url: https://jira-staging.example.internal/rest/oauth2/latest/token
        client_id: abc
        redirect_uri: http://127.0.0.1:8765/callback
`

func TestParseAndSelect(t *testing.T) {
	c, err := Parse([]byte(sample), "x")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvInstance, "")
	in, src, err := c.Select("")
	if err != nil || in.Name != "prod" || src != "default" {
		t.Fatalf("default: %v %v %v", in, src, err)
	}
	t.Setenv(EnvInstance, "staging")
	if in, src, _ = c.Select(""); in.Name != "staging" || src != "env" {
		t.Fatalf("env selection: %s %s", in.Name, src)
	}
	if in, src, _ = c.Select("prod"); in.Name != "prod" || src != "flag" {
		t.Fatalf("flag must win over env: %s %s", in.Name, src)
	}
	if _, _, err := c.Select("nope"); err == nil {
		t.Fatal("unknown instance must fail")
	}
}

func TestRejectsPlaintextSecretsAndUnsafeValues(t *testing.T) {
	cases := map[string]string{
		"token key":   strings.Replace(sample, "credential_ref: jira-prod", "credential_ref: jira-prod\n      token: abc", 1),
		"http":        strings.Replace(sample, "https://jira.example.internal", "http://jira.example.internal", 1),
		"basic":       strings.Replace(sample, "type: pat", "type: basic", 1),
		"userinfo":    strings.Replace(sample, "https://jira.example.internal", "https://u:p@jira.example.internal", 1),
		"bad ref":     strings.Replace(sample, "credential_ref: jira-prod", "credential_ref: ../etc/passwd", 1),
		"redirect":    strings.Replace(sample, "http://127.0.0.1:8765/callback", "https://evil.example/cb", 1),
		"no version":  strings.Replace(sample, "version: 1", "version: 2", 1),
		"bad default": strings.Replace(sample, "default_instance: prod", "default_instance: nope", 1),
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y), "x"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestAllowHTTPExplicit(t *testing.T) {
	y := strings.Replace(sample, "base_url: https://jira.example.internal", "base_url: http://localhost:8080\n    allow_insecure_http: true", 1)
	if _, err := Parse([]byte(y), "x"); err != nil {
		t.Fatal(err)
	}
}

func TestSetDefaultInstancePreservesComments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaultInstance(p, "staging"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "# keep me") || !strings.Contains(string(b), "default_instance: staging") {
		t.Fatalf("unexpected:\n%s", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %o", st.Mode().Perm())
	}
	if err := SetDefaultInstance(p, "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	if _, err := Load("../../examples/config.yaml"); err != nil {
		t.Fatal(err)
	}
}
