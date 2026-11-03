package sbx

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// RememberFile, in the settings folder, keeps the choices of the last setup
// (agent, agent kit, image, ...). They are the defaults for the next project,
// so an agent kit's name does not have to be re-entered for every folder.
const RememberFile = "setup.yaml"

// Remembered are the choices carried over between projects. The sandbox
// name is per project and is not carried over.
type Remembered struct {
	Agent    string `yaml:"agent,omitempty"`
	AgentKit string `yaml:"agent_kit,omitempty"`
	Template string `yaml:"image,omitempty"`
	ReadOnly *bool  `yaml:"read_only,omitempty"`
	Skill    *bool  `yaml:"skill,omitempty"`
}

// LoadRemembered reads <settings>/setup.yaml.
func LoadRemembered(settings string) (Remembered, bool) {
	var r Remembered
	b, err := os.ReadFile(filepath.Join(settings, RememberFile))
	if err != nil || yaml.Unmarshal(b, &r) != nil {
		return Remembered{}, false
	}
	return r, true
}

// SaveRemembered writes this plan's carry-over choices to <settings>/setup.yaml.
func (p *Plan) SaveRemembered() error {
	ro, skill := p.ReadOnly, p.Skill
	b, err := yaml.Marshal(Remembered{Agent: p.Agent, AgentKit: p.AgentKit, Template: p.Template, ReadOnly: &ro, Skill: &skill})
	if err != nil {
		return err
	}
	head := "# Choices of the last 'jira setup-sbx', used as defaults for the next project.\n"
	return os.WriteFile(filepath.Join(p.Settings, RememberFile), append([]byte(head), b...), 0o644)
}

var notNameChars = regexp.MustCompile(`[^a-z0-9]+`)

// DefaultName is the sandbox name for a new setup: <agent>-<project folder>-jira.
// sbx's own default, <agent>-<project folder>, is the name `sbx run <agent>`
// gives a sandbox in that folder; reusing it collides with such a sandbox.
func DefaultName(agent, project string) string {
	slug := func(s string) string {
		return strings.Trim(notNameChars.ReplaceAllString(strings.ToLower(s), "-"), "-")
	}
	parts := []string{}
	for _, s := range []string{slug(agent), slug(filepath.Base(project)), "jira"} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "-")
}
