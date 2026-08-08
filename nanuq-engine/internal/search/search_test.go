package search

// Tests for the TASK-006 part B pipeline: SearchService (search.go),
// EngineProcessor (processor.go) and SuspendedStatus (suspended.go).
//
// The tests use fake engines/requester/store/catalog (the search package
// must not import internal/engines — the real engines arrive in TASK-011)
// and cover: external bang redirect (CA-008), the standard multi-engine
// search, dedup via result.Merge, the suspend policy with exponential
// backoff (REQ-008), the per-engine watchdog timeout (CA-004) and the
// GetParams paging gate.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"nanuq-engine/internal/bang"
	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// ---------------------------------------------------------------------------
// fakes

// fakeEngine is a scripted engine.Engine. failMode controls Request():
//
//	""       -> success (no-op)
//	"error"  -> Request returns a generic error (not suspendable)
//
// The suspend failure ("failMode == suspend" per the TASK-006 test list) is
// delivered by the requester seam instead: like in online.py, a 429/403
// class failure happens at the network layer — the engine itself builds
// the request fine. TestSuspendBackoff wires a requester that returns an
// engine.EngineSuspendError.
type fakeEngine struct {
	name     string
	failMode string
	results  []*result.RawResult
	delay    time.Duration

	mu    sync.Mutex
	calls int
}

func (f *fakeEngine) Name() string                                      { return f.name }
func (f *fakeEngine) Shortcut() string                                  { return "f" + f.name }
func (f *fakeEngine) Categories() []string                              { return []string{"all"} }
func (f *fakeEngine) NeedsInit() bool                                   { return false }
func (f *fakeEngine) Setup(context.Context, *config.EngineConfig) error { return nil }
func (f *fakeEngine) Init(context.Context) error                        { return nil }

func (f *fakeEngine) Request(_ string, _ *engine.RequestParams) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.failMode == "error" {
		return fmt.Errorf("engine %s failed", f.name)
	}
	return nil
}

func (f *fakeEngine) Response(*http.Response) ([]*result.RawResult, error) {
	return f.results, nil
}

func (f *fakeEngine) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeRequester is a scripted Requester. When err is non-nil Do returns it
// (a suspendable failure — typically an engine.EngineSuspendError for the
// 429/403 class), otherwise it returns resp.
type fakeRequester struct {
	resp *http.Response
	err  error
}

func (r *fakeRequester) Do(context.Context, *engine.RequestParams) (*http.Response, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.resp, nil
}

// testBangStore implements BangResolver (Lookup + GetBangURL). It only
// knows the "ddg" bang; GetBangURL replicates the resolution of
// bang.Store.resolveURL for the query marker. (Named testBangStore: a
// fakeBangStore already exists in query_test.go.)
type testBangStore struct{}

func (testBangStore) Lookup(name string) (bang.BangDef, bool) {
	if name == "ddg" {
		return bang.BangDef{URL: "http://duckduckgo.com/?q=\x02", Rank: 19}, true
	}
	return bang.BangDef{}, false
}

func (testBangStore) GetBangURL(name, query string) (string, bool) {
	def, ok := testBangStore{}.Lookup(name)
	if !ok {
		return "", false
	}
	u := strings.ReplaceAll(def.URL, "\x02", url.QueryEscape(query))
	return u, true
}

// testCatalog answers the "all" category with the enabled engines. (Named
// testCatalog: a fakeCatalog already exists in query_test.go.)
type testCatalog struct {
	names []string
}

func (c testCatalog) Has(name string) bool {
	for _, n := range c.names {
		if n == name {
			return true
		}
	}
	return false
}

func (c testCatalog) ResolveShortcut(string) (string, bool) { return "", false }
func (c testCatalog) EnginesInCategory(cat string) ([]string, bool) {
	if cat == "all" {
		return c.names, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// helpers

func fakeHTTPResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}
}

func fakeMain(eng string, u string, title string) *result.RawResult {
	return result.NewMain(&result.MainResult{
		Title:     title,
		URL:       u,
		Engines:   []string{eng},
		Positions: []int{1},
	})
}

// newTestService builds a SearchService with one fake engine per entry of
// engines (key = engine name). requester may be nil (a default ok
// requester is used). cfg carries the global search policy.
func newTestService(t *testing.T, engines map[string]*fakeEngine, requester Requester, searchCfg config.Search) *SearchService {
	t.Helper()

	reg := engine.New()
	var ecfgs []config.EngineConfig
	names := make([]string, 0, len(engines))
	for name, fe := range engines {
		fe.name = name
		reg.Register(name, func(cfg *config.EngineConfig) (engine.Engine, error) {
			return engines[cfg.Name], nil
		})
		ecfgs = append(ecfgs, config.EngineConfig{Name: name, Engine: name, Weight: 1.0})
		names = append(names, name)
	}

	if requester == nil {
		requester = &fakeRequester{resp: fakeHTTPResponse()}
	}

	cfg := &config.Config{
		Search:  searchCfg,
		Engines: ecfgs,
	}
	return New(reg, testBangStore{}, testCatalog{names: names}, cfg, requester, nil)
}

// testRawQuery parses raw text against the fake store/catalog.
func testRawQuery(t *testing.T, raw string) *RawTextQuery {
	t.Helper()
	return Parse(raw, testBangStore{}, testCatalog{names: []string{"e1", "e2"}})
}

// ---------------------------------------------------------------------------
// external bang redirect (CA-008)

func TestSearchExternalBang(t *testing.T) {
	svc := newTestService(t, map[string]*fakeEngine{
		"e1": {failMode: "error"}, // must not be touched
	}, nil, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})

	raw := testRawQuery(t, "!!ddg hola")
	container := svc.Search(raw)

	if got, want := container.RedirectURL(), "http://duckduckgo.com/?q=hola"; got != want {
		t.Fatalf("RedirectURL = %q, want %q (CA-008)", got, want)
	}
	// The standard search must be skipped entirely: no engine ran.
	if p := svc.Processor("e1"); p != nil && !p.IsSuspended() {
		fe := p.engine.(*fakeEngine)
		if fe.callCount() != 0 {
			t.Fatalf("external bang search ran the engine %d times, want 0", fe.callCount())
		}
	}
}

// ---------------------------------------------------------------------------
// standard multi-engine search

func TestSearchStandardBasic(t *testing.T) {
	svc := newTestService(t, map[string]*fakeEngine{
		"e1": {results: []*result.RawResult{fakeMain("e1", "http://one.example/", "one")}},
		"e2": {results: []*result.RawResult{fakeMain("e2", "http://two.example/", "two")}},
	}, nil, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})

	container := svc.Search(testRawQuery(t, "hola"))
	container.Close(1.0, nil)

	got := container.GetOrderedResults()
	if len(got) != 2 {
		t.Fatalf("GetOrderedResults = %d results, want 2", len(got))
	}
	urls := map[string]bool{}
	for _, m := range got {
		urls[m.URL] = true
	}
	for _, want := range []string{"http://one.example/", "http://two.example/"} {
		if !urls[want] {
			t.Errorf("missing result URL %q in %v", want, urls)
		}
	}
}

// TestSearchDedup: two engines returning the same URL must produce a single
// merged result whose Engines list is the union (result.Merge).
func TestSearchDedup(t *testing.T) {
	svc := newTestService(t, map[string]*fakeEngine{
		"e1": {results: []*result.RawResult{fakeMain("e1", "http://same.example/", "title")}},
		"e2": {results: []*result.RawResult{fakeMain("e2", "http://same.example/", "title")}},
	}, nil, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})

	container := svc.Search(testRawQuery(t, "hola"))
	container.Close(1.0, nil)

	got := container.GetOrderedResults()
	if len(got) != 1 {
		t.Fatalf("GetOrderedResults = %d results, want 1 (dedup)", len(got))
	}
	engines := got[0].Engines
	if len(engines) != 2 || !has(engines, "e1") || !has(engines, "e2") {
		t.Fatalf("merged Engines = %v, want union {e1,e2}", engines)
	}
}

func has(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// suspension policy (REQ-008)

// TestSuspendBackoff verifies the suspension state machine: a suspendable
// failure (429/403 class delivered by the requester) suspends the engine
// (ban = ban_time_on_fail, 5s), a second consecutive failure doubles the
// ban (10s), and a reason listed in suspended_times uses that fixed ban
// (86400s for "cf_browser").
func TestSuspendBackoff(t *testing.T) {
	suspendErr := &engine.EngineSuspendError{Reason: "cf_browser", SuspendFor: time.Hour}

	t.Run("first failure bans 5s", func(t *testing.T) {
		svc := newTestService(t, map[string]*fakeEngine{
			"e1": {results: []*result.RawResult{fakeMain("e1", "http://one.example/", "one")}},
		}, &fakeRequester{err: suspendErr}, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})

		svc.Search(testRawQuery(t, "hola"))

		proc := svc.Processor("e1")
		if proc == nil {
			t.Fatal("processor e1 missing")
		}
		if !proc.IsSuspended() {
			t.Fatal("engine should be suspended after a suspendable failure (REQ-008)")
		}
		// ban = 5s: still suspended at +4s, already resumed at +6s.
		if !proc.suspended.IsSuspendedAt(time.Now().Add(4 * time.Second)) {
			t.Error("engine should still be suspended 4s into a 5s ban")
		}
		if proc.suspended.IsSuspendedAt(time.Now().Add(6 * time.Second)) {
			t.Error("engine should have auto-resumed 6s into a 5s ban")
		}
	})

	t.Run("second failure doubles ban to 10s", func(t *testing.T) {
		svc := newTestService(t, map[string]*fakeEngine{
			"e1": {results: []*result.RawResult{fakeMain("e1", "http://one.example/", "one")}},
		}, &fakeRequester{err: suspendErr}, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})

		proc := svc.Processor("e1")

		// First failure (via a full search so the suspendable error flows
		// through the pipeline: requester -> Search -> HandleException).
		svc.Search(testRawQuery(t, "hola"))
		if !proc.IsSuspended() {
			t.Fatal("engine not suspended after first failure")
		}

		// Second consecutive failure. The engine is suspended, so a new
		// Search would skip it (_get_requests) — that is the point of the
		// policy. Feed the second failure directly through the same
		// exception path the pipeline uses.
		proc.HandleException(NewResultContainer(), "e1", suspendErr, true)

		// ban = 5 * 2^1 = 10s.
		if !proc.suspended.IsSuspendedAt(time.Now().Add(9 * time.Second)) {
			t.Error("engine should still be suspended 9s into a 10s ban (backoff 5 -> 10)")
		}
		if proc.suspended.IsSuspendedAt(time.Now().Add(11 * time.Second)) {
			t.Error("engine should have auto-resumed 11s into a 10s ban")
		}
	})

	t.Run("suspended_times fixed ban", func(t *testing.T) {
		svc := newTestService(t, map[string]*fakeEngine{
			"e1": {results: []*result.RawResult{fakeMain("e1", "http://one.example/", "one")}},
		}, &fakeRequester{err: suspendErr}, config.Search{
			BanTimeOnFail:    5,
			MaxBanTimeOnFail: 120,
			SuspendedTimes:   map[string]int{"cf_browser": 86400},
		})

		proc := svc.Processor("e1")
		svc.Search(testRawQuery(t, "hola"))

		// The reason ("cf_browser") is in suspended_times: fixed 86400s
		// ban regardless of the backoff.
		if !proc.suspended.IsSuspendedAt(time.Now().Add(86399 * time.Second)) {
			t.Error("engine should be suspended for the configured 86400s ban")
		}
		if proc.suspended.IsSuspendedAt(time.Now().Add(86401 * time.Second)) {
			t.Error("engine should have auto-resumed after the configured 86400s ban")
		}
	})
}

// ---------------------------------------------------------------------------
// watchdog timeout (CA-004)

// TestEngineTimeout: an engine that hangs (delay 2s) with an engine
// timeout of 100ms must be reported unresponsive with reason "timeout"
// and the whole search must finish well under 5 seconds.
func TestEngineTimeout(t *testing.T) {
	reg := engine.New()
	hung := &fakeEngine{delay: 2 * time.Second, results: []*result.RawResult{fakeMain("e1", "http://one.example/", "one")}}
	reg.Register("e1", func(*config.EngineConfig) (engine.Engine, error) { return hung, nil })

	cfg := &config.Config{
		Search: config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120},
		Engines: []config.EngineConfig{
			{Name: "e1", Engine: "e1", Timeout: 0.1, Weight: 1.0},
		},
	}
	svc := New(reg, testBangStore{}, testCatalog{names: []string{"e1"}}, cfg, &fakeRequester{resp: fakeHTTPResponse()}, nil)

	start := time.Now()
	container := svc.Search(testRawQuery(t, "hola"))
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Fatalf("search with a hung engine took %v, want < 5s (CA-004)", elapsed)
	}

	found := false
	for _, u := range container.Unresponsive() {
		if u.Name == "e1" && u.Reason == "timeout" {
			found = true
		}
	}
	if !found {
		t.Fatalf("engine e1 not reported unresponsive with reason \"timeout\"; got %+v (CA-004)", container.Unresponsive())
	}
}

// TestSkipSuspended: a suspended engine is never invoked again (REQ-008,
// port of extend_container_if_suspended / the _get_requests skip).
func TestSkipSuspended(t *testing.T) {
	fe := &fakeEngine{results: []*result.RawResult{fakeMain("e1", "http://one.example/", "one")}}
	svc := newTestService(t, map[string]*fakeEngine{"e1": fe}, nil, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})

	// Suspend the engine manually (e.g. after an init failure).
	svc.Processor("e1").suspended.Suspend("init")
	if !svc.Processor("e1").IsSuspended() {
		t.Fatal("engine should be suspended")
	}

	container := svc.Search(testRawQuery(t, "hola"))
	container.Close(1.0, nil)

	if fe.callCount() != 0 {
		t.Fatalf("suspended engine was called %d times, want 0", fe.callCount())
	}
	if got := container.GetOrderedResults(); len(got) != 0 {
		t.Fatalf("suspended engine produced %d results, want 0", len(got))
	}
}

// ---------------------------------------------------------------------------
// GetParams

func TestGetParamsPaging(t *testing.T) {
	t.Run("page 1 without paging support still yields params", func(t *testing.T) {
		p := NewProcessor(&fakeEngine{name: "e1"}, &config.EngineConfig{}, nil)
		params, err := p.GetParams(&SearchQuery{Query: "q", Pageno: 1}, "none")
		if err != nil {
			t.Fatalf("GetParams error: %v", err)
		}
		if params == nil {
			t.Fatal("GetParams returned nil for page 1 (always supported)")
		}
		if params.Pageno != 1 || params.Method != "GET" {
			t.Fatalf("params = %+v, want Pageno 1 / Method GET", params)
		}
	})

	t.Run("page >1 without paging support is skipped", func(t *testing.T) {
		p := NewProcessor(&fakeEngine{name: "e1"}, &config.EngineConfig{}, nil)
		params, err := p.GetParams(&SearchQuery{Query: "q", Pageno: 2}, "none")
		if err != nil {
			t.Fatalf("GetParams error: %v", err)
		}
		if params != nil {
			t.Fatalf("GetParams = %+v, want nil (engine without paging support)", params)
		}
	})

	t.Run("page >1 with paging override yields params", func(t *testing.T) {
		p := NewProcessor(&fakeEngine{name: "e1"}, &config.EngineConfig{Overrides: map[string]any{"paging": true}}, nil)
		params, err := p.GetParams(&SearchQuery{Query: "q", Pageno: 2}, "none")
		if err != nil {
			t.Fatalf("GetParams error: %v", err)
		}
		if params == nil {
			t.Fatal("GetParams returned nil although the engine supports paging")
		}
		if params.Pageno != 2 {
			t.Fatalf("params.Pageno = %d, want 2", params.Pageno)
		}
	})
}

// ---------------------------------------------------------------------------
// pipeline integrity: error handling

// TestNonSuspendableErrorDoesNotSuspend: a generic Request error marks the
// engine unresponsive but must NOT suspend it.
func TestNonSuspendableErrorDoesNotSuspend(t *testing.T) {
	svc := newTestService(t, map[string]*fakeEngine{
		"e1": {failMode: "error"},
	}, nil, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})

	container := svc.Search(testRawQuery(t, "hola"))

	proc := svc.Processor("e1")
	if proc.IsSuspended() {
		t.Fatal("engine must not be suspended on a generic error (only 429/403 class)")
	}
	found := false
	for _, u := range container.Unresponsive() {
		if u.Name == "e1" {
			found = true
			if u.Reason != "engine e1 failed" {
				t.Fatalf("unresponsive reason = %q, want %q", u.Reason, "engine e1 failed")
			}
		}
	}
	if !found {
		t.Fatalf("engine e1 not reported unresponsive; got %+v", container.Unresponsive())
	}
}

// TestSuspendableErrorSuspends verifies the full suspend path through the
// pipeline: requester error -> Search wraps as errSuspendable ->
// HandleException suspends with the EngineSuspendError.Reason.
func TestSuspendableErrorSuspends(t *testing.T) {
	suspendErr := &engine.EngineSuspendError{Reason: "too_many_requests"}
	svc := newTestService(t, map[string]*fakeEngine{
		"e1": {results: []*result.RawResult{fakeMain("e1", "http://one.example/", "one")}},
	}, &fakeRequester{err: suspendErr}, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})

	container := svc.Search(testRawQuery(t, "hola"))

	proc := svc.Processor("e1")
	if !proc.IsSuspended() {
		t.Fatal("engine should be suspended on a 429/403 class failure (REQ-008)")
	}
	if got, want := proc.suspended.SuspendReason(), "too_many_requests"; got != want {
		t.Fatalf("SuspendReason = %q, want %q", got, want)
	}
	for _, u := range container.Unresponsive() {
		if u.Name == "e1" {
			if u.Reason != suspendErr.Error() {
				t.Fatalf("unresponsive reason = %q, want %q", u.Reason, suspendErr.Error())
			}
			return
		}
	}
	t.Fatal("engine e1 not reported unresponsive")
}

// TestSearchWithAnswerers: when answerers produce results the standard
// search is skipped and their raw results land in the container.
func TestSearchWithAnswerers(t *testing.T) {
	answerer := &testAnswerer{}
	storage := NewAnswererStorage()
	storage.Register(answerer)

	fe := &fakeEngine{results: []*result.RawResult{fakeMain("e1", "http://one.example/", "one")}}
	svc := newTestService(t, map[string]*fakeEngine{"e1": fe}, nil, config.Search{BanTimeOnFail: 5, MaxBanTimeOnFail: 120})
	svc.answerers = storage

	container := svc.Search(testRawQuery(t, "hola"))

	if fe.callCount() != 0 {
		t.Fatalf("standard search ran engine %d times with answerers present, want 0", fe.callCount())
	}
	// The fake answerer returns a correction-style raw result.
	if len(container.suggestions) != 1 || container.suggestions[0] != "fake suggestion" {
		t.Fatalf("suggestions = %v, want [fake suggestion]", container.suggestions)
	}
}

// testAnswerer is a scripted Answerer (named testAnswerer: a fakeAnswerer
// already exists in container_test.go).
type testAnswerer struct{}

func (testAnswerer) Name() string { return "fakeAnswerer" }
func (testAnswerer) Ask(context.Context, string) []*result.RawResult {
	return []*result.RawResult{result.NewSuggestion("fake suggestion")}
}

// compile-time checks that the fakes satisfy their contracts.
var (
	_ engine.Engine = (*fakeEngine)(nil)
	_ Requester     = (*fakeRequester)(nil)
	_ BangResolver  = testBangStore{}
	_ EngineCatalog = testCatalog{}
	_ Answerer      = testAnswerer{}
)

// silence the unused import if http tests get pruned later
var _ = errors.Is
