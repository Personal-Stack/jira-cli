package sbx

// Inspect reads what is set up on this machine for jira-cli in sbx: the
// user's ~/.sbxenv.yaml, a project's sbxenv.yaml, the kits they list (and
// their format, v2 or v3), the jira-cli kit and its settings. It only reads
// files; what needs sbx itself (installed, secrets, a running sandbox) is
// checked by the caller.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// BuiltinAgents are sbx's built-in agent names; they select v3 kits.
var BuiltinAgents = []string{"claude", "codex", "copilot", "cursor", "devin", "docker-agent", "droid", "kiro", "opencode", "shell"}

// KitInfo is one entry of an environment file's kits.
type KitInfo struct {
	Source string            `json:"source"`
	Dir    string            `json:"dir,omitempty"` // resolved local folder; "" for a registry or git reference
	Schema int               `json:"schema"`        // 1, 2 or 3; 0 when unknown (a remote reference)
	Name   string            `json:"name,omitempty"`
	Args   map[string]string `json:"args,omitempty"`

	Jira   bool `json:"jira,omitempty"`   // the kit of `jiracli sbx kit` / `jiracli sbx setup`
	Legacy bool `json:"legacy,omitempty"` // a project kit of `jiracli setup-sbx`
}

// Family is "v3", "v1/v2" or "" (unknown): sbx composes kits of one family only.
func (k KitInfo) Family() string { return schemaFamily(k.Schema) }

func schemaFamily(s int) string {
	switch s {
	case 3:
		return "v3"
	case 1, 2:
		return "v1/v2"
	}
	return ""
}

// EnvInfo is one environment file.
type EnvInfo struct {
	Path      string    `json:"path"`
	Exists    bool      `json:"exists"`
	Error     string    `json:"error,omitempty"`
	Generated bool      `json:"generated,omitempty"` // written by setup-sbx
	Agent     string    `json:"agent,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	Kits      []KitInfo `json:"kits,omitempty"`
	Mounts    []string  `json:"mounts,omitempty"` // additionalWorkspaces paths, resolved
	// Bindings maps a credential service to its approved apiKey domains.
	Bindings map[string][]string `json:"bindings,omitempty"`
}

// JiraKit returns the jira-cli kit entry, if any.
func (e *EnvInfo) JiraKit() *KitInfo {
	for i := range e.Kits {
		if e.Kits[i].Jira {
			return &e.Kits[i]
		}
	}
	return nil
}

// LegacyKit returns a setup-sbx project kit entry, if any.
func (e *EnvInfo) LegacyKit() *KitInfo {
	for i := range e.Kits {
		if e.Kits[i].Legacy {
			return &e.Kits[i]
		}
	}
	return nil
}

// Issue is something to fix or look at; Fix says how.
type Issue struct {
	Severity string `json:"severity"` // "error" (will not work), "warn", "info"
	Text     string `json:"text"`
	Fix      string `json:"fix,omitempty"`
}

// Inspection is what Inspect found.
type Inspection struct {
	Home     string  `json:"home"`
	Project  string  `json:"project"`
	Settings string  `json:"settings"`
	User     EnvInfo `json:"user_env"`
	Proj     EnvInfo `json:"project_env"`
	StrayYml string  `json:"stray_yml,omitempty"`

	Kit struct {
		Dir     string `json:"dir"`
		Exists  bool   `json:"exists"`
		Schema  int    `json:"schema,omitempty"`
		Version string `json:"version,omitempty"`
		Binary  bool   `json:"binary"`
		// ImageLayer: the kit has a Dockerfile, which sbx builds with Docker.
		ImageLayer bool `json:"image_layer,omitempty"`
	} `json:"kit"`
	Config struct {
		Path     string `json:"path"`
		BaseURL  string `json:"base_url,omitempty"`
		ReadOnly bool   `json:"read_only"`
	} `json:"config"`

	// Agent is the agent of the merged environment (project wins).
	Agent string `json:"agent,omitempty"`
	// AgentSchema is the format the agent's kits use: 3, 2 (v1/v2) or 0
	// (unknown), and AgentSchemaWhy how that was found.
	AgentSchema    int    `json:"agent_schema"`
	AgentSchemaWhy string `json:"agent_schema_why"`

	Issues []Issue `json:"issues,omitempty"`
}

// JiraTargets are the environment files that list the jira-cli kit.
func (in *Inspection) JiraTargets() []string {
	var t []string
	if in.User.JiraKit() != nil {
		t = append(t, in.User.Path)
	}
	if in.Proj.JiraKit() != nil {
		t = append(t, in.Proj.Path)
	}
	return t
}

// SetUp reports whether jira-cli is set up for this project in any way.
func (in *Inspection) SetUp() bool {
	return len(in.JiraTargets()) > 0 || in.Proj.LegacyKit() != nil
}

var descriptorVersion = regexp.MustCompile(`(?m)^version:\s*"?([^"\s]+)"?`)

// KitSchema reads a local kit folder: its format (1, 2 or 3; 0 if none
// found), name, version, and whether jira-cli generated it.
func KitSchema(dir string) (schema int, name, version string, jira bool) {
	if b, err := os.ReadFile(filepath.Join(dir, "spec.yaml")); err == nil {
		var s struct {
			SchemaVersion string `yaml:"schemaVersion"`
			Name          string `yaml:"name"`
			Version       string `yaml:"version"`
		}
		_ = yaml.Unmarshal(b, &s)
		switch s.SchemaVersion {
		case "1", "":
			schema = 1
		case "2":
			schema = 2
		}
		return schema, s.Name, s.Version, bytes.Contains(b, []byte(globalKitMarker))
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || !(bytes.Contains(b, []byte("docker/sandbox-kit:3")) || bytes.Contains(b, []byte(`schemaVersion: "3"`))) {
			continue
		}
		if m := descriptorVersion.FindSubmatch(b); m != nil {
			version = string(m[1])
		}
		return 3, strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".yaml"), ".yml"), version,
			bytes.Contains(b, []byte(globalKitMarker))
	}
	return 0, "", "", false
}

// readEnv reads one environment file; settings marks the jira-cli mount.
func readEnv(path string) EnvInfo {
	e := EnvInfo{Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return e
	}
	e.Exists, e.Generated = true, IsGenerated(b)
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		e.Error = err.Error()
		return e
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		if len(bytes.TrimSpace(b)) > 0 {
			e.Error = "not a YAML mapping"
		}
		return e
	}
	root, dir := doc.Content[0], filepath.Dir(path)
	if a := mapGet(root, "agent"); a != nil {
		e.Agent = a.Value
	}
	if w := mapGet(root, "workspace"); w != nil {
		e.Workspace = w.Value
		if p := mapGet(w, "path"); p != nil {
			e.Workspace = p.Value
		}
	}
	if kits := mapGet(root, "kits"); kits != nil && kits.Kind == yaml.SequenceNode {
		for _, n := range kits.Content {
			src := kitSource(n)
			if src == "" {
				continue
			}
			ki := KitInfo{Source: src}
			if r := resolveFrom(dir, src); filepath.IsAbs(r) {
				ki.Dir = r
				ki.Schema, ki.Name, _, ki.Jira = KitSchema(r)
				if b, err := os.ReadFile(filepath.Join(r, "jira-cli.yaml")); err == nil && !ki.Jira {
					ki.Legacy = bytes.Contains(b, []byte(kitMarker))
				}
			}
			if args := mapGet(n, "args"); args != nil && args.Kind == yaml.MappingNode {
				ki.Args = map[string]string{}
				for i := 0; i+1 < len(args.Content); i += 2 {
					ki.Args[args.Content[i].Value] = args.Content[i+1].Value
				}
			}
			e.Kits = append(e.Kits, ki)
		}
	}
	if ws := mapGet(root, "additionalWorkspaces"); ws != nil && ws.Kind == yaml.SequenceNode {
		for _, n := range ws.Content {
			if p := mapGet(n, "path"); p != nil {
				e.Mounts = append(e.Mounts, resolveFrom(dir, p.Value))
			}
		}
	}
	if bs := mapGet(root, "bindings"); bs != nil && bs.Kind == yaml.MappingNode {
		e.Bindings = map[string][]string{}
		for i := 0; i+1 < len(bs.Content); i += 2 {
			var domains []string
			if d := mapGet(mapGet(bs.Content[i+1], "apiKey"), "domains"); d != nil {
				for _, x := range d.Content {
					domains = append(domains, x.Value)
				}
			}
			e.Bindings[bs.Content[i].Value] = domains
		}
	}
	return e
}

// Inspect reads the setup for project (an absolute folder) with the
// settings folder settings; version is this jiracli's version.
func Inspect(home, project, settings, version string) *Inspection {
	in := &Inspection{Home: home, Project: project, Settings: settings}
	in.User = readEnv(filepath.Join(home, UserEnvName))
	in.Proj = readEnv(filepath.Join(project, "sbxenv.yaml"))
	in.StrayYml = StrayUserEnv(home)
	in.Kit.Dir = filepath.Join(settings, KitDirName)
	if st, err := os.Stat(in.Kit.Dir); err == nil && st.IsDir() {
		in.Kit.Exists = true
		in.Kit.Schema, _, in.Kit.Version, _ = KitSchema(in.Kit.Dir)
		_, err := os.Stat(filepath.Join(in.Kit.Dir, "bin", "jiracli"))
		in.Kit.Binary = err == nil
		_, err = os.Stat(filepath.Join(in.Kit.Dir, "jira-cli.dockerfile"))
		in.Kit.ImageLayer = err == nil
	}
	in.Config.Path = filepath.Join(settings, "config.yaml")
	in.Config.BaseURL = readConfig(in.Config.Path).baseURL
	in.Config.ReadOnly = configReadOnly(in.Config.Path)
	in.Agent = in.Proj.Agent
	if in.Agent == "" {
		in.Agent = in.User.Agent
	}
	in.AgentSchema, in.AgentSchemaWhy = in.agentSchema()
	in.Issues = in.issues(version)
	return in
}

// agentSchema infers the format of the agent's kits: from the local
// non-jira kits the files list, else from a built-in agent name.
func (in *Inspection) agentSchema() (int, string) {
	remote := false
	for _, e := range []*EnvInfo{&in.User, &in.Proj} {
		for _, k := range e.Kits {
			if k.Jira || k.Legacy {
				continue
			}
			if k.Schema != 0 {
				return map[bool]int{true: 3, false: 2}[k.Schema == 3],
					fmt.Sprintf("kit %s in %s is %s", k.Source, e.Path, k.Family())
			}
			if k.Dir == "" {
				remote = true
			}
		}
	}
	if remote {
		return 0, "the agent's kits are remote references; their format is known only to sbx ('sbx env plan' decides)"
	}
	if slices.Contains(BuiltinAgents, in.Agent) {
		return 3, fmt.Sprintf("%q is a built-in agent (v3)", in.Agent)
	}
	if in.Agent == "" {
		return 0, "no agent set"
	}
	return 0, fmt.Sprintf("agent %q: no local kit to read its format from", in.Agent)
}

func (in *Inspection) issues(version string) []Issue {
	var is []Issue
	add := func(sev, text, fix string) { is = append(is, Issue{sev, text, fix}) }
	if in.StrayYml != "" {
		add("warn", in.StrayYml+" exists: sbx reads only ~/"+UserEnvName+", so its settings are ignored", "move them into ~/"+UserEnvName+" and remove "+in.StrayYml)
	}
	for _, e := range []*EnvInfo{&in.User, &in.Proj} {
		if e.Error != "" {
			add("error", e.Path+": "+e.Error, "fix the file")
		}
		if e.Exists && len(e.Mounts) > 0 && e.Workspace == "" {
			add("error", e.Path+" has additionalWorkspaces but no workspace: sbx refuses it (\"additionalWorkspaces requires a 'workspace:'\")",
				"run 'jiracli sbx setup' (update), which adds the workspace")
		}
	}
	jiraEntries := map[string]*KitInfo{}
	for _, e := range []*EnvInfo{&in.User, &in.Proj} {
		if k := e.JiraKit(); k != nil {
			jiraEntries[e.Path] = k
		}
	}
	if lk := in.Proj.LegacyKit(); lk != nil && len(jiraEntries) > 0 {
		add("error", fmt.Sprintf("%s lists setup-sbx's own jira-cli kit (%s) and the shared kit is listed too: sbx refuses two kits providing jira-cli", in.Proj.Path, lk.Source),
			"migrate the project to the shared kit ('jiracli sbx setup', action migrate)")
	}
	if u, p := in.User.JiraKit(), in.Proj.JiraKit(); u != nil && p != nil && u.Dir != p.Dir {
		add("error", "the two files list different jira-cli kit folders ("+u.Dir+", "+p.Dir+"): sbx refuses two", "keep one source")
	}
	for path, k := range jiraEntries {
		if k.Dir != in.Kit.Dir {
			add("info", fmt.Sprintf("%s lists the kit at %s, not at %s (the settings folder)", path, k.Dir, in.Kit.Dir), "")
		}
		if fam := schemaFamily(in.AgentSchema); fam != "" && k.Family() != "" && fam != k.Family() {
			add("error", fmt.Sprintf("the jira-cli kit is %s but the agent's kits are %s (%s): sbx does not mix them", k.Family(), fam, in.AgentSchemaWhy),
				fmt.Sprintf("update with the %s format ('jiracli sbx setup', action update)", map[bool]string{true: "v3", false: "v2"}[fam == "v3"]))
		}
		host := k.Args["host"]
		if host == "" {
			add("error", path+": the jira-cli kit has no host arg", "run 'jiracli sbx setup' (update)")
		}
		secret := or2(k.Args["secret"], "jira")
		bound := false
		for _, e := range []*EnvInfo{&in.User, &in.Proj} {
			for _, d := range e.Bindings[secret] {
				bound = bound || strings.EqualFold(d, host)
			}
		}
		if host != "" && !bound {
			add("warn", fmt.Sprintf("no binding approves the sbx secret %q for %s: sbx asks on first start, and withholds the token without a terminal", secret, host), "run 'jiracli sbx setup' (update)")
		}
		mounted := slices.Contains(in.User.Mounts, in.Settings) || slices.Contains(in.Proj.Mounts, in.Settings)
		if !mounted {
			add("error", in.Settings+" is not mounted (additionalWorkspaces): the sandbox has no binary and no config.yaml", "run 'jiracli sbx setup' (update)")
		}
		if k.Args["readOnly"] == "0" && in.Config.ReadOnly {
			add("warn", in.Config.Path+" sets read_only: true, so jiracli stays read-only although the kit allows writes", "edit "+in.Config.Path)
		}
		if host != "" && in.Config.BaseURL != "" {
			if h, _, err := ParseBaseURL(NormalizeBaseURL(in.Config.BaseURL)); err == nil && !strings.EqualFold(h, host) {
				add("warn", fmt.Sprintf("the kit is for %s but %s points at %s", host, in.Config.Path, in.Config.BaseURL), "align them ('jiracli sbx setup', init)")
			}
		}
	}
	if len(jiraEntries) > 0 {
		switch {
		case !in.Kit.Exists:
			add("error", "the kit folder "+in.Kit.Dir+" does not exist", "run 'jiracli sbx setup' (update)")
		case !in.Kit.Binary:
			add("error", "the kit has no binary ("+in.Kit.Dir+"/bin/jiracli)", "run 'jiracli sbx setup' (update)")
		case in.Kit.ImageLayer:
			add("warn", "the kit has an image layer (jira-cli.dockerfile): sbx builds it with Docker, and fails when no Docker daemon runs", "update it ('jiracli sbx setup', action update): the kit then needs no Docker; --image-layer keeps the layer")
		case in.Kit.Version != "" && KitVersion(version) != "0.0.0" && in.Kit.Version != KitVersion(version):
			add("info", fmt.Sprintf("the kit is from jiracli %s; this is %s", in.Kit.Version, KitVersion(version)), "update it ('jiracli sbx setup', action update)")
		}
	} else if in.Kit.Exists && in.Proj.LegacyKit() == nil {
		add("info", "the kit "+in.Kit.Dir+" exists but no environment file lists it", "run 'jiracli sbx setup' (init)")
	}
	if !in.User.Exists && !in.Proj.Exists {
		add("info", "no environment file: neither ~/"+UserEnvName+" nor "+in.Proj.Path, "")
	} else if in.Agent == "" {
		add("warn", "no agent is set in "+in.User.Path+" or "+in.Proj.Path+": 'sbx env run' needs one", "set agent: in one of them")
	}
	return is
}

func or2(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
