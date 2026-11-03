#!/usr/bin/env python3
"""Minimal fake Jira DC for testing scripts/phase4-readonly.sh (stdlib only).

Mimics the parts that matter: PAT auth (anonymous -> 401), X-AREQUESTID on
every response, REST v2 pagination (startAt/maxResults/total), Agile
pagination (isLast), bare-array user search, kanban boards rejecting sprints.
Every request is appended to --log (method path) so tests can prove that no
write reached the server.

Usage: fake_jira.py --port N --cert CERT --key KEY --token TOKEN --log FILE [--issues N]
"""
import argparse, http.server, json, ssl, urllib.parse

ap = argparse.ArgumentParser()
for a in ("--cert", "--key", "--token", "--log"):
    ap.add_argument(a, required=True)
ap.add_argument("--port", type=int, required=True)
ap.add_argument("--issues", type=int, default=75)
args = ap.parse_args()

PROJECT = "DEMO"
ISSUES = [{"id": str(10000 + i), "key": f"{PROJECT}-{i + 1}", "self": "x",
           "fields": {"summary": f"issue {i + 1}", "status": {"name": "Open", "statusCategory": {"key": "new"}}}}
          for i in range(args.issues)]
BOARDS = [{"id": i + 1, "name": f"board {i + 1}", "type": "scrum" if i == 0 else "kanban",
           "location": {"projectKey": PROJECT}} for i in range(5)]
seq = [0]


class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def send(self, code, body):
        b = json.dumps(body).encode()
        seq[0] += 1
        self.send_response(code)
        self.send_header("Content-Type", "application/json;charset=UTF-8")
        self.send_header("X-AREQUESTID", f"1x{seq[0]}x1")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def handle_any(self):
        u = urllib.parse.urlsplit(self.path)
        q = urllib.parse.parse_qs(u.query)
        with open(args.log, "a") as f:
            f.write(f"{self.command} {u.path}\n")
        if self.headers.get("Authorization") != "Bearer " + args.token:
            return self.send(401, {"errorMessages": ["You are not authenticated."]})
        p, m = u.path, self.command
        start = int(q.get("startAt", ["0"])[0])
        mx = int(q.get("maxResults", ["50"])[0])
        if m == "POST" and p == "/rest/api/2/search":
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
            n = min(int(body.get("maxResults", 50)), 50)
            return self.send(200, {"startAt": 0, "maxResults": n, "total": len(ISSUES), "issues": ISSUES[:n]})
        if m != "GET":
            return self.send(405, {"errorMessages": [f"fake Jira: {m} not supported"]})
        if p == "/rest/api/2/serverInfo":
            return self.send(200, {"version": "10.3.26", "deploymentType": "Server",
                                   "baseUrl": f"https://localhost:{args.port}", "buildNumber": 10030026})
        if p == "/rest/api/2/myself":
            return self.send(200, {"name": "jdoe", "key": "JIRAUSER1", "displayName": "Jane Doe", "active": True})
        if p == "/rest/api/2/search":
            mx = min(mx, 50)  # server-side cap, like Jira
            return self.send(200, {"expand": "names", "startAt": start, "maxResults": mx, "total": len(ISSUES),
                                   "issues": ISSUES[start:start + mx]})
        if p.startswith("/rest/api/2/issue/"):
            key = p.split("/")[5]
            if key not in {i["key"] for i in ISSUES}:
                return self.send(404, {"errorMessages": ["Issue does not exist"]})
            tail = p.split("/")[6] if len(p.split("/")) > 6 else ""
            if tail == "transitions":
                return self.send(200, {"transitions": [{"id": "21", "name": "Start", "to": {"name": "In Progress"}}]})
            if tail == "comment":
                return self.send(200, {"startAt": 0, "maxResults": 5, "total": 0, "comments": []})
            if tail == "editmeta":
                return self.send(200, {"fields": {"summary": {"required": True}}})
            return self.send(200, next(i for i in ISSUES if i["key"] == key))
        if p == f"/rest/api/2/project/{PROJECT}":
            return self.send(200, {"id": "10000", "key": PROJECT, "name": "Demo"})
        if p == "/rest/api/2/project":
            return self.send(200, [{"id": "10000", "key": PROJECT, "name": "Demo"}])
        if p == "/rest/api/2/field":
            return self.send(200, [{"id": "summary", "name": "Summary"}, {"id": "status", "name": "Status"}])
        if p == "/rest/agile/1.0/board":
            vals = BOARDS[start:start + mx]
            return self.send(200, {"maxResults": mx, "startAt": start, "isLast": start + mx >= len(BOARDS), "values": vals})
        if p.startswith("/rest/agile/1.0/board/") and p.endswith("/sprint"):
            bid = int(p.split("/")[5])
            if bid != 1:
                return self.send(400, {"errorMessages": ["The board does not support sprints"]})
            return self.send(200, {"maxResults": 5, "startAt": 0, "isLast": True, "values": [{"id": 7, "name": "Sprint 1"}]})
        if p == "/rest/api/2/user/search":
            return self.send(200, [{"name": "jdoe", "displayName": "Jane Doe"}][:mx])
        return self.send(404, {"errorMessages": [f"fake Jira: {p} not implemented"]})

    do_GET = do_POST = do_PUT = do_DELETE = handle_any


ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(args.cert, args.key)
srv = http.server.ThreadingHTTPServer(("127.0.0.1", args.port), H)
srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
srv.serve_forever()
