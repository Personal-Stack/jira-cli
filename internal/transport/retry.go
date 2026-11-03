package transport

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// RetryPolicy decides whether a failed attempt is retried.
//
// Rules:
//   - 429 Too Many Requests: retried for every method; the server rejected
//     the request before processing it. Retry-After is honoured.
//   - 502/503/504 and network errors: retried only for idempotent
//     operations (GET/HEAD, or operations explicitly marked idempotent in
//     the risk metadata). A POST that timed out may have been applied.
//   - TLS/certificate errors, context cancellation: never retried.
type RetryPolicy struct {
	MaxAttempts int           // total attempts, including the first (default 3)
	BaseDelay   time.Duration // default 500ms
	MaxDelay    time.Duration // cap for backoff and Retry-After (default 30s)
	Idempotent  bool
}

func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = 500 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 30 * time.Second
	}
	return p
}

// Next returns whether to retry after attempt (1-based) and how long to wait.
func (p RetryPolicy) Next(attempt int, resp *http.Response, err error) (bool, time.Duration) {
	p = p.withDefaults()
	if attempt >= p.MaxAttempts {
		return false, 0
	}
	backoff := p.backoff(attempt)
	if err != nil {
		if !p.Idempotent || errors.Is(err, context.Canceled) || errors.Is(err, ErrDowngrade) || isTLSError(err) {
			return false, 0
		}
		return true, backoff
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return p.afterHeader(resp, backoff)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		if !p.Idempotent {
			return false, 0
		}
		return p.afterHeader(resp, backoff)
	}
	return false, 0
}

func (p RetryPolicy) afterHeader(resp *http.Response, fallback time.Duration) (bool, time.Duration) {
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return true, fallback
	}
	var d time.Duration
	if s, err := strconv.Atoi(ra); err == nil {
		d = time.Duration(s) * time.Second
	} else if t, err := http.ParseTime(ra); err == nil {
		d = time.Until(t)
	} else {
		return true, fallback
	}
	if d < 0 {
		d = 0
	}
	if d > p.MaxDelay {
		return false, 0 // do not block for minutes; surface the 429 instead
	}
	return true, d
}

// backoff is exponential with full jitter.
func (p RetryPolicy) backoff(attempt int) time.Duration {
	d := p.BaseDelay << (attempt - 1)
	if d > p.MaxDelay || d <= 0 {
		d = p.MaxDelay
	}
	return time.Duration(rand.Int64N(int64(d) + 1))
}

// Sleep waits d or until ctx is done.
func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
