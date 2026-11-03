package sbx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
)

// Validators for single answers, so a wrong value is reported when it is
// given instead of after the whole setup. NewPlan applies the same rules.

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

// ValidRef checks an image or kit reference.
func ValidRef(v string) error {
	if !imageRe.MatchString(v) {
		return fmt.Errorf("%q is not an image or kit reference", v)
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

// ValidFile checks that path is a readable regular file.
func ValidFile(path string) error {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return fmt.Errorf("file not found: %s", path)
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

var agentKitNameRe = regexp.MustCompile(`agent kit name "([^"]+)"`)

// AgentNameFromSbx extracts the agent kit's real name from sbx's error
// `agent "x" does not match agent kit name "y"`.
func AgentNameFromSbx(out []byte) (string, bool) {
	m := agentKitNameRe.FindSubmatch(out)
	if m == nil || ValidName(string(m[1])) != nil {
		return "", false
	}
	return string(m[1]), true
}
