package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/generated"
	"github.com/personal-stack/jira-cli/internal/operation"
)

type mcpReply struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		ProtocolVersion string           `json:"protocolVersion"`
		Instructions    string           `json:"instructions"`
		Tools           []map[string]any `json:"tools"`
		Content         []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *rpcError `json:"error"`
}

func (r mcpReply) text() string {
	if len(r.Result.Content) == 0 {
		return ""
	}
	return r.Result.Content[0].Text
}

// mcp runs `jiracli mcp flags...` with msgs as the client's messages and
// returns the replies by id, and the requests Jira received.
func (e *env) mcp(t *testing.T, flags []string, msgs ...string) map[string]mcpReply {
	t.Helper()
	before := len(e.jira.requests)
	code, out, errOut := e.run(strings.Join(msgs, "\n")+"\n", append([]string{"mcp"}, flags...)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	replies := map[string]mcpReply{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var r mcpReply
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("stdout is not JSON-RPC: %q", line)
		}
		replies[string(r.ID)] = r
	}
	e.jira.mu.Lock()
	e.sent = e.jira.requests[before:]
	e.jira.mu.Unlock()
	return replies
}

func call(id int, tool string, args any) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	return string(b)
}

const mcpInit = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`

func TestMCPHandshakeAndTools(t *testing.T) {
	e := setup(t, "")
	r := e.mcp(t, nil, mcpInit, `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, `{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`, `not json`)
	if got := r["0"].Result.ProtocolVersion; got != "2025-06-18" {
		t.Fatalf("negotiated %q", got)
	}
	if !strings.Contains(r["0"].Result.Instructions, "read-only") {
		t.Fatalf("instructions do not state the mode: %s", r["0"].Result.Instructions)
	}
	var names []string
	for _, tool := range r["1"].Result.Tools {
		names = append(names, tool["name"].(string))
		if tool["name"] == "jira_run" && tool["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Fatal("jira_run not marked read-only in read-only mode")
		}
	}
	if strings.Join(names, ",") != "jira_commands,jira_describe,jira_run" {
		t.Fatalf("tools %v", names)
	}
	if r["2"].Error != nil || r["3"].Error == nil || r["3"].Error.Code != rpcMethodNotFound {
		t.Fatalf("ping %+v, resources/list %+v", r["2"].Error, r["3"].Error)
	}
	if r["null"].Error == nil || r["null"].Error.Code != rpcParseError {
		t.Fatalf("no parse error for bad JSON: %+v", r)
	}
	if len(r) != 5 { // the notification gets no reply
		t.Fatalf("%d replies", len(r))
	}
	if len(e.sent) != 0 {
		t.Fatalf("Jira was called: %v", e.sent)
	}
}

func TestMCPReadOnlyByDefault(t *testing.T) {
	e := setup(t, "")
	r := e.mcp(t, nil,
		call(1, "jira_run", map[string]any{"command": "issue get", "params": map[string]any{"issueIdOrKey": "ABC-1", "fields": "summary"}}),
		call(2, "jira_run", map[string]any{"command": "issue delete", "params": map[string]any{"issueIdOrKey": "ABC-1"}, "confirm": "issue delete"}),
		call(3, "jira_run", map[string]any{"command": "issue create", "body": map[string]any{"fields": map[string]any{"summary": "x"}}}),
		call(4, "jira_run", map[string]any{"command": "issue delete", "params": map[string]any{"issueIdOrKey": "ABC-1"}, "dry_run": true}),
		call(5, "jira_commands", map[string]any{"group": "issue"}),
	)
	if r["1"].Result.IsError || !strings.Contains(r["1"].text(), `"key":"ABC-1"`) {
		t.Fatalf("read failed: %s", r["1"].text())
	}
	for _, id := range []string{"2", "3"} {
		if !r[id].Result.IsError || !strings.Contains(r[id].text(), "refused_by_mcp_mode") {
			t.Fatalf("call %s not refused: %s", id, r[id].text())
		}
	}
	if r["4"].Result.IsError || !strings.Contains(r["4"].text(), `"dry_run":true`) || !strings.Contains(r["4"].text(), "REDACTED") {
		t.Fatalf("dry run: %s", r["4"].text())
	}
	list := r["5"].text()
	if !strings.Contains(list, `"issue get"`) || strings.Contains(list, `"issue delete"`) || !strings.Contains(list, "refused_in_this_mode_not_listed") {
		t.Fatalf("list shows refused commands: %s", list)
	}
	if len(e.sent) != 1 || !strings.HasPrefix(e.sent[0], "GET /jira/rest/api/2/issue/ABC-1?fields=summary") {
		t.Fatalf("Jira received %v, want only the read", e.sent)
	}
}

func TestMCPWritesAndConfirmation(t *testing.T) {
	e := setup(t, "")
	body := map[string]any{"fields": map[string]any{"summary": "x"}}
	r := e.mcp(t, []string{"--allow-writes"},
		call(1, "jira_run", map[string]any{"command": "issue create", "body": body}),
		call(2, "jira_run", map[string]any{"command": "issue delete", "params": map[string]any{"issueIdOrKey": "ABC-1"}, "confirm": "issue delete"}),
	)
	if r["1"].Result.IsError || !strings.Contains(r["1"].text(), `"ok":true`) {
		t.Fatalf("write: %s", r["1"].text())
	}
	if !strings.Contains(r["2"].text(), "refused_by_mcp_mode") {
		t.Fatalf("destructive allowed with --allow-writes only: %s", r["2"].text())
	}

	r = e.mcp(t, []string{"--allow-writes", "--allow-destructive"},
		call(1, "jira_run", map[string]any{"command": "issue delete", "params": map[string]any{"issueIdOrKey": "ABC-1"}}),
		call(2, "jira_run", map[string]any{"command": "issue delete", "params": map[string]any{"issueIdOrKey": "ABC-1"}, "confirm": "issue delete"}),
	)
	if !strings.Contains(r["1"].text(), "confirmation_required") {
		t.Fatalf("destructive ran without confirm: %s", r["1"].text())
	}
	if r["2"].Result.IsError || len(e.sent) != 1 || !strings.HasPrefix(e.sent[0], "DELETE /jira/rest/api/2/issue/ABC-1") {
		t.Fatalf("confirmed delete: %s, sent %v", r["2"].text(), e.sent)
	}
}

func TestMCPInstanceReadOnlyWins(t *testing.T) {
	e := setup(t, "    read_only: true\n")
	r := e.mcp(t, []string{"--allow-writes", "--allow-destructive"},
		call(1, "jira_run", map[string]any{"command": "issue create", "body": map[string]any{"fields": map[string]any{}}}))
	if !strings.Contains(r["1"].text(), "refused_by_mcp_mode") || len(e.sent) != 0 {
		t.Fatalf("read_only instance allowed a write: %s, sent %v", r["1"].text(), e.sent)
	}
	e = setup(t, "")
	e.vars["JIRA_READ_ONLY"] = "1"
	r = e.mcp(t, []string{"--allow-writes"},
		call(1, "jira_run", map[string]any{"command": "issue create", "body": map[string]any{"fields": map[string]any{}}}))
	if !strings.Contains(r["1"].text(), "refused_by_mcp_mode") || len(e.sent) != 0 {
		t.Fatalf("JIRA_READ_ONLY=1 allowed a write: %s", r["1"].text())
	}
}

func TestMCPArgumentChecks(t *testing.T) {
	e := setup(t, "")
	run := func(args map[string]any) map[string]any { args["command"] = "issue get"; return args }
	r := e.mcp(t, []string{"--allow-writes", "--allow-destructive"},
		call(1, "jira_run", run(map[string]any{})),
		call(2, "jira_run", run(map[string]any{"params": map[string]any{"issueIdOrKey": ".."}})),
		call(3, "jira_run", run(map[string]any{"params": map[string]any{"issueIdOrKey": "ABC-1", "nope": "1"}})),
		call(4, "jira_run", run(map[string]any{"params": map[string]any{"issueIdOrKey": "ABC-1"}, "file": "/etc/passwd"})),
		call(5, "jira_run", map[string]any{"command": "issue add-attachment", "params": map[string]any{"issueIdOrKey": "ABC-1"}, "body": "x"}),
		call(6, "jira_run", map[string]any{"command": "issue search", "params": map[string]any{"maxResults": "many"}}),
		call(7, "jira_run", map[string]any{"command": "issue create", "body": `{"fields":{}}`, "dry_run": true}),
		call(8, "jira_run", map[string]any{"command": "issue create", "body": []any{1}}),
		call(9, "jira_run", map[string]any{"command": "no such"}),
		call(10, "jira_run", run(map[string]any{"params": map[string]any{"issueIdOrKey": "../../admin"}, "dry_run": true})),
	)
	for id, want := range map[string]string{
		"1": "missing required parameter", "2": "not a valid path value", "3": "unknown parameter",
		"4": "unknown field", "5": "file uploads are not available", "6": "not an integer",
		"8": "must be a JSON object", "9": "unknown command",
	} {
		if !r[id].Result.IsError || !strings.Contains(r[id].text(), want) {
			t.Errorf("call %s: want error %q, got %s", id, want, r[id].text())
		}
	}
	if r["7"].Result.IsError || !strings.Contains(r["7"].text(), `"body":{"fields":{}}`) {
		t.Errorf("a JSON body passed as text: %s", r["7"].text())
	}
	if !strings.Contains(r["10"].text(), "issue/..%2F..%2Fadmin") {
		t.Errorf("path value not escaped: %s", r["10"].text())
	}
	if len(e.sent) != 0 {
		t.Fatalf("Jira was called: %v", e.sent)
	}
}

func TestMCPPaginationAndOutputLimit(t *testing.T) {
	e := setup(t, "")
	r := e.mcp(t, []string{"--max-pages", "2", "--max-output", "1024"},
		call(1, "jira_run", map[string]any{"command": "issue search", "params": map[string]any{"jql": "project = ABC"}, "all": true}),
		call(2, "jira_describe", map[string]any{"command": "issue search"}),
	)
	if !strings.Contains(r["1"].text(), "[incomplete: stopped after 2 pages") || len(e.sent) != 2 {
		t.Fatalf("pages not capped: %s, sent %v", r["1"].text(), e.sent)
	}
	if d := r["2"].text(); !strings.Contains(d, "[truncated by jiracli mcp") || len(d) > 1024+300 {
		t.Fatalf("output not limited (%d bytes)", len(d))
	}
}

func TestMCPFlagChecks(t *testing.T) {
	e := setup(t, "")
	if code, _, errOut := e.run("", "mcp", "--allow-destructive"); code != 2 || !strings.Contains(errOut, "needs --allow-writes") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

// Every operation must be reachable: path parameters map to params, and
// no parameter name is used twice in one operation (params are keyed by it).
func TestMCPCoversEveryOperation(t *testing.T) {
	n := 0
	for _, p := range generated.Profiles {
		for _, op := range p.Operations {
			seen := map[string]bool{}
			for _, prm := range op.Params {
				if seen[prm.Name] {
					t.Errorf("%s %s: parameter %q twice", op.Group, op.Name, prm.Name)
				}
				seen[prm.Name] = true
			}
			if (mcpPolicy{}).allows(op.Risk) != (op.Risk == operation.RiskRead) {
				t.Errorf("%s %s: read-only mode allows %s", op.Group, op.Name, op.Risk)
			}
			n++
		}
	}
	if n < 400 {
		t.Fatalf("only %d operations", n)
	}
}
