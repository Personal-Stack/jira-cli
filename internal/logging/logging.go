// Package logging builds the structured logger used by the CLI and the
// generator. Every record passes through a redacting handler, so a mistake
// at a call site (logging a header, a token-bearing URL, a secret field)
// cannot leak credentials into logs.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// Environment variables (flags take precedence).
const (
	EnvLevel  = "JIRA_LOG_LEVEL"  // debug, info, warn, error
	EnvFormat = "JIRA_LOG_FORMAT" // text, json
	EnvFile   = "JIRA_LOG_FILE"   // append logs to this file instead of stderr
)

// Options configure a logger.
type Options struct {
	Level  string // debug, info, warn (default), error
	Format string // text (default), json
	File   string // optional path; logs go there instead of the default writer
	// Attrs are attached to every record (e.g. run_id, component).
	Attrs []slog.Attr
}

// ParseLevel parses a level name.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "warn", "warning":
		return slog.LevelWarn, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("invalid log level %q (debug, info, warn, error)", s)
}

// New returns a logger writing to w (or Options.File) and a closer that must
// be called before exit. Log files are created with mode 0600: logs contain
// hostnames, JQL and issue keys, which may be confidential.
func New(w io.Writer, o Options) (*slog.Logger, io.Closer, error) {
	lvl, err := ParseLevel(o.Level)
	if err != nil {
		return nil, nil, err
	}
	var closer io.Closer = nopCloser{}
	if o.File != "" {
		f, err := os.OpenFile(o.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("log file: %w", err)
		}
		w, closer = f, f
	}
	hopts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	switch strings.ToLower(o.Format) {
	case "", "text":
		h = slog.NewTextHandler(w, hopts)
	case "json":
		h = slog.NewJSONHandler(w, hopts)
	default:
		closer.Close()
		return nil, nil, fmt.Errorf("invalid log format %q (text, json)", o.Format)
	}
	h = NewRedactingHandler(h)
	if len(o.Attrs) > 0 {
		h = h.WithAttrs(o.Attrs)
	}
	return slog.New(h), closer, nil
}

// Discard is a logger that drops everything.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// NewID returns a short random identifier for correlating log lines.
func NewID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "000000000000"
	}
	return hex.EncodeToString(b)
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// ---- redaction ----

const redacted = "[REDACTED]"

// sensitiveKeys are attribute keys whose values are always replaced.
var sensitiveKeys = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true, "set-cookie": true,
	"password": true, "passwd": true, "secret": true, "token": true, "pat": true,
	"access_token": true, "refresh_token": true, "id_token": true, "client_secret": true,
	"code": true, "code_verifier": true, "api_key": true, "apikey": true, "private_key": true,
	"body": true, "request_body": true,
}

var sensitiveSuffixes = []string{"_token", "_secret", "_password", "-token", "-secret"}

// IsSensitiveKey reports whether values logged under key must be redacted.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	if sensitiveKeys[k] {
		return true
	}
	for _, s := range sensitiveSuffixes {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

var (
	bearerRe   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{4,}`)
	queryRe    = regexp.MustCompile(`(?i)([?&](?:code|state|token|access_token|refresh_token|client_secret|code_verifier|password|os_password)=)[^&\s"]*`)
	userinfoRe = regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`)
)

// RedactString scrubs credential-shaped substrings from free text.
func RedactString(s string) string {
	s = bearerRe.ReplaceAllString(s, "$1 "+redacted)
	s = queryRe.ReplaceAllString(s, "${1}"+redacted)
	s = userinfoRe.ReplaceAllString(s, "${1}"+redacted+"@")
	return s
}

// RedactingHandler wraps a handler and scrubs every attribute and message.
type RedactingHandler struct{ next slog.Handler }

// NewRedactingHandler wraps h.
func NewRedactingHandler(h slog.Handler) *RedactingHandler { return &RedactingHandler{next: h} }

func (h *RedactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, RedactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, nr)
}

func (h *RedactingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = redactAttr(a)
	}
	return &RedactingHandler{next: h.next.WithAttrs(out)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, redacted)
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, RedactString(v.String()))
	case slog.KindGroup:
		g := v.Group()
		out := make([]any, len(g))
		for i, ga := range g {
			out[i] = redactAttr(ga)
		}
		return slog.Group(a.Key, out...)
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return slog.String(a.Key, RedactString(err.Error()))
		}
		return slog.String(a.Key, RedactString(fmt.Sprint(v.Any())))
	}
	return slog.Attr{Key: a.Key, Value: v}
}
