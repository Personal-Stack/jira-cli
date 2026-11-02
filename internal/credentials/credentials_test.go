package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestSecretNeverFormats(t *testing.T) {
	s := NewSecret([]byte("super-secret-token"))
	b, _ := json.Marshal(map[string]any{"s": s})
	out := fmt.Sprintf("%v %s %+v %#v %q %x", s, s, s, s, s, s) + string(b)
	if strings.Contains(out, "super-secret") {
		t.Fatalf("secret leaked: %s", out)
	}
}

func TestChainOrder(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit")
	os.WriteFile(explicit, []byte("from-file\n"), 0o600)
	sdir := filepath.Join(dir, "secrets")
	os.Mkdir(sdir, 0o700)
	os.WriteFile(filepath.Join(sdir, "jira-prod"), []byte("from-dir"), 0o600)
	e := map[string]string{
		EnvCredentialFile: explicit, EnvCredentialsDir: sdir, EnvNoKeyring: "1",
		"JIRA_CREDENTIAL_JIRA_PROD": "from-env", "JIRA_CREDENTIAL_OTHER": "other-env",
	}
	c := Default(Options{PrimaryRef: "jira-prod", Getenv: env(e)})
	f, err := c.Get("jira-prod")
	if err != nil || f.Secret.Reveal() != "from-file" || f.Source != "file:"+explicit {
		t.Fatalf("explicit file must win: %+v %v", f, err)
	}
	// JIRA_CREDENTIAL_FILE applies only to the primary ref.
	if f, err = c.Get("other"); err != nil || f.Secret.Reveal() != "other-env" {
		t.Fatalf("env fallback: %+v %v", f, err)
	}
	delete(e, EnvCredentialFile)
	c = Default(Options{PrimaryRef: "jira-prod", Getenv: env(e)})
	if f, _ = c.Get("jira-prod"); f.Secret.Reveal() != "from-dir" {
		t.Fatalf("dir should win over env: %+v", f)
	}
	if _, err := c.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := c.Get("../etc/passwd"); err == nil {
		t.Fatal("path traversal ref accepted")
	}
	if _, err := c.Writer(); err == nil {
		t.Fatal("no writer expected with keyring disabled")
	}
}

func TestReadSecretFilePermissions(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s")
	os.WriteFile(p, []byte("x"), 0o600)
	if _, w, err := ReadSecretFile(p); err != nil || len(w) != 0 {
		t.Fatalf("0600: %v %v", w, err)
	}
	os.Chmod(p, 0o644)
	if _, w, err := ReadSecretFile(p); err != nil || len(w) != 1 {
		t.Fatalf("0644 should warn: %v %v", w, err)
	}
	os.Chmod(p, 0o666)
	if _, _, err := ReadSecretFile(p); err == nil {
		t.Fatal("world-writable must be refused")
	}
	os.Chmod(p, 0o600)
	os.WriteFile(p, []byte("\n"), 0o600)
	if _, _, err := ReadSecretFile(p); err == nil {
		t.Fatal("empty must be refused")
	}
	if _, _, err := ReadSecretFile(dir); err == nil {
		t.Fatal("directory must be refused")
	}
}

func TestEnvName(t *testing.T) {
	if got := EnvName("jira-prod.v2"); got != "JIRA_CREDENTIAL_JIRA_PROD_V2" {
		t.Fatal(got)
	}
}
