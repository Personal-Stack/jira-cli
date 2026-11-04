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
	Agent, Secret   string
	KitPath         string // where the jira-cli kit is (or goes)
	ReadOnly, Skill *bool

	Name    string // the project's own sandbox name (only when Project)
	From    string // where they came from, "" if nowhere
	Project bool   // from this project's sbxenv.yaml
}

// saved is DefaultsFile's format. Earlier versions also stored an agent kit
// (agent_kit, or a reference in agent with the name in agent_name); only
// the name is used now.
type saved struct {
	Agent     string `yaml:"agent,omitempty"`
	AgentName string `yaml:"agent_name,omitempty"`
	AgentKit  string `yaml:"agent_kit,omitempty"`
	KitPath   string `yaml:"kit_path,omitempty"`
	Secret    string `yaml:"secret,omitempty"` // also read by scripts/jira-docker.sh
	ReadOnly  *bool  `yaml:"read_only,omitempty"`
	Skill     *bool  `yaml:"skill,omitempty"`
}

// LoadDefaults reads the defaults for project.
func LoadDefaults(project, settings string) Defaults {
	var d Defaults
	path := filepath.Join(settings, DefaultsFile)
	var s saved
	if b, err := os.ReadFile(path); err == nil && yaml.Unmarshal(b, &s) == nil {
		agent := s.Agent
		if s.AgentKit == "" && ValidAgent(agent) != nil {
			agent = s.AgentName // a kit reference: keep only its name
		}
		if ValidAgent(agent) != nil {
			agent = ""
		}
		d = Defaults{Agent: agent, KitPath: s.KitPath, Secret: s.Secret, ReadOnly: s.ReadOnly, Skill: s.Skill, From: "your last setup (" + path + ")"}
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
	d.Agent, d.Name, d.ReadOnly = env.Agent, env.Name, &ro
	for _, n := range env.Kits {
		if isJiraKit(project, n.Value) {
			d.KitPath = strings.TrimPrefix(n.Value, "./")
		}
	}
	d.From, d.Project = "this project's previous setup", true
	return d
}

// BaseAgent is the agent of the user's ~/.sbxenv.yaml, which sbx merges
// under every project's sbxenv.yaml; "" if there is none. When that file
// brings an agent kit, sbx accepts no other agent name.
func BaseAgent(home string) string {
	var base struct {
		Agent string `yaml:"agent"`
	}
	b, err := os.ReadFile(filepath.Join(home, ".sbxenv.yaml"))
	if err != nil || yaml.Unmarshal(b, &base) != nil || ValidAgent(base.Agent) != nil {
		return ""
	}
	return base.Agent
}

// SaveDefaults stores this plan's choices for the next project.
func (p *Plan) SaveDefaults() error {
	ro, skill := p.ReadOnly, p.Skill
	s := saved{Agent: p.Agent, KitPath: p.KitPath, Secret: p.Secret, ReadOnly: &ro, Skill: &skill}
	if s.KitPath == DefaultKitPath {
		s.KitPath = ""
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	head := "# Choices of the last 'jiracli setup-sbx', used as defaults for the next project.\n"
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
