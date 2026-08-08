package search

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// loadEngine is a deterministic fake engine used for load/concurrency tests.
//
// Unlike fakeEngine (search_test.go), loadEngine builds FRESH RawResults on
// every Response call. This mirrors a real engine, which parses the HTML of
// each request and allocates new results per call — and it is REQUIRED here:
// SearchService keeps pointers to *result.MainResult in the container, and
// result.Merge mutates them in place, so reusing a static results slice across
// concurrent searches would share mutable pointers between containers and
// produce false data races under -race.
type loadEngine struct {
	name      string
	delay     time.Duration // simulated latency per request
	perEngine int           // unique results returned per call
	shared    bool          // also emit one result on the shared URL
}

func (e *loadEngine) Name() string     { return e.name }
func (e *loadEngine) Shortcut() string { return "l" + e.name }
func (e *loadEngine) Categories() []string {
	return []string{"all"}
}
func (e *loadEngine) NeedsInit() bool { return false }
func (e *loadEngine) Setup(_ context.Context, _ *config.EngineConfig) error {
	return nil
}
func (e *loadEngine) Init(_ context.Context) error { return nil }

func (e *loadEngine) Request(_ string, _ *engine.RequestParams) error {
	if e.delay > 0 {
		time.Sleep(e.delay)
	}
	return nil
}

// Response builds fresh results on every call (see doc comment above).
func (e *loadEngine) Response(_ *http.Response) ([]*result.RawResult, error) {
	raws := make([]*result.RawResult, 0, e.perEngine+1)
	for j := 0; j < e.perEngine; j++ {
		raws = append(raws, result.NewMain(&result.MainResult{
			Title:     e.name + "-" + strconv.Itoa(j),
			Content:   "content from " + e.name + " #" + strconv.Itoa(j),
			URL:       "https://load.example/" + e.name + "/" + strconv.Itoa(j),
			Engines:   []string{e.name},
			Positions: []int{1},
		}))
	}
	if e.shared {
		// Shared URL: every engine returns the same URL so the container
		// merges it concurrently and the engine list must end up as a
		// set-union of all engine names, without duplicates.
		raws = append(raws, result.NewMain(&result.MainResult{
			Title:     "shared",
			Content:   "shared result merged across engines",
			URL:       "https://load.example/shared",
			Engines:   []string{e.name},
			Positions: []int{1},
		}))
	}
	return raws, nil
}

// buildLoadService constructs a SearchService wired with loadEngines.
// It deliberately does NOT take *testing.T so benchmarks can reuse it.
//
// Returns the service, the engine list and the per-search expected counts:
// unique results = E*perEngine, plus 1 merged shared result.
func buildLoadService(numEngines, perEngine int) (*SearchService, []*loadEngine, int) {
	reg := engine.New()
	engines := make([]*loadEngine, 0, numEngines)
	ecfgs := make([]config.EngineConfig, 0, numEngines)
	names := make([]string, 0, numEngines)
	for i := 0; i < numEngines; i++ {
		le := &loadEngine{
			name:      fmt.Sprintf("load-e%d", i),
			delay:     time.Duration(1+(i%5)) * time.Millisecond, // 1..5ms simulated latency
			perEngine: perEngine,
			shared:    true,
		}
		engines = append(engines, le)
		name := le.name
		reg.Register(name, func(cfg *config.EngineConfig) (engine.Engine, error) {
			return engines[indexOfLoadEngine(engines, cfg.Name)], nil
		})
		ecfgs = append(ecfgs, config.EngineConfig{Name: name, Engine: name, Weight: 1.0})
		names = append(names, name)
	}
	cfg := &config.Config{
		Search:  config.Search{SafeSearch: 0},
		Engines: ecfgs,
	}
	svc := New(reg, testBangStore{}, testCatalog{names: names}, cfg, &fakeRequester{resp: fakeHTTPResponse()}, nil)
	return svc, engines, numEngines*perEngine + 1
}

func indexOfLoadEngine(engines []*loadEngine, name string) int {
	for i, e := range engines {
		if e.name == name {
			return i
		}
	}
	return 0
}

// rawTextQueryForLoad builds a RawTextQuery without *testing.T so it works
// in benchmarks too. The catalog lists exactly the load engines.
func rawTextQueryForLoad(names []string, query string) *RawTextQuery {
	return Parse(query, testBangStore{}, testCatalog{names: names})
}

// TestLoadConcurrencyLevels hammers the search pipeline with increasing
// concurrency levels against a single shared SearchService, using
// deterministic fake engines with simulated latency (1-5ms).
//
// For each level, N=50 searches are spread over the level's goroutines and
// we verify: no errors, no panics, every container holds the exact expected
// result count (no lost updates), the shared URL is merged into a single
// result whose Engines field is the set-union of all engine names without
// duplicates, and no engine was reported unresponsive.
//
// The elapsed time per level is logged so the report can answer "how much
// concurrency does the engine support".
func TestLoadConcurrencyLevels(t *testing.T) {
	const (
		numEngines = 8
		perEngine  = 4
		searches   = 50
	)
	levels := []int{1, 4, 8, 16, 32, 64}

	svc, engines, wantResults := buildLoadService(numEngines, perEngine)
	// Shared result must carry exactly the union of all engine names.
	wantEngines := make([]string, 0, numEngines)
	for _, e := range engines {
		wantEngines = append(wantEngines, e.name)
	}
	raw := rawTextQueryForLoad(engineNames(engines), "concurrency load test")

	for _, level := range levels {
		level := level
		t.Run(fmt.Sprintf("level_%d", level), func(t *testing.T) {
			// Defend against false positives: if a previous level left the
			// service suspended, the pipeline would skip engines and counts
			// would drop — fail fast instead of reporting phantom results.
			start := time.Now()

			var wg sync.WaitGroup
			errCh := make(chan error, searches)
			panicCh := make(chan any, level)
			resultsPer := make([]int, level)

			perWorker := searches / level
			remainder := searches % level

			for w := 0; w < level; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					defer func() {
						if r := recover(); r != nil {
							panicCh <- r
						}
					}()
					n := perWorker
					if w < remainder {
						n++
					}
					count := 0
					for i := 0; i < n; i++ {
						container := svc.Search(raw)
						if container == nil {
							errCh <- fmt.Errorf("worker %d: nil container", w)
							return
						}
						ordered := container.GetOrderedResults()
						if len(ordered) != wantResults {
							errCh <- fmt.Errorf("worker %d: got %d results, want %d", w, len(ordered), wantResults)
							return
						}
						count += len(ordered)
						// Shared URL must be a single merged result whose
						// Engines is exactly the set-union, no duplicates.
						sharedFound := false
						for _, mr := range ordered {
							if mr.URL == "https://load.example/shared" {
								sharedFound = true
								if len(mr.Engines) != len(wantEngines) {
									errCh <- fmt.Errorf("worker %d: shared result has %d engines, want %d: %v", w, len(mr.Engines), len(wantEngines), mr.Engines)
									return
								}
								for _, en := range wantEngines {
									if !has(mr.Engines, en) {
										errCh <- fmt.Errorf("worker %d: shared result missing engine %q (got %v)", w, en, mr.Engines)
										return
									}
								}
								// No duplicates allowed.
								seen := make(map[string]bool, len(mr.Engines))
								for _, en := range mr.Engines {
									if seen[en] {
										errCh <- fmt.Errorf("worker %d: shared result has duplicate engine %q", w, en)
										return
									}
									seen[en] = true
								}
							}
						}
						if !sharedFound {
							errCh <- fmt.Errorf("worker %d: shared URL missing from ordered results", w)
							return
						}
						if un := container.Unresponsive(); len(un) != 0 {
							errCh <- fmt.Errorf("worker %d: %d unresponsive engines: %+v", w, len(un), un)
							return
						}
					}
					resultsPer[w] = count
				}(w)
			}

			wg.Wait()
			close(errCh)
			close(panicCh)
			elapsed := time.Since(start)

			for err := range errCh {
				t.Error(err)
			}
			for r := range panicCh {
				t.Errorf("level %d: panic during search: %v", level, r)
			}

			// Cross-check totals: every worker must have produced exactly
			// its share of results (n searches x wantResults each).
			total := 0
			for _, c := range resultsPer {
				total += c
			}
			if total != searches*wantResults {
				t.Errorf("level %d: total results %d, want %d", level, total, searches*wantResults)
			}

			t.Logf("level=%-2d goroutines=%3d searches=%d elapsed=%v (%.1f searches/s)",
				level, level, searches, elapsed, float64(searches)/elapsed.Seconds())
		})
	}
}

// TestLoadConcurrencyHighLevel is an extra stress run beyond the levels in
// TestLoadConcurrencyLevels: 128 goroutines x 50 searches = 6400 searches
// against the same service. If the pipeline starts failing at this level,
// this test marks it.
func TestLoadConcurrencyHighLevel(t *testing.T) {
	const (
		numEngines = 8
		perEngine  = 4
		searches   = 50
		level      = 128
	)

	svc, engines, wantResults := buildLoadService(numEngines, perEngine)
	wantEngines := engineNames(engines)
	raw := rawTextQueryForLoad(wantEngines, "high level concurrency load test")

	start := time.Now()
	var wg sync.WaitGroup
	errCh := make(chan error, searches)
	perWorker := searches / level
	remainder := searches % level

	for w := 0; w < level; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			n := perWorker
			if w < remainder {
				n++
			}
			for i := 0; i < n; i++ {
				container := svc.Search(raw)
				if container == nil {
					errCh <- fmt.Errorf("worker %d: nil container", w)
					return
				}
				ordered := container.GetOrderedResults()
				if len(ordered) != wantResults {
					errCh <- fmt.Errorf("worker %d: got %d results, want %d", w, len(ordered), wantResults)
					return
				}
				for _, mr := range ordered {
					if mr.URL == "https://load.example/shared" {
						if len(mr.Engines) != len(wantEngines) {
							errCh <- fmt.Errorf("worker %d: shared result has %d engines, want %d", w, len(mr.Engines), len(wantEngines))
							return
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	elapsed := time.Since(start)

	for err := range errCh {
		t.Error(err)
	}
	t.Logf("level=%d goroutines=%3d searches=%d elapsed=%v (%.1f searches/s)",
		level, level, searches, elapsed, float64(searches)/elapsed.Seconds())
}

func engineNames(engines []*loadEngine) []string {
	names := make([]string, 0, len(engines))
	for _, e := range engines {
		names = append(names, e.name)
	}
	return names
}
