package sbx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

func kitPlan(t *testing.T, readOnly bool) *KitPlan {
	t.Helper()
	home := t.TempDir()
	k, err := NewKitPlan(KitOptions{Settings: filepath.Join(home, ".config", "jira-cli-sbx"), BaseURL: "https://Jira.Example.com:8443/jira",
		Secret: "jira", ReadOnly: readOnly, Skill: true, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// userEnv is the parsed shape of ~/.sbxenv.yaml the tests look at.
type userEnv struct {
	SchemaVersion string `yaml:"schemaVersion"`
	Agent         string `yaml:"agent"`
	Kits          []yaml.Node
	Workspaces    []struct {
		Path     string `yaml:"path"`
		ReadOnly bool   `yaml:"readOnly"`
	} `yaml:"additionalWorkspaces"`
	Bindings map[string]struct {
		APIKey struct {
			Domains []string `yaml:"domains"`
		} `yaml:"apiKey"`
	} `yaml:"bindings"`
}

func parseEnv(t *testing.T, b []byte) userEnv {
	t.Helper()
	var e userEnv
	if err := yaml.Unmarshal(b, &e); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return e
}

func TestNewKitPlan(t *testing.T) {
	k := kitPlan(t, true)
	if k.Host != "jira.example.com" || k.Port != 8443 || k.CredEnv != "JIRA_CREDENTIAL_JIRA_PROD" {
		t.Fatalf("%+v", k)
	}
	want := [][2]string{{"host", "jira.example.com"}, {"port", "8443"}, {"secret", "jira"}, {"readOnly", "1"}}
	for i, a := range k.Args() {
		if a != want[i] {
			t.Fatalf("args %v", k.Args())
		}
	}
	home := t.TempDir()
	for name, o := range map[string]KitOptions{
		"no url":         {Settings: home + "/s", Secret: "jira", Home: home},
		"http":           {Settings: home + "/s", Secret: "jira", Home: home, BaseURL: "http://jira.example.com"},
		"bad secret":     {Settings: home + "/s", Secret: "Jira", Home: home, BaseURL: "https://jira.example.com"},
		"settings=home":  {Settings: home, Secret: "jira", Home: home, BaseURL: "https://jira.example.com"},
		"relative":       {Settings: "s", Secret: "jira", Home: home, BaseURL: "https://jira.example.com"},
		"settings quote": {Settings: home + "/a b", Secret: "jira", Home: home, BaseURL: "https://jira.example.com"},
	} {
		if _, err := NewKitPlan(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUserEnvContentNewFile(t *testing.T) {
	k := kitPlan(t, true)
	b, err := k.UserEnvContent(nil, k.KitDir)
	if err != nil {
		t.Fatal(err)
	}
	e := parseEnv(t, b)
	if e.SchemaVersion != "1" || len(e.Kits) != 1 || len(e.Workspaces) != 1 {
		t.Fatalf("%s", b)
	}
	if !strings.HasPrefix(string(b), "schemaVersion: \"1\"\n") {
		t.Errorf("schemaVersion not first:\n%s", b)
	}
	var kit struct {
		Source string            `yaml:"source"`
		Args   map[string]string `yaml:"args"`
	}
	if err := e.Kits[0].Decode(&kit); err != nil || kit.Source != k.KitDir || kit.Args["host"] != "jira.example.com" || kit.Args["readOnly"] != "1" {
		t.Fatalf("kit %+v (%v)\n%s", kit, err, b)
	}
	if w := e.Workspaces[0]; w.Path != k.Settings || !w.ReadOnly {
		t.Fatalf("workspace %+v", w)
	}
	if d := e.Bindings["jira"].APIKey.Domains; len(d) != 1 || d[0] != "jira.example.com" {
		t.Fatalf("bindings %+v", e.Bindings)
	}
}

func TestUserEnvContentKeepsTheRest(t *testing.T) {
	k := kitPlan(t, false)
	existing := `# my defaults
schemaVersion: "1"
agent: devex-opencode # the company agent
workspace: ${{ env.projectDir }}
kits:
  - company.example.com/devex/opencode:3
  - source: ` + k.KitDir + `
    args: {host: old.example.com}
additionalWorkspaces:
  - path: ~/notes
env:
  FOO: bar
bindings:
  jira:
    apiKey:
      domains: [old.example.com]
  github:
    apiKey:
      domains: [api.github.com]
`
	b, err := k.UserEnvContent([]byte(existing), k.KitDir)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"# my defaults", "agent: devex-opencode # the company agent", "workspace: ${{ env.projectDir }}",
		"company.example.com/devex/opencode:3", "path: ~/notes", "FOO: bar", "api.github.com"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %q:\n%s", want, s)
		}
	}
	e := parseEnv(t, b)
	if len(e.Kits) != 2 || strings.Count(s, k.KitDir) != 1 || strings.Contains(s, "host: old.example.com") {
		t.Fatalf("kit not replaced in place:\n%s", s)
	}
	if e.Kits[0].Value != "company.example.com/devex/opencode:3" {
		t.Fatalf("order changed:\n%s", s)
	}
	if d := e.Bindings["jira"].APIKey.Domains; len(d) != 2 || d[0] != "old.example.com" || d[1] != "jira.example.com" {
		t.Fatalf("domains %v", d)
	}
	if !strings.Contains(s, `readOnly: "0"`) {
		t.Fatalf("readOnly arg:\n%s", s)
	}
	// Idempotent.
	again, err := k.UserEnvContent(b, k.KitDir)
	if err != nil || string(again) != s {
		t.Fatalf("second run changed the file (%v):\n%s\n---\n%s", err, s, again)
	}
}

func TestUserEnvContentReplacesAGeneratedKitElsewhere(t *testing.T) {
	k := kitPlan(t, true)
	old := filepath.Join(t.TempDir(), "kit")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "jira-cli.yaml"), []byte("# "+globalKitMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := k.UserEnvContent([]byte("schemaVersion: \"1\"\nkits: ["+old+", other/kit]\n"), k.KitDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), old) || !strings.Contains(string(b), "other/kit") || len(parseEnv(t, b).Kits) != 2 {
		t.Fatalf("%s", b)
	}
}

func TestUserEnvContentRefusesNonMapping(t *testing.T) {
	k := kitPlan(t, true)
	for _, in := range []string{"- a\n- b\n", "a: [\n"} {
		if _, err := k.UserEnvContent([]byte(in), k.KitDir); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestRemoveFromUserEnv(t *testing.T) {
	k := kitPlan(t, true)
	orig := "schemaVersion: \"1\"\nagent: opencode\nkits: [other/kit]\n"
	added, err := k.UserEnvContent([]byte(orig), k.KitDir)
	if err != nil {
		t.Fatal(err)
	}
	out, removed, err := k.RemoveFromUserEnv(added)
	if err != nil || !removed {
		t.Fatalf("removed %v, %v", removed, err)
	}
	e := parseEnv(t, out)
	if len(e.Kits) != 1 || e.Kits[0].Value != "other/kit" || len(e.Workspaces) != 0 || e.Agent != "opencode" {
		t.Fatalf("%s", out)
	}
	if len(e.Bindings["jira"].APIKey.Domains) != 1 {
		t.Fatalf("binding should be kept:\n%s", out)
	}
	if _, removed, _ := k.RemoveFromUserEnv([]byte(orig)); removed {
		t.Fatal("removed from a file without the kit")
	}
}

func TestWriteGlobalKitAndUserEnvKit(t *testing.T) {
	k := kitPlan(t, true)
	bin := filepath.Join(t.TempDir(), "jiracli")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	skill := fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: jira\n---\n\n# Jira\n\nbody\n")}, "scripts/x.sh": {Data: []byte("#!/bin/sh\n")}}
	if err := k.WriteKit(k.KitDir, bin, "v1.2.3-rc1", skill); err != nil {
		t.Fatal(err)
	}
	kit, _ := os.ReadFile(filepath.Join(k.KitDir, "jira-cli.yaml"))
	for _, want := range []string{globalKitMarker, `version: "1.2.3"`, "- ${{ kit.args.host }}:${{ kit.args.port }}",
		"service: ${{ kit.args.secret }}", `domain: "${{ kit.args.host }}"`, "env: JIRA_READ_ONLY", "env: JIRA_CONFIG",
		`default: "` + k.ConfigFile + `"`, "agent-skill@1", "path: " + globalSkillPath, k.BinPath(),
		"[ -e /usr/bin/jiracli ] || ln -sf '" + k.BinPath() + "' /usr/bin/jiracli"} {
		if !strings.Contains(string(kit), want) {
			t.Errorf("kit lacks %q", want)
		}
	}
	var d map[string]any
	if err := yaml.Unmarshal(kit, &d); err != nil {
		t.Fatalf("kit is not YAML: %v", err)
	}
	df, _ := os.ReadFile(filepath.Join(k.KitDir, "jira-cli.dockerfile"))
	if !strings.Contains(string(df), "COPY skill/jira/ "+globalSkillPath+"/") {
		t.Errorf("dockerfile:\n%s", df)
	}
	md, _ := os.ReadFile(filepath.Join(k.SkillDir(), "SKILL.md"))
	if !strings.Contains(string(md), "# Jira\n\n## This sandbox\n") || !strings.Contains(string(md), k.BinPath()) {
		t.Errorf("SKILL.md:\n%s", md)
	}
	if st, err := os.Stat(filepath.Join(k.SkillDir(), "scripts", "x.sh")); err != nil || st.Mode()&0o100 == 0 {
		t.Errorf("script not executable: %v", err)
	}

	if UserEnvKit(k.Home) != "" {
		t.Fatal("no ~/.sbxenv.yaml: no kit")
	}
	env, _ := k.UserEnvContent(nil, k.KitDir)
	if err := os.WriteFile(k.EnvFile, env, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := UserEnvKit(k.Home); got != k.KitDir {
		t.Fatalf("UserEnvKit = %q", got)
	}

	// Without the skill: no skill capability, no COPY.
	k.Skill = false
	if err := k.WriteKit(k.KitDir, bin, "v1.2.3", skill); err != nil {
		t.Fatal(err)
	}
	kit, _ = os.ReadFile(filepath.Join(k.KitDir, "jira-cli.yaml"))
	df, _ = os.ReadFile(filepath.Join(k.KitDir, "jira-cli.dockerfile"))
	if strings.Contains(string(kit), "agent-skill@1") || strings.Contains(string(df), "skill/") {
		t.Fatal("skill still in the kit")
	}
	if _, err := os.Stat(k.SkillDir()); err == nil {
		t.Fatal("stale skill folder kept")
	}
}

func TestKitWriteSettings(t *testing.T) {
	k := kitPlan(t, false)
	notes, err := k.WriteSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(k.ConfigFile)
	if !strings.Contains(string(cfg), "read_only: false") || !strings.Contains(string(cfg), "base_url: https://Jira.Example.com:8443/jira") {
		t.Fatalf("config:\n%s", cfg)
	}
	for _, n := range notes {
		if n.Warn {
			t.Errorf("unexpected warning: %s", n.Text)
		}
	}
	// An existing read-only config wins over the arg: say so.
	if err := os.WriteFile(k.ConfigFile, []byte(strings.Replace(string(cfg), "read_only: false", "read_only: true", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	notes, _ = k.WriteSettings()
	if len(notes) == 0 || !notes[len(notes)-1].Warn || !strings.Contains(notes[len(notes)-1].Text, "read_only: true") {
		t.Fatalf("notes %+v", notes)
	}
}

func TestValidationEnv(t *testing.T) {
	k := kitPlan(t, true)
	b, err := k.ValidationEnv([]byte("schemaVersion: \"1\"\nworkspace: ${{ env.projectDir }}\n"), "/tmp/scratch/kit", "shell")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "agent: shell") || !strings.Contains(s, "workspace: .") || strings.Contains(s, "projectDir") || !strings.Contains(s, "source: /tmp/scratch/kit") {
		t.Fatalf("%s", s)
	}
	b, _ = k.ValidationEnv([]byte("schemaVersion: \"1\"\nagent: opencode\n"), "/tmp/scratch/kit", "shell")
	if !strings.Contains(string(b), "agent: opencode") || strings.Contains(string(b), "shell") {
		t.Fatalf("%s", b)
	}
}

func TestStrayUserEnv(t *testing.T) {
	home := t.TempDir()
	if StrayUserEnv(home) != "" {
		t.Fatal("no .sbxenv.yml: nothing to report")
	}
	p := filepath.Join(home, ".sbxenv.yml")
	if err := os.WriteFile(p, []byte("agent: opencode\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if StrayUserEnv(home) != p || !strings.Contains(StrayUserEnvNote(p), "sbx reads only ~/.sbxenv.yaml") {
		t.Fatalf("%q", StrayUserEnv(home))
	}
}

func TestWriteGlobalKitV2(t *testing.T) {
	k := kitPlan(t, true)
	k.Schema = 2
	bin := filepath.Join(t.TempDir(), "jiracli")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	skill := fstest.MapFS{"SKILL.md": {Data: []byte("# Jira\n")}}
	if err := k.WriteKit(k.KitDir, bin, "v1.2.3", skill); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(k.KitDir, "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	spec := string(b)
	for _, want := range []string{globalKitMarker, `schemaVersion: "2"`, "kind: mixin", "name: jira-cli",
		`- "${{ kit.args.host }}:${{ kit.args.port }}"`, `service: "${{ kit.args.secret }}"`, `domain: "${{ kit.args.host }}"`,
		`JIRA_READ_ONLY: "${{ kit.args.readOnly }}"`, `JIRA_CONFIG: "${{ kit.args.config }}"`, "proxyManaged: true",
		"ln -sf '" + k.BinPath() + "' /usr/bin/jiracli", k.SkillPath() + "/SKILL.md"} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec.yaml lacks %q", want)
		}
	}
	var d struct {
		SchemaVersion string                    `yaml:"schemaVersion"`
		Args          map[string]map[string]any `yaml:"args"`
	}
	if err := yaml.Unmarshal(b, &d); err != nil || d.SchemaVersion != "2" || len(d.Args) != 5 {
		t.Fatalf("spec.yaml: %v %+v", err, d)
	}
	for _, f := range []string{"jira-cli.yaml", "jira-cli.dockerfile"} {
		if _, err := os.Stat(filepath.Join(k.KitDir, f)); err == nil {
			t.Errorf("%s in a v2 kit", f)
		}
	}
	if !IsGlobalKit(k.KitDir) {
		t.Fatal("IsGlobalKit: v2 kit not recognized")
	}
	k.Schema = 7
	if _, err := NewKitPlan(k.KitOptions); err == nil {
		t.Fatal("schema 7 accepted")
	}
}

func TestKitSchema(t *testing.T) {
	d := t.TempDir()
	if s, _, _, _ := KitSchema(d); s != 0 {
		t.Fatalf("empty folder: %d", s)
	}
	if err := os.WriteFile(filepath.Join(d, "spec.yaml"), []byte("schemaVersion: \"2\"\nname: x\nversion: \"1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, n, v, j := KitSchema(d); s != 2 || n != "x" || v != "1.0" || j {
		t.Fatalf("v2: %d %q %q %v", s, n, v, j)
	}
	d3 := t.TempDir()
	if err := os.WriteFile(filepath.Join(d3, "tool.yaml"), []byte("# syntax=docker/sandbox-kit:3\nschemaVersion: \"3\"\nversion: \"2.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, n, v, _ := KitSchema(d3); s != 3 || n != "tool" || v != "2.1.0" {
		t.Fatalf("v3: %d %q %q", s, n, v)
	}
}

func TestAbsolutizeAndSwap(t *testing.T) {
	in := "schemaVersion: \"1\"\nworkspace: .\nkits:\n  - ./kits/a\n  - source: ../b\n    args: {x: \"1\"}\n  - docker.io/sbx/claude:1\nadditionalWorkspaces:\n  - path: ./shared\nmounts:\n  /home/agent/h: ./h\n"
	b, err := Absolutize([]byte(in), "/p/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"workspace: /p/proj", "- /p/proj/kits/a", "source: /p/b", "- docker.io/sbx/claude:1", "path: /p/proj/shared", "/home/agent/h: /p/proj/h"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("lacks %q:\n%s", want, b)
		}
	}
	k := kitPlan(t, true)
	bin := filepath.Join(t.TempDir(), "jiracli")
	_ = os.WriteFile(bin, []byte("x"), 0o755)
	if err := k.WriteKit(k.KitDir, bin, "v1.0.0", fstest.MapFS{"SKILL.md": {Data: []byte("# J\n")}}); err != nil {
		t.Fatal(err)
	}
	env, _ := k.UserEnvContent([]byte("schemaVersion: \"1\"\nkits: [other/kit]\n"), k.KitDir)
	sw, err := SwapKitSource(env, k.Home, "/scratch/kit")
	if err != nil || !strings.Contains(string(sw), "source: /scratch/kit") || strings.Contains(string(sw), k.KitDir) || !strings.Contains(string(sw), `host: "jira.example.com"`) {
		t.Fatalf("%v\n%s", err, sw)
	}
}
