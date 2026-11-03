package safety

import (
	"io"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/operation"
)

var (
	read = &operation.Operation{Group: "issue", Name: "get", Method: "GET", Risk: operation.RiskRead}
	wr   = &operation.Operation{Group: "issue", Name: "create", Method: "POST", Risk: operation.RiskWrite}
	del  = &operation.Operation{Group: "issue", Name: "delete", Method: "DELETE", Risk: operation.RiskDestructive, ConfirmationRequired: true}
)

func code(err error) string {
	if err == nil {
		return ""
	}
	return clierr.As(err).Code
}

func TestReadOnlyBlocksEverythingButReads(t *testing.T) {
	o := Options{ReadOnly: true, Yes: true}
	if Check(read, o) != nil {
		t.Fatal("read blocked")
	}
	if code(Check(wr, o)) != "read_only_mode" || code(Check(del, o)) != "read_only_mode" {
		t.Fatal("write allowed in read-only mode")
	}
	if Check(del, Options{ReadOnly: true, DryRun: true}) != nil {
		t.Fatal("dry-run sends nothing and must be allowed in read-only mode")
	}
}

func TestDestructiveNeedsConfirmation(t *testing.T) {
	if code(Check(del, Options{})) != "confirmation_required" {
		t.Fatal("unattended destructive op allowed")
	}
	if Check(del, Options{Yes: true}) != nil || Check(del, Options{DryRun: true}) != nil || Check(wr, Options{}) != nil {
		t.Fatal("unexpected refusal")
	}
	if Check(del, Options{Interactive: true, In: strings.NewReader("issue delete\n"), Out: io.Discard}) != nil {
		t.Fatal("typed confirmation rejected")
	}
	if code(Check(del, Options{Interactive: true, In: strings.NewReader("y\n"), Out: io.Discard})) != "confirmation_declined" {
		t.Fatal("'y' must not be enough")
	}
}
