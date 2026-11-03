package sbx

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultsFile, in the settings folder, keeps the last setup's choices; they
// are the defaults for the next project.
const DefaultsFile = "setup.yaml"

// Defaults are the choices a run starts from: this project's previous setup
// if it has one, else the last setup of any project (DefaultsFile).
type Defaults struct {
	Agent, AgentName, Secret string
	Kits                     []string
	KitPath                  string // where the jira-cli kit is (or goes)
	ReadOnly, Skill          *bool

	Name    string // the project's own sandbox name (only when Project)
	From    string // where they came from, "" if nowhere
	Project bool   // from this project's sbxenv.yaml
}

// saved is DefaultsFile's format. AgentKit is how earlier versions stored a
// kit (with Agent holding its name).
type saved struct {
	Agent     string   `yaml:"agent,omitempty"`
	AgentName string   `yaml:"agent_name,omitempty"`
	AgentKit  string   `yaml:"agent_kit,omitempty"`
	Kits      []string `yaml:"kits,omitempty"`
	KitPath   string   `yaml:"kit_path,omitempty"`
	Secret    string   `yaml:"secret,omitempty"` // also read by scripts/jira-docker.sh
	ReadOnly  *bool    `yaml:"read_only,omitempty"`
	Skill     *bool    `yaml:"skill,omitempty"`
}

// LoadDefaults reads the defaults for project.
func LoadDefaults(project, settings string) Defaults {
	var d Defaults
	path := filepath.Join(settings, DefaultsFile)
	var s saved
	if b, err := os.ReadFile(path); err == nil && yaml.Unmarshal(b, &s) == nil {
		if s.AgentKit != "" {
			s.Agent, s.AgentName = s.AgentKit, s.Agent
		}
		d = Defaults{Agent: s.Agent, AgentName: s.AgentName, Kits: s.Kits, KitPath: s.KitPath, Secret: s.Secret, ReadOnly: s.ReadOnly, Skill: s.Skill, From: "your last setup (" + path + ")"}
	}
	b, err := os.ReadFile(filepath.Join(project, "sbxenv.yaml"))
	if err != nil || !IsGenerated(b) {
		return d
	}
	var env struct {
		Name  string            `yaml:"name"`
		Agent string            `yaml:"agent"`
		Kits  []yaml.Node       `yaml:"kits"`
		Env   map[string]string `yaml:"env"`
	}
	if yaml.Unmarshal(b, &env) != nil {
		return d
	}
	ro := env.Env["JIRA_READ_ONLY"] == "1"
	savedAgent := d.Agent
	d.Agent, d.AgentName, d.Name, d.ReadOnly, d.Kits = env.Agent, "", env.Name, &ro, nil
	// The agent kit: the one the last setup named, or (files of earlier
	// versions) the first registry kit unless the agent is that name.
	var refs []string
	for _, n := range env.Kits {
		k := n.Value
		if n.Kind == yaml.MappingNode { // {source: ..., args: ...}: keep as written
			n.Style = yaml.FlowStyle
			out, _ := yaml.Marshal(&n)
			k = strings.TrimSpace(string(out))
		}
		if isJiraKit(project, k) {
			d.KitPath = strings.TrimPrefix(k, "./")
		} else {
			refs = append(refs, k)
		}
	}
	agentKit := ""
	for _, k := range refs {
		if k == savedAgent && IsKitRef(k) {
			agentKit = k
		}
	}
	if agentKit == "" && savedAgent != env.Agent {
		for _, k := range refs {
			if !strings.HasPrefix(k, ".") && !strings.HasPrefix(k, "{") {
				agentKit = k
				break
			}
		}
	}
	for _, k := range refs {
		if k == agentKit {
			d.Agent, d.AgentName = k, env.Agent
		} else {
			d.Kits = append(d.Kits, k)
		}
	}
	d.From, d.Project = "this project's previous setup", true
	return d
}

// SaveDefaults stores this plan's choices for the next project.
func (p *Plan) SaveDefaults() error {
	ro, skill := p.ReadOnly, p.Skill
	s := saved{Agent: p.Agent, Kits: p.Kits, KitPath: p.KitPath, Secret: p.Secret, ReadOnly: &ro, Skill: &skill}
	if s.KitPath == DefaultKitPath {
		s.KitPath = ""
	}
	if p.AgentKit != "" && p.AgentName != AgentFromKit(p.AgentKit) {
		s.AgentName = p.AgentName
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	head := "# Choices of the last 'jira setup-sbx', used as defaults for the next project.\n"
	return os.WriteFile(filepath.Join(p.Settings, DefaultsFile), append([]byte(head), b...), 0o644)
}

var notNameChars = regexp.MustCompile(`[^a-z0-9]+`)

// DefaultName is a new project's sandbox name: <agent>-<project folder>-jira.
// sbx's own default, <agent>-<project folder>, is the name `sbx run` gives a
// sandbox in that folder; reusing it collides with such a sandbox.
func DefaultName(agentName, project string) string {
	var parts []string
	for _, s := range []string{agentName, filepath.Base(project), "jira"} {
		if s = strings.Trim(notNameChars.ReplaceAllString(strings.ToLower(s), "-"), "-"); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "-")
}

type configInfo struct{ baseURL, credRef string }

// readConfig reads base_url and credential_ref of the default (or first)
// instance of a jira-cli config file.
func readConfig(path string) configInfo {
	var c struct {
		Default   string `yaml:"default_instance"`
		Instances map[string]struct {
			BaseURL string `yaml:"base_url"`
			Auth    struct {
				CredRef string `yaml:"credential_ref"`
			} `yaml:"auth"`
		} `yaml:"instances"`
	}
	b, err := os.ReadFile(path)
	if err != nil || yaml.Unmarshal(b, &c) != nil {
		return configInfo{}
	}
	in, ok := c.Instances[c.Default]
	if !ok {
		for _, i := range c.Instances {
			in = i
			break
		}
	}
	return configInfo{in.BaseURL, in.Auth.CredRef}
}

// BaseURLFromConfig returns the Jira base URL of <settings>/config.yaml, or "".
func BaseURLFromConfig(settings string) string {
	return readConfig(filepath.Join(settings, "config.yaml")).baseURL
}
