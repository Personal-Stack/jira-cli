package generator

import (
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/personal-stack/jira-cli/internal/operation"
)

type skipped struct {
	r      *rawOp
	reason string
}

// errUnsupported marks constructs the CLI cannot map safely; the operation is
// skipped and listed in the generation report.
type errUnsupported struct{ msg string }

func (e errUnsupported) Error() string { return e.msg }

func unsupported(format string, a ...any) error { return errUnsupported{fmt.Sprintf(format, a...)} }

// mapOperation converts one raw operation into CLI metadata. Command names are
// assigned later, once collisions can be evaluated across the group.
func mapOperation(r *rawOp, aliases map[string]string) (*operation.Operation, error) {
	op := &operation.Operation{
		OriginalOperationID: r.origID,
		OperationID:         r.op.OperationID,
		Method:              r.method,
		Path:                r.path,
		Summary:             cleanText(r.op.Summary, true),
		Description:         cleanText(r.op.Description, false),
		Deprecated:          r.op.Deprecated,
	}
	if op.OperationID == "" {
		return nil, unsupported("operation has no operationId")
	}
	if len(r.op.Tags) == 0 {
		return nil, unsupported("operation has no tag (cannot determine command group)")
	}
	op.Tag = r.op.Tags[0]

	params, err := mapParams(r, aliases)
	if err != nil {
		return nil, err
	}
	op.Params = params

	if r.op.RequestBody != nil && r.op.RequestBody.Value != nil {
		b, err := mapBody(r.op.RequestBody.Value)
		if err != nil {
			return nil, err
		}
		op.Body = b
	}
	if r.ovr != nil && r.ovr.BodyRequired != nil {
		if op.Body == nil {
			return nil, fmt.Errorf("overlay sets body_required but the operation has no request body")
		}
		op.Body.Required = *r.ovr.BodyRequired
	}
	op.ResponseTypes = responseTypes(r.op.Responses)

	var hasStart, hasLimit bool
	for _, p := range op.Params {
		if p.In == operation.InQuery && p.Name == "startAt" && p.Type == "integer" {
			hasStart = true
		}
		if p.In == operation.InQuery && p.Name == "maxResults" && p.Type == "integer" {
			hasLimit = true
		}
	}
	if hasStart && hasLimit && r.method == "GET" {
		op.Pagination = &operation.Pagination{StartParam: "startAt", LimitParam: "maxResults"}
	}
	return op, nil
}

func mapParams(r *rawOp, aliases map[string]string) ([]operation.Param, error) {
	// Path-item parameters apply unless overridden by name+location.
	type key struct{ name, in string }
	merged := map[key]*openapi3.Parameter{}
	var order []key
	add := func(ps openapi3.Parameters) {
		for _, pr := range ps {
			if pr == nil || pr.Value == nil {
				continue
			}
			k := key{pr.Value.Name, pr.Value.In}
			if _, ok := merged[k]; !ok {
				order = append(order, k)
			}
			merged[k] = pr.Value
		}
	}
	add(r.item.Parameters)
	add(r.op.Parameters)

	tmpl := templateRe.FindAllStringSubmatch(r.path, -1)
	pathIdx := map[string]int{}
	for i, m := range tmpl {
		if _, dup := pathIdx[m[1]]; dup {
			return nil, fmt.Errorf("path template repeats {%s}", m[1])
		}
		pathIdx[m[1]] = i
	}

	var pathParams, others []operation.Param
	seenPath := map[string]bool{}
	for _, k := range order {
		p := merged[k]
		if p.Content != nil {
			return nil, unsupported("parameter %s uses content encoding", p.Name)
		}
		out := operation.Param{
			Name:        p.Name,
			In:          operation.ParamLocation(p.In),
			Required:    p.Required,
			Description: cleanText(p.Description, true),
		}
		typ, arr, enum, def, err := scalarType(p.Schema, aliases)
		if err != nil {
			return nil, unsupported("parameter %s: %v", p.Name, err)
		}
		out.Type, out.Array, out.Enum, out.Default = typ, arr, enum, def
		switch p.In {
		case "path":
			if _, ok := pathIdx[p.Name]; !ok {
				return nil, fmt.Errorf("path parameter %q has no {%s} in path template (fix with an overlay)", p.Name, p.Name)
			}
			if arr {
				return nil, unsupported("array path parameter %s", p.Name)
			}
			out.Required = true
			seenPath[p.Name] = true
			pathParams = append(pathParams, out)
		case "query", "header":
			out.Flag = kebab(p.Name)
			if r.ovr != nil {
				if po, ok := r.ovr.Params[p.Name]; ok && po.Flag != "" {
					out.Flag = po.Flag
				}
			}
			if p.In == "header" && isForbiddenHeader(p.Name) {
				return nil, unsupported("header parameter %s is controlled by the transport layer", p.Name)
			}
			others = append(others, out)
		case "cookie":
			return nil, unsupported("cookie parameter %s", p.Name)
		default:
			return nil, unsupported("parameter %s in %q", p.Name, p.In)
		}
	}
	for name := range pathIdx {
		if !seenPath[name] {
			return nil, fmt.Errorf("path template {%s} has no parameter definition (fix with an overlay)", name)
		}
	}
	sort.SliceStable(pathParams, func(i, j int) bool { return pathIdx[pathParams[i].Name] < pathIdx[pathParams[j].Name] })
	sort.SliceStable(others, func(i, j int) bool {
		if others[i].In != others[j].In {
			return others[i].In > others[j].In // query before header
		}
		return others[i].Name < others[j].Name
	})
	return append(pathParams, others...), nil
}

func isForbiddenHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "cookie", "proxy-authorization", "host", "content-length", "content-type", "accept":
		return true
	}
	return false
}

func scalarType(sr *openapi3.SchemaRef, aliases map[string]string) (typ string, array bool, enum []string, def string, err error) {
	if sr != nil && sr.Ref != "" {
		if t, ok := aliases[sr.Ref[strings.LastIndex(sr.Ref, "/")+1:]]; ok {
			return t, false, nil, "", nil
		}
	}
	if sr == nil || sr.Value == nil || sr.Value.Type == nil || sr.Value.Type.IsEmpty() {
		return "string", false, nil, "", nil // untyped parameters are sent verbatim
	}
	s := sr.Value
	if s.Type.Is("array") {
		if s.Items == nil || s.Items.Value == nil {
			return "string", true, nil, "", nil
		}
		t, arr, e, _, err := scalarType(s.Items, aliases)
		if err != nil {
			return "", false, nil, "", err
		}
		if arr {
			return "", false, nil, "", fmt.Errorf("nested arrays are not supported")
		}
		return t, true, e, "", nil
	}
	switch {
	case s.Type.Is("string"), s.Type.Is("integer"), s.Type.Is("number"), s.Type.Is("boolean"):
		typ = (*s.Type)[0]
	default:
		return "", false, nil, "", fmt.Errorf("type %v cannot be expressed as a flag", *s.Type)
	}
	for _, e := range s.Enum {
		enum = append(enum, fmt.Sprint(e))
	}
	if s.Default != nil {
		def = fmt.Sprint(s.Default)
	}
	return typ, false, enum, def, nil
}

// mapBody chooses how the request body is supplied. JSON is preferred, then
// multipart file upload, then raw bytes for other declared media types.
func mapBody(rb *openapi3.RequestBody) (*operation.Body, error) {
	types := make([]string, 0, len(rb.Content))
	for ct := range rb.Content {
		types = append(types, ct)
	}
	sort.Strings(types)
	pick := func(pred func(string) bool) string {
		for _, t := range types {
			if pred(t) {
				return t
			}
		}
		return ""
	}
	isJSON := func(t string) bool { return t == "application/json" || strings.HasSuffix(t, "+json") }
	if ct := pick(isJSON); ct != "" {
		b := &operation.Body{Mode: operation.BodyJSON, ContentType: ct, Required: rb.Required}
		if mt := rb.Content[ct]; mt.Schema != nil {
			describeSchema(b, mt.Schema)
		}
		return b, nil
	}
	if ct := pick(func(t string) bool { return t == "multipart/form-data" }); ct != "" {
		b := &operation.Body{Mode: operation.BodyMultipart, ContentType: ct, Required: rb.Required, MultipartField: "file"}
		if mt := rb.Content[ct]; mt.Schema != nil && mt.Schema.Value != nil {
			names := sortedKeys(mt.Schema.Value.Properties)
			for _, n := range names {
				ps := mt.Schema.Value.Properties[n]
				if ps.Value != nil && (ps.Value.Format == "binary" || ps.Value.Type == nil || ps.Value.Type.IsEmpty() || ps.Value.Type.Is("object")) {
					b.MultipartField = n
					break
				}
			}
		}
		return b, nil
	}
	if len(types) == 0 {
		return nil, unsupported("request body declares no media type")
	}
	ct := types[0]
	if ct == "*/*" {
		ct = "application/octet-stream"
	}
	return &operation.Body{Mode: operation.BodyRaw, ContentType: ct, Required: rb.Required}, nil
}

func describeSchema(b *operation.Body, sr *openapi3.SchemaRef) {
	if sr.Ref != "" {
		b.Schema = sr.Ref[strings.LastIndex(sr.Ref, "/")+1:]
	}
	s := sr.Value
	if s == nil || s.Type == nil || s.Type.IsEmpty() {
		if s != nil && len(s.Properties) > 0 {
			b.JSONType = "object"
		}
	} else {
		b.JSONType = (*s.Type)[0]
	}
	if s == nil {
		return
	}
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	for _, n := range sortedKeys(s.Properties) {
		p := operation.Property{Name: n, Type: "any", Required: req[n]}
		if v := s.Properties[n].Value; v != nil {
			if v.Type != nil && !v.Type.IsEmpty() {
				p.Type = (*v.Type)[0]
			} else if len(v.Properties) > 0 {
				p.Type = "object"
			}
			for _, e := range v.Enum {
				p.Enum = append(p.Enum, fmt.Sprint(e))
			}
		}
		b.Properties = append(b.Properties, p)
	}
}

func responseTypes(rs *openapi3.Responses) []string {
	if rs == nil {
		return nil
	}
	set := map[string]bool{}
	for code, r := range rs.Map() {
		if !strings.HasPrefix(code, "2") || r.Value == nil {
			continue
		}
		for ct := range r.Value.Content {
			set[ct] = true
		}
	}
	return sortedKeys(set)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var (
	tagRe       = regexp.MustCompile(`<[^>]{0,200}>`)
	javadocRe   = regexp.MustCompile(`\{@(?:code|link|literal)\s+([^}]*)\}`)
	blankRunsRe = regexp.MustCompile(`\n{3,}`)
	spaceRe     = regexp.MustCompile(`\s+`)
)

// cleanText turns spec prose (HTML + Javadoc) into plain terminal text.
// Spec text is untrusted: control characters are removed so it cannot inject
// terminal escape sequences into --help output.
func cleanText(s string, oneLine bool) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</li>`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`(?i)<li>`).ReplaceAllString(s, "- ")
	s = javadocRe.ReplaceAllString(s, "$1")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
	if oneLine {
		return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(blankRunsRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
