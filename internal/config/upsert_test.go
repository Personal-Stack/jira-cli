package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertInstanceCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "jira-cli", "config.yaml")
	if err := UpsertInstance(path, InstanceUpdate{Name: "prod", BaseURL: "https://jira.example.com/jira", CredentialRef: "jira-prod", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	in := c.Instances["prod"]
	if c.DefaultInstance != "prod" || in.BaseURL != "https://jira.example.com/jira" || in.Auth.Type != "pat" || in.Auth.CredentialRef != "jira-prod" || !in.ReadOnly || in.Deployment != "datacenter" {
		t.Fatalf("%+v %+v", c, in)
	}
}

func TestUpsertInstanceUpdatesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := `# my jiracli setup
version: 1
default_instance: prod
instances:
  prod:
    deployment: datacenter
    base_url: https://old.example.com # old host
    auth:
      type: pat
      credential_ref: jira-prod
    tls:
      ca_bundle: ` + path + `.ca
    timeouts:
      request: 30s
    read_only: true
  staging:
    deployment: datacenter
    base_url: https://staging.example.com
    auth: {type: pat, credential_ref: jira-staging}
`
	if err := os.WriteFile(path+".ca", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertInstance(path, InstanceUpdate{Name: "prod", BaseURL: "https://new.example.com", CredentialRef: "team-pat"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{"# my jiracli setup", "base_url: https://new.example.com", "credential_ref: team-pat", "request: 30s", "staging:", "https://staging.example.com"} {
		if !strings.Contains(s, want) {
			t.Errorf("lacks %q:\n%s", want, s)
		}
	}
	for _, gone := range []string{"read_only", "ca_bundle", "old.example.com"} {
		if strings.Contains(s, gone) {
			t.Errorf("still has %q:\n%s", gone, s)
		}
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}

	// A new instance does not take the default unless asked to.
	if err := UpsertInstance(path, InstanceUpdate{Name: "dev", BaseURL: "https://dev.example.com", CredentialRef: "jira-dev"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(path); c.DefaultInstance != "prod" || len(c.Instances) != 3 {
		t.Fatalf("default %q, %d instances", c.DefaultInstance, len(c.Instances))
	}
	if err := UpsertInstance(path, InstanceUpdate{Name: "dev", BaseURL: "https://dev.example.com", CredentialRef: "jira-dev", MakeDefault: true}); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(path); c.DefaultInstance != "dev" {
		t.Fatalf("default %q", c.DefaultInstance)
	}

	// Invalid result: refused, file unchanged.
	before, _ := os.ReadFile(path)
	if err := UpsertInstance(path, InstanceUpdate{Name: "prod", BaseURL: "ftp://x", CredentialRef: "jira-prod"}); err == nil {
		t.Fatal("invalid base_url accepted")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("file changed after a refused update")
	}
}
