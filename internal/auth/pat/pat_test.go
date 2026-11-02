package pat

import (
	"context"
	"net/http"
	"testing"

	"github.com/personal-stack/jira-cli/internal/credentials"
)

type fakeStore map[string]string

func (f fakeStore) Get(ref string) (*credentials.Found, error) {
	v, ok := f[ref]
	if !ok {
		return nil, credentials.ErrNotFound
	}
	return &credentials.Found{Secret: credentials.NewSecret([]byte(v)), Source: "test"}, nil
}

func TestApply(t *testing.T) {
	p := New("prod", "me@example.com", fakeStore{"prod": "abc123"})
	req, _ := http.NewRequest("GET", "https://x", nil)
	if err := p.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "Bearer abc123" {
		t.Fatal("wrong header")
	}
	if st := p.Status(context.Background()); !st.Present || st.Source != "test" {
		t.Fatalf("%+v", st)
	}
}

func TestMissingAndInvalid(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://x", nil)
	if err := New("nope", "", fakeStore{}).Apply(context.Background(), req); err == nil {
		t.Fatal("expected error")
	}
	if err := New("x", "", fakeStore{"x": "abc\r\nX-Evil: 1"}).Apply(context.Background(), req); err == nil {
		t.Fatal("header injection accepted")
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("header set on failure")
	}
}
