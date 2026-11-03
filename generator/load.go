package generator

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// loadedSpec is one parsed specification.
type loadedSpec struct {
	ref    SpecRef
	sha256 string
	doc    *openapi3.T
}

// rawOp is one operation from one specification, before CLI mapping.
type rawOp struct {
	spec   *loadedSpec
	origID string // operationId as written in the spec
	method string
	path   string
	op     *openapi3.Operation
	item   *openapi3.PathItem
	ovr    *OpOverride
}

func (r *rawOp) key() string { return r.method + " " + r.path }

// loadSpec parses an OpenAPI document. External references are disabled:
// specifications are untrusted input and must never trigger network or file
// access during generation.
func loadSpec(b []byte) (*openapi3.T, error) {
	l := openapi3.NewLoader()
	l.IsExternalRefsAllowed = false
	l.ReadFromURIFunc = func(_ *openapi3.Loader, u *url.URL) ([]byte, error) {
		return nil, fmt.Errorf("external reference %q not allowed", u.String())
	}
	doc, err := l.LoadFromData(b)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		return nil, fmt.Errorf("unsupported OpenAPI version %q (need 3.x)", doc.OpenAPI)
	}
	if doc.Info == nil || doc.Info.Version == "" {
		return nil, fmt.Errorf("info.version is required for provenance")
	}
	return doc, nil
}

var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}

// collectOps lists operations in deterministic (path, method) order.
func collectOps(s *loadedSpec) []*rawOp {
	paths := s.doc.Paths.InMatchingOrder()
	sort.Strings(paths)
	var out []*rawOp
	for _, p := range paths {
		item := s.doc.Paths.Value(p)
		for _, m := range methods {
			if op := item.GetOperation(m); op != nil {
				out = append(out, &rawOp{spec: s, origID: op.OperationID, method: m, path: p, op: op, item: item})
			}
		}
	}
	return out
}

// applyOverlays removes excluded operations and attaches overrides. It also
// rewrites parameters on the parsed document (moving/dropping parameters
// that the spec declares incorrectly) so that strict validation runs on the
// corrected document.
func applyOverlays(ops []*rawOp, overlays []*Overlay, names []string) ([]*rawOp, []skipped, error) {
	var skips []skipped
	for oi, ov := range overlays {
		for _, ex := range ov.Exclude {
			n := 0
			kept := ops[:0]
			for _, r := range ops {
				if r.method == strings.ToUpper(ex.Method) && r.path == ex.Path && (ex.Spec == "" || ex.Spec == r.spec.ref.Name) {
					n++
					skips = append(skips, skipped{r, "excluded by overlay " + names[oi] + ": " + ex.Reason})
					continue
				}
				kept = append(kept, r)
			}
			ops = kept
			if n == 0 {
				return nil, nil, fmt.Errorf("overlay %s: exclusion %s %s matches no operation (stale overlay?)", names[oi], ex.Method, ex.Path)
			}
		}
		keys := make([]string, 0, len(ov.Operations))
		for k := range ov.Operations {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			o := ov.Operations[k]
			var match []*rawOp
			for _, r := range ops {
				if r.key() == k && (o.Spec == "" || o.Spec == r.spec.ref.Name) {
					match = append(match, r)
				}
			}
			switch {
			case len(match) == 0:
				return nil, nil, fmt.Errorf("overlay %s: %q matches no operation (stale overlay?)", names[oi], k)
			case len(match) > 1:
				return nil, nil, fmt.Errorf("overlay %s: %q matches operations in several specs; set spec:", names[oi], k)
			}
			r := match[0]
			if r.ovr == nil {
				r.ovr = &OpOverride{}
			}
			merge(r.ovr, &o)
			if o.OperationID != "" {
				r.op.OperationID = o.OperationID
			}
			if err := fixParams(r, o.Params); err != nil {
				return nil, nil, fmt.Errorf("overlay %s: %s: %w", names[oi], k, err)
			}
		}
	}
	return ops, skips, nil
}

func merge(dst, src *OpOverride) {
	if src.OperationID != "" {
		dst.OperationID = src.OperationID
	}
	if src.Group != "" {
		dst.Group = src.Group
	}
	if src.Command != "" {
		dst.Command = src.Command
	}
	if dst.Params == nil {
		dst.Params = map[string]ParamOverride{}
	}
	for k, v := range src.Params {
		dst.Params[k] = v
	}
	if src.BodyRequired != nil {
		dst.BodyRequired = src.BodyRequired
	}
}

func fixParams(r *rawOp, ovr map[string]ParamOverride) error {
	for name, p := range ovr {
		found := false
		var params openapi3.Parameters
		for _, pr := range r.op.Parameters {
			if pr.Value == nil || pr.Value.Name != name {
				params = append(params, pr)
				continue
			}
			found = true
			if p.Drop {
				continue
			}
			if p.In != "" || p.Rename != "" {
				cp := *pr.Value
				if p.Rename != "" {
					cp.Name = p.Rename
				}
				if p.In != "" {
					cp.In = p.In
					if p.In == "query" {
						cp.Required = false
					}
				}
				params = append(params, &openapi3.ParameterRef{Value: &cp})
				continue
			}
			params = append(params, pr)
		}
		if !found {
			return fmt.Errorf("param %q not found", name)
		}
		r.op.Parameters = params
	}
	return nil
}

// renamePaths applies overlay path renames to the parsed documents before
// operations are collected.
func renamePaths(specs []*loadedSpec, overlays []*Overlay, names []string) error {
	for oi, ov := range overlays {
		for _, rn := range ov.RenamePaths {
			n := 0
			for _, s := range specs {
				if rn.Spec != "" && rn.Spec != s.ref.Name {
					continue
				}
				from := s.doc.Paths.Value(rn.From)
				if from == nil {
					continue
				}
				n++
				to := s.doc.Paths.Value(rn.To)
				if to == nil {
					to = &openapi3.PathItem{}
					s.doc.Paths.Set(rn.To, to)
				}
				for _, m := range methods {
					op := from.GetOperation(m)
					if op == nil {
						continue
					}
					if to.GetOperation(m) != nil {
						return fmt.Errorf("overlay %s: cannot move %s %s onto %s: method already defined", names[oi], m, rn.From, rn.To)
					}
					to.SetOperation(m, op)
				}
				to.Parameters = append(to.Parameters, from.Parameters...)
				s.doc.Paths.Delete(rn.From)
			}
			if n == 0 {
				return fmt.Errorf("overlay %s: rename_paths from %s matches no path (stale overlay?)", names[oi], rn.From)
			}
		}
	}
	return nil
}

var templateRe = regexp.MustCompile(`\{([^}]+)\}`)

// validateDoc runs kin-openapi's structural validation on the corrected
// document. Example validation is disabled: examples do not influence
// generated behaviour.
func validateDoc(s *loadedSpec) error {
	return s.doc.Validate(context.Background(), openapi3.DisableExamplesValidation())
}

// SpecVersion parses a document and returns info.version.
func SpecVersion(b []byte) (string, error) {
	doc, err := loadSpec(b)
	if err != nil {
		return "", err
	}
	return doc.Info.Version, nil
}
