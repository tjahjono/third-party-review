package aiclient

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// transportError wraps a network-level failure, which is always worth retrying.
type transportError struct{ err error }

func (e *transportError) Error() string { return "ai: transport: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// httpError wraps a non-200 response. The endpoint is included because the
// most common misconfiguration is an AI_BASE_URL missing or duplicating the
// /api or /v1 prefix, and a bare 404 gives the operator nothing to go on.
type httpError struct {
	status   int
	body     string
	endpoint string
}

func (e *httpError) Error() string {
	body := strings.TrimSpace(e.body)
	if len(body) > 500 {
		body = body[:500] + "..."
	}
	switch e.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("ai: %d from %s - check AI_API_KEY (Open WebUI issues keys under Settings > Account): %s",
			e.status, e.endpoint, body)
	case http.StatusNotFound:
		return fmt.Sprintf("ai: 404 from %s - check AI_BASE_URL and AI_CHAT_PATH (Open WebUI is usually .../api, OpenAI .../v1): %s",
			e.endpoint, body)
	}
	return fmt.Sprintf("ai: %d from %s: %s", e.status, e.endpoint, body)
}

// Status exposes the HTTP status for retry decisions.
func (e *httpError) Status() int { return e.status }

// retryable reports whether another attempt could plausibly succeed.
func retryable(err error) bool {
	var te *transportError
	if errors.As(err, &te) {
		return true
	}
	var he *httpError
	if errors.As(err, &he) {
		switch he.status {
		case http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		case http.StatusBadRequest:
			// Worth one more attempt with response_format removed.
			return true
		}
		return false
	}
	return false
}

// isBadRequest reports whether the failure was a 400, which is how backends
// that do not understand response_format usually complain.
func isBadRequest(err error) bool {
	var he *httpError
	return errors.As(err, &he) && he.status == http.StatusBadRequest
}
