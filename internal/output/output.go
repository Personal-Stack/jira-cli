// Package output renders responses as JSON (default), YAML or a table.
// JSON and YAML output are lossless: every field and the original key order
// are preserved. Tables are a lossy, human-oriented view.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Format is an output format.
type Format string

const (
	JSON  Format = "json"
	YAML  Format = "yaml"
	Table Format = "table"
)

// Parse validates a format name.
func Parse(s string) (Format, error) {
	switch Format(s) {
	case JSON, YAML, Table:
		return Format(s), nil
	}
	return "", fmt.Errorf("unsupported output format %q (json, yaml, table)", s)
}

// WriteRaw renders a JSON document given as bytes.
func WriteRaw(w io.Writer, body []byte, f Format) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	switch f {
	case YAML:
		var n yaml.Node
		// JSON is valid YAML 1.2; decoding into a Node preserves key order.
		if err := yaml.Unmarshal(body, &n); err != nil {
			return fmt.Errorf("render yaml: %w", err)
		}
		normalizeStyle(&n)
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(&n); err != nil {
			return err
		}
		return enc.Close()
	case Table:
		return table(w, body)
	default:
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err != nil {
			return fmt.Errorf("render json: %w", err)
		}
		buf.WriteByte('\n')
		_, err := w.Write(buf.Bytes())
		return err
	}
}

// Write renders any Go value via its JSON encoding (struct field order).
func Write(w io.Writer, v any, f Format) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return WriteRaw(w, buf.Bytes(), f)
}

// normalizeStyle switches JSON flow style to block style for readable YAML.
func normalizeStyle(n *yaml.Node) {
	n.Style &^= yaml.FlowStyle
	if n.Kind == yaml.ScalarNode && n.Style&(yaml.DoubleQuotedStyle) != 0 {
		n.Style &^= yaml.DoubleQuotedStyle
	}
	for _, c := range n.Content {
		normalizeStyle(c)
	}
}

var preferred = []string{"key", "id", "name", "displayName", "summary", "status", "type", "issuetype", "assignee", "state", "self"}

const maxCols = 8

// table renders the item array of a response (or the object itself as
// key/value rows).
func table(w io.Writer, body []byte) error {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return err
	}
	rows := rowsOf(v)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if rows == nil {
		obj, ok := v.(map[string]any)
		if !ok {
			fmt.Fprintln(tw, cell(v))
			return tw.Flush()
		}
		keys := sortedKeys(obj)
		fmt.Fprintln(tw, "FIELD\tVALUE")
		for _, k := range keys {
			fmt.Fprintf(tw, "%s\t%s\n", k, cell(obj[k]))
		}
		return tw.Flush()
	}
	flat := make([]map[string]any, len(rows))
	for i, r := range rows {
		flat[i] = flatten(r)
	}
	cols := columns(flat)
	fmt.Fprintln(tw, strings.ToUpper(strings.Join(cols, "\t")))
	for _, r := range flat {
		vals := make([]string, len(cols))
		for i, c := range cols {
			vals[i] = cell(r[c])
		}
		fmt.Fprintln(tw, strings.Join(vals, "\t"))
	}
	return tw.Flush()
}

func rowsOf(v any) []map[string]any {
	toRows := func(a []any) []map[string]any {
		out := make([]map[string]any, 0, len(a))
		for _, x := range a {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			} else {
				out = append(out, map[string]any{"value": x})
			}
		}
		return out
	}
	switch t := v.(type) {
	case []any:
		return toRows(t)
	case map[string]any:
		for _, k := range []string{"issues", "values", "worklogs", "comments", "users", "projects"} {
			if a, ok := t[k].([]any); ok {
				return toRows(a)
			}
		}
		for _, k := range sortedKeys(t) {
			if a, ok := t[k].([]any); ok && len(a) > 0 {
				if _, isObj := a[0].(map[string]any); isObj {
					return toRows(a)
				}
			}
		}
	}
	return nil
}

// flatten lifts one level of nested objects (e.g. Jira's "fields") into
// dotted keys and reduces named objects to their display value.
func flatten(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok && k == "fields" {
			for sk, sv := range sub {
				out[sk] = sv
			}
			continue
		}
		out[k] = v
	}
	return out
}

func columns(rows []map[string]any) []string {
	seen := map[string]bool{}
	var scalar []string
	for _, r := range rows {
		for _, k := range sortedKeys(r) {
			if !seen[k] && displayable(r[k]) {
				seen[k] = true
				scalar = append(scalar, k)
			}
		}
	}
	var cols []string
	for _, p := range preferred {
		if seen[p] {
			cols = append(cols, p)
		}
	}
	for _, k := range scalar {
		if len(cols) >= maxCols {
			break
		}
		if !contains(cols, k) && k != "expand" {
			cols = append(cols, k)
		}
	}
	if len(cols) > maxCols {
		cols = cols[:maxCols]
	}
	return cols
}

func displayable(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		return named(t) != ""
	case []any:
		return false
	}
	return true
}

func named(m map[string]any) string {
	for _, k := range []string{"name", "displayName", "key", "value"} {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}

func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		s := strings.ReplaceAll(strings.ReplaceAll(t, "\n", " "), "\t", " ")
		if len([]rune(s)) > 60 {
			s = string([]rune(s)[:57]) + "..."
		}
		return s
	case map[string]any:
		if n := named(t); n != "" {
			return n
		}
		return "{…}"
	case []any:
		return fmt.Sprintf("[%d]", len(t))
	}
	return fmt.Sprint(v)
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
