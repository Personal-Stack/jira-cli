package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/operation"
)

// annotation keys exposed on generated commands.
const (
	annOperationID = "jira.operation_id"
	annRisk        = "jira.risk"
	annGroup       = "jira.group"
)

// registerProfile adds one command group per tag and one command per
// operation. Generated commands contain no logic: they all delegate to the
// executor.
func (a *App) registerProfile(root *cobra.Command, p *operation.Profile) {
	groups := map[string]*cobra.Command{}
	for i := range p.Operations {
		op := &p.Operations[i]
		g, ok := groups[op.Group]
		if !ok {
			g = &cobra.Command{
				Use:         op.Group,
				Short:       fmt.Sprintf("%s operations", op.Tag),
				GroupID:     "api",
				Annotations: map[string]string{annGroup: "true"},
			}
			groups[op.Group] = g
			root.AddCommand(g)
		}
		g.AddCommand(a.operationCommand(p, op))
	}
}

func (a *App) operationCommand(p *operation.Profile, op *operation.Operation) *cobra.Command {
	pathParams := op.PathParams()
	use := op.Name
	for _, pp := range pathParams {
		use += " <" + pp.Name + ">"
	}
	short := op.Summary
	if short == "" {
		short = op.Method + " " + op.Path
	}
	if op.Risk != operation.RiskRead {
		short += " [" + string(op.Risk) + "]"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  longHelp(p, op),
		Args:  exactArgs(pathParams),
		Annotations: map[string]string{
			annOperationID: op.OperationID,
			annRisk:        string(op.Risk),
		},
		Deprecated: deprecatedMsg(op),
	}
	if len(pathParams) > 0 {
		var ex []string
		for _, pp := range pathParams {
			ex = append(ex, "<"+pp.Name+">")
		}
		cmd.Example = fmt.Sprintf("  jiracli %s %s %s", op.Group, op.Name, strings.Join(ex, " "))
	}
	f := cmd.Flags()
	for _, prm := range op.Params {
		if prm.In == operation.InPath {
			continue
		}
		desc := prm.Description
		if prm.In == operation.InHeader {
			desc = "[header " + prm.Name + "] " + desc
		} else if prm.Flag != kebabFlag(prm.Name) {
			desc = "[query " + prm.Name + "] " + desc
		}
		if len(prm.Enum) > 0 {
			desc += " (one of: " + strings.Join(prm.Enum, ", ") + ")"
		}
		if prm.Default != "" {
			desc += " (server default: " + prm.Default + ")"
		}
		switch {
		case prm.Array:
			f.StringArray(prm.Flag, nil, desc+" (repeatable)")
		case prm.Type == "boolean":
			f.Bool(prm.Flag, false, desc)
		case prm.Type == "string":
			f.String(prm.Flag, "", desc)
		default:
			f.String(prm.Flag, "", desc+" ("+prm.Type+")")
		}
		if prm.Required {
			_ = cmd.MarkFlagRequired(prm.Flag)
		}
	}
	if op.Body != nil {
		switch op.Body.Mode {
		case operation.BodyMultipart:
			f.String("file", "", "file to upload (multipart field \""+op.Body.MultipartField+"\")")
		default:
			f.String("file", "", "read the request body from this file")
			f.Bool("stdin", false, "read the request body from standard input")
			if op.Body.Mode == operation.BodyRaw {
				f.String("content-type", op.Body.ContentType, "request body media type")
			}
		}
	}
	if op.Pagination != nil {
		f.Bool("all", false, "follow pagination and return all results merged into one document")
		f.Int("max-pages", 100, "with --all: stop after this many pages")
	}
	cmd.RunE = func(c *cobra.Command, args []string) error { return a.execute(c, p, op, args) }
	return cmd
}

func deprecatedMsg(op *operation.Operation) string {
	if op.Deprecated {
		return "the operation is marked deprecated in the API specification"
	}
	return ""
}

func longHelp(p *operation.Profile, op *operation.Operation) string {
	var b strings.Builder
	if op.Summary != "" {
		b.WriteString(op.Summary + "\n\n")
	}
	if op.Description != "" && op.Description != op.Summary {
		b.WriteString(op.Description + "\n\n")
	}
	if pps := op.PathParams(); len(pps) > 0 {
		b.WriteString("Arguments:\n")
		for _, pp := range pps {
			fmt.Fprintf(&b, "  <%s>  %s\n", pp.Name, pp.Description)
		}
		b.WriteString("\n")
	}
	if bd := op.Body; bd != nil {
		fmt.Fprintf(&b, "Request body: %s (%s)", bd.ContentType, bd.Mode)
		if bd.Schema != "" {
			fmt.Fprintf(&b, ", schema %s", bd.Schema)
		}
		if bd.Required {
			b.WriteString(", required")
		}
		b.WriteString("\n")
		if len(bd.Properties) > 0 {
			props := make([]string, 0, len(bd.Properties))
			for _, pr := range bd.Properties {
				s := pr.Name + ":" + pr.Type
				if pr.Required {
					s += "*"
				}
				props = append(props, s)
			}
			sort.Strings(props)
			fmt.Fprintf(&b, "Top-level properties (* required): %s\n", strings.Join(props, ", "))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Operation: %s %s\nOperation ID: %s", op.Method, p.BasePath+op.Path, op.OperationID)
	if op.OriginalOperationID != op.OperationID {
		fmt.Fprintf(&b, " (spec: %s)", op.OriginalOperationID)
	}
	fmt.Fprintf(&b, "\nRisk: %s (%s)", op.Risk, op.RiskSource)
	if op.ConfirmationRequired {
		b.WriteString(", requires --yes or interactive confirmation")
	}
	fmt.Fprintf(&b, "\nSpecification: %s %s (profile %s)", p.Spec.Name, p.Spec.Version, p.Name)
	return b.String()
}

func exactArgs(pps []operation.Param) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != len(pps) {
			names := make([]string, len(pps))
			for i, p := range pps {
				names[i] = "<" + p.Name + ">"
			}
			return usageErr(fmt.Sprintf("%s expects %d argument(s) %s, got %d", cmd.CommandPath(), len(pps), strings.Join(names, " "), len(args)))
		}
		for i, a := range args {
			if strings.TrimSpace(a) == "" {
				return usageErr(fmt.Sprintf("argument <%s> must not be empty", pps[i].Name))
			}
		}
		return nil
	}
}

func kebabFlag(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r + 32)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
