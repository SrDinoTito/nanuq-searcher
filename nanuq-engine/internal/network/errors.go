package network

// HTTP error classification for the outbound layer (TASK-007).
//
// Port of raise_for_httperror.py (example/searxng/searx/network/
// raise_for_httperror.py): 402/403 → access denied, 429 → too many
// requests, Cloudflare challenge/firewall pages → cf_browser. Each mapped
// failure is returned as an engine.EngineSuspendError so the search
// pipeline (TASK-006, REQ-008) suspends the engine: HandleException reads
// the Reason string and looks it up in config.search.suspended_times
// (e.g. "cf_browser": 86400). The sentinels below let callers compare with
// errors.Is while errors.As still sees the suspension error.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nanuq-engine/internal/engine"
)

// Sentinel errors for the network layer. Concrete errors returned by this
// package wrap them (REQ-NF-007) so both errors.Is and errors.As against
// *engine.EngineSuspendError work.
var (
	// ErrAccessDenied is wrapped by 402/403 (and Cloudflare firewall)
	// suspension errors.
	ErrAccessDenied = errors.New("network: access denied")

	// ErrTooManyRequests is wrapped by 429 suspension errors.
	ErrTooManyRequests = errors.New("network: too many requests")

	// ErrTimeout is wrapped by request-timeout suspension errors.
	ErrTimeout = errors.New("network: timeout")
)

// Default suspension durations (informative defaults mirroring the
// reference settings.yml suspended_times). The real ban policy is decided
// by config.search.suspended_times via the Reason string; these values
// only fill EngineSuspendError.SuspendFor when no policy entry exists.
const (
	accessDeniedSuspend    = 3 * time.Minute // SearxEngineAccessDenied: 180
	tooManyRequestsSuspend = 3 * time.Minute // SearxEngineTooManyRequests: 180
	cfSuspension           = 24 * time.Hour  // cf_SearxEngineAccessDenied: 86400
	timeoutSuspend         = 5 * time.Second // ban_time_on_fail default
)

// httpStatusError couples an engine suspension decision with its sentinel:
// Unwrap exposes the sentinel (errors.Is) and As exposes the
// *engine.EngineSuspendError (errors.As, the mechanism the processor uses
// in HandleException, processor.go L201-206).
type httpStatusError struct {
	sus      *engine.EngineSuspendError
	sentinel error
}

// Error implements the error interface (delegates to the suspension
// error so the message stays "engine: suspended (reason) for ...").
func (e *httpStatusError) Error() string { return e.sus.Error() }

// Unwrap exposes the sentinel error for errors.Is.
func (e *httpStatusError) Unwrap() error { return e.sentinel }

// As exposes the wrapped *engine.EngineSuspendError so the suspension
// pipeline can extract the Reason via errors.As (processor.go
// HandleException). target must be a **engine.EngineSuspendError, which
// is exactly what errors.As passes for `var se *engine.EngineSuspendError;
// errors.As(err, &se)`.
func (e *httpStatusError) As(target any) bool {
	se, ok := target.(**engine.EngineSuspendError)
	if !ok {
		return false
	}
	*se = e.sus
	return true
}

// suspendHTTP builds an httpStatusError carrying the suspension reason and
// the sentinel.
func suspendHTTP(reason string, d time.Duration, sentinel error) error {
	return &httpStatusError{
		sus: &engine.EngineSuspendError{
			Reason:     reason,
			SuspendFor: d,
		},
		sentinel: sentinel,
	}
}

// IsSuspendError reports whether err (or anything it wraps) is an engine
// suspension error — the 429/403 class used by the pipeline to ban an
// engine (REQ-008, EC-005).
func IsSuspendError(err error) bool {
	var susErr *engine.EngineSuspendError
	return errors.As(err, &susErr)
}

// RaiseForHTTPError classifies a non-2xx HTTP response into an engine
// suspension error (port of raise_for_httperror, raise_for_httperror.py):
//
//   - Cloudflare challenge/firewall pages (403/429/503 with cf markers in
//     body or Server header) → Reason "cf_browser"
//   - 402/403 → Reason "access denied"
//   - 429 → Reason "too many requests"
//   - any other status >= 400 → generic error (the pipeline still
//     suspends, with the "exception" reason, via processor.go
//     HandleException)
//
// body must be the full response body (Do reads it to allow the Cloudflare
// detection). A 2xx status returns nil. A nil resp returns nil.
func RaiseForHTTPError(resp *http.Response, body []byte) error {
	if resp == nil {
		return nil
	}
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}

	switch {
	case isCloudflarePage(resp, body):
		return suspendHTTP("cf_browser", cfSuspension, ErrAccessDenied)
	case resp.StatusCode == http.StatusPaymentRequired || resp.StatusCode == http.StatusForbidden:
		return suspendHTTP("access denied", accessDeniedSuspend, ErrAccessDenied)
	case resp.StatusCode == http.StatusTooManyRequests:
		return suspendHTTP("too many requests", tooManyRequestsSuspend, ErrTooManyRequests)
	default:
		return fmt.Errorf("network: unexpected HTTP status %d", resp.StatusCode)
	}
}

// isCloudflarePage detects a Cloudflare challenge or firewall interstitial
// (port of is_cloudflare_challenge / is_cloudflare_firewall /
// raise_for_cloudflare_captcha). It relies on body markers — the cf
// challenge token, the "cf-browser-verification" wrapper and the
// "Cloudflare" brand string — plus the "Server: cloudflare" header, for
// statuses that Cloudflare uses for these interstitials (403/429/503).
func isCloudflarePage(resp *http.Response, body []byte) bool {
	bodyStr := string(body)
	cfStatus := resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusServiceUnavailable

	// "Server: cloudflare" fingerprint (Python is_cloudflare).
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Server")), "cloudflare") && cfStatus {
		return true
	}
	// Challenge platform markers (Python is_cloudflare_challenge).
	if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) &&
		(strings.Contains(bodyStr, "__cf_chl_jschl_tk__=") || strings.Contains(bodyStr, "__cf_chl")) {
		return true
	}
	// Body markers required by TASK-007: cf-browser-verification /
	// "Cloudflare" brand on a Cloudflare-worthy status.
	if cfStatus &&
		(strings.Contains(bodyStr, "cf-browser-verification") || strings.Contains(bodyStr, "Cloudflare")) {
		return true
	}
	return false
}

// timeoutSuspendError builds the suspension error for a request timeout.
func timeoutSuspendError() error {
	return suspendHTTP("timeout", timeoutSuspend, ErrTimeout)
}
