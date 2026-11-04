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
	return Options{Project: project, Settings: filepath.Join(root, "settings"), BaseURL: "https://jira.example.com:8443/jira",
		Agent: "opencode", Secret: "jira", ReadOnly: true, Skill: true}
}

func mustPlan(t *testing.T, o Options) *Plan {
	t.Helper()
	p, err := NewPlan(o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewPlan(t *testing.T) {
	p := mustPlan(t, testOptions(t))
	if p.Host != "jira.example.com" || p.Port != 8443 || p.Agent != "opencode" || p.CredEnv != "JIRA_CREDENTIAL_JIRA_PROD" ||
		p.KitDir != filepath.Join(p.Project, "tmp", ".sbx", "jira-cli") {
		t.Fatalf("%+v", p)
	}
	// An existing config decides base_url and credential_ref.
	o := testOptions(t)
	o.BaseURL = ""
	write(t, filepath.Join(o.Settings, "config.yaml"), "default_instance: b\ninstances:\n  a: {base_url: https://a.example.com}\n  b:\n    base_url: https://b.example.com/jira\n    auth: {credential_ref: team-pat}\n")
	if p := mustPlan(t, o); p.BaseURL != "https://b.example.com/jira" || p.CredRef != "team-pat" || p.CredEnv != "JIRA_CREDENTIAL_TEAM_PAT" {
		t.Fatalf("from config: %+v", p)
	}
}

func TestNewPlanRejects(t *testing.T) {
	for name, mutate := range map[string]func(*Options){
		"http":                func(o *Options) { o.BaseURL = "http://jira.example.com" },
		"credentials in URL":  func(o *Options) { o.BaseURL = "https://u:p@jira.example.com" },
		"quote in URL":        func(o *Options) { o.BaseURL = "https://jira.example.com/'x" },
		"settings in project": func(o *Options) { o.Settings = filepath.Join(o.Project, "settings") },
		"settings with space": func(o *Options) { o.Settings += " x" },
		"relative project":    func(o *Options) { o.Project = "proj" },
		"missing project":     func(o *Options) { o.Project += "-missing" },
		"agent with space":    func(o *Options) { o.Agent = "my agent" },
		"image as agent":      func(o *Options) { o.Agent = "company.jfrog.io/team/opencode-sbx:1.2" },
		"kit dir outside":     func(o *Options) { o.KitPath = "../x" },
		"bad name":            func(o *Options) { o.Name = "a/b" },
		"bad secret":          func(o *Options) { o.Secret = "Jira" },
	} {
		t.Run(name, func(t *testing.T) {
			o := testOptions(t)
			mutate(&o)
			if _, err := NewPlan(o); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestWriteKitAndEnvFile(t *testing.T) {
	o := testOptions(t)
	o.Agent, o.Name = "devex-opencode", "jira-sbx"
	p := mustPlan(t, o)
	bin := filepath.Join(t.TempDir(), "jira")
	write(t, bin, "binary")
	if err := p.WriteKit(bin, "v0.2.0-3-gabc"); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteEnvFile(); err != nil {
		t.Fatal(err)
	}

	var kit struct {
		Kind, Version, Dockerfile string
		Capabilities              []struct {
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
	if allow := caps["com.docker.sandbox/network-policy@1"]["runtime"].(map[string]any)["allow"].([]any); len(allow) != 1 || allow[0] != "jira.example.com:8443" {
		t.Fatalf("allow: %v", allow)
	}
	// A failing startup hook aborts the sandbox start ("failed to run
	// sandbox container"): the only hook is best effort.
	startup := caps["com.docker.sandbox/lifecycle@1"]["startup"].([]any)
	if hook := startup[0].(map[string]any)["command"].([]any)[2].(string); len(startup) != 1 || !strings.HasSuffix(strings.TrimSpace(hook), "|| true") {
		t.Fatalf("startup hooks can fail the start: %v", startup)
	}
	if st, err := os.Stat(p.BinPath()); err != nil || st.Mode().Perm() != 0o755 || p.BinPath() != filepath.Join(p.Project, "tmp", ".sbx", "jira-cli", "bin", "jira") {
		t.Fatalf("binary: %v %v", st, err)
	}
	if df, _ := os.ReadFile(filepath.Join(p.KitDir, "jira-cli.dockerfile")); !strings.Contains(string(df), "COPY --chmod=0755 bin/jira /usr/bin/jira") {
		t.Fatalf("dockerfile:\n%s", df)
	}

	var env struct {
		Name, Agent string
		Kits        []string
		Additional  []map[string]any  `yaml:"additionalWorkspaces"`
		Env         map[string]string `yaml:"env"`
		Options     map[string]string `yaml:"sandboxOptions"`
		Bindings    map[string]map[string]map[string][]string
	}
	eb, _ := os.ReadFile(p.EnvFile)
	if !IsGenerated(eb) || yaml.Unmarshal(eb, &env) != nil {
		t.Fatalf("env file:\n%s", eb)
	}
	if env.Name != "jira-sbx" || env.Agent != "devex-opencode" || strings.Join(env.Kits, ",") != "./tmp/.sbx/jira-cli" ||
		env.Additional[0]["path"] != p.Settings || env.Additional[0]["readOnly"] != true || len(env.Additional[0]) != 2 ||
		env.Env["JIRA_CONFIG"] != p.ConfigFile || env.Env["JIRA_READ_ONLY"] != "1" || env.Options != nil ||
		env.Bindings["jira"]["apiKey"]["domains"][0] != "jira.example.com" {
		t.Fatalf("env file:\n%s", eb)
	}

	// The project's setup is the next run's defaults, the sbx name included.
	d := LoadDefaults(p.Project, p.Settings)
	if !d.Project || d.Agent != "devex-opencode" || d.Name != "jira-sbx" || d.KitPath != "tmp/.sbx/jira-cli" || !*d.ReadOnly {
		t.Fatalf("defaults: %+v", d)
	}
}

func TestOptionalFieldsOmittedAndForeignEnvFile(t *testing.T) {
	o := testOptions(t)
	o.ReadOnly = false
	p := mustPlan(t, o)
	s, _ := p.EnvFileContent()
	for _, k := range []string{"name:", "sandboxOptions", "JIRA_READ_ONLY", "target:"} {
		if strings.Contains(s, k) {
			t.Errorf("unexpected %q in:\n%s", k, s)
		}
	}
	write(t, p.EnvFile, "schemaVersion: \"1\"\n")
	if p.CheckEnvFile() == nil {
		t.Fatal("foreign file accepted")
	}
	p.Force = true
	if p.CheckEnvFile() != nil || !IsGenerated([]byte("# generated by jira-cli scripts/sbx-setup.sh\n")) {
		t.Fatal("--force / the former script's files")
	}
}

func TestSettingsCreatedOnceAndKept(t *testing.T) {
	p := mustPlan(t, testOptions(t))
	if _, err := p.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(p.ConfigFile)
	for _, want := range []string{"base_url: https://jira.example.com:8443/jira", "credential_ref: jira-prod", "read_only: true"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	write(t, p.BashrcFile, "# mine\n")
	write(t, p.ConfigFile, "x: /etc/jira-cli/ca.pem\n")
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
	if last := notes[len(notes)-1]; !last.Warn || !strings.Contains(last.Text, "/etc/jira-cli/") {
		t.Fatalf("no warning: %+v", notes)
	}
}

func TestSkillKeepsLocalEditsUnlessForced(t *testing.T) {
	skill := fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: jira\n---\n\n# Jira\n\nBody.\n")}, "scripts/check.sh": {Data: []byte("#!/bin/sh\n")}}
	p := mustPlan(t, testOptions(t))
	if _, err := p.WriteSkill(skill); err != nil {
		t.Fatal(err)
	}
	// The sandbox section follows the title: the agent finds the binary at once.
	md, _ := os.ReadFile(filepath.Join(p.SkillDir, "SKILL.md"))
	if !strings.HasPrefix(string(md), "---\nname: jira\n---\n\n# Jira\n\n## This sandbox\n") ||
		!strings.Contains(string(md), "**`"+p.BinPath()+"`**") || !strings.Contains(string(md), p.ConfigFile) ||
		!strings.Contains(string(md), "JIRA_CREDENTIAL_JIRA_PROD") || !strings.Contains(string(md), "Read-only") || !strings.HasSuffix(string(md), "Body.\n") {
		t.Fatalf("SKILL.md:\n%s", md)
	}
	if st, _ := os.Stat(filepath.Join(p.SkillDir, "scripts", "check.sh")); st.Mode().Perm() != 0o755 {
		t.Fatal("script not executable")
	}
	if n, _ := p.WriteSkill(skill); n.Warn {
		t.Fatal("an unchanged skill counts as edited")
	}
	write(t, filepath.Join(p.SkillDir, "SKILL.md"), "edited")
	if n, err := p.WriteSkill(skill); err != nil || !n.Warn {
		t.Fatalf("note %+v err %v", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.SkillDir, "SKILL.md")); string(b) != "edited" {
		t.Fatal("edit lost")
	}
	p.Force = true
	if _, err := p.WriteSkill(skill); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.SkillDir, "SKILL.md")); !bytes.Equal(b, md) {
		t.Fatal("--force did not replace it")
	}
}

func TestDefaults(t *testing.T) {
	o := testOptions(t)
	o.Agent, o.Secret, o.ReadOnly, o.KitPath = "devex-opencode", "team-jira", false, "build/jira-kit"
	p := mustPlan(t, o)
	if err := os.MkdirAll(p.Settings, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveDefaults(); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir() // a project without a setup gets the saved defaults
	d := LoadDefaults(other, p.Settings)
	if d.Project || d.Agent != "devex-opencode" || d.Secret != "team-jira" || d.KitPath != "build/jira-kit" || *d.ReadOnly || !*d.Skill {
		t.Fatalf("saved: %+v", d)
	}
	// Earlier versions saved an agent kit (an image given as the agent ended
	// up there): only the name is kept, so no kit comes back.
	for _, legacy := range []string{
		"agent: devex-opencode\nagent_kit: company.jfrog.io/team/opencode-sbx:1.2\n",
		"agent: company.jfrog.io/team/opencode-sbx:1.2\nagent_name: devex-opencode\nkits: [reg.example.com/x:1]\n",
	} {
		write(t, filepath.Join(p.Settings, DefaultsFile), legacy)
		if d := LoadDefaults(other, p.Settings); d.Agent != "devex-opencode" {
			t.Fatalf("legacy %q: %+v", legacy, d)
		}
	}
	for _, c := range [][3]string{
		{"devex-opencode", "/Users/x/Projects/My Work", "devex-opencode-my-work-jira"},
		{"Agent.X", "/", "agent-x-jira"},
	} {
		if got := DefaultName(c[0], c[1]); got != c[2] || ValidName(got) != nil {
			t.Errorf("DefaultName(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	for v, want := range map[string]string{"v0.1.0": "0.1.0", "v1.2.3-4-gabc-dirty": "1.2.3", "dev": "0.0.0"} {
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
	if err := CheckLinuxBinary(self, map[string]string{"amd64": "arm64", "arm64": "amd64"}[runtime.GOARCH]); err == nil {
		t.Fatal("wrong architecture accepted")
	}
	fake := filepath.Join(t.TempDir(), "jira")
	write(t, fake, "not elf")
	if err := CheckLinuxBinary(fake, runtime.GOARCH); err == nil {
		t.Fatal("non-ELF accepted")
	}
}

func TestBinaryFromReleaseVerifiesChecksum(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a linux test binary")
	}
	self, _ := os.Executable()
	elfBytes, _ := os.ReadFile(self)
	asset := "jira-cli_1.2.3_linux_" + runtime.GOARCH + ".tar.gz"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "jira-cli/jira", Mode: 0o755, Size: int64(len(elfBytes)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(elfBytes)
	_, _ = tw.Close(), gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	tamper := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.2.3/SHA256SUMS":
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"))
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
	if p, src, err := GetBinary(context.Background(), req); err != nil || src != SourceRelease {
		t.Fatalf("%v %s", err, src)
	} else if got, _ := os.ReadFile(p); !bytes.Equal(got, elfBytes) {
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

func TestReservedNamesAndBaseAgent(t *testing.T) {
	for _, n := range []string{"opencode", "OpenCode", "env", "docker-agent"} {
		if ValidSandboxName(n) == nil {
			t.Errorf("%s accepted", n)
		}
	}
	if ValidSandboxName("opencode-my-work-jira") != nil || ValidAgent("opencode") != nil {
		t.Fatal("a normal sandbox name, or the built-in agent, refused")
	}
	home := t.TempDir()
	if BaseAgent(home) != "" {
		t.Fatal("no ~/.sbxenv.yaml: no agent")
	}
	write(t, filepath.Join(home, ".sbxenv.yaml"), "agent: devex-opencode\n")
	if got := BaseAgent(home); got != "devex-opencode" {
		t.Fatalf("base agent %q", got)
	}
}
