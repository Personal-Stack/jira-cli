package generator

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/personal-stack/jira-cli/internal/operation"
	"gopkg.in/yaml.v3"
)

// RiskFile is the separate business-risk metadata (OpenAPI cannot express it).
type RiskFile struct {
	Version int `yaml:"version"`
	// AdminTags marks every non-GET operation under these tags administrative.
	AdminTags []string `yaml:"admin_tags"`
	// DestructiveWords: a non-GET operation whose operationId contains one of
	// these words is destructive regardless of HTTP method.
	DestructiveWords []string            `yaml:"destructive_words"`
	Operations       map[string]RiskRule `yaml:"operations"` // key: resolved operationId
}

// RiskRule is an explicit classification for one operation.
type RiskRule struct {
	Risk                 operation.Risk `yaml:"risk"`
	ConfirmationRequired *bool          `yaml:"confirmation_required,omitempty"`
	Idempotent           *bool          `yaml:"idempotent,omitempty"`
	Reason               string         `yaml:"reason"`
}

func parseRisk(b []byte, name string) (*RiskFile, error) {
	var r RiskFile
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("risk file %s: %w", name, err)
	}
	if r.Version != 1 {
		return nil, fmt.Errorf("risk file %s: unsupported version %d", name, r.Version)
	}
	for id, rule := range r.Operations {
		switch rule.Risk {
		case operation.RiskRead, operation.RiskWrite, operation.RiskDestructive, operation.RiskAdministrative:
		default:
			return nil, fmt.Errorf("risk file %s: %s: invalid risk %q", name, id, rule.Risk)
		}
		if strings.TrimSpace(rule.Reason) == "" {
			return nil, fmt.Errorf("risk file %s: %s: reason is required", name, id)
		}
	}
	return &r, nil
}

// classify assigns risk using, in order of precedence: the explicit risk file
// entry, then conservative heuristics. Heuristics only ever raise risk above
// the HTTP-method default; they never lower it.
func classify(op *operation.Operation, rf *RiskFile) {
	if rf != nil {
		if rule, ok := rf.Operations[op.OperationID]; ok {
			op.Risk = rule.Risk
			op.RiskSource = "risk-file: " + rule.Reason
			op.ConfirmationRequired = op.Risk.Severity() >= operation.RiskAdministrative.Severity()
			if rule.ConfirmationRequired != nil {
				op.ConfirmationRequired = *rule.ConfirmationRequired
			}
			op.Idempotent = defaultIdempotent(op.Method)
			if rule.Idempotent != nil {
				op.Idempotent = *rule.Idempotent
			}
			return
		}
	}
	switch op.Method {
	case "GET", "HEAD":
		op.Risk, op.RiskSource = operation.RiskRead, "http-method"
	case "DELETE":
		op.Risk, op.RiskSource = operation.RiskDestructive, "http-method"
	default:
		op.Risk, op.RiskSource = operation.RiskWrite, "http-method"
	}
	if op.Method != "GET" && op.Method != "HEAD" && rf != nil {
		for _, t := range rf.AdminTags {
			if strings.EqualFold(t, op.Tag) && op.Risk.Severity() < operation.RiskAdministrative.Severity() {
				op.Risk, op.RiskSource = operation.RiskAdministrative, "admin-tag: "+t
			}
		}
		words := splitWords(strings.TrimRight(op.OperationID, "_0123456789"))
		for _, w := range rf.DestructiveWords {
			for _, ow := range words {
				if ow == strings.ToLower(w) && op.Risk != operation.RiskDestructive {
					op.Risk, op.RiskSource = operation.RiskDestructive, "operation-name: "+w
				}
			}
		}
	}
	op.ConfirmationRequired = op.Risk.Severity() >= operation.RiskAdministrative.Severity()
	op.Idempotent = defaultIdempotent(op.Method)
}

// defaultIdempotent: only safe methods are retried by default. PUT/DELETE are
// idempotent per RFC 9110, but Jira does not guarantee it, so we stay
// conservative; the risk file can opt individual operations in.
func defaultIdempotent(method string) bool { return method == "GET" || method == "HEAD" }
