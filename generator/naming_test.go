package generator

import (
	"os"
	"reflect"
	"testing"
)

func TestSplitWords(t *testing.T) {
	cases := map[string][]string{
		"getIssueLinkTypes": {"get", "issue", "link", "types"},
		"getURLForX":        {"get", "url", "for", "x"},
		"email-templates":   {"email", "templates"},
		"universal_avatar":  {"universal", "avatar"},
		"get_4":             {"get", "4"},
		"permission-id":     {"permission", "id"},
	}
	for in, want := range cases {
		if got := splitWords(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitWords(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCommandName(t *testing.T) {
	cases := []struct{ op, tag, want string }{
		{"getIssue", "issue", "get"},
		{"createIssues", "issue", "create"},
		{"getIssueLinkTypes", "issueLinkType", "get"},
		{"getAllBoards", "board", "get-all"},
		{"addWatcher_1", "issue", "add-watcher"},
		{"getIssue", "issueLinkType", "get-issue"},
		{"issue", "issue", "issue"},
		{"getEmailTemplates", "email-templates", "get"},
	}
	for _, c := range cases {
		if got := commandName(c.op, c.tag); got != c.want {
			t.Errorf("commandName(%q,%q) = %q, want %q", c.op, c.tag, got, c.want)
		}
	}
}

func TestPackageName(t *testing.T) {
	if got := PackageName("datacenter-10.3"); got != "datacenter103" {
		t.Fatal(got)
	}
}

func TestExampleCustomSpecLoads(t *testing.T) {
	b, err := os.ReadFile("../examples/custom-spec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := SpecVersion(b); err != nil || v != "2.1" {
		t.Fatalf("%v %v", v, err)
	}
}
