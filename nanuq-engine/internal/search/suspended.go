package search

// This file implements the SuspendedStatus — a faithful Go port of
// SearXNG's suspended-status state machine (searx/search/processors/
// abstract.py L78-109, REQ-008, EC-005). When an engine fails repeatedly
// it is "suspended" for an exponentially growing ban period; while
// suspended the search pipeline skips it (port of
// extend_container_if_suspended, abstract.py L235-241).
//
// Two suspension modes are supported (REQ-008):
//   - configured bans: when the failure reason is listed in
//     search.suspended_times (e.g. "cf_browser": 86400), the engine is
//     suspended for exactly that many seconds (port of the SearXNG
//     "cf_*" ban table);
//   - exponential backoff: otherwise the ban grows as
//     ban_time_on_fail × 2^consecutive_errors, capped at
//     max_ban_time_on_fail (defaults 5s and 120s).
//
// The HTTP 429 / 403 failures (TooManyRequests / AccessDenied) are what
// trigger these suspensions (REQ-008, EC-005); the caller — EngineProcessor
// — maps the engine errors onto Suspend(reason).

import (
	"sync"
	"time"
)

// SuspendedStatus tracks the suspension state of a single engine (port of
// the SuspendedStatus class, abstract.py L78-109; REQ-008). All fields are
// guarded by mu: Suspend/Resume/IsSuspendedAt may be called from the
// engine goroutines while the pipeline checks IsSuspended concurrently.
type SuspendedStatus struct {
	mu sync.Mutex

	// consecutiveErrors counts the consecutive failures that have not yet
	// been followed by a successful search (continuous_errors, abstract.py
	// L80). It drives the exponential backoff.
	consecutiveErrors int
	// suspendEndTime is the wall-clock time until which the engine stays
	// suspended; the zero time means "not suspended" (suspend_end_time,
	// abstract.py L81).
	suspendEndTime time.Time
	// suspendReason is the last reason the engine was suspended for
	// (suspend_reason, abstract.py L82), used for diagnostics.
	suspendReason string
	// banTimeOnFail is the base ban length in seconds (search.ban_time_on_fail,
	// default 5).
	banTimeOnFail int
	// maxBanTimeOnFail caps the exponential backoff in seconds
	// (search.max_ban_time_on_fail, default 120).
	maxBanTimeOnFail int
	// suspendedTimes maps a failure reason to a fixed ban in seconds
	// (search.suspended_times, e.g. "cf_browser": 86400). A reason listed
	// here bypasses the backoff and bans for exactly that duration.
	suspendedTimes map[string]int
}

// NewSuspendedStatus creates a SuspendedStatus with the given ban policy.
// A zero banTimeOnFail (and an empty suspendedTimes) is a valid policy:
// every failure then suspends for 0s, which the pipeline treats as "skip
// this engine for this search only" (port of the default policy,
// abstract.py L87-88).
func NewSuspendedStatus(banTimeOnFail, maxBanTimeOnFail int, suspendedTimes map[string]int) *SuspendedStatus {
	return &SuspendedStatus{
		banTimeOnFail:    banTimeOnFail,
		maxBanTimeOnFail: maxBanTimeOnFail,
		suspendedTimes:   suspendedTimes,
	}
}

// Suspend suspends the engine for a ban period derived from reason (port
// of suspend(), abstract.py L91-102; REQ-008). If reason is listed in
// suspendedTimes, the ban is that fixed duration; otherwise it grows
// exponentially with the consecutive-error count and is capped at
// maxBanTimeOnFail.
func (s *SuspendedStatus) Suspend(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if ban, ok := s.suspendedTimes[reason]; ok && ban > 0 {
		// Configured ban (search.suspended_times): suspend for exactly
		// ban seconds regardless of the failure history (port of the
		// SearXNG "cf_*" entries, REQ-008).
		s.suspendEndTime = now.Add(time.Duration(ban) * time.Second)
		s.suspendReason = reason
		return
	}

	// Exponential backoff: ban_time_on_fail × 2^consecutive_errors, capped
	// at max_ban_time_on_fail (REQ-008). The multiplier overflows for
	// large counts, so it is computed as a float and clamped.
	multiplier := 1
	for i := 0; i < s.consecutiveErrors; i++ {
		multiplier *= 2
		if multiplier > s.maxBanTimeOnFail {
			multiplier = s.maxBanTimeOnFail
			break
		}
	}
	ban := s.banTimeOnFail * multiplier
	if s.maxBanTimeOnFail > 0 && ban > s.maxBanTimeOnFail {
		ban = s.maxBanTimeOnFail
	}

	s.suspendEndTime = time.Now().Add(time.Duration(ban) * time.Second)
	s.suspendReason = reason
	s.consecutiveErrors++
}

// IsSuspended reports whether the engine is currently suspended, taking
// the current wall-clock time (port of the is_suspended property,
// abstract.py L84-85).
func (s *SuspendedStatus) IsSuspended() bool {
	return s.IsSuspendedAt(time.Now())
}

// IsSuspendedAt reports whether the engine is suspended at the given
// instant (port of is_suspended, abstract.py L84-85). When the ban has
// already elapsed, the engine is auto-resumed: the error counter is reset
// and the suspension state cleared, mirroring SearXNG's behaviour of
// simply letting a lapsed suspend_end_time expire.
func (s *SuspendedStatus) IsSuspendedAt(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.suspendEndTime.IsZero() {
		return false
	}
	if now.Before(s.suspendEndTime) {
		return true
	}
	// Auto-resume once the ban has elapsed (abstract.py has no explicit
	// resume on expiry — the property comparison is enough; the explicit
	// reset keeps consecutiveErrors from accumulating forever).
	s.consecutiveErrors = 0
	s.suspendEndTime = time.Time{}
	s.suspendReason = ""
	return false
}

// Resume clears the suspension and the consecutive-error counter (port of
// resume(), abstract.py L104-109). Called by the pipeline after a
// successful engine response.
func (s *SuspendedStatus) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.consecutiveErrors = 0
	s.suspendEndTime = time.Time{}
	s.suspendReason = ""
}

// SuspendReason returns the reason of the last suspension (port of the
// suspend_reason attribute, abstract.py L82), for diagnostics and for the
// unresponsive-engine report.
func (s *SuspendedStatus) SuspendReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.suspendReason
}
