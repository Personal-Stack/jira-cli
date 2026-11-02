// Package operation defines the metadata model shared by the code generator
// (which emits it as Go source) and the runtime CLI (which turns it into
// Cobra commands and HTTP requests). It must stay dependency-free.
package operation

// Risk classifies the business impact of executing an operation.
type Risk string

const (
	RiskRead           Risk = "read"
	RiskWrite          Risk = "write"
	RiskDestructive    Risk = "destructive"
	RiskAdministrative Risk = "administrative"
)

// Severity orders risks; higher is more dangerous.
func (r Risk) Severity() int {
	switch r {
	case RiskRead:
		return 0
	case RiskWrite:
		return 1
	case RiskAdministrative:
		return 2
	case RiskDestructive:
		return 3
	}
	return 3 // unknown values are treated as the most dangerous
}

// ParamLocation is where a parameter is sent.
type ParamLocation string

const (
	InPath   ParamLocation = "path"
	InQuery  ParamLocation = "query"
	InHeader ParamLocation = "header"
)

// Param describes a single API parameter.
type Param struct {
	Name        string        `json:"name"`
	In          ParamLocation `json:"in"`
	Flag        string        `json:"flag,omitempty"` // CLI flag name (query/header); empty for path params
	Type        string        `json:"type"`           // string, integer, number, boolean
	Array       bool          `json:"array,omitempty"`
	Required    bool          `json:"required,omitempty"`
	Enum        []string      `json:"enum,omitempty"`
	Default     string        `json:"default,omitempty"`
	Description string        `json:"description,omitempty"`
}

// BodyMode is how request bodies are supplied and encoded.
type BodyMode string

const (
	BodyJSON      BodyMode = "json"      // JSON document from --file/--stdin
	BodyMultipart BodyMode = "multipart" // file upload from --file
	BodyRaw       BodyMode = "raw"       // bytes from --file/--stdin, sent with ContentType
)

// Property is a top-level property of a JSON request body schema.
type Property struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

// Body describes an operation's request body.
type Body struct {
	Mode           BodyMode   `json:"mode"`
	ContentType    string     `json:"content_type"`
	Required       bool       `json:"required,omitempty"`
	Schema         string     `json:"schema,omitempty"`     // component schema name, if any
	JSONType       string     `json:"json_type,omitempty"`  // object, array, string, ...
	Properties     []Property `json:"properties,omitempty"` // top-level properties (JSON mode)
	MultipartField string     `json:"multipart_field,omitempty"`
}

// Pagination describes offset-based pagination parameters.
type Pagination struct {
	StartParam string `json:"start_param"`
	LimitParam string `json:"limit_param"`
}

// Operation is one generated API operation and its CLI mapping.
type Operation struct {
	Group                string      `json:"group"`
	Name                 string      `json:"name"`
	OperationID          string      `json:"operation_id"`          // resolved (unique) operation ID
	OriginalOperationID  string      `json:"original_operation_id"` // as written in the spec
	Tag                  string      `json:"tag"`
	Method               string      `json:"method"`
	Path                 string      `json:"path"` // relative to the profile base path
	Summary              string      `json:"summary,omitempty"`
	Description          string      `json:"description,omitempty"`
	Deprecated           bool        `json:"deprecated,omitempty"`
	Params               []Param     `json:"params,omitempty"`
	Body                 *Body       `json:"body,omitempty"`
	Risk                 Risk        `json:"risk"`
	RiskSource           string      `json:"risk_source"`
	ConfirmationRequired bool        `json:"confirmation_required"`
	Idempotent           bool        `json:"idempotent"`
	Pagination           *Pagination `json:"pagination,omitempty"`
	ResponseTypes        []string    `json:"response_content_types,omitempty"`
}

// PathParams returns path parameters in the order they appear in Path.
func (o *Operation) PathParams() []Param {
	var out []Param
	for _, p := range o.Params {
		if p.In == InPath {
			out = append(out, p)
		}
	}
	return out
}

// Source records where a specification file came from.
type Source struct {
	Name    string `json:"name"`
	Kind    string `json:"source"` // official, custom, remote
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// Skipped records an operation the generator did not turn into a command.
type Skipped struct {
	OperationID string `json:"operation_id"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Reason      string `json:"reason"`
}

// Profile is a compiled-in set of operations generated from one specification
// plus its overlays.
type Profile struct {
	Name       string      `json:"name"`
	Deployment string      `json:"deployment"`
	BasePath   string      `json:"base_path"`
	Spec       Source      `json:"spec"`
	Overlays   []Source    `json:"overlays,omitempty"`
	Operations []Operation `json:"-"`
	Skipped    []Skipped   `json:"skipped,omitempty"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// Find returns the operation with the given group and name.
func (p *Profile) Find(group, name string) *Operation {
	for i := range p.Operations {
		if p.Operations[i].Group == group && p.Operations[i].Name == name {
			return &p.Operations[i]
		}
	}
	return nil
}
