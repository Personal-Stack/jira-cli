package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedaction(t *testing.T) {
	var buf bytes.Buffer
	log, _, err := New(&buf, Options{Level: "debug", Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("sending Authorization: Bearer abcdefSECRET1",
		"authorization", "Bearer abcdefSECRET2",
		"refresh_token", "SECRET3",
		"client_secret", "SECRET4",
		"url", "https://jira/callback?code=SECRET5&state=SECRET6&x=1",
		"proxy", "http://user:SECRET7@proxy:3128",
		"error", errors.New("token endpoint said Bearer SECRET8xyz"),
		slog.Group("headers", "Cookie", "JSESSIONID=SECRET9"),
		"token_url", "https://jira/rest/oauth2/latest/token", // not secret: _url suffix
		"credential_ref", "jira-prod",
	)
	out := buf.String()
	for i := 1; i <= 9; i++ {
		if strings.Contains(out, "SECRET"+string(rune('0'+i))) {
			t.Errorf("SECRET%d leaked: %s", i, out)
		}
	}
	for _, keep := range []string{"x=1", "jira-prod", "oauth2/latest/token"} {
		if !strings.Contains(out, keep) {
			t.Errorf("over-redacted %q: %s", keep, out)
		}
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
}

func TestLevelsAndFormats(t *testing.T) {
	var buf bytes.Buffer
	log, _, _ := New(&buf, Options{Level: "warn"})
	log.Info("hidden")
	log.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Fatal(buf.String())
	}
	if _, _, err := New(&buf, Options{Level: "loud"}); err == nil {
		t.Fatal("invalid level accepted")
	}
	if _, _, err := New(&buf, Options{Format: "xml"}); err == nil {
		t.Fatal("invalid format accepted")
	}
}

func TestLogFileMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "jira.log")
	log, c, err := New(nil, Options{File: p, Attrs: []slog.Attr{slog.String("run_id", "abc")}})
	if err != nil {
		t.Fatal(err)
	}
	log.Warn("x")
	c.Close()
	st, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	if st.Mode().Perm() != 0o600 || !strings.Contains(string(b), "run_id=abc") {
		t.Fatalf("mode %o content %s", st.Mode().Perm(), b)
	}
}
