package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/personal-stack/jira-cli/internal/auth"
	"github.com/personal-stack/jira-cli/internal/clierr"
)

type staticAuth struct {
	token     string
	refreshed int
}

func (s *staticAuth) Method() string { return "pat" }
func (s *staticAuth) Apply(_ context.Context, r *http.Request) error {
	r.Header.Set("Authorization", "Bearer "+s.token)
	return nil
}
func (s *staticAuth) Status(context.Context) auth.Status { return auth.Status{} }

type refreshingAuth struct{ staticAuth }

func (r *refreshingAuth) Refresh(context.Context) error { r.refreshed++; r.token = "new"; return nil }

func client(t *testing.T, h http.Handler, a auth.Provider, log *slog.Logger) *Client {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL + "/jira")
	return &Client{HTTP: srv.Client(), BaseURL: u, BasePath: "/rest", Auth: a, Log: log, MaxAttempts: 3}
}

func TestURLBuildingKeepsEscapes(t *testing.T) {
	u, _ := url.Parse("https://jira.example/ctx/")
	c := &Client{BaseURL: u, BasePath: "/rest"}
	got := c.URL("/api/2/issue/"+url.PathEscape("A/B"), url.Values{"jql": {"project = X"}})
	if got != "https://jira.example/ctx/rest/api/2/issue/A%2FB?jql=project+%3D+X" {
		t.Fatal(got)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		layer  clierr.Layer
		exit   int
	}{
		{401, `{"errorMessages":["You are not authenticated"]}`, clierr.LayerAuth, 4},
		{403, `{"errorMessages":["Forbidden"]}`, clierr.LayerAuthorization, 5},
		{404, `{"errorMessages":["Issue does not exist"],"errors":{}}`, clierr.LayerAPI, 6},
		{400, `{"errorMessages":[],"errors":{"summary":"required"}}`, clierr.LayerAPI, 7},
	}
	for _, tc := range cases {
		c := client(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		}), &staticAuth{token: "t"}, nil)
		_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/x", Idempotent: true})
		e := clierr.As(err)
		if e.Layer != tc.layer || e.HTTPStatus != tc.status || e.ExitCode() != tc.exit {
			t.Errorf("%d: got %+v exit %d", tc.status, e, e.ExitCode())
		}
	}
}

func TestRefreshOnceOn401(t *testing.T) {
	c := client(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{}`)
	}), &refreshingAuth{staticAuth{token: "old"}}, nil)
	a := c.Auth.(*refreshingAuth)
	if _, err := c.Do(context.Background(), Request{Method: "POST", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if a.refreshed != 1 {
		t.Fatalf("refreshed %d times", a.refreshed)
	}
}

func TestRetriesRespectIdempotency(t *testing.T) {
	var n atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	})
	c := client(t, h, &staticAuth{token: "t"}, nil)
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/x", Idempotent: true}); err != nil || n.Load() != 3 {
		t.Fatalf("GET should retry: %v %d", err, n.Load())
	}
	n.Store(0)
	if _, err := c.Do(context.Background(), Request{Method: "POST", Path: "/x"}); err == nil || n.Load() != 1 {
		t.Fatalf("POST must not be retried on 503: %v %d", err, n.Load())
	}
	// 429 is retried even for POST (request was not processed).
	n.Store(0)
	h429 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		io.WriteString(w, `{}`)
	})
	c = client(t, h429, &staticAuth{token: "t"}, nil)
	if _, err := c.Do(context.Background(), Request{Method: "POST", Path: "/x"}); err != nil || n.Load() != 2 {
		t.Fatalf("429 retry: %v %d", err, n.Load())
	}
}

func TestHTMLLoginPageDetected(t *testing.T) {
	c := client(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html;charset=UTF-8")
		io.WriteString(w, "<html>SSO login</html>")
	}), &staticAuth{token: "t"}, nil)
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/x", Idempotent: true})
	if e := clierr.As(err); e == nil || e.Code != "unexpected_html" {
		t.Fatalf("got %v", err)
	}
}

func TestTokenNeverLogged(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := client(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}), &staticAuth{token: "SUPERSECRET-PAT"}, log)
	c.Do(context.Background(), Request{Method: "GET", Path: "/x", Idempotent: true, Body: []byte(`{"password":"hunter2"}`)})
	if strings.Contains(buf.String(), "SUPERSECRET") || strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("secret in logs: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "status=500") {
		t.Fatalf("expected debug log, got %s", buf.String())
	}
}

func TestFetchAllObjectPages(t *testing.T) {
	total := 7
	c := client(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		max, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
		var issues []map[string]any
		for i := start; i < start+max && i < total; i++ {
			issues = append(issues, map[string]any{"key": "X-" + strconv.Itoa(i)})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"expand": "names", "startAt": start, "maxResults": max, "total": total, "issues": issues})
	}), &staticAuth{token: "t"}, nil)
	res, err := c.FetchAll(context.Background(), Request{Method: "GET", Path: "/api/2/search", Idempotent: true},
		Page{StartParam: "startAt", LimitParam: "maxResults", PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Expand string           `json:"expand"`
		Total  int              `json:"total"`
		Issues []map[string]any `json:"issues"`
	}
	json.Unmarshal(res.Body, &out)
	if res.Pages != 3 || len(out.Issues) != 7 || out.Total != 7 || out.Expand != "names" || !res.Complete {
		t.Fatalf("pages=%d items=%d %+v", res.Pages, len(out.Issues), out)
	}
	if !strings.HasPrefix(string(res.Body), `{"expand"`) {
		t.Fatalf("key order not preserved: %s", res.Body[:40])
	}
}

func TestFetchAllAgileIsLastAndArrays(t *testing.T) {
	c := client(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/users") {
			n := 2
			if start >= 4 {
				n = 1
			}
			arr := make([]int, n)
			json.NewEncoder(w).Encode(arr)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"startAt": start, "maxResults": 2, "isLast": start >= 2, "values": []int{start, start + 1}})
	}), &staticAuth{token: "t"}, nil)
	res, err := c.FetchAll(context.Background(), Request{Method: "GET", Path: "/agile/1.0/board", Idempotent: true}, Page{StartParam: "startAt", LimitParam: "maxResults"})
	if err != nil || res.Pages != 2 || res.Items != 4 || !strings.Contains(string(res.Body), `"isLast":true`) {
		t.Fatalf("agile: %v %+v %s", err, res, res.Body)
	}
	res, err = c.FetchAll(context.Background(), Request{Method: "GET", Path: "/users", Idempotent: true}, Page{StartParam: "startAt", LimitParam: "maxResults", PageSize: 2})
	if err != nil || res.Pages != 3 || res.Items != 5 {
		t.Fatalf("array: %v %+v", err, res)
	}
	res, _ = c.FetchAll(context.Background(), Request{Method: "GET", Path: "/agile/1.0/board", Idempotent: true}, Page{StartParam: "startAt", LimitParam: "maxResults", MaxPages: 1})
	if res.Complete || !strings.Contains(string(res.Body), `"isLast":false`) {
		t.Fatalf("max pages: %+v %s", res, res.Body)
	}
}
