package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/personal-stack/jira-cli/internal/clierr"
)

// Page describes offset pagination parameters of an operation.
type Page struct {
	StartParam string
	LimitParam string
	PageSize   int // 0 = server default
	MaxPages   int // safety bound
}

// PageResult is the merged result of FetchAll.
type PageResult struct {
	Body     []byte
	Pages    int
	Items    int
	Complete bool // false if MaxPages stopped the iteration
	Last     *Response
}

// itemKeys are the array fields Jira uses for paged collections, in order of
// preference. If none is present the first array-valued field is used.
var itemKeys = []string{"issues", "values", "worklogs", "comments", "users", "projects", "dashboards", "filters", "sprints", "versions"}

// FetchAll follows Jira's offset pagination (startAt/maxResults) and merges
// pages. Jira uses two shapes:
//   - an object with an item array plus isLast (Agile API) or total (REST v2);
//   - a bare array (e.g. user search), finished when a short page arrives.
//
// The merged object keeps every field of the first page (in order), replaces
// the item array with all items and sets startAt=0, maxResults=<items>,
// isLast=true where those fields exist. No response field is dropped.
func (c *Client) FetchAll(ctx context.Context, r Request, p Page) (*PageResult, error) {
	if p.MaxPages <= 0 {
		p.MaxPages = 100
	}
	q := cloneValues(r.Query)
	start := 0
	if s := q.Get(p.StartParam); s != "" {
		start, _ = strconv.Atoi(s)
	}
	limit := p.PageSize
	if limit == 0 {
		if s := q.Get(p.LimitParam); s != "" {
			limit, _ = strconv.Atoi(s)
		}
	}
	res := &PageResult{}
	var first *orderedObject
	var itemKey string
	var all []json.RawMessage
	isArray := false

	for res.Pages < p.MaxPages {
		q.Set(p.StartParam, strconv.Itoa(start))
		if limit > 0 {
			q.Set(p.LimitParam, strconv.Itoa(limit))
		}
		r.Query = q
		resp, err := c.Do(ctx, r)
		if err != nil {
			return nil, err
		}
		res.Pages++
		res.Last = resp
		trimmed := bytes.TrimSpace(resp.Body)
		var items []json.RawMessage
		done := false

		if len(trimmed) > 0 && trimmed[0] == '[' {
			isArray = true
			if err := json.Unmarshal(trimmed, &items); err != nil {
				return nil, clierr.Wrap(clierr.LayerResponse, "invalid_json", "cannot parse page", err)
			}
			done = len(items) == 0 || (limit > 0 && len(items) < limit)
		} else {
			obj, err := parseObject(trimmed)
			if err != nil {
				return nil, clierr.Wrap(clierr.LayerResponse, "invalid_json", "cannot parse page", err)
			}
			if first == nil {
				first = obj
				itemKey = obj.itemKey()
				if itemKey == "" {
					// Not a paged collection: return as-is.
					res.Body, res.Complete = resp.Body, true
					return res, nil
				}
			}
			if v, ok := obj.get(itemKey); ok {
				if err := json.Unmarshal(v, &items); err != nil {
					return nil, clierr.Wrap(clierr.LayerResponse, "invalid_json", "item array is not an array", err)
				}
			}
			var isLast bool
			if v, ok := obj.get("isLast"); ok && json.Unmarshal(v, &isLast) == nil && isLast {
				done = true
			}
			var total, gotLimit int
			if v, ok := obj.get("maxResults"); ok && json.Unmarshal(v, &gotLimit) == nil && limit == 0 {
				limit = gotLimit
			}
			if v, ok := obj.get("total"); ok && json.Unmarshal(v, &total) == nil && start+len(items) >= total {
				done = true
			}
			if len(items) == 0 {
				done = true
			}
			if _, hasLast := obj.get("isLast"); !hasLast {
				if _, hasTotal := obj.get("total"); !hasTotal && limit > 0 && len(items) < limit {
					done = true
				}
			}
			if t, ok := obj.get("total"); ok {
				first.set("total", t)
			}
		}
		all = append(all, items...)
		start += len(items)
		if done {
			res.Complete = true
			break
		}
	}
	res.Items = len(all)
	if all == nil {
		all = []json.RawMessage{}
	}
	if isArray {
		b, err := json.Marshal(all)
		res.Body = b
		return res, err
	}
	arr, _ := json.Marshal(all)
	first.set(itemKey, arr)
	if _, ok := first.get("startAt"); ok {
		first.set("startAt", json.RawMessage("0"))
	}
	if _, ok := first.get("maxResults"); ok {
		first.set("maxResults", json.RawMessage(strconv.Itoa(len(all))))
	}
	if _, ok := first.get("isLast"); ok {
		first.set("isLast", json.RawMessage(strconv.FormatBool(res.Complete)))
	}
	res.Body = first.marshal()
	return res, nil
}

// orderedObject is a JSON object that preserves key order.
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(b []byte) (*orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}
	o := &orderedObject{vals: map[string]json.RawMessage{}}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := o.vals[k]; !dup {
			o.keys = append(o.keys, k)
		}
		o.vals[k] = v
	}
	return o, nil
}

func (o *orderedObject) get(k string) (json.RawMessage, bool) { v, ok := o.vals[k]; return v, ok }

func (o *orderedObject) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *orderedObject) itemKey() string {
	for _, k := range itemKeys {
		if v, ok := o.vals[k]; ok && isArrayJSON(v) {
			return k
		}
	}
	for _, k := range o.keys {
		if isArrayJSON(o.vals[k]) {
			return k
		}
	}
	return ""
}

func isArrayJSON(v json.RawMessage) bool {
	t := bytes.TrimSpace(v)
	return len(t) > 0 && t[0] == '['
}

func (o *orderedObject) marshal() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}
