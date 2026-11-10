package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/api"
	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/operation"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/safety"
)

// maxBodySize bounds request bodies read from files/stdin.
const maxBodySize = 32 << 20

func usageErr(msg string) error { return clierr.New(clierr.LayerUsage, "invalid_usage", msg) }

// buildRequest converts arguments and flags into an API request. It performs
// all input validation before anything else happens.
func buildRequest(cmd *cobra.Command, op *operation.Operation, args []string, stdin io.Reader) (api.Request, error) {
	r := api.Request{Method: op.Method, Query: url.Values{}, Header: http.Header{}, Idempotent: op.Idempotent}
	for _, t := range op.ResponseTypes {
		if strings.HasPrefix(t, "text/html") {
			r.AcceptHTML = true
		}
	}
	// Path: escape each argument individually.
	path := op.Path
	for i, pp := range op.PathParams() {
		path = strings.Replace(path, "{"+pp.Name+"}", url.PathEscape(args[i]), 1)
	}
	r.Path = path

	f := cmd.Flags()
	for _, p := range op.Params {
		if p.In == operation.InPath || !f.Changed(p.Flag) {
			continue
		}
		var vals []string
		switch {
		case p.Array:
			vals, _ = f.GetStringArray(p.Flag)
		case p.Type == "boolean":
			b, _ := f.GetBool(p.Flag)
			vals = []string{strconv.FormatBool(b)}
		default:
			s, _ := f.GetString(p.Flag)
			vals = []string{s}
		}
		for _, v := range vals {
			if err := validateValue(p, v); err != nil {
				return r, err
			}
		}
		if p.In == operation.InHeader {
			r.Header[http.CanonicalHeaderKey(p.Name)] = vals
		} else {
			r.Query[p.Name] = vals
		}
	}
	if op.Body != nil {
		if err := readBody(cmd, op, stdin, &r); err != nil {
			return r, err
		}
	}
	return r, nil
}

func validateValue(p operation.Param, v string) error {
	if strings.ContainsAny(v, "\r\n") && p.In == operation.InHeader {
		return usageErr(fmt.Sprintf("--%s: header values must not contain newlines", p.Flag))
	}
	switch p.Type {
	case "integer":
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return usageErr(fmt.Sprintf("--%s: %q is not an integer", p.Flag, v))
		}
	case "number":
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return usageErr(fmt.Sprintf("--%s: %q is not a number", p.Flag, v))
		}
	}
	if len(p.Enum) > 0 {
		for _, e := range p.Enum {
			if e == v {
				return nil
			}
		}
		return usageErr(fmt.Sprintf("--%s: %q is not one of %s", p.Flag, v, strings.Join(p.Enum, ", ")))
	}
	return nil
}

func readBody(cmd *cobra.Command, op *operation.Operation, stdin io.Reader, r *api.Request) error {
	f := cmd.Flags()
	file, _ := f.GetString("file")
	useStdin := false
	if f.Lookup("stdin") != nil {
		useStdin, _ = f.GetBool("stdin")
	}
	if file != "" && useStdin {
		return usageErr("use either --file or --stdin, not both")
	}
	if file == "" && !useStdin {
		if op.Body.Required {
			hint := "--file <path> or --stdin"
			if op.Body.Mode == operation.BodyMultipart {
				hint = "--file <path>"
			}
			return usageErr("this operation requires a request body: " + hint)
		}
		return nil
	}
	var data []byte
	var err error
	if useStdin {
		data, err = io.ReadAll(io.LimitReader(stdin, maxBodySize+1))
	} else {
		data, err = readLimited(file)
	}
	if err != nil {
		return usageErr("cannot read request body: " + err.Error())
	}
	if len(data) > maxBodySize {
		return usageErr(fmt.Sprintf("request body exceeds %d bytes", maxBodySize))
	}

	switch op.Body.Mode {
	case operation.BodyJSON:
		if err := validateJSONBody(op.Body, data); err != nil {
			return err
		}
		r.Body, r.ContentType = data, op.Body.ContentType
	case operation.BodyMultipart:
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, op.Body.MultipartField, filepath.Base(file)))
		h.Set("Content-Type", "application/octet-stream")
		pw, _ := mw.CreatePart(h)
		pw.Write(data)
		mw.Close()
		r.Body, r.ContentType = buf.Bytes(), mw.FormDataContentType()
		// Jira's XSRF filter rejects multipart uploads without this header.
		r.Header.Set("X-Atlassian-Token", "no-check")
	default:
		ct, _ := f.GetString("content-type")
		r.Body, r.ContentType = data, ct
	}
	return nil
}

func readLimited(path string) ([]byte, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return io.ReadAll(io.LimitReader(fh, maxBodySize+1))
}

// validateJSONBody checks syntax, top-level type and required top-level
// properties declared by the schema. Deeper validation is left to Jira,
// whose field configuration (custom fields, screens) is instance-specific.
func validateJSONBody(b *operation.Body, data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return usageErr("request body is not valid JSON: " + err.Error())
	}
	if dec.More() {
		return usageErr("request body contains more than one JSON value")
	}
	switch b.JSONType {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return usageErr("request body must be a JSON object (schema " + b.Schema + ")")
		}
		var missing []string
		for _, p := range b.Properties {
			if _, ok := obj[p.Name]; p.Required && !ok {
				missing = append(missing, p.Name)
			}
		}
		if len(missing) > 0 {
			return usageErr("request body is missing required properties: " + strings.Join(missing, ", "))
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return usageErr("request body must be a JSON array")
		}
	}
	return nil
}

func (a *App) execute(cmd *cobra.Command, p *operation.Profile, op *operation.Operation, args []string) error {
	format, err := a.Format()
	if err != nil {
		return err
	}
	in, _, err := a.Instance()
	if err != nil {
		return err
	}
	ip, err := a.ProfileFor(in)
	if err != nil {
		return err
	}
	if ip.Name != p.Name {
		return clierr.New(clierr.LayerConfig, "profile_mismatch", fmt.Sprintf("instance %s uses profile %s but the command was built for %s", in.Name, ip.Name, p.Name))
	}
	log := a.Log().With("instance", in.Name, "operation_id", op.OperationID)
	log.Info("operation", "method", op.Method, "path", p.BasePath+op.Path, "risk", string(op.Risk),
		"profile", p.Name, "spec_version", p.Spec.Version, "dry_run", a.dryRun)
	req, err := buildRequest(cmd, op, args, a.In)
	if err != nil {
		log.Debug("request validation failed", "error", err)
		return err
	}
	if err := safety.Check(op, safety.Options{
		ReadOnly: a.ReadOnly(in), Yes: a.yes, DryRun: a.dryRun,
		Interactive: a.IsTerminal(), In: a.In, Out: a.Err,
	}); err != nil {
		log.Info("refused by safety policy", "error_code", clierr.As(err).Code, "risk", string(op.Risk), "read_only", a.ReadOnly(in))
		return err
	}
	all := false
	maxPages := 0
	if op.Pagination != nil {
		all, _ = cmd.Flags().GetBool("all")
		maxPages, _ = cmd.Flags().GetInt("max-pages")
	}
	if a.dryRun {
		return a.printDryRun(a.Out, in.BaseURL, p, op, req, all, format)
	}
	client, err := a.Client(in, p)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	var resp *api.Response
	var body []byte
	if all {
		res, err := client.FetchAll(ctx, req, api.Page{StartParam: op.Pagination.StartParam, LimitParam: op.Pagination.LimitParam, MaxPages: maxPages})
		if err != nil {
			return err
		}
		if !res.Complete {
			a.Log().Warn("stopped before the last page; results are incomplete", "pages", res.Pages, "items", res.Items, "max_pages", maxPages)
		}
		resp, body = res.Last, res.Body
	} else {
		resp, err = client.Do(ctx, req)
		if err != nil {
			return err
		}
		body = resp.Body
	}
	return a.printResponse(a.Out, resp, body, format)
}

func (a *App) printResponse(w io.Writer, resp *api.Response, body []byte, f output.Format) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		// Explicit success record so callers can verify writes (e.g. 204).
		return output.Write(w, map[string]any{"ok": true, "status": resp.Status}, f)
	}
	if resp.IsJSON() || json.Valid(trimmed) {
		if err := output.WriteRaw(w, trimmed, f); err != nil {
			return clierr.Wrap(clierr.LayerResponse, "render_failed", "cannot render response", err)
		}
		return nil
	}
	_, err := w.Write(body)
	return err
}

// printDryRun shows exactly what would be sent, without loading credentials
// or opening a connection.
func (a *App) printDryRun(w io.Writer, baseURL string, p *operation.Profile, op *operation.Operation, r api.Request, all bool, f output.Format) error {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return clierr.Wrap(clierr.LayerConfig, "invalid_base_url", "invalid base_url", err)
	}
	c := &api.Client{BaseURL: u, BasePath: p.BasePath}
	headers := map[string]string{"Accept": "application/json", "Authorization": "[REDACTED: added at send time]"}
	for k, v := range r.Header {
		headers[k] = strings.Join(v, ", ")
	}
	if r.ContentType != "" {
		headers["Content-Type"] = r.ContentType
	}
	out := map[string]any{
		"dry_run": true,
		"command": op.Group + " " + op.Name,
		"operation": map[string]any{
			"operation_id": op.OperationID, "risk": op.Risk, "confirmation_required": op.ConfirmationRequired,
			"idempotent": op.Idempotent,
		},
		"request": map[string]any{"method": r.Method, "url": c.URL(r.Path, r.Query), "headers": headers},
	}
	req := out["request"].(map[string]any)
	if r.Body != nil {
		if strings.Contains(r.ContentType, "json") && json.Valid(r.Body) {
			req["body"] = json.RawMessage(r.Body)
		} else {
			req["body_bytes"] = len(r.Body)
		}
	}
	if all {
		req["paginate"] = true
	}
	return output.Write(w, out, f)
}
