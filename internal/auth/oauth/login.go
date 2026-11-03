package oauth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// LoginOptions controls the interactive authorization-code flow.
type LoginOptions struct {
	PKCE bool
	// Manual: instead of a loopback listener, the user pastes the final
	// redirect URL (for hosts where the browser runs elsewhere).
	Manual  bool
	In      io.Reader
	Out     io.Writer
	Timeout time.Duration
	// OnAuthURL is called with the authorization URL (tests, automation).
	OnAuthURL func(string)
}

// Login runs the authorization-code flow and returns the token.
func Login(ctx context.Context, conf *oauth2.Config, httpClient *http.Client, o LoginOptions) (*oauth2.Token, error) {
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	state, err := randomString(32)
	if err != nil {
		return nil, err
	}
	var authOpts, exchOpts []oauth2.AuthCodeOption
	if o.PKCE {
		v := oauth2.GenerateVerifier()
		authOpts = append(authOpts, oauth2.S256ChallengeOption(v))
		exchOpts = append(exchOpts, oauth2.VerifierOption(v))
	}
	authURL := conf.AuthCodeURL(state, authOpts...)

	var code string
	if o.Manual {
		code, err = manual(ctx, authURL, state, o)
	} else {
		code, err = loopback(ctx, conf.RedirectURL, authURL, state, o)
	}
	if err != nil {
		return nil, err
	}
	if httpClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	}
	tok, err := conf.Exchange(ctx, code, exchOpts...)
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			return nil, fmt.Errorf("token endpoint rejected the authorization code: %s %s", re.ErrorCode, re.ErrorDescription)
		}
		return nil, err
	}
	return tok, nil
}

func announce(o LoginOptions, authURL string) {
	if o.OnAuthURL != nil {
		o.OnAuthURL(authURL)
	}
	fmt.Fprintf(o.Out, "Open this URL in a browser and approve access:\n\n  %s\n\n", authURL)
}

func loopback(ctx context.Context, redirect, authURL, state string, o LoginOptions) (string, error) {
	ru, err := url.Parse(redirect)
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(ru.Hostname(), ru.Port()))
	if err != nil {
		return "", fmt.Errorf("cannot listen on %s for the OAuth redirect: %w (use --manual)", ru.Host, err)
	}
	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	mux := http.NewServeMux()
	path := ru.Path
	if path == "" {
		path = "/"
	}
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		code, err := checkCallback(r.URL.Query(), state)
		if err != nil {
			http.Error(w, "Authorization failed: "+html.EscapeString(err.Error()), http.StatusBadRequest)
		} else {
			io.WriteString(w, "<html><body>Authorization complete. You can close this window.</body></html>")
		}
		select {
		case ch <- result{code, err}:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	announce(o, authURL)
	fmt.Fprintf(o.Out, "Waiting for the redirect on %s ...\n", redirect)
	select {
	case r := <-ch:
		return r.code, r.err
	case <-ctx.Done():
		return "", errors.New("timed out waiting for the OAuth redirect")
	}
}

func manual(ctx context.Context, authURL, state string, o LoginOptions) (string, error) {
	announce(o, authURL)
	fmt.Fprint(o.Out, "After approving, paste the full URL your browser was redirected to:\n> ")
	lines := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(o.In)
		s.Buffer(make([]byte, 64<<10), 64<<10)
		if s.Scan() {
			lines <- s.Text()
		}
		close(lines)
	}()
	select {
	case line, ok := <-lines:
		if !ok {
			return "", errors.New("no redirect URL provided")
		}
		u, err := url.Parse(strings.TrimSpace(line))
		if err != nil {
			return "", fmt.Errorf("invalid URL: %w", err)
		}
		return checkCallback(u.Query(), state)
	case <-ctx.Done():
		return "", errors.New("timed out waiting for the redirect URL")
	}
}

func checkCallback(q url.Values, state string) (string, error) {
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("authorization server returned error %q: %s", e, q.Get("error_description"))
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
		return "", errors.New("state mismatch (possible CSRF); aborting")
	}
	code := q.Get("code")
	if code == "" {
		return "", errors.New("redirect did not contain an authorization code")
	}
	return code, nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
