// Package github implements the authenticated GitHub Contents operations used
// for write-once publishing.  Failures are classified into a small closed set
// of categories so callers can persist retry/auth/permission outcomes without
// ever storing remote response text.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

// Category is the uploader-facing classification of a GitHub failure.
type Category string

const (
	// CategoryRetryable covers timeouts, network failures, 5xx, and rate
	// limiting; the queue may retry these with backoff indefinitely.
	CategoryRetryable Category = "retryable"
	// CategoryRateLimit is a retryable rate-limit response (429, or 403 with
	// x-ratelimit-remaining: 0 / Retry-After / a rate-limit message).
	CategoryRateLimit Category = "rate_limit"
	// CategoryAuth means the credential itself is rejected and must be
	// refreshed or reauthorized before the queue continues.
	CategoryAuth Category = "auth"
	// CategoryPermission means the authenticated user/App lacks access to the
	// configured repository or path; retrying cannot repair it.
	CategoryPermission Category = "permission"
	// CategoryPermanent covers every other definitive failure, including
	// conflicts, validation failures, and malformed remote content.
	CategoryPermanent Category = "permanent"
)

// Retryable reports whether the queue should persist retryable_error.
func (c Category) Retryable() bool {
	return c == CategoryRetryable || c == CategoryRateLimit
}

// ProtocolCategory maps the GitHub classification into the closed wire
// vocabulary that the queue persists in last_error_category.
func (c Category) ProtocolCategory() protocol.ErrorCategory {
	switch c {
	case CategoryAuth:
		return protocol.ErrorReauthorizationRequired
	case CategoryPermission:
		return protocol.ErrorRejectedPermission
	default:
		return protocol.ErrorInternal
	}
}

// Error is a sanitized GitHub failure.  It deliberately never wraps the
// underlying network error, request URL, headers, or response body, so it can
// be persisted or logged without leaking a token or transcript.
type Error struct {
	Category   Category
	HTTPStatus int
	Op         string
}

func (e *Error) Error() string {
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("github %s failed: %s (http %d)", e.Op, e.Category, e.HTTPStatus)
	}
	return fmt.Sprintf("github %s failed: %s", e.Op, e.Category)
}

// Retryable reports whether the error is safe to retry with backoff.
func (e *Error) Retryable() bool {
	return e != nil && e.Category.Retryable()
}

func newError(op string, category Category, status int) *Error {
	return &Error{Category: category, HTTPStatus: status, Op: op}
}

// classifyHTTP maps a non-success HTTP response to a category using only the
// status, rate-limit headers, and a bounded prefix of the documented message
// field.  The message is never retained.
func classifyHTTP(op string, status int, header http.Header, body []byte) *Error {
	return newError(op, classifyStatus(status, header, body), status)
}

func classifyStatus(status int, header http.Header, body []byte) Category {
	switch {
	case status == http.StatusUnauthorized:
		return CategoryAuth
	case status == http.StatusForbidden:
		if isRateLimited(header, body) {
			return CategoryRateLimit
		}
		if messageContains(body, "bad credentials") {
			return CategoryAuth
		}
		return CategoryPermission
	case status == http.StatusNotFound:
		// Repository/branch/path access failures are permission failures.
		// InspectFile intercepts a 404 on the target path before classifying.
		return CategoryPermission
	case status == http.StatusConflict:
		return CategoryPermanent
	case status == http.StatusTooManyRequests:
		return CategoryRateLimit
	case status >= 500:
		return CategoryRetryable
	default:
		return CategoryPermanent
	}
}

func isRateLimited(header http.Header, body []byte) bool {
	if strings.TrimSpace(header.Get("x-ratelimit-remaining")) == "0" {
		return true
	}
	if strings.TrimSpace(header.Get("Retry-After")) != "" {
		return true
	}
	return messageContains(body, "rate limit")
}

// messageContains reads only the documented GitHub message field, truncated
// before comparison, so an arbitrarily large body is never inspected.
func messageContains(body []byte, needle string) bool {
	if len(body) == 0 {
		return false
	}
	truncated := body
	if len(truncated) > 4096 {
		truncated = truncated[:4096]
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(truncated), &payload); err != nil {
		return false
	}
	message := payload.Message
	if len(message) > 120 {
		message = message[:120]
	}
	return strings.Contains(strings.ToLower(message), strings.ToLower(needle))
}
