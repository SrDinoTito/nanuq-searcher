package search

// This file implements the Answerers registry — a faithful Go port of the
// SearXNG answerer machinery (searx/answerers, REQ-009) restricted to the
// pieces TASK-006 needs: the Answerer contract and the storage that asks
// every registered answerer and feeds the results into the
// ResultContainer (searx/search/__init__.py search_answerers, L71-75).
//
// Part A registers no answerers (the registry is empty by design — the
// answerer plugins are part of a later phase, TASK-006 phase F); the
// storage contract is in place so that phase only needs to Register
// implementations.

import (
	"context"

	"nanuq-engine/internal/result"
)

// Answerer answers instant questions about the search query (port of the
// Answerer ABC, searx/answerers/_core.py L40-47). The Python contract
// exposes keywords and an answer(query) method; this port folds the
// keywords into Name and adds a context so the search timeout (DSG-005)
// can be honoured. Ask must be safe to call concurrently — the storage
// invokes every answerer serially, but a future parallel phase may ask
// them in goroutines.
type Answerer interface {
	// Name returns the identifier of the answerer (in SearXNG the
	// keyword it answers to, _core.py keywords; registered under that
	// key, _core.py register L66-70).
	Name() string

	// Ask returns the raw results answering query, or nil when the
	// answerer has nothing to contribute. Implementations must respect
	// ctx cancellation.
	Ask(ctx context.Context, query string) []*result.RawResult
}

// AnswererStorage holds the registered answerers and answers queries by
// asking every one of them (port of AnswerStorage, searx/answerers/
// _core.py L55-97, as used by search_answerers in searx/search/__init__.py
// L71-75). The registry is keyed by Answerer.Name.
//
// NOTE: the Python ask() (L72-88) first matches the first non-empty word
// of the query against the registered keywords and only then calls the
// matching answerers. That keyword routing belongs to the answerer
// selection policy of a later phase; TASK-006 specifies that this storage
// asks every registered answerer unconditionally and concatenates the
// results.
type AnswererStorage struct {
	answerers map[string]Answerer
}

// NewAnswererStorage creates an empty AnswererStorage.
func NewAnswererStorage() *AnswererStorage {
	return &AnswererStorage{answerers: make(map[string]Answerer)}
}

// Register adds an answerer to the storage under its Name (port of
// register(), _core.py L66-70). Registering a second answerer with the
// same Name replaces the first one.
func (s *AnswererStorage) Register(a Answerer) {
	s.answerers[a.Name()] = a
}

// Ask asks every registered answerer and concatenates the results in
// registration order (map iteration order — unspecified but stable for a
// fixed set of answerers). With no registered answerers it returns nil.
// A nil context is tolerated and forwarded as-is.
func (s *AnswererStorage) Ask(ctx context.Context, query string) []*result.RawResult {
	var all []*result.RawResult
	for _, a := range s.answerers {
		if raws := a.Ask(ctx, query); len(raws) > 0 {
			all = append(all, raws...)
		}
	}
	return all
}
