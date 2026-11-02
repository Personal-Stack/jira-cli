package output

import (
	"bytes"
	"strings"
	"testing"
)

const doc = `{"zeta":1,"alpha":{"nested":[1,2]},"big":12345678901234567890,"issues":[{"key":"A-1","fields":{"summary":"Hello","status":{"name":"Open"}}}]}`

func TestJSONPreservesOrderAndPrecision(t *testing.T) {
	var b bytes.Buffer
	if err := WriteRaw(&b, []byte(doc), JSON); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if strings.Index(s, "zeta") > strings.Index(s, "alpha") || !strings.Contains(s, "12345678901234567890") {
		t.Fatalf("order/precision lost:\n%s", s)
	}
}

func TestYAMLPreservesOrderAndPrecision(t *testing.T) {
	var b bytes.Buffer
	if err := WriteRaw(&b, []byte(doc), YAML); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !strings.HasPrefix(s, "zeta: 1\nalpha:") || !strings.Contains(s, "12345678901234567890") || !strings.Contains(s, "summary: Hello") {
		t.Fatalf("unexpected yaml:\n%s", s)
	}
}

func TestTable(t *testing.T) {
	var b bytes.Buffer
	if err := WriteRaw(&b, []byte(doc), Table); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !strings.Contains(s, "KEY") || !strings.Contains(s, "A-1") || !strings.Contains(s, "Open") || !strings.Contains(s, "Hello") {
		t.Fatalf("table:\n%s", s)
	}
}

func TestParse(t *testing.T) {
	if _, err := Parse("xml"); err == nil {
		t.Fatal("expected error")
	}
}
