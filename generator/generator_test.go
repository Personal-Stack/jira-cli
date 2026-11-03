package generator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/internal/operation"
)

const baseSpec = `openapi: 3.0.1
info: {title: Fixture, version: "1.2.3"}
paths:
  /api/2/issue/{issueIdOrKey}:
    get:
      operationId: getIssue
      tags: [issue]
      summary: Get <b>issue</b> {@code key}
      parameters:
        - {name: issueIdOrKey, in: path, required: true, schema: {type: string}}
        - {name: fields, in: query, schema: {type: array, items: {type: string}}}
        - {name: expand, in: query, schema: {$ref: '#/components/schemas/StringList'}}
        - {name: mode, in: query, schema: {type: string, enum: [a, b], default: a}}
    delete:
      operationId: deleteIssue
      tags: [issue]
      parameters:
        - {name: issueIdOrKey, in: path, required: true, schema: {type: string}}
  /api/2/issue:
    post:
      operationId: createIssue
      tags: [issue]
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/IssueBean'}
  /api/2/issue/{issueIdOrKey}/archive:
    put:
      operationId: archiveIssue
      tags: [issue]
      parameters:
        - {name: issueIdOrKey, in: path, required: true, schema: {type: string}}
  /api/2/search:
    get:
      operationId: search_1
      tags: [search]
      parameters:
        - {name: startAt, in: query, schema: {type: integer}}
        - {name: maxResults, in: query, schema: {type: integer}}
        - {name: jql, in: query, schema: {type: string}}
  /api/2/cookie:
    get:
      operationId: cookieOp
      tags: [misc]
      parameters:
        - {name: session, in: cookie, schema: {type: string}}
  /api/2/attachment:
    post:
      operationId: addAttachment
      tags: [attachment]
      requestBody:
        required: true
        content:
          multipart/form-data:
            schema:
              type: object
              properties:
                file: {type: string, format: binary}
components:
  schemas:
    StringList: {type: object}
    IssueBean:
      type: object
      required: [fields]
      properties:
        fields: {type: object, additionalProperties: true}
        update: {type: object}
        transition: {type: object, properties: {id: {type: string}}}
        status: {type: string, enum: [open, closed]}
`

const baseOverlay = `version: 1
schema_aliases:
  StringList: string
`

const baseRisk = `version: 1
admin_tags: [attachment]
destructive_words: [archive]
operations:
  createIssue:
    risk: write
    idempotent: false
    reason: test
`

type fixture struct {
	files map[string]string
	specs []string // extra spec paths beyond specs/base.yaml
}

func writeFixture(t *testing.T, f fixture) string {
	t.Helper()
	root := t.TempDir()
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	files := map[string]string{}
	for name, content := range f.files {
		if strings.HasSuffix(name, ".yaml") && strings.Contains(content, "openapi:") {
			content = withResponses(content)
		}
		files[name] = content
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	b.WriteString("version: 1\nprofiles:\n  - name: test-1.0\n    deployment: datacenter\n    base_path: /rest\n    specs:\n")
	for _, sp := range append([]string{"specs/base.yaml"}, f.specs...) {
		ver := "1.2.3"
		if strings.Contains(files[sp], `version: "9.9"`) {
			ver = "9.9"
		}
		b.WriteString("      - {name: " + strings.TrimSuffix(filepath.Base(sp), ".yaml") + ", source: custom, path: " + sp + ", version: \"" + ver + "\", sha256: " + sum(files[sp]) + "}\n")
	}
	b.WriteString("    overlays:\n      - {path: specs/overlay.yaml, sha256: " + sum(files["specs/overlay.yaml"]) + "}\n")
	b.WriteString("    risk: {path: specs/risk.yaml, sha256: " + sum(files["specs/risk.yaml"]) + "}\n")
	os.WriteFile(filepath.Join(root, "specs/manifest.yaml"), []byte(b.String()), 0o644)
	return root
}

// withResponses adds the mandatory responses object after every tags line.
func withResponses(spec string) string {
	var out []string
	for _, l := range strings.Split(spec, "\n") {
		out = append(out, l)
		if strings.HasPrefix(l, "      tags: ") {
			out = append(out, "      responses: {'200': {description: ok}}")
		}
	}
	return strings.Join(out, "\n")
}

func base() fixture {
	return fixture{files: map[string]string{"specs/base.yaml": baseSpec, "specs/overlay.yaml": baseOverlay, "specs/risk.yaml": baseRisk}}
}

func gen(t *testing.T, f fixture) (*Result, error) {
	return Generate(Options{Root: writeFixture(t, f), Manifest: "specs/manifest.yaml"})
}

func find(p *operation.Profile, group, name string) *operation.Operation { return p.Find(group, name) }

func TestMapping(t *testing.T) {
	res, err := gen(t, base())
	if err != nil {
		t.Fatal(err)
	}
	p := res.Profiles[0]
	get := find(p, "issue", "get")
	if get == nil {
		t.Fatalf("issue get missing; have %+v", p.Operations)
	}
	if get.Summary != "Get issue key" {
		t.Errorf("summary not cleaned: %q", get.Summary)
	}
	want := map[string]operation.Param{}
	for _, prm := range get.Params {
		want[prm.Name] = prm
	}
	if !want["fields"].Array || want["fields"].Flag != "fields" {
		t.Error("array query param")
	}
	if want["expand"].Type != "string" {
		t.Error("StringList alias not applied")
	}
	if strings.Join(want["mode"].Enum, ",") != "a,b" || want["mode"].Default != "a" {
		t.Error("enum/default")
	}
	if get.Risk != operation.RiskRead || !get.Idempotent {
		t.Error("GET must be read + idempotent")
	}

	create := find(p, "issue", "create")
	if create.Body == nil || create.Body.Mode != operation.BodyJSON || create.Body.Schema != "IssueBean" || create.Body.JSONType != "object" {
		t.Fatalf("body: %+v", create.Body)
	}
	var req []string
	for _, pr := range create.Body.Properties {
		if pr.Required {
			req = append(req, pr.Name)
		}
	}
	if strings.Join(req, ",") != "fields" {
		t.Errorf("required properties: %v", req)
	}
	if create.RiskSource != "risk-file: test" {
		t.Errorf("risk file not applied: %s", create.RiskSource)
	}

	if d := find(p, "issue", "delete"); d.Risk != operation.RiskDestructive || !d.ConfirmationRequired {
		t.Error("DELETE must be destructive + confirmation")
	}
	if a := find(p, "issue", "archive"); a.Risk != operation.RiskDestructive {
		t.Errorf("PUT archive must be raised to destructive by name, got %s", a.Risk)
	}
	att := find(p, "attachment", "add")
	if att == nil || att.Body.Mode != operation.BodyMultipart || att.Body.MultipartField != "file" || att.Risk != operation.RiskAdministrative {
		t.Fatalf("multipart/admin tag: %+v", att)
	}
	if s := find(p, "search", "search"); s == nil || s.Pagination == nil {
		t.Fatal("pagination not detected")
	}
	if len(p.Skipped) != 1 || p.Skipped[0].OperationID != "cookieOp" || !strings.Contains(p.Skipped[0].Reason, "cookie") {
		t.Fatalf("cookie op should be skipped and reported: %+v", p.Skipped)
	}
	if len(p.Warnings) == 0 {
		t.Error("expected warning for suffixed operationId search_1")
	}
}

func TestDeterministic(t *testing.T) {
	root := writeFixture(t, base())
	a, err := Generate(Options{Root: root, Manifest: "specs/manifest.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(Options{Root: root, Manifest: "specs/manifest.yaml"})
	if len(a.Files) != len(b.Files) {
		t.Fatal("file sets differ")
	}
	for name, content := range a.Files {
		if !bytes.Equal(content, b.Files[name]) {
			t.Fatalf("%s differs between runs", name)
		}
	}
}

func expectErr(t *testing.T, f fixture, substr string) {
	t.Helper()
	_, err := gen(t, f)
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Fatalf("want error containing %q, got %v", substr, err)
	}
}

func TestDuplicateOperationIDs(t *testing.T) {
	f := base()
	f.files["specs/base.yaml"] = strings.Replace(baseSpec, "operationId: deleteIssue", "operationId: getIssue", 1)
	// kin-openapi rejects duplicate operationIds during validation.
	expectErr(t, f, "same operation id")
	f.files["specs/overlay.yaml"] = baseOverlay + "operations:\n  DELETE /api/2/issue/{issueIdOrKey}:\n    operation_id: removeIssue\n"
	if _, err := gen(t, f); err != nil {
		t.Fatalf("overlay rename should resolve duplicates: %v", err)
	}
}

func TestNamingCollision(t *testing.T) {
	f := base()
	// An explicit overlay name wins; the colliding mechanical name falls
	// back to the full operationId.
	f.files["specs/overlay.yaml"] = baseOverlay + "operations:\n  DELETE /api/2/issue/{issueIdOrKey}:\n    command: get\n"
	res, err := gen(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if find(res.Profiles[0], "issue", "get-issue") == nil || find(res.Profiles[0], "issue", "get").OperationID != "deleteIssue" {
		t.Fatal("fallback naming not applied")
	}
	// Two explicit names that collide are an error.
	f.files["specs/overlay.yaml"] += "  GET /api/2/issue/{issueIdOrKey}:\n    command: get\n"
	expectErr(t, f, `command "issue" "get" is ambiguous`)
}

func TestReservedNames(t *testing.T) {
	f := base()
	f.files["specs/base.yaml"] = strings.Replace(baseSpec, "tags: [misc]", "tags: [config]", 1)
	f.files["specs/base.yaml"] = strings.Replace(f.files["specs/base.yaml"], "in: cookie", "in: query", 1)
	expectErr(t, f, `group "config" (tag "config") collides`)

	f = base()
	f.files["specs/base.yaml"] = strings.Replace(baseSpec, "name: jql, in: query", "name: output, in: query", 1)
	expectErr(t, f, "flag --output (param output) collides")
}

func TestPathParamMismatch(t *testing.T) {
	f := base()
	f.files["specs/base.yaml"] = strings.Replace(baseSpec, "- {name: session, in: cookie, schema: {type: string}}", "- {name: ghost, in: path, required: true, schema: {type: string}}", 1)
	expectErr(t, f, "path parameter")
	f.files["specs/overlay.yaml"] = baseOverlay + "operations:\n  GET /api/2/cookie:\n    params:\n      ghost: {in: query}\n"
	if _, err := gen(t, f); err != nil {
		t.Fatalf("overlay should fix the parameter: %v", err)
	}
}

func TestStaleOverlayAndRisk(t *testing.T) {
	f := base()
	f.files["specs/overlay.yaml"] = baseOverlay + "operations:\n  GET /api/2/nothing:\n    command: x\n"
	expectErr(t, f, "matches no operation")
	f = base()
	f.files["specs/risk.yaml"] = baseRisk + "  ghostOp:\n    risk: read\n    reason: x\n"
	expectErr(t, f, `risk file: operation "ghostOp" does not exist`)
}

func TestChecksumPinned(t *testing.T) {
	root := writeFixture(t, base())
	os.WriteFile(filepath.Join(root, "specs/base.yaml"), []byte(baseSpec+"\n# tampered\n"), 0o644)
	_, err := Generate(Options{Root: root, Manifest: "specs/manifest.yaml"})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got %v", err)
	}
}

func TestExternalRefsRejected(t *testing.T) {
	f := base()
	f.files["specs/base.yaml"] = strings.Replace(baseSpec, "$ref: '#/components/schemas/IssueBean'", "$ref: 'https://evil.example/schema.yaml#/X'", 1)
	expectErr(t, f, "")
	_, err := gen(t, f)
	if err == nil {
		t.Fatal("external reference must be rejected")
	}
}

func TestCrossSpecConflict(t *testing.T) {
	f := base()
	f.files["specs/custom.yaml"] = `openapi: 3.0.1
info: {title: Custom, version: "9.9"}
paths:
  /api/2/issue:
    post:
      operationId: createIssueCustom
      tags: [issue]
  /api/2/custom/thing:
    get:
      operationId: getThing
      tags: [custom]
`
	f.specs = []string{"specs/custom.yaml"}
	expectErr(t, f, "is defined by both base and custom")

	f.files["specs/overlay.yaml"] = baseOverlay + "exclude:\n  - {spec: base, method: POST, path: /api/2/issue, reason: replaced by custom spec}\n"
	f.files["specs/risk.yaml"] = "version: 1\ndestructive_words: [archive]\n"
	res, err := gen(t, f)
	if err != nil {
		t.Fatal(err)
	}
	p := res.Profiles[0]
	if find(p, "custom", "get-thing") == nil || find(p, "issue", "create-custom") == nil {
		t.Fatalf("custom operations missing: %v", p.Operations)
	}
	if len(p.Overlays) == 0 || p.Overlays[0].Name != "custom" || p.Overlays[0].Version != "9.9" {
		t.Fatalf("custom spec provenance: %+v", p.Overlays)
	}
}

func TestCleanTextStripsControlChars(t *testing.T) {
	if got := cleanText("a\x1b[31mred\x07</p>b", true); got != "a[31mred b" {
		t.Fatalf("%q", got)
	}
}
