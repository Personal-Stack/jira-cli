// Package skills embeds the agent skills shipped with jira-cli, so commands
// such as `jiracli setup-sbx` can install them without a repository clone.
package skills

import "embed"

// FS holds the skill directories, e.g. "jira/SKILL.md".
//
//go:embed jira
var FS embed.FS
