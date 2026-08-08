package engine

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors for the engine lifecycle. Callers compare with errors.Is;
// producers add context by wrapping (REQ-NF-007).
var (
	// ErrEngineNotFound is returned by Instantiate when the module named by
	// EngineConfig.Engine was never registered (REQ-004).
	ErrEngineNotFound = errors.New("engine: module not registered")

	// ErrNoResults is returned by Response when an engine produced no results
	// for the query.
	ErrNoResults = errors.New("engine: no results")

	// ErrInvalidConfig is wrapped by factories when an EngineConfig entry is
	// unusable, e.g. fmt.Errorf("%w: missing search_url", ErrInvalidConfig).
	ErrInvalidConfig = errors.New("engine: invalid config")
)

// EngineSuspendError describes an engine suspension decided by the processor
// (REQ-008). The processor uses SuspendFor as the backoff duration before the
// engine is tried again; the network layer propagates the suspend decision
// through this error so the search loop can skip the engine until then.
type EngineSuspendError struct {
	// Reason describes why the engine was suspended.
	Reason string

	// SuspendFor is the duration the engine stays suspended.
	SuspendFor time.Duration
}

// Error implements the error interface.
func (e *EngineSuspendError) Error() string {
	return fmt.Sprintf("engine: suspended (%s) for %s", e.Reason, e.SuspendFor)
}
