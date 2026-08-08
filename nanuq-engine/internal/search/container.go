package search

// This file implements the ResultContainer — a faithful Go port of
// SearXNG's ResultContainer (searx/results.py L53-269, REQ-009). It
// collects the raw results emitted by every engine during a search,
// merges the main results with URL-based deduplication (EC-006), and once
// closed computes the final scores (REQ-010, DSG-010).
//
// Thread-safety mirrors the Python original (results.py L53 uses an
// RLock): the container is used concurrently — Extend is called from the
// engine goroutines (DSG-005) while Close/GetOrderedResults run after the
// engines finished. Every method locks mu around the shared state.

import (
	"sync"
	"time"

	"nanuq-engine/internal/result"
)

// UnresponsiveEngine reports an engine that failed during a search (port
// of the UnresponsiveEngine NamedTuple, results.py L51; REQ-009). Name is
// the engine instance name; Reason the error kind (e.g. "timeout" for a
// watchdog timeout, EC-003).
type UnresponsiveEngine struct {
	Name   string
	Reason string
}

// Timing records the wall-clock duration of a single engine request
// (port of the Timing NamedTuple, results.py L52; REQ-009). EngineName
// names the engine; Duration is the measured runtime.
type Timing struct {
	EngineName string
	Duration   time.Duration
}

// ResultContainer collects and merges the raw results produced by every
// engine during a search — port of the ResultContainer class
// (results.py L53-269, REQ-009).
//
// Extend dispatches each incoming RawResult by its Kind (DSG-004) exactly
// like the Python extend() does with its type checks and legacy-dict
// branches; main results are merged with URL-based deduplication
// (_merge_main_result, results.py L167-181; EC-006). Close computes the
// final score of every merged result (close(), results.py L183-189) and
// GetOrderedResults returns them ranked and grouped (get_ordered_results,
// results.py L191-247; REQ-010, DSG-010).
type ResultContainer struct {
	mu sync.Mutex

	// mainResultsMap holds the merged main results keyed by URL
	// (deduplication across engines, EC-006). The Python original keys by
	// the result hash (results.py L53); the URL is the stable, hashable
	// identity of a main result in this port.
	mainResultsMap map[string]*result.MainResult
	// infoboxes collects the infobox payloads ('infobox' branch,
	// results.py L135-138).
	infoboxes []result.Infobox
	// suggestions collects the query-suggestion strings ('suggestion'
	// branch, results.py L127-129). NOTE: the Python original keeps a set
	// (automatic dedup); TASK-006 specifies plain append.
	suggestions []string
	// corrections collects the query-correction strings ('correction'
	// branch, results.py L132-133). Same NOTE as suggestions.
	corrections []string
	// answers collects the answers of every AnswerSet received
	// (BaseAnswer branch, results.py L94-98).
	answers []result.Answer
	// engineData stores engine-specific data keyed by the engine name
	// ('engine_data' branch, results.py L140-147).
	engineData map[string]any
	// unresponsive lists the engines that failed during the search
	// (add_unresponsive_engine, results.py L249-255).
	unresponsive []UnresponsiveEngine
	// timings records per-engine durations (add_timing, results.py
	// L257-262). Populated by a later phase (TASK-006 part A keeps the
	// field, per REQ-009, without the timing plumbing).
	timings []Timing
	// redirectURL is the redirect target of the search (e.g. an external
	// bang redirect, results.py redirect_url).
	redirectURL string
	// paging reports whether any engine supports result paging (extend(),
	// results.py L149-152). Not used by part A beyond storage.
	paging bool
}

// NewResultContainer creates an empty ResultContainer (port of the
// ResultContainer constructor, results.py L53-72).
func NewResultContainer() *ResultContainer {
	return &ResultContainer{
		mainResultsMap: make(map[string]*result.MainResult),
		engineData:     make(map[string]any),
	}
}

// Extend merges the raw results produced by one engine into the container
// (port of extend(), results.py L82-152; REQ-009, DSG-004). Each RawResult
// is routed by its Kind to its target collection, mirroring the Python
// type checks (BaseAnswer / MainResult branches) and legacy-dict keys
// (suggestion / answer / correction / infobox / engine_data branches).
//
// engineName is the engine instance name: it is stamped onto every main
// result the engine produced (result['engine'] = engine_name, results.py
// L88) and keys the engine_data entries when the RawResult carries no
// engine name of its own (results.py L144-147). Answerers pass an empty
// name (search.go searchAnswerers), which leaves main results unattributed
// — faithful to the Python branch that only assigns result['engine'] for
// real engines.
//
// The container must not be extended after Close (the Python _closed
// guard, results.py L83-85, is enforced by the caller contract — the
// TASK-006 struct has no _closed flag).
func (c *ResultContainer) Extend(engineName string, rawResults []*result.RawResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, r := range rawResults {
		if r == nil {
			continue
		}
		switch r.Kind {
		case result.KindAnswer:
			// BaseAnswer branch (results.py L94-98): every Answer of the
			// AnswerSet is appended to the answer list.
			if r.Answer != nil {
				c.answers = append(c.answers, r.Answer.Answers...)
			}
		case result.KindMain:
			// MainResult branch (results.py L99-101) -> _merge_main_result
			// (results.py L167-181): the container is keyed by URL, so a
			// second result with the same URL is merged into the existing
			// one (EC-006).
			m := r.Main
			if m == nil {
				continue
			}
			// Engine attribution: stamp the caller's engine name onto the
			// result before it enters the container, mirroring extend()'s
			// result['engine'] = engine_name right after the type dispatch
			// (results.py L88). Answerers pass an empty engineName
			// (search.go searchAnswerers) and leave Engines untouched.
			if engineName != "" && !enginesContain(m.Engines, engineName) {
				m.Engines = append(m.Engines, engineName)
			}
			if existing, ok := c.mainResultsMap[m.URL]; ok {
				result.Merge(existing, m)
			} else {
				c.mainResultsMap[m.URL] = m
			}
		case result.KindCorrection:
			// 'correction' legacy-dict branch (results.py L132-133).
			if r.Str != nil {
				c.corrections = append(c.corrections, *r.Str)
			}
		case result.KindSuggestion:
			// 'suggestion' legacy-dict branch (results.py L127-129).
			if r.Str != nil {
				c.suggestions = append(c.suggestions, *r.Str)
			}
		case result.KindInfobox:
			// 'infobox' legacy-dict branch (results.py L135-138).
			if r.Infobox != nil {
				c.infoboxes = append(c.infoboxes, *r.Infobox)
			}
		case result.KindEngineData:
			// 'engine_data' legacy-dict branch (results.py L140-147): keyed
			// by the engine name carried in Str, falling back to the
			// engine_name passed to extend() (result.engine or engine_name,
			// results.py L88).
			name := engineName
			if r.Str != nil {
				name = *r.Str
			}
			c.engineData[name] = r.Data
		case result.KindKeyValue, result.KindCode, result.KindPaper,
			result.KindFile, result.KindImage, result.KindTranslations,
			result.KindWeather:
			// TODO(TASK-006, phase B): these typed results (searx/
			// result_types) are not part of the search pipeline yet and
			// are silently skipped, mirroring the Python extend() which
			// raises NotImplementedError for unknown typed results
			// (results.py L101-103) — no production path emits them.
		}
	}
}

// enginesContain reports whether name is already present in engines — the
// attribution dedup guard (results.py L88 stamps a set, so a name must not
// be repeated). result.Merge applies the same set union when two results
// share a URL.
func enginesContain(engines []string, name string) bool {
	for _, e := range engines {
		if e == name {
			return true
		}
	}
	return false
}

// Close computes the final score of every merged main result (port of
// close(), results.py L183-189; REQ-010, DSG-010).
//
// weight is the combined engine weight — the product of the cfg.Weight of
// every engine that contributed to the search. Following the Python
// calculate_score (results.py L24-29), the effective weight of a result
// is weight × len(r.Positions), and the score is Σ(effective_weight /
// position) over the result's own positions — see
// result.CalculateScore. The caller (search.go, TASK-006 phase B) applies
// the combined engine weight by multiplying the individual engine weights
// before calling Close.
//
// positions is accepted for the TASK-006 signature contract; the
// per-result positions accumulated by Merge during Extend are
// authoritative, exactly as close() scores each merged result from its own
// positions (results.py L185-187).
func (c *ResultContainer) Close(weight float64, positions []int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, r := range c.mainResultsMap {
		r.Score = result.CalculateScore(weight*float64(len(r.Positions)), r.Positions)
	}
}

// GetOrderedResults returns the merged main results, ordered and grouped
// as in get_ordered_results (results.py L191-247; REQ-010, DSG-010):
// stable sort by score descending, then grouping by
// category:template:img_src with max_count=8 and max_distance=20 — see
// result.GetOrderedResults.
//
// The caller must have set the Category field of every result beforehand
// (the Python engine->category lookup, results.py L204-208, is omitted in
// this port) and must have called Close first — get_ordered_results()
// itself closes the container when needed (results.py L192-193), which is
// impossible here because Close needs the caller-provided weight.
//
// The returned slice is freshly allocated; the container's map is left
// untouched.
func (c *ResultContainer) GetOrderedResults() []*result.MainResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	results := make([]*result.MainResult, 0, len(c.mainResultsMap))
	for _, r := range c.mainResultsMap {
		results = append(results, r)
	}
	return result.GetOrderedResults(results)
}

// Answers returns a copy of the collected answers (BaseAnswer branch,
// results.py L94-98; REQ-009). The copy is freshly allocated, so callers
// cannot mutate the container's internal slice. Used by the UI layer to
// build the JSON response (TASK-012b, CA-003).
func (c *ResultContainer) Answers() []result.Answer {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]result.Answer, len(c.answers))
	copy(out, c.answers)
	return out
}

// Corrections returns a copy of the query-correction strings
// ('correction' branch, results.py L132-133; REQ-009). The copy is freshly
// allocated, so callers cannot mutate the container's internal slice. Used
// by the UI layer to build the JSON response (TASK-012b, CA-003).
func (c *ResultContainer) Corrections() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]string, len(c.corrections))
	copy(out, c.corrections)
	return out
}

// Infoboxes returns a copy of the collected infoboxes ('infobox' branch,
// results.py L135-138; REQ-009). The copy is freshly allocated, so callers
// cannot mutate the container's internal slice. Used by the UI layer to
// build the JSON response (TASK-012b, CA-003).
func (c *ResultContainer) Infoboxes() []result.Infobox {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]result.Infobox, len(c.infoboxes))
	copy(out, c.infoboxes)
	return out
}

// Suggestions returns a copy of the query-suggestion strings ('suggestion'
// branch, results.py L127-129; REQ-009). The copy is freshly allocated, so
// callers cannot mutate the container's internal slice. Used by the UI
// layer to build the JSON response (TASK-012b, CA-003).
func (c *ResultContainer) Suggestions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]string, len(c.suggestions))
	copy(out, c.suggestions)
	return out
}

// Timings returns a copy of the per-engine timings (add_timing, results.py
// L257-262; REQ-009). The copy is freshly allocated, so callers cannot
// mutate the container's internal slice. Used by the UI layer to expose
// the per-engine durations in the JSON response (TASK-012b, CA-003).
func (c *ResultContainer) Timings() []Timing {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]Timing, len(c.timings))
	copy(out, c.timings)
	return out
}

// AddUnresponsiveEngine records an engine that failed to answer (port of
// add_unresponsive_engine(), results.py L249-255; REQ-009, EC-003). The
// Python original only records engines whose display_error_messages flag
// is set; in this phase display_error_messages is always true (TASK-006),
// so every failure is recorded unconditionally.
func (c *ResultContainer) AddUnresponsiveEngine(name, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unresponsive = append(c.unresponsive, UnresponsiveEngine{Name: name, Reason: reason})
}

// AddTiming records the wall-clock duration of a single engine request
// (port of add_timing(), results.py L257-262; REQ-009). It is called from
// the search pipeline after each engine finishes (search.go, TASK-006
// phase B) to keep per-engine timings available for the UI.
func (c *ResultContainer) AddTiming(engineName string, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.timings = append(c.timings, Timing{EngineName: engineName, Duration: d})
}

// Unresponsive returns a copy of the unresponsive-engine records (port of
// the result_container.unresponsive attribute, results.py L53; REQ-009).
// The copy is freshly allocated, so callers cannot mutate the container's
// internal slice. Used by the UI layer (TASK-012) and by tests to assert
// watchdog timeouts (CA-004).
func (c *ResultContainer) Unresponsive() []UnresponsiveEngine {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]UnresponsiveEngine, len(c.unresponsive))
	copy(out, c.unresponsive)
	return out
}

// RedirectURL returns the redirect target of the search (results.py
// redirect_url), or "" when none was set.
func (c *ResultContainer) RedirectURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.redirectURL
}

// SetRedirectURL sets the redirect target of the search (results.py
// redirect_url — e.g. set by the external-bang handling, searx/search/
// __init__.py search_external_bang).
func (c *ResultContainer) SetRedirectURL(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.redirectURL = url
}

// Reset clears the container back to its empty state. The Python original
// has no equivalent — it allocates a fresh ResultContainer per search
// (search/__init__.py Search.__init__); Reset is provided for test reuse
// and for a container that is intentionally pooled.
func (c *ResultContainer) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.mainResultsMap = make(map[string]*result.MainResult)
	c.infoboxes = nil
	c.suggestions = nil
	c.corrections = nil
	c.answers = nil
	c.engineData = make(map[string]any)
	c.unresponsive = nil
	c.timings = nil
	c.redirectURL = ""
	c.paging = false
}
