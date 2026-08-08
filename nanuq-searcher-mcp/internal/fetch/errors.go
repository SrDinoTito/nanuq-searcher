package fetch

import (
	"errors"
	"fmt"
)

// ErrNotHTML is the sentinel error returned when a response is not HTML
// (REQ-010 guardrail: reject JSON, PDF, XML, ... without trying to convert).
// Use errors.Is to detect it; the concrete *NotHTMLError carries the
// received Content-Type so callers can render EC-004 messages like
// "no es HTML: <content-type>".
var ErrNotHTML = errors.New("response is not HTML")

// NotHTMLError describes a response whose Content-Type is not HTML. It wraps
// ErrNotHTML with the received Content-Type for descriptive, distinguishable
// errors.
type NotHTMLError struct {
	ContentType string
}

// Error implements error.
func (e *NotHTMLError) Error() string {
	return fmt.Sprintf("%s: content-type %q", ErrNotHTML, e.ContentType)
}

// Unwrap exposes ErrNotHTML for errors.Is.
func (e *NotHTMLError) Unwrap() error { return ErrNotHTML }

// ErrUnsupportedScheme is returned when the request URL (or a redirect
// target) is not http/https (NFR-003, DSG-012).
var ErrUnsupportedScheme = errors.New("unsupported URL scheme: only http and https are allowed")

// ErrTooManyRedirects is returned when a redirect chain exceeds
// Config.MaxRedirects (EC-003).
var ErrTooManyRedirects = errors.New("too many redirects")

// HTTPError describes a non-2xx HTTP status response. The client only
// surfaces the status — it does not decide retry policy (that belongs to
// the crawler, TASK-012); the tool renders the message in markdown.
type HTTPError struct {
	StatusCode int
	Status     string
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d %s", e.StatusCode, e.Status)
}
