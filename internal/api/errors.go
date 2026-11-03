package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/personal-stack/jira-cli/internal/clierr"
)

// jiraError is Jira's standard error body.
type jiraError struct {
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"`
	Message       string            `json:"message"`
}

// APIError converts an HTTP error response into a structured error.
func APIError(r *Response) *clierr.Error {
	layer := clierr.LayerAPI
	code := "http_" + fmt.Sprint(r.Status)
	switch r.Status {
	case http.StatusUnauthorized:
		layer = clierr.LayerAuth
	case http.StatusForbidden:
		layer = clierr.LayerAuthorization
	}
	e := &clierr.Error{Layer: layer, Code: code, HTTPStatus: r.Status, Message: http.StatusText(r.Status), Details: map[string]any{}}
	var je jiraError
	if r.IsJSON() && json.Unmarshal(r.Body, &je) == nil {
		var parts []string
		parts = append(parts, je.ErrorMessages...)
		keys := make([]string, 0, len(je.Errors))
		for k := range je.Errors {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, k+": "+je.Errors[k])
		}
		if je.Message != "" {
			parts = append(parts, je.Message)
		}
		if len(parts) > 0 {
			e.Message = strings.Join(parts, "; ")
		}
		if len(je.ErrorMessages) > 0 {
			e.Details["errorMessages"] = je.ErrorMessages
		}
		if len(je.Errors) > 0 {
			e.Details["errors"] = je.Errors
		}
	}
	// Seraph (Jira's auth filter) explains 401/403 causes, e.g.
	// AUTHENTICATION_DENIED when CAPTCHA is required after failed logins.
	if reason := r.Header.Get("X-Seraph-LoginReason"); reason != "" {
		e.Details["login_reason"] = reason
		if strings.Contains(reason, "AUTHENTICATION_DENIED") {
			e.Hint = "Jira requires a CAPTCHA for this account after failed logins; log in via the browser once"
		}
	}
	switch r.Status {
	case http.StatusUnauthorized:
		if e.Hint == "" {
			e.Hint = "the credential was rejected: check that the token is valid and not expired (`jira auth status`, `jira auth login`)"
		}
	case http.StatusForbidden:
		e.Hint = "authenticated, but this account lacks the Jira permission for this operation"
	case http.StatusTooManyRequests:
		if ra := r.Header.Get("Retry-After"); ra != "" {
			e.Details["retry_after"] = ra
		}
		e.Hint = "rate limited by Jira; retry later"
	}
	if len(e.Details) == 0 {
		e.Details = nil
	}
	return e
}
