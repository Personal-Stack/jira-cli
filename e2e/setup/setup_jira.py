#!/usr/bin/env python3
"""Completes the Jira Data Center setup wizard and creates a PAT for the CLI.

Runs inside the Jira container (python3 stdlib only). Forms are parsed, not
hard-coded: hidden fields such as atl_token and default radio values are
taken from the page; only the fields below are filled. If the wizard shows
a step this script does not know, it stops and prints the form fields.

Environment:
  E2E_LICENSE        Jira Software DC license key (required)
  E2E_ADMIN_PASSWORD admin password (default: generated, written next to PAT)
  E2E_BASE_URL       Jira base URL as seen by clients (https://jira-proxy:8443)
  E2E_SECRETS_DIR    where to write the PAT (/e2e-secrets)
"""
import base64, html.parser, http.cookiejar, json, os, secrets, sys, time
import urllib.error, urllib.parse, urllib.request

BASE = "http://localhost:8080"
LICENSE = os.environ.get("E2E_LICENSE", "").strip()
PUBLIC_BASE = os.environ.get("E2E_BASE_URL", "https://jira-proxy:8443")
SECRETS = os.environ.get("E2E_SECRETS_DIR", "/e2e-secrets")
ADMIN = "admin"
PASSWORD = os.environ.get("E2E_ADMIN_PASSWORD") or secrets.token_urlsafe(18)

FILL = {
    "title": "jira-cli e2e",
    "mode": "private",
    "baseURL": PUBLIC_BASE,
    "setupLicenseKey": LICENSE,
    "username": ADMIN,
    "fullname": "E2E Administrator",
    "email": "admin@example.invalid",
    "password": PASSWORD,
    "confirm": PASSWORD,
    "noemail": "true",
}


def log(msg, **kv):
    print(json.dumps({"component": "e2e-setup", "msg": msg, **kv}), flush=True)


class Rewrite(urllib.request.HTTPRedirectHandler):
    """Jira redirects to its public URL (https://jira-proxy:8443); stay on
    the container-local connector instead."""
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        u = urllib.parse.urlsplit(newurl)
        newurl = BASE + u.path + ("?" + u.query if u.query else "")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


class InternalPolicy(http.cookiejar.DefaultCookiePolicy):
    """Jira runs with ATL_TOMCAT_SECURE=true (it is behind the TLS proxy), so
    its cookies are marked Secure. This script talks to the container-local
    plain-HTTP connector, so Secure cookies must still be sent here."""
    def return_ok_secure(self, cookie, request):
        return True


jar = http.cookiejar.CookieJar(policy=InternalPolicy())
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), Rewrite())


def request(path, data=None, timeout=600):
    body = urllib.parse.urlencode(data).encode() if data is not None else None
    req = urllib.request.Request(BASE + path, data=body)
    with opener.open(req, timeout=timeout) as r:
        return r.geturl(), r.read().decode("utf-8", "replace")


class Forms(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.forms, self.cur, self.errors, self._err = [], None, [], 0

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "form":
            self.cur = {"action": a.get("action", ""), "fields": {}, "radios": {}}
            self.forms.append(self.cur)
        elif tag in ("input", "textarea", "select") and self.cur is not None and a.get("name"):
            name, typ = a["name"], a.get("type", "text")
            if typ == "radio":
                self.cur["radios"].setdefault(name, [])
                self.cur["radios"][name].append(a.get("value"))
                if "checked" in a:
                    self.cur["fields"][name] = a.get("value")
            elif typ == "checkbox":
                if "checked" in a:
                    self.cur["fields"][name] = a.get("value", "true")
            elif typ not in ("submit", "button"):
                self.cur["fields"].setdefault(name, a.get("value", ""))
        if "class" in a and "error" in (a.get("class") or ""):
            self._err += 1

    def handle_endtag(self, tag):
        if tag == "form":
            self.cur = None

    def handle_data(self, data):
        if self._err and data.strip():
            self.errors.append(data.strip())
            self._err -= 1


def setup_form(page):
    p = Forms()
    p.feed(page)
    forms = [f for f in p.forms if "Setup" in f["action"]]
    if not forms:
        return None, p.errors
    # Some steps render several forms with the same action (e.g. the license
    # page has a decorative one); use the one with the most fillable fields.
    def score(f):
        return sum(1 for k in list(f["fields"]) + list(f["radios"]) if k in FILL)
    return max(forms, key=score), p.errors


def status():
    try:
        return json.loads(request("/status", timeout=30)[1]).get("state")
    except Exception as e:  # noqa: BLE001
        return f"unreachable: {e}"


def wizard():
    st = status()
    log("jira status", state=st)
    if st == "RUNNING":
        log("already set up; skipping wizard")
        return
    if not LICENSE:
        sys.exit("E2E_LICENSE is empty: a Jira Software Data Center license is required (see e2e/README.md)")
    url, page = request("/secure/SetupApplicationProperties!default.jspa")
    previous = None
    for _ in range(10):
        form, errors = setup_form(page)
        if form is None:
            log("wizard finished", last_url=urllib.parse.urlsplit(url).path)
            return
        action = form["action"].split("?")[0]
        if action == previous:
            if "License" in action:
                sys.exit("license rejected by Jira: it must be a valid Jira Software Data Center license "
                         "(evaluation or timebomb) for version 10.3 and server ID shown on the setup page")
            sys.exit(f"wizard step {action} was rejected: {errors[:5]}")
        data = dict(form["fields"])
        known = [k for k in data if k in FILL] + [k for k in form["radios"] if k in FILL]
        for k in set(known):
            data[k] = FILL[k]
        unknown_required = [k for k in data if not data[k] and k not in FILL and k != "nextStep"]
        log("wizard step", action=action, filled=sorted(set(known)), empty=sorted(unknown_required))
        if not known:
            sys.exit(f"unknown wizard step {action}; fields={sorted(data)} radios={form['radios']}")
        target = action if action.startswith("/") else "/secure/" + action
        t0 = time.time()
        url, page = request(target, data)
        log("wizard step submitted", action=action, seconds=round(time.time() - t0, 1),
            next=urllib.parse.urlsplit(url).path)
        previous = action
    sys.exit("wizard did not finish within 10 steps")


def wait_running(timeout=600):
    end = time.time() + timeout
    while time.time() < end:
        st = status()
        if st == "RUNNING":
            return
        time.sleep(5)
    sys.exit(f"Jira not RUNNING after setup (state={status()})")


def create_pat():
    # Bootstrap only: Basic auth with the throwaway admin is used to mint
    # the PAT; the CLI itself never uses Basic auth.
    auth = base64.b64encode(f"{ADMIN}:{PASSWORD}".encode()).decode()
    body = json.dumps({"name": f"jira-cli-e2e-{int(time.time())}", "expirationDuration": 1}).encode()
    req = urllib.request.Request(BASE + "/rest/pat/latest/tokens", data=body, method="POST",
                                 headers={"Authorization": "Basic " + auth, "Content-Type": "application/json",
                                          "X-Atlassian-Token": "no-check"})
    for attempt in range(1, 13):
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                tok = json.loads(r.read())["rawToken"]
                break
        except urllib.error.HTTPError as e:
            log("PAT creation failed", status=e.code, attempt=attempt)
            if attempt == 12:
                sys.exit(f"cannot create PAT: HTTP {e.code}")
            time.sleep(10)
    os.makedirs(SECRETS, exist_ok=True)
    path = os.path.join(SECRETS, "jira-e2e-pat")
    with open(path, "w") as f:
        f.write(tok)
    # 0644: read by the distroless CLI container (uid 65532). The CLI warns
    # about it, which the scenario asserts. Throwaway instance only.
    os.chmod(path, 0o644)
    with open(os.path.join(SECRETS, "admin-password"), "w") as f:
        f.write(PASSWORD)
    log("PAT written", path=path, expires_days=1)


if __name__ == "__main__":
    wizard()
    wait_running()
    create_pat()
    log("setup complete", base_url=PUBLIC_BASE, admin=ADMIN)
