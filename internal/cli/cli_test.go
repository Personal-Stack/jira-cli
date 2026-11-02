package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/personal-stack/jira-cli/generated"
	"github.com/personal-stack/jira-cli/internal/testpki"
)

const token = "PAT-SECRET-0123456789"

type fakeJira struct {
	mu       sync.Mutex
	requests []string
	auths    []string
}

func (f *fakeJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.Header().Set("X-AREQUESTID", "870x1234x1")
	if r.URL.Path == "/jira/rest/api/2/serverInfo" {
		io.WriteString(w, `{"version":"10.3.26","deploymentType":"Server","baseUrl":"`+"https://"+r.Host+`/jira","buildNumber":1}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(401)
		io.WriteString(w, `{"errorMessages":["You are not authenticated"]}`)
		return
	}
	switch {
	case r.URL.Path == "/jira/rest/api/2/myself":
		io.WriteString(w, `{"name":"jdoe","key":"JIRAUSER1","displayName":"Jane Doe","active":true}`)
	case r.URL.Path == "/jira/rest/api/2/issue/ABC-1" && r.Method == "GET":
		io.WriteString(w, `{"id":"10001","key":"ABC-1","fields":{"summary":"First","status":{"name":"Open"}}}`)
	case r.URL.Path == "/jira/rest/api/2/issue/NOPE-1":
		w.WriteHeader(404)
		io.WriteString(w, `{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`)
	case r.URL.Path == "/jira/rest/api/2/search":
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		var issues []string
		for i := start; i < start+2 && i < 5; i++ {
			issues = append(issues, `{"key":"ABC-`+strconv.Itoa(i)+`"}`)
		}
		io.WriteString(w, `{"startAt":`+strconv.Itoa(start)+`,"maxResults":2,"total":5,"issues":[`+strings.Join(issues, ",")+`]}`)
	default:
		w.WriteHeader(204)
	}
}

type env struct {
	t    *testing.T
	jira *fakeJira
	vars map[string]string
	dir  string
}

func setup(t *testing.T, extra string) *env {
	p := testpki.New(t)
	fj := &fakeJira{}
	srv := httptest.NewUnstartedServer(fj)
	cert, _, _ := p.Issue(t, "jira", "127.0.0.1")
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	ca := testpki.WriteFile(t, dir, "ca.pem", p.CAPEM)
	tok := testpki.WriteFile(t, dir, "pat", []byte(token+"\n"))
	cfg := `version: 1
default_instance: test
instances:
  test:
    deployment: datacenter
    base_url: ` + srv.URL + `/jira
    auth:
      type: pat
      credential_ref: test-pat
    tls:
      ca_bundle: ` + ca + "\n" + extra
	cfgPath := testpki.WriteFile(t, dir, "config.yaml", []byte(cfg))
	return &env{t: t, jira: fj, dir: dir, vars: map[string]string{
		"JIRA_CONFIG": cfgPath, "JIRA_CREDENTIAL_FILE": tok, "JIRA_NO_KEYRING": "1", "JIRA_CREDENTIALS_DIR": dir + "/none",
	}}
}

func (e *env) run(stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	app := NewApp("test", generated.Profiles, generated.DefaultProfile)
	app.In, app.Out, app.Err = strings.NewReader(stdin), &out, &errb
	app.Getenv = func(k string) string { return e.vars[k] }
	app.IsTerminal = func() bool { return false }
	code := app.Execute(context.Background(), args)
	if strings.Contains(out.String()+errb.String(), token) {
		e.t.Fatalf("token leaked in output of %v", args)
	}
	// Surface the CLI's stderr (logs + errors) when a test fails, so CI
	// output shows what happened.
	e.t.Cleanup(func() {
		if e.t.Failed() {
			e.t.Logf("jira %v (exit %d) stderr:\n%s", args, code, errb.String())
		}
	})
	return code, out.String(), errb.String()
}

func TestGetIssue(t *testing.T) {
	e := setup(t, "")
	code, out, errs := e.run("", "issue", "get", "ABC-1", "--fields", "summary,status")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil || v["key"] != "ABC-1" {
		t.Fatalf("output: %s", out)
	}
	if e.jira.requests[0] != "GET /jira/rest/api/2/issue/ABC-1?fields=summary%2Cstatus" {
		t.Fatalf("request: %v", e.jira.requests)
	}
	if e.jira.auths[0] != "Bearer "+token {
		t.Fatal("PAT not sent as Bearer token")
	}
}

func TestTableAndYAML(t *testing.T) {
	e := setup(t, "")
	_, out, _ := e.run("", "issue", "get", "ABC-1", "-o", "yaml")
	if !strings.HasPrefix(out, "id: \"10001\"\nkey: ABC-1") {
		t.Fatalf("yaml: %q", out)
	}
	_, out, _ = e.run("", "issue", "search", "--jql", "project = ABC", "-o", "table")
	if !strings.Contains(out, "KEY") || !strings.Contains(out, "ABC-0") {
		t.Fatalf("table: %s", out)
	}
}

func TestPaginationAll(t *testing.T) {
	e := setup(t, "")
	code, out, errs := e.run("", "issue", "search", "--jql", "project = ABC", "--all")
	if code != 0 {
		t.Fatalf("exit %d %s", code, errs)
	}
	var v struct {
		Issues []map[string]any `json:"issues"`
		Total  int              `json:"total"`
	}
	json.Unmarshal([]byte(out), &v)
	if len(v.Issues) != 5 || len(e.jira.requests) != 3 {
		t.Fatalf("got %d issues in %d requests: %v", len(v.Issues), len(e.jira.requests), e.jira.requests)
	}
}

func TestNotFoundIsStructured(t *testing.T) {
	e := setup(t, "")
	code, _, errs := e.run("", "issue", "get", "NOPE-1")
	if code != 6 {
		t.Fatalf("exit %d", code)
	}
	var v struct {
		Error struct {
			Layer      string `json:"layer"`
			HTTPStatus int    `json:"http_status"`
			Message    string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(errs), &v); err != nil || v.Error.HTTPStatus != 404 || v.Error.Layer != "jira_api" || !strings.Contains(v.Error.Message, "does not exist") {
		t.Fatalf("stderr: %s", errs)
	}
}

func TestDestructiveRefusedWithoutYes(t *testing.T) {
	e := setup(t, "")
	code, _, errs := e.run("", "issue", "delete", "ABC-1")
	if code != 9 || len(e.jira.requests) != 0 || !strings.Contains(errs, "confirmation_required") {
		t.Fatalf("exit %d, requests %v, %s", code, e.jira.requests, errs)
	}
	code, out, _ := e.run("", "issue", "delete", "ABC-1", "--yes")
	if code != 0 || !strings.Contains(out, `"ok": true`) || e.jira.requests[0] != "DELETE /jira/rest/api/2/issue/ABC-1?" {
		t.Fatalf("exit %d %s %v", code, out, e.jira.requests)
	}
}

func TestReadOnlyInstance(t *testing.T) {
	e := setup(t, "    read_only: true\n")
	body := filepath.Join(e.dir, "issue.json")
	os.WriteFile(body, []byte(`{"fields":{"summary":"x"}}`), 0o600)
	code, _, errs := e.run("", "issue", "create", "--file", body)
	if code != 9 || !strings.Contains(errs, "read_only_mode") || len(e.jira.requests) != 0 {
		t.Fatalf("exit %d %s %v", code, errs, e.jira.requests)
	}
	if code, _, _ := e.run("", "issue", "get", "ABC-1"); code != 0 {
		t.Fatal("reads must work in read-only mode")
	}
	// The POST search is classified read and therefore allowed.
	if code, _, errs := e.run(`{"jql":"project = ABC"}`, "issue", "search-post", "--stdin"); code != 0 {
		t.Fatalf("search-post in read-only: %d %s", code, errs)
	}
}

func TestReadOnlyEnv(t *testing.T) {
	e := setup(t, "")
	e.vars["JIRA_READ_ONLY"] = "1"
	if code, _, _ := e.run("", "issue", "delete", "ABC-1", "--yes"); code != 9 {
		t.Fatalf("JIRA_READ_ONLY must block, exit %d", code)
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	e := setup(t, "")
	e.vars["JIRA_CREDENTIAL_FILE"] = filepath.Join(e.dir, "does-not-exist")
	code, out, errs := e.run(`{"fields":{"summary":"hello"}}`, "issue", "create", "--stdin", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d %s", code, errs)
	}
	if len(e.jira.requests) != 0 {
		t.Fatal("dry-run sent a request")
	}
	if !strings.Contains(out, `"dry_run": true`) || !strings.Contains(out, `"summary": "hello"`) || !strings.Contains(out, "REDACTED") || !strings.Contains(out, "/jira/rest/api/2/issue") {
		t.Fatalf("dry-run output: %s", out)
	}
}

func TestBodyValidation(t *testing.T) {
	e := setup(t, "")
	code, _, errs := e.run(`not json`, "issue", "create", "--stdin")
	if code != 2 || !strings.Contains(errs, "not valid JSON") {
		t.Fatalf("exit %d %s", code, errs)
	}
	// editIssue declares a required body in the spec (createIssue does not).
	code, _, errs = e.run("", "issue", "edit", "ABC-1")
	if code != 2 || !strings.Contains(errs, "requires a request body") {
		t.Fatalf("exit %d %s", code, errs)
	}
	code, _, _ = e.run("", "issue", "get")
	if code != 2 {
		t.Fatalf("missing arg: exit %d", code)
	}
	code, _, errs = e.run("", "issue", "search", "--max-results", "ten")
	if code != 2 || !strings.Contains(errs, "not an integer") {
		t.Fatalf("exit %d %s", code, errs)
	}
	if len(e.jira.requests) != 0 {
		t.Fatalf("invalid input reached the server: %v", e.jira.requests)
	}
}

func TestBadCredential(t *testing.T) {
	e := setup(t, "")
	os.WriteFile(e.vars["JIRA_CREDENTIAL_FILE"], []byte("wrong"), 0o600)
	code, _, errs := e.run("", "auth", "test")
	if code != 4 || !strings.Contains(errs, `"layer": "authentication"`) {
		t.Fatalf("exit %d %s", code, errs)
	}
}

func TestDiscoveryAndDoctor(t *testing.T) {
	e := setup(t, "")
	code, out, _ := e.run("", "commands", "list")
	var cl struct {
		Count    int `json:"count"`
		Commands []struct {
			Command string `json:"command"`
			Risk    string `json:"risk"`
		} `json:"commands"`
	}
	json.Unmarshal([]byte(out), &cl)
	if code != 0 || cl.Count != len(generated.Profiles[generated.DefaultProfile].Operations) || cl.Count < 400 {
		t.Fatalf("commands list: %d %d", code, cl.Count)
	}
	code, out, _ = e.run("", "commands", "describe", "issue", "get")
	if code != 0 || !strings.Contains(out, `"operation_id": "getIssue"`) || !strings.Contains(out, `"sha256"`) {
		t.Fatalf("describe: %s", out)
	}
	code, out, _ = e.run("", "capabilities")
	if code != 0 || !strings.Contains(out, `"instance": "test"`) {
		t.Fatalf("capabilities: %s", out)
	}
	code, out, errs := e.run("", "doctor")
	if code != 0 || !strings.Contains(out, `"ok": true`) || !strings.Contains(out, `"jdoe"`) {
		t.Fatalf("doctor %d: %s %s", code, out, errs)
	}
	code, out, _ = e.run("", "auth", "status")
	if code != 0 || !strings.Contains(out, `"credential_present": true`) {
		t.Fatalf("auth status: %s", out)
	}
	code, out, _ = e.run("", "config", "validate")
	if code != 0 || !strings.Contains(out, `"valid": true`) {
		t.Fatalf("config validate: %s", out)
	}
}

func TestDoctorReportsTLSLayer(t *testing.T) {
	e := setup(t, "")
	// Replace the CA bundle with an unrelated CA.
	other := testpki.New(t)
	cfg, _ := os.ReadFile(e.vars["JIRA_CONFIG"])
	ca2 := testpki.WriteFile(t, e.dir, "other-ca.pem", other.CAPEM)
	os.WriteFile(e.vars["JIRA_CONFIG"], []byte(strings.Replace(string(cfg), filepath.Join(e.dir, "ca.pem"), ca2, 1)), 0o600)
	code, out, _ := e.run("", "doctor")
	if code != 8 || !strings.Contains(out, `"layer": "tls_certificate"`) {
		t.Fatalf("doctor exit %d: %s", code, out)
	}
}

func TestStructuredTrace(t *testing.T) {
	e := setup(t, "")
	code, _, errs := e.run("", "issue", "get", "ABC-1", "--log-level", "debug", "--log-format", "json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var runID string
	msgs := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(errs), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		id, _ := rec["run_id"].(string)
		if id == "" || (runID != "" && id != runID) {
			t.Fatalf("missing/inconsistent run_id: %v", rec)
		}
		runID = id
		msgs[rec["msg"].(string)] = rec
	}
	for _, m := range []string{"command started", "config loaded", "instance selected", "operation", "credential resolved", "http request", "http response", "command finished"} {
		if _, ok := msgs[m]; !ok {
			t.Errorf("missing log event %q; got %v", m, keys(msgs))
		}
	}
	resp := msgs["http response"]
	if resp["jira_request_id"] != "870x1234x1" || resp["status"] != float64(200) || resp["request_id"] == nil || resp["tls_ms"] == nil {
		t.Errorf("http response fields: %v", resp)
	}
	if msgs["operation"]["operation_id"] != "getIssue" || msgs["command finished"]["exit_code"] != float64(0) {
		t.Errorf("operation/finish fields: %v %v", msgs["operation"], msgs["command finished"])
	}
	if msgs["credential resolved"]["source"] != "file:"+e.vars["JIRA_CREDENTIAL_FILE"] {
		t.Errorf("credential source: %v", msgs["credential resolved"])
	}
}

func TestFailureIsTraced(t *testing.T) {
	e := setup(t, "")
	e.vars["JIRA_LOG_LEVEL"] = "info"
	e.vars["JIRA_LOG_FORMAT"] = "json"
	code, _, errs := e.run("", "issue", "get", "NOPE-1")
	if code != 6 || !strings.Contains(errs, `"msg":"command finished"`) || !strings.Contains(errs, `"exit_code":6`) || !strings.Contains(errs, `"error_layer":"jira_api"`) {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

func TestLogFile(t *testing.T) {
	e := setup(t, "")
	logPath := filepath.Join(e.dir, "trace.log")
	e.vars["JIRA_LOG_FILE"] = logPath
	code, _, errs := e.run("", "issue", "get", "ABC-1", "-v")
	if code != 0 || errs != "" {
		t.Fatalf("exit %d, stderr should be empty when logging to a file: %q", code, errs)
	}
	b, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(b), "http response") || strings.Contains(string(b), token) {
		t.Fatalf("log file: %v %s", err, b)
	}
	if code, _, _ := e.run("", "issue", "get", "ABC-1", "--log-level", "loud"); code != 2 {
		t.Fatalf("invalid level must be a usage error, got %d", code)
	}
}

func keys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
