package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrRevisionCheckForbidden signals that the account may not read node type
// config, so revision settings cannot be verified. Callers should fall back
// to read-only bundle discovery.
var ErrRevisionCheckForbidden = errors.New("not permitted to read node type config")

type errorObject struct {
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// APIError is a non-2xx JSON:API response.
type APIError struct {
	StatusCode int
	Method     string // request method that failed
	Items      []errorObject
}

func (e *APIError) Error() string {
	msg := http.StatusText(e.StatusCode)
	if d := e.detail(); d != "" {
		msg = d
	}
	return fmt.Sprintf("%d %s: %s", e.StatusCode, http.StatusText(e.StatusCode), msg)
}

func (e *APIError) detail() string {
	for _, it := range e.Items {
		if it.Detail != "" {
			return it.Detail
		}
	}
	for _, it := range e.Items {
		if it.Title != "" {
			return it.Title
		}
	}
	return ""
}

func (e *APIError) isWrite() bool {
	return e.Method == http.MethodPost || e.Method == http.MethodPatch
}

// UserMessage maps an API failure to actionable, user-facing text.
func (e *APIError) UserMessage() string {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return "Authentication failed — the site rejected the access token. Check the consumer's client ID and secret."
	case http.StatusForbidden:
		if e.isWrite() {
			return "Write blocked (403) — check the account's permissions, and that JSON:API accepts " +
				"create/read/update operations at /admin/config/services/jsonapi. " +
				"Publishing/unpublishing additionally needs publish permissions or 'administer nodes'."
		}
		return "Permission denied (403) — the account can't read this resource."
	case http.StatusNotFound:
		return "Not found — the content may have been removed, or the path is wrong."
	case http.StatusMethodNotAllowed:
		return "The site's JSON:API is read-only. Enable writes at /admin/config/services/jsonapi " +
			"(\"Accept all JSON:API create, read, update operations\")."
	case http.StatusUnprocessableEntity:
		if d := e.detail(); d != "" {
			return "Drupal rejected the content: " + d
		}
		return "Drupal rejected the content (validation failed)."
	default:
		if d := e.detail(); d != "" {
			return fmt.Sprintf("Request failed (%d): %s", e.StatusCode, d)
		}
		return fmt.Sprintf("Request failed (%d %s).", e.StatusCode, http.StatusText(e.StatusCode))
	}
}

// UserMessage renders any error from this package for the status bar.
func UserMessage(err error) string {
	var authErr *AuthError
	if errors.As(err, &authErr) {
		return authErr.UserMessage()
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.UserMessage()
	}
	if errors.Is(err, ErrRevisionCheckForbidden) {
		return "Couldn't verify revision settings (permission denied) — running read-only."
	}
	msg := err.Error()
	if strings.Contains(msg, "context deadline exceeded") {
		return "The site took too long to respond — try again."
	}
	return msg
}

func statusOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// IsForbidden reports whether err is an HTTP 403 from the API.
func IsForbidden(err error) bool { return statusOf(err) == http.StatusForbidden }

// IsUnauthorized reports whether err is an HTTP 401 from the API.
func IsUnauthorized(err error) bool { return statusOf(err) == http.StatusUnauthorized }

// IsNotFound reports whether err is an HTTP 404 from the API.
func IsNotFound(err error) bool { return statusOf(err) == http.StatusNotFound }
