// Package api is the single HTTP client used by every Jira operation. It
// applies authentication, retries, the response size limit and error
// classification. Generated operations never talk to net/http directly.
package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/personal-stack/jira-cli/internal/auth"
	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/transport"
)

// MaxResponseSize bounds buffered responses.
const MaxResponseSize = 256 << 20

// Client executes requests against one Jira instance.
type Client struct {
	HTTP        *http.Client
	BaseURL     *url.URL // instance base_url (may include a context path, e.g. /jira)
	BasePath    string   // profile base path, e.g. /rest
	Auth        auth.Provider
	Log         *slog.Logger
	MaxAttempts int
	UserAgent   string
}

// Request is a fully resolved API request.
type Request struct {
	Method      string
	Path        string // already expanded, relative to BasePath, e.g. /api/2/issue/ABC-1
	Query       url.Values
	Header      http.Header
	Body        []byte
	ContentType string
	Idempotent  bool
	// AcceptHTML disables the "unexpected HTML" guard for operations that
	// legitimately return HTML.
	AcceptHTML bool
}

// Response is a buffered response.
type Response struct {
	Status      int
	Header      http.Header
	Body        []byte
	ContentType string
	URL         string
}

// IsJSON reports whether the body is JSON.
func (r *Response) IsJSON() bool {
	mt, _, _ := mime.ParseMediaType(r.ContentType)
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// URL builds the absolute URL for a request.
func (c *Client) URL(path string, q url.Values) string {
	u := *c.BaseURL
	// path is already escaped (path parameters are escaped individually), so
	// it is used as RawPath and the decoded form as Path.
	raw := strings.TrimRight(c.BaseURL.EscapedPath(), "/") + c.BasePath + path
	dec, err := url.PathUnescape(raw)
	if err != nil {
		dec = raw
	}
	u.Path, u.RawPath = dec, raw
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// Do sends the request with retries and auth refresh.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	full := c.URL(r.Path, r.Query)
	policy := transport.RetryPolicy{MaxAttempts: c.MaxAttempts, Idempotent: r.Idempotent}
	refreshed := false
	for attempt := 1; ; attempt++ {
		req, err := c.build(ctx, r, full)
		if err != nil {
			return nil, err
		}
		start := time.Now()
		resp, err := c.HTTP.Do(req)
		if err != nil {
			c.log().Debug("request failed", "method", r.Method, "path", r.Path, "attempt", attempt, "error", err.Error())
			if retry, wait := policy.Next(attempt, nil, err); retry {
				if serr := transport.Sleep(ctx, wait); serr != nil {
					return nil, clierr.Classify(serr)
				}
				continue
			}
			return nil, clierr.Classify(err)
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, MaxResponseSize+1))
		resp.Body.Close()
		c.log().Debug("response", "method", r.Method, "path", r.Path, "status", resp.StatusCode,
			"attempt", attempt, "duration_ms", time.Since(start).Milliseconds(), "bytes", len(body))
		if rerr != nil {
			return nil, clierr.Wrap(clierr.LayerTransport, "read_failed", "reading response body failed", rerr)
		}
		if len(body) > MaxResponseSize {
			return nil, clierr.New(clierr.LayerResponse, "response_too_large", fmt.Sprintf("response exceeds %d bytes", MaxResponseSize))
		}
		out := &Response{Status: resp.StatusCode, Header: resp.Header, Body: body, ContentType: resp.Header.Get("Content-Type"), URL: full}

		// One forced token refresh on 401 for refreshable providers. A 401
		// means the request was not processed, so replaying is safe.
		if resp.StatusCode == http.StatusUnauthorized && !refreshed {
			if rf, ok := c.Auth.(auth.Refresher); ok {
				refreshed = true
				if err := rf.Refresh(ctx); err == nil {
					continue
				}
			}
		}
		if retry, wait := policy.Next(attempt, resp, nil); retry {
			c.log().Debug("retrying", "status", resp.StatusCode, "wait", wait.String())
			if serr := transport.Sleep(ctx, wait); serr != nil {
				return nil, clierr.Classify(serr)
			}
			continue
		}
		if resp.StatusCode >= 400 {
			return out, APIError(out)
		}
		if !r.AcceptHTML && isHTML(out.ContentType) {
			return out, clierr.New(clierr.LayerAuth, "unexpected_html",
				"received an HTML page instead of an API response").
				WithHint("a reverse proxy or SSO gateway probably intercepted the request (login page); check base_url and authentication")
		}
		return out, nil
	}
}

func (c *Client) build(ctx context.Context, r Request, full string) (*http.Request, error) {
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, full, body)
	if err != nil {
		return nil, clierr.Wrap(clierr.LayerUsage, "invalid_request", "cannot build request", err)
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Auth != nil {
		if err := c.Auth.Apply(ctx, req); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func (c *Client) log() *slog.Logger {
	if c.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return c.Log
}

func isHTML(ct string) bool {
	mt, _, _ := mime.ParseMediaType(ct)
	return mt == "text/html"
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}
