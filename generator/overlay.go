package generator

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Overlay is an explicit, reviewable set of corrections applied on top of
// specifications. Every key must match an existing operation, so stale
// overlays fail loudly instead of silently doing nothing.
type Overlay struct {
	Version     int          `yaml:"version"`
	RenamePaths []PathRename `yaml:"rename_paths"`
	// SchemaAliases maps component schema names to the scalar type they are
	// serialized as when used in parameters (e.g. Jira's StringList, declared
	// as an empty object but sent as a comma-separated string).
	SchemaAliases map[string]string     `yaml:"schema_aliases"`
	Exclude       []Exclusion           `yaml:"exclude"`
	Operations    map[string]OpOverride `yaml:"operations"` // key: "METHOD /path"
}

// PathRename moves the operations of one path template onto another. It
// fixes specs that declare the same resource under two differently named
// templates (which OpenAPI forbids). Moving onto an existing method fails.
type PathRename struct {
	Spec   string `yaml:"spec,omitempty"`
	From   string `yaml:"from"`
	To     string `yaml:"to"`
	Reason string `yaml:"reason"`
}

// Exclusion removes an operation from generation (recorded in the report).
type Exclusion struct {
	Spec   string `yaml:"spec,omitempty"` // restrict to one spec name; empty = any
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
}

// OpOverride adjusts a single operation.
type OpOverride struct {
	Spec        string                   `yaml:"spec,omitempty"`
	OperationID string                   `yaml:"operation_id,omitempty"`
	Group       string                   `yaml:"group,omitempty"`
	Command     string                   `yaml:"command,omitempty"`
	Reason      string                   `yaml:"reason,omitempty"`
	Params      map[string]ParamOverride `yaml:"params,omitempty"`
	// BodyRequired corrects the spec's requestBody.required flag.
	BodyRequired *bool `yaml:"body_required,omitempty"`
}

// ParamOverride fixes or renames a parameter.
type ParamOverride struct {
	In     string `yaml:"in,omitempty"`     // move to query/header (spec defects)
	Rename string `yaml:"rename,omitempty"` // rename (e.g. to match a path template)
	Flag   string `yaml:"flag,omitempty"`   // CLI flag name
	Drop   bool   `yaml:"drop,omitempty"`   // remove a bogus parameter
	Reason string `yaml:"reason,omitempty"`
}

var (
	opKeyRe   = regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH|HEAD) (/\S*)$`)
	cmdNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	opIDRe    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

func parseOverlay(b []byte, name string) (*Overlay, error) {
	var o Overlay
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&o); err != nil {
		return nil, fmt.Errorf("overlay %s: %w", name, err)
	}
	if o.Version != 1 {
		return nil, fmt.Errorf("overlay %s: unsupported version %d", name, o.Version)
	}
	for _, e := range o.Exclude {
		if !opKeyRe.MatchString(strings.ToUpper(e.Method) + " " + e.Path) {
			return nil, fmt.Errorf("overlay %s: invalid exclusion %s %s", name, e.Method, e.Path)
		}
		if strings.TrimSpace(e.Reason) == "" {
			return nil, fmt.Errorf("overlay %s: exclusion %s %s needs a reason", name, e.Method, e.Path)
		}
	}
	for k, v := range o.SchemaAliases {
		switch v {
		case "string", "integer", "number", "boolean":
		default:
			return nil, fmt.Errorf("overlay %s: schema alias %s: unsupported type %q", name, k, v)
		}
	}
	for _, r := range o.RenamePaths {
		if !strings.HasPrefix(r.From, "/") || !strings.HasPrefix(r.To, "/") || r.From == r.To {
			return nil, fmt.Errorf("overlay %s: invalid path rename %q -> %q", name, r.From, r.To)
		}
		if strings.TrimSpace(r.Reason) == "" {
			return nil, fmt.Errorf("overlay %s: path rename %s needs a reason", name, r.From)
		}
	}
	for k, v := range o.Operations {
		if !opKeyRe.MatchString(k) {
			return nil, fmt.Errorf("overlay %s: invalid operation key %q (want \"METHOD /path\")", name, k)
		}
		if v.Command != "" && !cmdNameRe.MatchString(v.Command) {
			return nil, fmt.Errorf("overlay %s: %s: invalid command name %q", name, k, v.Command)
		}
		if v.Group != "" && !cmdNameRe.MatchString(v.Group) {
			return nil, fmt.Errorf("overlay %s: %s: invalid group name %q", name, k, v.Group)
		}
		if v.OperationID != "" && !opIDRe.MatchString(v.OperationID) {
			return nil, fmt.Errorf("overlay %s: %s: invalid operation_id %q", name, k, v.OperationID)
		}
		for pn, p := range v.Params {
			if p.In != "" && p.In != "query" && p.In != "header" {
				return nil, fmt.Errorf("overlay %s: %s: param %s: in must be query or header", name, k, pn)
			}
			if p.Rename != "" && !opIDRe.MatchString(strings.ReplaceAll(p.Rename, "-", "_")) {
				return nil, fmt.Errorf("overlay %s: %s: param %s: invalid rename %q", name, k, pn, p.Rename)
			}
			if p.Flag != "" && !cmdNameRe.MatchString(p.Flag) {
				return nil, fmt.Errorf("overlay %s: %s: param %s: invalid flag %q", name, k, pn, p.Flag)
			}
		}
	}
	return &o, nil
}
