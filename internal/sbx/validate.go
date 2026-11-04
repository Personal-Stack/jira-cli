package sbx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Validators for single answers, so a wrong value is reported when it is
// given instead of after the whole setup. NewPlan applies the same rules.

// Values matching these are written into YAML and shell snippets unquoted.
var (
	plainPath = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
	plainName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	kebab     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	baseURLRe = regexp.MustCompile(`^https://[A-Za-z0-9._~:/%+=,;-]+$`)
)

// ValidName checks an agent, instance, credential ref or sandbox name.
func ValidName(v string) error {
	if !plainName.MatchString(v) {
		return fmt.Errorf("%q: use letters, digits, '.', '_' and '-'", v)
	}
	return nil
}

// ValidSecretName checks an sbx secret (credential service) name.
func ValidSecretName(v string) error {
	if !kebab.MatchString(v) {
		return fmt.Errorf("%q: use lowercase letters, digits and '-'", v)
	}
	return nil
}

var agentKitNameRe = regexp.MustCompile(`agent kit name "([^"]+)"`)

// AgentNameFromSbx extracts the agent name sbx requires from its error
// `agent "x" does not match agent kit name "y"`.
func AgentNameFromSbx(out []byte) (string, bool) {
	m := agentKitNameRe.FindSubmatch(out)
	if m == nil || ValidAgent(string(m[1])) != nil {
		return "", false
	}
	return string(m[1]), true
}

// reserved are words sbx resolves itself in `sbx run <x>` / `sbx exec <x>`:
// its built-in agents and its subcommands. A sandbox with such a name
// would be ambiguous.
var reserved = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`claude codex copilot cursor devin docker-agent droid gemini kiro opencode shell
		env run exec create ls list rm stop start login logout secret policy template ports diagnose version help`) {
		reserved[w] = true
	}
}

// ValidSandboxName checks a sandbox name: a plain name that is not one of
// sbx's agents or commands.
func ValidSandboxName(v string) error {
	if reserved[strings.ToLower(v)] {
		return fmt.Errorf("%q is reserved by sbx (an agent or command name); choose another sandbox name", v)
	}
	return ValidName(v)
}

// ValidAgent checks the agent: an sbx agent's name, not an image or kit
// reference (those would end up as kits in sbxenv.yaml).
func ValidAgent(v string) error {
	if strings.ContainsAny(v, "/:@") {
		return fmt.Errorf("%q looks like an image or kit reference: give the agent's name (e.g. opencode, or the name sbx shows for your agent)", v)
	}
	return ValidName(v)
}

var kitPathRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// ValidKitPath checks the kit's folder: relative and inside the project
// (the sandbox only sees the project).
func ValidKitPath(v string) error {
	for _, seg := range strings.Split(v, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%q: the kit folder must be inside the project (no '.' or '..')", v)
		}
	}
	if !kitPathRe.MatchString(v) {
		return fmt.Errorf("%q: use a path relative to the project, with letters, digits, '.', '_', '-' and '/'", v)
	}
	return nil
}

// ValidSettingsPath checks the settings folder path.
func ValidSettingsPath(v string) error {
	if !plainPath.MatchString(v) {
		return fmt.Errorf("%q: use an absolute path with letters, digits, '.', '_', '-' and '/'", v)
	}
	return nil
}

// ValidDir checks that path is an existing directory.
func ValidDir(path string) error {
	if st, err := os.Stat(path); err != nil || !st.IsDir() || !filepath.IsAbs(path) {
		return fmt.Errorf("not a directory: %s", path)
	}
	return nil
}

// ParseBaseURL checks a Jira base URL and returns its host and port.
func ParseBaseURL(v string) (host string, port int, err error) {
	if !baseURLRe.MatchString(v) {
		return "", 0, fmt.Errorf("%q: the Jira base URL must be https://... without spaces, quotes or '#'", v)
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", 0, fmt.Errorf("%q is not a URL", v)
	}
	if u.User != nil {
		return "", 0, errors.New("the Jira base URL must not contain credentials")
	}
	host, port = u.Hostname(), 443
	if !hostRe.MatchString(host) {
		return "", 0, fmt.Errorf("cannot use host %q", host)
	}
	if ps := u.Port(); ps != "" {
		if port, err = strconv.Atoi(ps); err != nil || port < 1 || port > 65535 {
			return "", 0, fmt.Errorf("invalid port in %q", v)
		}
	}
	return host, port, nil
}

// Dial is the reachability probe (a variable for tests).
var Dial = func(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// CheckReachable resolves the Jira host and opens a TCP connection to it.
// It does not verify TLS: inside the sandbox the sbx proxy terminates TLS,
// so the host's trust store is what matters there, not this process's.
func CheckReachable(ctx context.Context, baseURL string) error {
	host, port, err := ParseBaseURL(baseURL)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if err := Dial(ctx, addr); err != nil {
		return fmt.Errorf("cannot reach %s: %v (VPN or proxy needed? --skip-checks skips this)", addr, err)
	}
	return nil
}
