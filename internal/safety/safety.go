// Package safety enforces risk-aware execution: read-only mode, explicit
// confirmation for destructive and administrative operations, and refusal to
// run them unattended without --yes.
package safety

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/operation"
)

// Options describe the execution context.
type Options struct {
	ReadOnly    bool // instance read_only, --read-only or JIRA_READ_ONLY=1
	Yes         bool // --yes
	DryRun      bool
	Interactive bool // stdin and stderr are terminals
	In          io.Reader
	Out         io.Writer
}

// Check decides whether op may run. It is called before any credential is
// loaded or any network request is made.
func Check(op *operation.Operation, o Options) error {
	// A dry run never sends the request, so it is allowed in read-only mode
	// (useful to preview writes against production).
	if o.ReadOnly && op.Risk != operation.RiskRead && !o.DryRun {
		return clierr.New(clierr.LayerSafety, "read_only_mode",
			fmt.Sprintf("%s %s is a %s operation; the instance is in read-only mode", op.Group, op.Name, op.Risk)).
			WithHint("remove read_only from the instance config / unset JIRA_READ_ONLY to allow writes")
	}
	if o.DryRun || !op.ConfirmationRequired || o.Yes {
		return nil
	}
	if !o.Interactive {
		return clierr.New(clierr.LayerSafety, "confirmation_required",
			fmt.Sprintf("%s %s is %s and requires confirmation", op.Group, op.Name, op.Risk)).
			WithHint("re-run with --yes after verifying the target, or use --dry-run to inspect the request")
	}
	want := op.Group + " " + op.Name
	fmt.Fprintf(o.Out, "WARNING: %s (%s %s) is classified as %s.\nType %q to continue: ", want, op.Method, op.Path, op.Risk, want)
	line, _ := bufio.NewReader(o.In).ReadString('\n')
	if strings.TrimSpace(line) != want {
		return clierr.New(clierr.LayerSafety, "confirmation_declined", "operation not confirmed")
	}
	return nil
}
