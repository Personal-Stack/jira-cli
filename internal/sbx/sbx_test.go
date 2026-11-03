package sbx

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{
		Project: project, Settings: filepath.Join(root, "settings"), BaseURL: "https://jira.example.com:8443/jira",
		Instance: "prod", CredRef: "jira-prod", Service: "jira", Agent: "opencode", ReadOnly: true, Skill: true,
	}
}

func TestNewPlanDerivesHostPortAndCredentialEnv(t *testing.T) {
	p, err := NewPlan(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != "jira.example.com" || p.Port != 8443 || p.CredEnv != "JIRA_CREDENTIAL_JIRA_PROD" {
		t.Fatalf("host=%s port=%d env=%s", p.Host, p.Port, p.CredEnv)
	}
}

func TestNewPlanRejects(t *testing.T) {
	cases := map[string]func(*Options){
		"http":               func(o *Options) { o.BaseURL = "http://jira.example.com" },
		"credentials in URL": func(o *Options) { o.BaseURL = "https://u:p@jira.example.com" },
		"quote in URL":       func(o *Options) { o.BaseURL = "https://jira.example.com/'x" },
		"settings in project": func(o *Options) {
			o.Settings = filepath.Join(o.Project, "settings")
		},
		"settings with space": func(o *Options) { o.Settings += " x" },
		"relative project":    func(o *Options) { o.Project = "proj" },
		"missing project":     func(o *Options) { o.Project += "-missing" },
		"agent with space":    func(o *Options) { o.Agent = "my agent" },
		"bad name":            func(o *Options) { o.Name = "a/b" },
		"bad secret":          func(o *Options) { o.Service = "Jira" },
		"bad image":           func(o *Options) { o.Template = "img name" },
		"missing CA":          func(o *Options) { o.CABundle = "/does/not/exist.pem" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o := testOptions(t)
			mutate(&o)
			if _, err := NewPlan(o); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestBaseURLFromExistingConfig(t *testing.T) {
	o := testOptions(t)
	if err := os.MkdirAll(o.Settings, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "version: 1\ndefault_instance: b\ninstances:\n  a:\n    base_url: https://a.example.com\n  b:\n    base_url: https://b.example.com/jira\n"
	if err := os.WriteFile(filepath.Join(o.Settings, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	o.BaseURL = ""
	p, err := NewPlan(o)
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL != "https://b.example.com/jira" {
		t.Fatalf("base url %q", p.BaseURL)
	}
}

func fakeBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "jira")
	if err := os.WriteFile(p, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteKitAndEnvFile(t *testing.T) {
	o := testOptions(t)
	o.AgentKit, o.Agent, o.Template, o.Name = "reg.example.com/team/opencode:1.0", "devex-opencode", "reg.example.com/img:1", "jira-sbx"
	p, err := NewPlan(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.WriteKit(map[string]string{"arm64": fakeBinary(t)}, "v0.2.0-3-gabc"); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteEnvFile(); err != nil {
		t.Fatal(err)
	}

	var kit struct {
		Kind         string
		Version      string
		Dockerfile   string
		Capabilities []struct {
			Type   string
			Config map[string]any
		}
	}
	b, _ := os.ReadFile(filepath.Join(p.KitDir, "jira-cli.yaml"))
	if err := yaml.Unmarshal(b, &kit); err != nil {
		t.Fatalf("kit yaml: %v\n%s", err, b)
	}
	if kit.Kind != "mixin" || kit.Version != "0.2.0" || kit.Dockerfile != "jira-cli.dockerfile" {
		t.Fatalf("kit header: %+v", kit)
	}
	caps := map[string]map[string]any{}
	for _, c := range kit.Capabilities {
		caps[c.Type] = c.Config
	}
	cred := caps["com.docker.sandbox/credential@1"]
	key := cred["apiKey"].(map[string]any)
	inject := key["inject"].([]any)[0].(map[string]any)
	if cred["service"] != "jira" || key["name"] != "JIRA_CREDENTIAL_JIRA_PROD" || key["proxyManaged"] != true ||
		inject["domain"] != "jira.example.com" || inject["header"] != "Authorization" || inject["format"] != "Bearer %s" {
		t.Fatalf("credential: %v", cred)
	}
	allow := caps["com.docker.sandbox/network-policy@1"]["runtime"].(map[string]any)["allow"].([]any)
	if len(allow) != 1 || allow[0] != "jira.example.com:8443" {
		t.Fatalf("allow: %v", allow)
	}
	// A failing startup hook aborts the sandbox start ("failed to run sandbox
	// container"): the only hook is best effort, and nothing is linked at start.
	startup := caps["com.docker.sandbox/lifecycle@1"]["startup"].([]any)
	if len(startup) != 1 {
		t.Fatalf("startup hooks: %v", startup)
	}
	hook := startup[0].(map[string]any)["command"].([]any)[2].(string)
	if !strings.HasSuffix(strings.TrimSpace(hook), "|| true") || strings.Contains(hook, "ln -s") {
		t.Fatalf("hook can fail the start: %s", hook)
	}
	if _, ok := caps["com.docker.sandbox/agent-skill@1"]; ok {
		t.Fatal("the skill must not be bundled in the kit")
	}
	if st, err := os.Stat(filepath.Join(p.KitDir, "bin", "jira-arm64")); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("binary: %v %v", st, err)
	}
	df, _ := os.ReadFile(filepath.Join(p.KitDir, "jira-cli.dockerfile"))
	if !strings.Contains(string(df), "COPY --chmod=0755 bin/jira-${TARGETARCH} /usr/bin/jira") {
		t.Fatalf("dockerfile:\n%s", df)
	}

	var env struct {
		Name, Agent string
		Kits        []string
		Workspace   string
		Additional  []map[string]any  `yaml:"additionalWorkspaces"`
		Env         map[string]string `yaml:"env"`
		Options     map[string]string `yaml:"sandboxOptions"`
		Bindings    map[string]map[string]map[string][]string
	}
	eb, _ := os.ReadFile(p.EnvFile)
	if !IsGenerated(eb) {
		t.Fatal("marker missing")
	}
	if err := yaml.Unmarshal(eb, &env); err != nil {
		t.Fatalf("env yaml: %v\n%s", err, eb)
	}
	if env.Name != "jira-sbx" || env.Agent != "devex-opencode" || len(env.Kits) != 2 || env.Kits[0] != o.AgentKit || env.Kits[1] != "./.sbx/jira-cli" ||
		env.Additional[0]["path"] != p.Settings || env.Additional[0]["readOnly"] != true || len(env.Additional[0]) != 2 ||
		env.Env["JIRA_CONFIG"] != p.ConfigFile || env.Env["JIRA_READ_ONLY"] != "1" || env.Options["template"] != o.Template ||
		env.Bindings["jira"]["apiKey"]["domains"][0] != "jira.example.com" {
		t.Fatalf("env file:\n%s", eb)
	}

	prev, ok := ReadPrevious(p.EnvFile)
	if !ok || prev != (Previous{Agent: "devex-opencode", AgentKit: o.AgentKit, Template: o.Template, Name: "jira-sbx", ReadOnly: true}) {
		t.Fatalf("previous: %+v %v", prev, ok)
	}
}

func TestOptionalFieldsOmitted(t *testing.T) {
	o := testOptions(t)
	o.ReadOnly = false
	p, _ := NewPlan(o)
	s, err := p.EnvFileContent()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name:", "sandboxOptions", "JIRA_READ_ONLY", "target:"} {
		if strings.Contains(s, k) {
			t.Errorf("unexpected %q in:\n%s", k, s)
		}
	}
}

func TestForeignEnvFileRefused(t *testing.T) {
	o := testOptions(t)
	p, _ := NewPlan(o)
	if err := os.WriteFile(p.EnvFile, []byte("schemaVersion: \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p.CheckEnvFile() == nil {
		t.Fatal("foreign file accepted")
	}
	p.KitOnly = true
	if p.CheckEnvFile() != nil {
		t.Fatal("--kit-only must not care")
	}
	legacy := []byte("# generated by jira-cli scripts/sbx-setup.sh; re-run it to regenerate.\n")
	if !IsGenerated(legacy) {
		t.Fatal("files from the former script must count as generated")
	}
}

func TestSettingsCreatedOnceAndKept(t *testing.T) {
	o := testOptions(t)
	ca := filepath.Join(t.TempDir(), "My CA.pem")
	if err := os.WriteFile(ca, []byte("pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.CABundle = ca
	p, _ := NewPlan(o)
	if _, err := p.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p.ConfigFile)
	cfg := string(b)
	for _, want := range []string{"base_url: https://jira.example.com:8443/jira", "credential_ref: jira-prod", "read_only: true", "ca_bundle: " + filepath.Join(p.Settings, "My_CA.pem")} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	if err := os.WriteFile(p.BashrcFile, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile, []byte("x: /etc/jira-cli/ca.pem\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes, err := p.WriteSettings()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.BashrcFile); string(b) != "# mine\n" {
		t.Fatal("bashrc overwritten")
	}
	if b, _ := os.ReadFile(p.ConfigFile); string(b) != "x: /etc/jira-cli/ca.pem\n" {
		t.Fatal("config overwritten")
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"credential_ref", "/etc/jira-cli/", "ca_bundle"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no warning about %s:\n%s", want, joined)
		}
	}
}

func TestSkillKeepsLocalEditsUnlessForced(t *testing.T) {
	skill := fstest.MapFS{"SKILL.md": {Data: []byte("v1")}, "scripts/check.sh": {Data: []byte("#!/bin/sh\n")}}
	p, _ := NewPlan(testOptions(t))
	if _, err := p.WriteSkill(skill); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(p.SkillDir, "scripts", "check.sh")); st.Mode().Perm() != 0o755 {
		t.Fatal("script not executable")
	}
	if err := os.WriteFile(filepath.Join(p.SkillDir, "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	note, err := p.WriteSkill(skill)
	if err != nil || !strings.HasPrefix(note, "warning:") {
		t.Fatalf("note %q err %v", note, err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.SkillDir, "SKILL.md")); string(b) != "edited" {
		t.Fatal("edit lost")
	}
	p.Force = true
	if _, err := p.WriteSkill(skill); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.SkillDir, "SKILL.md")); string(b) != "v1" {
		t.Fatal("--force did not replace it")
	}
}

func TestAgentFromKitAndKitVersion(t *testing.T) {
	for ref, want := range map[string]string{"sbx/opencode:1.2@sha256:ab": "opencode", "reg.example.com:5000/team/devex-opencode": "devex-opencode", "opencode": "opencode"} {
		if got := AgentFromKit(ref); got != want {
			t.Errorf("%s: %s", ref, got)
		}
	}
	for v, want := range map[string]string{"v0.1.0": "0.1.0", "v1.2.3-4-gabc-dirty": "1.2.3", "dev": "0.0.0", "42882a1": "0.0.0"} {
		if got := KitVersion(v); got != want {
			t.Errorf("%s: %s", v, got)
		}
	}
}

func TestCheckLinuxBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a linux test binary")
	}
	self, _ := os.Executable()
	if err := CheckLinuxBinary(self, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	other := map[string]string{"amd64": "arm64", "arm64": "amd64"}[runtime.GOARCH]
	if err := CheckLinuxBinary(self, other); err == nil {
		t.Fatal("wrong architecture accepted")
	}
	if err := CheckLinuxBinary(fakeBinary(t), runtime.GOARCH); err == nil {
		t.Fatal("non-ELF accepted")
	}
}

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestBinaryFromReleaseVerifiesChecksum(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a linux test binary")
	}
	self, _ := os.Executable()
	elfBytes, _ := os.ReadFile(self)
	asset := "jira-cli_1.2.3_linux_" + runtime.GOARCH + ".tar.gz"
	archive := tarGz(t, "jira-cli_1.2.3_linux_"+runtime.GOARCH+"/jira", elfBytes)
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	tamper := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.2.3/SHA256SUMS":
			_, _ = w.Write([]byte(sums))
		case "/v1.2.3/" + asset:
			if tamper {
				_, _ = w.Write(append(append([]byte{}, archive...), 'x'))
				return
			}
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := ReleaseBase
	ReleaseBase = srv.URL
	defer func() { ReleaseBase = old }()

	req := BinaryRequest{Source: SourceRelease, Arch: runtime.GOARCH, Version: "v1.2.3", Dir: t.TempDir()}
	p, src, err := GetBinary(context.Background(), req)
	if err != nil || src != SourceRelease {
		t.Fatalf("%v %s", err, src)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, elfBytes) {
		t.Fatal("extracted binary differs")
	}
	tamper = true
	if _, _, err := GetBinary(context.Background(), req); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered archive: %v", err)
	}
	req.Version = "v1.2.3-4-gabc"
	if _, _, err := GetBinary(context.Background(), req); err == nil || !strings.Contains(err.Error(), "not a released version") {
		t.Fatalf("dev version: %v", err)
	}
}

func TestDefaultNameAndRemembered(t *testing.T) {
	for _, c := range [][3]string{
		{"devex-opencode", "/Users/x/Projects/My Work", "devex-opencode-my-work-jira"},
		{"opencode", "/tmp/jira-cli", "opencode-jira-cli-jira"},
		{"Agent.X", "/", "agent-x-jira"},
	} {
		if got := DefaultName(c[0], c[1]); got != c[2] || !plainName.MatchString(got) {
			t.Errorf("DefaultName(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	o := testOptions(t)
	o.Agent, o.AgentKit, o.ReadOnly, o.Skill = "devex-opencode", "reg.example.com/k:1", false, true
	p, _ := NewPlan(o)
	if err := os.MkdirAll(p.Settings, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveRemembered(); err != nil {
		t.Fatal(err)
	}
	r, ok := LoadRemembered(p.Settings)
	if !ok || r.Agent != "devex-opencode" || r.AgentKit != "reg.example.com/k:1" || r.ReadOnly == nil || *r.ReadOnly || r.Skill == nil || !*r.Skill {
		t.Fatalf("remembered: %+v %v", r, ok)
	}
}
