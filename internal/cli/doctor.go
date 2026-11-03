package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/api"
	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/operation"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/transport"
)

type check struct {
	Name   string         `json:"name"`
	Status string         `json:"status"` // ok, warn, fail, skip
	Layer  clierr.Layer   `json:"layer,omitempty"`
	Detail string         `json:"detail,omitempty"`
	Hint   string         `json:"hint,omitempty"`
	Data   map[string]any `json:"data,omitempty"`
}

func (a *App) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration, DNS, TCP, TLS, proxy, Jira reachability and authentication",
		Long: `Runs layered checks against the selected instance and reports the first
failing layer: configuration, DNS, TCP, TLS, HTTP (unauthenticated
/rest/api/2/serverInfo), credentials and authentication (/rest/api/2/myself).
Only read requests are sent. Secrets are never printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			checks, failed := a.runDoctor(ctxOrBackground(cmd))
			res := map[string]any{"ok": failed == nil, "checks": checks}
			if err := output.Write(a.Out, res, f); err != nil {
				return err
			}
			if failed != nil {
				return clierr.New(failed.Layer, "doctor_failed", "check "+failed.Name+" failed: "+failed.Detail).WithHint(failed.Hint)
			}
			return nil
		},
	}
}

func (a *App) runDoctor(ctx context.Context) ([]check, *check) {
	var checks []check
	var failed *check
	add := func(c check) bool {
		checks = append(checks, c)
		if c.Status == "fail" && failed == nil {
			cc := c
			failed = &cc
		}
		return c.Status != "fail"
	}
	fail := func(name string, err error) bool {
		e := clierr.As(err)
		return add(check{Name: name, Status: "fail", Layer: e.Layer, Detail: e.Error(), Hint: e.Hint})
	}

	// 1. configuration
	in, src, err := a.Instance()
	if err != nil {
		fail("config", err)
		return checks, failed
	}
	p, err := a.ProfileFor(in)
	if err != nil {
		fail("config", err)
		return checks, failed
	}
	c := check{Name: "config", Status: "ok", Data: map[string]any{"file": a.ConfigPath(), "instance": in.Name, "selected_by": src, "profile": p.Name, "read_only": a.ReadOnly(in)}}
	if err := in.CheckFiles(); err != nil {
		c.Status, c.Layer, c.Detail = "fail", clierr.LayerConfig, err.Error()
	} else if !in.TLS.VerifyEnabled() {
		c.Status, c.Detail = "warn", "TLS certificate verification is disabled (tls.verify: false)"
	}
	if !add(c) {
		return checks, failed
	}

	u, _ := url.Parse(in.BaseURL)
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	connect, _, _ := in.Timeouts.Durations()

	// Is a proxy in use for this URL?
	proxyFn, _ := transport.ProxyFunc(in.Proxy)
	var proxyURL *url.URL
	if proxyFn != nil {
		proxyURL, _ = proxyFn(&http.Request{URL: u})
	}

	// 2-4. DNS, TCP, TLS (direct connections only; through a proxy the
	// HTTP check below covers the path end to end).
	if proxyURL != nil {
		add(check{Name: "network", Status: "skip", Detail: "proxy in use (" + proxyURL.Scheme + "://" + proxyURL.Host + "); DNS/TCP/TLS are checked end-to-end by the HTTP check"})
		pc := check{Name: "proxy_tcp", Status: "ok", Data: map[string]any{"proxy": proxyURL.Host}}
		pport := proxyURL.Port()
		if pport == "" {
			pport = map[string]string{"https": "443", "http": "80", "socks5": "1080"}[proxyURL.Scheme]
		}
		conn, err := (&net.Dialer{Timeout: connect}).DialContext(ctx, "tcp", net.JoinHostPort(proxyURL.Hostname(), pport))
		if err != nil {
			e := clierr.Classify(err)
			pc = check{Name: "proxy_tcp", Status: "fail", Layer: clierr.LayerProxy, Detail: e.Error()}
		} else {
			conn.Close()
		}
		if !add(pc) {
			return checks, failed
		}
	} else {
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			fail("dns", err)
			return checks, failed
		}
		add(check{Name: "dns", Status: "ok", Data: map[string]any{"host": host, "addresses": addrs}})

		start := time.Now()
		conn, err := (&net.Dialer{Timeout: connect}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		if err != nil {
			fail("tcp", err)
			return checks, failed
		}
		conn.Close()
		add(check{Name: "tcp", Status: "ok", Data: map[string]any{"address": net.JoinHostPort(host, port), "connect_ms": time.Since(start).Milliseconds()}})

		if u.Scheme == "https" {
			if !add(tlsCheck(ctx, in, host, port, connect)) {
				return checks, failed
			}
		}
	}

	client, err := a.Client(in, p)
	if err != nil {
		fail("http", err)
		return checks, failed
	}

	// 5. Unauthenticated reachability + version compatibility. Production
	// instances often disable anonymous access: a 401/403 from Jira itself
	// still proves DNS, TCP, TLS, proxy and HTTP routing work, so it is not
	// a failure; the version is then checked with credentials (step 8).
	anon := *client
	anon.Auth = nil
	versionPending := false
	resp, err := anon.Do(ctx, api.Request{Method: "GET", Path: "/api/2/serverInfo", Idempotent: true})
	if e := clierr.As(err); err != nil && resp != nil && (e.HTTPStatus == 401 || e.HTTPStatus == 403) {
		c := check{Name: "http", Status: "ok", Data: map[string]any{"status": resp.Status}}
		if id := resp.Header.Get("X-AREQUESTID"); id != "" {
			c.Detail = fmt.Sprintf("Jira is reachable; anonymous access is disabled (HTTP %d), which is normal; version is checked after authentication", resp.Status)
			c.Data["jira_request_id"] = id
		} else {
			c.Status = "warn"
			c.Detail = fmt.Sprintf("HTTP %d without Jira's X-AREQUESTID header: the answer may come from a gateway/SSO proxy in front of Jira rather than Jira itself", resp.Status)
		}
		add(c)
		versionPending = true
	} else if err != nil {
		fail("http", err)
		return checks, failed
	} else if !add(serverInfoCheck(resp, in, p)) {
		return checks, failed
	}

	// 6. Credentials present (no network).
	st := client.Auth.Status(ctx)
	if !st.Present {
		add(check{Name: "credentials", Status: "fail", Layer: clierr.LayerCredentials, Detail: st.Error,
			Hint: "run `jira auth login` or set JIRA_CREDENTIAL_FILE"})
		return checks, failed
	}
	cc := check{Name: "credentials", Status: "ok", Data: map[string]any{"method": st.Method, "source": st.Source}}
	if len(st.Warnings) > 0 {
		cc.Status, cc.Detail = "warn", strings.Join(st.Warnings, "; ")
	}
	add(cc)

	// 7. Authentication.
	me, err := whoami(ctx, client)
	if err != nil {
		fail("authentication", err)
		return checks, failed
	}
	add(check{Name: "authentication", Status: "ok", Data: map[string]any{"user": me["name"], "displayName": me["displayName"]}})

	// 8. Version compatibility with credentials, when the anonymous probe
	// was refused.
	if versionPending {
		resp, err := client.Do(ctx, api.Request{Method: "GET", Path: "/api/2/serverInfo", Idempotent: true})
		if err != nil {
			fail("version", err)
			return checks, failed
		}
		c := serverInfoCheck(resp, in, p)
		c.Name = "version"
		add(c)
	}
	return checks, failed
}

func tlsCheck(ctx context.Context, in *config.Instance, host, port string, timeout time.Duration) check {
	tc, err := transport.TLSConfig(in.TLS)
	if err != nil {
		return check{Name: "tls", Status: "fail", Layer: clierr.LayerConfig, Detail: err.Error()}
	}
	if tc.ServerName == "" {
		tc.ServerName = host
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout}, Config: tc}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		e := clierr.Classify(err)
		return check{Name: "tls", Status: "fail", Layer: e.Layer, Detail: e.Error(), Hint: e.Hint}
	}
	defer conn.Close()
	cs := conn.(*tls.Conn).ConnectionState()
	data := map[string]any{"version": tls.VersionName(cs.Version), "cipher_suite": tls.CipherSuiteName(cs.CipherSuite), "alpn": cs.NegotiatedProtocol}
	c := check{Name: "tls", Status: "ok", Data: data}
	if len(cs.PeerCertificates) > 0 {
		leaf := cs.PeerCertificates[0]
		days := int(time.Until(leaf.NotAfter).Hours() / 24)
		data["subject"], data["issuer"], data["not_after"], data["days_remaining"] = leaf.Subject.String(), leaf.Issuer.String(), leaf.NotAfter.UTC(), days
		if days < 14 {
			c.Status, c.Detail = "warn", fmt.Sprintf("server certificate expires in %d days", days)
		}
	}
	if !in.TLS.VerifyEnabled() {
		c.Status, c.Detail = "warn", "certificate verification disabled (tls.verify: false)"
	}
	return c
}

func serverInfoCheck(resp *api.Response, in *config.Instance, p *operation.Profile) check {
	var si struct {
		Version        string `json:"version"`
		DeploymentType string `json:"deploymentType"`
		BaseURL        string `json:"baseUrl"`
		BuildNumber    int    `json:"buildNumber"`
		ServerTitle    string `json:"serverTitle"`
	}
	if err := json.Unmarshal(resp.Body, &si); err != nil {
		return check{Name: "http", Status: "fail", Layer: clierr.LayerResponse, Detail: "serverInfo is not valid JSON"}
	}
	c := check{Name: "http", Status: "ok", Data: map[string]any{
		"jira_version": si.Version, "deployment_type": si.DeploymentType, "jira_base_url": si.BaseURL,
		"build_number": si.BuildNumber, "profile_spec_version": p.Spec.Version,
	}}
	var warns []string
	if si.DeploymentType != "" && !strings.EqualFold(si.DeploymentType, "Server") && !strings.EqualFold(si.DeploymentType, "DataCenter") {
		warns = append(warns, "unexpected deploymentType "+si.DeploymentType+" (this CLI targets Data Center)")
	}
	if mm(si.Version) != "" && mm(si.Version) != mm(p.Spec.Version) {
		warns = append(warns, fmt.Sprintf("Jira %s differs from the specification version %s of profile %s; some commands may not exist or behave differently", si.Version, p.Spec.Version, p.Name))
	}
	if si.BaseURL != "" && !strings.EqualFold(strings.TrimRight(si.BaseURL, "/"), strings.TrimRight(in.BaseURL, "/")) {
		warns = append(warns, "Jira's configured base URL "+si.BaseURL+" differs from base_url; links in responses will point there")
	}
	if len(warns) > 0 {
		c.Status, c.Detail = "warn", strings.Join(warns, "; ")
	}
	return c
}

// mm returns "major.minor".
func mm(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}
