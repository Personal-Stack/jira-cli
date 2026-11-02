package cli

import (
	"fmt"
	"runtime"
	"sort"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/operation"
	"github.com/personal-stack/jira-cli/internal/output"
)

type cmdFlag struct {
	Flag        string   `json:"flag"`
	Param       string   `json:"param"`
	In          string   `json:"in"`
	Type        string   `json:"type"`
	Array       bool     `json:"array,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     string   `json:"server_default,omitempty"`
	Description string   `json:"description,omitempty"`
}

type cmdArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type cmdInfo struct {
	Command              string          `json:"command"`
	Group                string          `json:"group"`
	Name                 string          `json:"name"`
	Summary              string          `json:"summary,omitempty"`
	Description          string          `json:"description,omitempty"`
	OperationID          string          `json:"operation_id"`
	OriginalOperationID  string          `json:"original_operation_id,omitempty"`
	Method               string          `json:"method"`
	Path                 string          `json:"path"`
	Risk                 operation.Risk  `json:"risk"`
	RiskSource           string          `json:"risk_source,omitempty"`
	ConfirmationRequired bool            `json:"confirmation_required"`
	Idempotent           bool            `json:"idempotent"`
	Deprecated           bool            `json:"deprecated,omitempty"`
	Paginated            bool            `json:"paginated"`
	Args                 []cmdArg        `json:"args"`
	Flags                []cmdFlag       `json:"flags"`
	Body                 *operation.Body `json:"body,omitempty"`
	ResponseTypes        []string        `json:"response_content_types,omitempty"`
}

func describe(p *operation.Profile, op *operation.Operation, full bool) cmdInfo {
	ci := cmdInfo{
		Command: op.Group + " " + op.Name, Group: op.Group, Name: op.Name, Summary: op.Summary,
		OperationID: op.OperationID, Method: op.Method, Path: p.BasePath + op.Path,
		Risk: op.Risk, ConfirmationRequired: op.ConfirmationRequired, Idempotent: op.Idempotent,
		Deprecated: op.Deprecated, Paginated: op.Pagination != nil, Args: []cmdArg{}, Flags: []cmdFlag{},
	}
	if op.OriginalOperationID != op.OperationID {
		ci.OriginalOperationID = op.OriginalOperationID
	}
	for _, prm := range op.Params {
		if prm.In == operation.InPath {
			ci.Args = append(ci.Args, cmdArg{Name: prm.Name, Description: prm.Description})
			continue
		}
		f := cmdFlag{Flag: "--" + prm.Flag, Param: prm.Name, In: string(prm.In), Type: prm.Type, Array: prm.Array, Required: prm.Required, Enum: prm.Enum, Default: prm.Default}
		if full {
			f.Description = prm.Description
		}
		ci.Flags = append(ci.Flags, f)
	}
	if full {
		ci.Description, ci.RiskSource, ci.Body, ci.ResponseTypes = op.Description, op.RiskSource, op.Body, op.ResponseTypes
	} else if op.Body != nil {
		ci.Body = &operation.Body{Mode: op.Body.Mode, ContentType: op.Body.ContentType, Required: op.Body.Required, Schema: op.Body.Schema}
	}
	return ci
}

// activeProfile resolves the profile without failing when no config exists.
func (a *App) activeProfile() (*operation.Profile, error) {
	in, _, err := a.Instance()
	if err != nil {
		if p, ok := a.Profiles[a.Default]; ok {
			return p, nil
		}
		return nil, err
	}
	return a.ProfileFor(in)
}

func (a *App) commandsCmd() *cobra.Command {
	c := &cobra.Command{Use: "commands", Short: "Discover generated API commands"}
	var group, risk string
	list := &cobra.Command{
		Use:   "list",
		Short: "List generated API commands with their parameters, risk and provenance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			p, err := a.activeProfile()
			if err != nil {
				return err
			}
			var cmds []cmdInfo
			for i := range p.Operations {
				op := &p.Operations[i]
				if (group == "" || op.Group == group) && (risk == "" || string(op.Risk) == risk) {
					cmds = append(cmds, describe(p, op, false))
				}
			}
			if cmds == nil {
				cmds = []cmdInfo{}
			}
			if f == output.Table {
				rows := make([]map[string]any, len(cmds))
				for i, c := range cmds {
					rows[i] = map[string]any{"command": c.Command, "method": c.Method, "path": c.Path, "risk": c.Risk, "summary": c.Summary}
				}
				return output.Write(a.Out, rows, f)
			}
			return output.Write(a.Out, map[string]any{
				"profile": p.Name, "spec": p.Spec, "count": len(cmds), "commands": cmds,
			}, f)
		},
	}
	list.Flags().StringVar(&group, "group", "", "only commands of this group (e.g. issue)")
	list.Flags().StringVar(&risk, "risk", "", "only commands of this risk (read, write, destructive, administrative)")
	show := &cobra.Command{
		Use:   "describe <group> <name>",
		Short: "Describe one command in full (parameters, body schema, risk, provenance)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			p, err := a.activeProfile()
			if err != nil {
				return err
			}
			op := p.Find(args[0], args[1])
			if op == nil {
				return clierr.New(clierr.LayerUsage, "unknown_command", fmt.Sprintf("no command %q %q in profile %s", args[0], args[1], p.Name)).
					WithHint("run `jira commands list` to see available commands")
			}
			d := describe(p, op, true)
			return output.Write(a.Out, map[string]any{"command": d, "provenance": map[string]any{
				"profile": p.Name, "spec": p.Spec, "overlays": p.Overlays,
			}}, f)
		},
	}
	c.AddCommand(list, show)
	return c
}

func (a *App) capabilitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "Describe this binary, its profiles and the selected instance (machine-readable)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			names := make([]string, 0, len(a.Profiles))
			for n := range a.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)
			var profiles []map[string]any
			for _, n := range names {
				p := a.Profiles[n]
				profiles = append(profiles, map[string]any{
					"name": p.Name, "deployment": p.Deployment, "spec": p.Spec.Name, "spec_version": p.Spec.Version,
					"commands": len(p.Operations), "default": n == a.Default,
				})
			}
			active := map[string]any{}
			if in, src, err := a.Instance(); err != nil {
				active["error"] = clierr.As(err)
			} else {
				p, _ := a.ProfileFor(in)
				active = map[string]any{
					"instance": in.Name, "selected_by": src, "deployment": in.Deployment, "base_url": in.BaseURL,
					"auth_method": in.Auth.Type, "read_only": a.ReadOnly(in),
				}
				if p != nil {
					active["profile"] = p.Name
				}
			}
			return output.Write(a.Out, map[string]any{
				"cli":                 map[string]any{"name": "jira", "version": a.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH},
				"profiles":            profiles,
				"active":              active,
				"auth_methods":        []string{"pat", "oauth"},
				"credential_backends": []string{"file (JIRA_CREDENTIAL_FILE)", "secrets-dir (JIRA_CREDENTIALS_DIR, default /run/secrets)", "env (JIRA_CREDENTIAL_<REF>)", "keyring"},
				"output_formats":      []string{"json", "yaml", "table"},
				"features": map[string]bool{
					"dry_run": true, "pagination": true, "risk_classification": true, "read_only_mode": true,
					"confirmation": true, "mtls": true, "custom_ca": true, "oauth_pkce": true,
				},
				"global_flags": globalFlags(cmd.Root()),
				"exit_codes": map[string]string{
					"0": "success", "1": "internal error", "2": "usage error", "3": "configuration/credentials",
					"4": "authentication (401)", "5": "authorization (403)", "6": "not found (404)", "7": "other Jira API error",
					"8": "network/TLS/proxy/timeout", "9": "refused by safety policy", "10": "rate limited (429)",
				},
			}, f)
		},
	}
}

func (a *App) specCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "spec",
		Short: "Show compiled-in API specifications and their provenance",
		Long: `Show compiled-in API specifications and their provenance.

Specifications are compiled into the binary at build time. Updating,
validating and generating specifications are development tasks:
  go run ./cmd/jiragen validate|generate|update   (see docs/openapi-generation.md)`,
	}
	list := &cobra.Command{
		Use: "list", Short: "List compiled-in profiles", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			names := make([]string, 0, len(a.Profiles))
			for n := range a.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)
			var out []map[string]any
			for _, n := range names {
				p := a.Profiles[n]
				out = append(out, map[string]any{"profile": p.Name, "deployment": p.Deployment, "spec": p.Spec, "overlays": p.Overlays, "commands": len(p.Operations)})
			}
			return output.Write(a.Out, out, f)
		},
	}
	status := &cobra.Command{
		Use: "status", Short: "Show the active profile, provenance and generation report summary", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			p, err := a.activeProfile()
			if err != nil {
				return err
			}
			byRisk, byMethod := map[string]int{}, map[string]int{}
			for _, op := range p.Operations {
				byRisk[string(op.Risk)]++
				byMethod[op.Method]++
			}
			return output.Write(a.Out, map[string]any{
				"profile": p.Name, "deployment": p.Deployment, "base_path": p.BasePath,
				"spec": p.Spec, "overlays": p.Overlays,
				"commands": len(p.Operations), "by_risk": byRisk, "by_method": byMethod,
				"skipped": p.Skipped, "warnings": len(p.Warnings),
			}, f)
		},
	}
	c.AddCommand(list, status)
	return c
}
