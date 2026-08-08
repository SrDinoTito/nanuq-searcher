package search

import (
	"context"
	"testing"

	"nanuq-engine/internal/result"
)

// --- ResultContainer: Extend dispatch (TASK-006 part A, REQ-009) ---

// TestExtendMainDedup verifies the KindMain branch: Extend attributes the
// caller engine name to each result (result['engine'] = engine_name,
// results.py L88), and two results with the same URL are merged into a
// single map entry (dedup by URL, EC-006) — the attributed Engines sets
// are unioned (origin first), the longest content wins and the positions
// are accumulated (result.Merge; _merge_main_result, results.py L167-181).
func TestExtendMainDedup(t *testing.T) {
	c := NewResultContainer()

	first := result.NewMain(&result.MainResult{
		Title: "Example", Content: "short", URL: "https://example.com",
		Positions: []int{1},
	})
	second := result.NewMain(&result.MainResult{
		Title: "Example", Content: "a much longer content", URL: "https://example.com",
		Positions: []int{2},
	})

	// The first Extend stamps "google" onto the stored result.
	c.Extend("google", []*result.RawResult{first})
	if m := c.mainResultsMap["https://example.com"]; m == nil {
		t.Fatal("main result not found after the first Extend")
	} else if len(m.Engines) != 1 || m.Engines[0] != "google" {
		t.Errorf("after Extend(\"google\") Engines = %v, want [google] (attributed)", m.Engines)
	}

	c.Extend("ddg", []*result.RawResult{second})

	if len(c.mainResultsMap) != 1 {
		t.Fatalf("mainResultsMap has %d entries, want 1 (dedup by URL)", len(c.mainResultsMap))
	}
	m := c.mainResultsMap["https://example.com"]
	if m == nil {
		t.Fatal("merged main result not found under its URL")
	}
	if len(m.Engines) != 2 || m.Engines[0] != "google" || m.Engines[1] != "ddg" {
		t.Errorf("Engines = %v, want [google ddg] (set union, origin first)", m.Engines)
	}
	if m.Content != "a much longer content" {
		t.Errorf("Content = %q, want the longer of the two", m.Content)
	}
	if len(m.Positions) != 2 || m.Positions[0] != 1 || m.Positions[1] != 2 {
		t.Errorf("Positions = %v, want [1 2] (accumulated)", m.Positions)
	}

	// A third, different URL adds a second entry, also attributed.
	c.Extend("ddg", []*result.RawResult{result.NewMain(&result.MainResult{
		URL: "https://other.example", Title: "Other",
	})})
	if len(c.mainResultsMap) != 2 {
		t.Errorf("mainResultsMap has %d entries, want 2", len(c.mainResultsMap))
	}
	if om := c.mainResultsMap["https://other.example"]; om == nil {
		t.Errorf("other.example main result not found")
	} else if len(om.Engines) != 1 || om.Engines[0] != "ddg" {
		t.Errorf("other.example Engines = %v, want [ddg] (attributed)", om.Engines)
	}
}

// TestExtendMainEmptyEngineName verifies the answerer path: Extend with an
// empty engineName (search.go searchAnswerers) leaves the result's Engines
// untouched — mirroring extend(), which only assigns result['engine'] for
// real engines (results.py L88).
func TestExtendMainEmptyEngineName(t *testing.T) {
	c := NewResultContainer()
	c.Extend("", []*result.RawResult{result.NewMain(&result.MainResult{
		Title: "T", URL: "https://answer.example",
	})})

	m := c.mainResultsMap["https://answer.example"]
	if m == nil {
		t.Fatal("main result not found after Extend with empty engine name")
	}
	if len(m.Engines) != 0 {
		t.Errorf("Engines = %v, want nil/empty (answerer results are not attributed)", m.Engines)
	}
}

// TestExtendMainEngineNotDuplicated verifies the attribution edge cases: a
// result that already carries engines gets the caller name appended when it
// is new (results.py L88 stamps a set), and never duplicated when already
// present.
func TestExtendMainEngineNotDuplicated(t *testing.T) {
	c := NewResultContainer()

	// Pre-set engines: the caller name is appended when absent.
	c.Extend("google", []*result.RawResult{result.NewMain(&result.MainResult{
		Title: "T1", URL: "https://one.example", Engines: []string{"bing"},
	})})
	if m := c.mainResultsMap["https://one.example"]; m == nil {
		t.Error("one.example main result not found")
	} else if len(m.Engines) != 2 || m.Engines[0] != "bing" || m.Engines[1] != "google" {
		t.Errorf("Engines = %v, want [bing google] (caller name appended)", m.Engines)
	}

	// Caller name already present: no duplicate is added.
	c.Extend("ddg", []*result.RawResult{result.NewMain(&result.MainResult{
		Title: "T2", URL: "https://two.example", Engines: []string{"ddg"},
	})})
	if m := c.mainResultsMap["https://two.example"]; m == nil {
		t.Error("two.example main result not found")
	} else if len(m.Engines) != 1 || m.Engines[0] != "ddg" {
		t.Errorf("Engines = %v, want [ddg] (no duplicate)", m.Engines)
	}
}

// TestExtendAnswer verifies the KindAnswer branch (BaseAnswer,
// results.py L94-98): every Answer of the AnswerSet is appended.
func TestExtendAnswer(t *testing.T) {
	c := NewResultContainer()
	c.Extend("ans", []*result.RawResult{
		result.NewAnswer(&result.AnswerSet{
			Answers: []result.Answer{{Title: "A1", Content: "C1"}, {Title: "A2", Content: "C2"}},
		}),
	})

	if len(c.answers) != 2 {
		t.Fatalf("answers has %d entries, want 2", len(c.answers))
	}
	if c.answers[0].Title != "A1" || c.answers[1].Content != "C2" {
		t.Errorf("answers = %+v, want [A1 C2] payloads", c.answers)
	}

	// A nil AnswerSet payload is tolerated.
	c.Extend("ans", []*result.RawResult{{Kind: result.KindAnswer}})
	if len(c.answers) != 2 {
		t.Errorf("answers has %d entries after nil AnswerSet, want 2", len(c.answers))
	}
}

// TestExtendCorrection verifies the KindCorrection branch ('correction'
// legacy dict, results.py L132-133).
func TestExtendCorrection(t *testing.T) {
	c := NewResultContainer()
	c.Extend("e", []*result.RawResult{result.NewCorrection("did you mean X")})

	if len(c.corrections) != 1 || c.corrections[0] != "did you mean X" {
		t.Errorf("corrections = %v, want [did you mean X]", c.corrections)
	}
}

// TestExtendSuggestion verifies the KindSuggestion branch ('suggestion'
// legacy dict, results.py L127-129).
func TestExtendSuggestion(t *testing.T) {
	c := NewResultContainer()
	c.Extend("e", []*result.RawResult{result.NewSuggestion("try also Y")})

	if len(c.suggestions) != 1 || c.suggestions[0] != "try also Y" {
		t.Errorf("suggestions = %v, want [try also Y]", c.suggestions)
	}
}

// TestExtendInfobox verifies the KindInfobox branch ('infobox' legacy
// dict, results.py L135-138).
func TestExtendInfobox(t *testing.T) {
	c := NewResultContainer()
	ib := &result.Infobox{Title: "IB", Content: "C", URLs: []string{"https://x.example"}}
	c.Extend("e", []*result.RawResult{result.NewInfobox(ib)})

	if len(c.infoboxes) != 1 {
		t.Fatalf("infoboxes has %d entries, want 1", len(c.infoboxes))
	}
	if c.infoboxes[0].Title != "IB" || len(c.infoboxes[0].URLs) != 1 {
		t.Errorf("infoboxes[0] = %+v, want title IB with 1 URL", c.infoboxes[0])
	}
}

// TestExtendEngineData verifies the KindEngineData branch ('engine_data'
// legacy dict, results.py L140-147): the payload is stored keyed by the
// engine name carried in Str, falling back to the engine name passed to
// Extend (result.engine or engine_name, results.py L88).
func TestExtendEngineData(t *testing.T) {
	c := NewResultContainer()
	payload := map[string]any{"currency": "EUR"}
	c.Extend("ddg", []*result.RawResult{result.NewEngineData("ddg", payload)})

	if got, ok := c.engineData["ddg"].(map[string]any); !ok || got["currency"] != "EUR" {
		t.Errorf("engineData[ddg] = %v, want map with currency=EUR", c.engineData["ddg"])
	}

	// No engine name in the RawResult: fall back to the engineName arg.
	c.Extend("fallback", []*result.RawResult{
		{Kind: result.KindEngineData, Data: payload},
	})
	if got, ok := c.engineData["fallback"].(map[string]any); !ok || got["currency"] != "EUR" {
		t.Errorf("engineData[fallback] = %v, want map with currency=EUR", c.engineData["fallback"])
	}
}

// TestExtendIgnoredKinds verifies that the typed result kinds which are
// not part of the search pipeline yet (TODO(TASK-006, phase B)) are
// silently skipped without touching any collection.
func TestExtendIgnoredKinds(t *testing.T) {
	c := NewResultContainer()
	c.Extend("e", []*result.RawResult{
		result.NewKeyValue("k", "v"),
		result.NewCode("t", "c", "go", "code"),
		result.NewPaper("t", "c", "u", "authors"),
		result.NewFile("t", "c", "u"),
		result.NewTranslations(&result.Translations{Translations: []string{"hola"}, Source: "es", Target: "en"}),
		result.NewWeather(&result.WeatherAnswer{Temperature: "20", Condition: "sunny", Location: "BCN", Units: "C"}),
	})

	if len(c.mainResultsMap) != 0 || len(c.answers) != 0 || len(c.infoboxes) != 0 ||
		len(c.corrections) != 0 || len(c.suggestions) != 0 || len(c.engineData) != 0 {
		t.Errorf("ignored kinds leaked into the container: map=%d answers=%d infoboxes=%d corrections=%d suggestions=%d engineData=%d",
			len(c.mainResultsMap), len(c.answers), len(c.infoboxes), len(c.corrections), len(c.suggestions), len(c.engineData))
	}
}

// --- ResultContainer: Close scoring (TASK-006, REQ-010, DSG-010) ---

// TestCloseScores verifies the Close port of close() (results.py
// L183-189): score = CalculateScore(weight * len(r.Positions),
// r.Positions). With a result at positions [1,2] and weight 0.5 the
// effective weight is 0.5*2 = 1.0, so the score is 1.0/1 + 1.0/2 =
// 1.0 + 0.5 = 1.5 — the TASK-006 criterion ("weight 1.0 → score 1.0+0.5"
// refers to the effective weight of 1.0; the combined engine weight is
// applied by the caller by multiplying, per the Close contract).
func TestCloseScores(t *testing.T) {
	c := NewResultContainer()
	c.Extend("e", []*result.RawResult{result.NewMain(&result.MainResult{
		Title: "T", URL: "https://example.com", Engines: []string{"e"},
		Positions: []int{1, 2},
	})})

	c.Close(0.5, []int{1, 2})

	m := c.mainResultsMap["https://example.com"]
	if m == nil {
		t.Fatal("main result not found after Close")
	}
	if m.Score != 1.5 {
		t.Errorf("Score = %v, want 1.5 (= 1.0/1 + 1.0/2)", m.Score)
	}
}

// TestGetOrderedResults verifies the GetOrderedResults port
// (get_ordered_results, results.py L191-247): results are returned
// ordered by score descending, the grouping honours the pre-set Category,
// and the container map is left untouched.
func TestGetOrderedResults(t *testing.T) {
	c := NewResultContainer()
	c.Extend("e", []*result.RawResult{
		result.NewMain(&result.MainResult{
			Title: "Low", URL: "https://low.example", Engines: []string{"e"},
			Positions: []int{1}, Category: "general",
		}),
		result.NewMain(&result.MainResult{
			Title: "High", URL: "https://high.example", Engines: []string{"e"},
			Positions: []int{1, 2}, Category: "general",
		}),
	})
	// Low scores 0.5*1/1 = 0.5; High scores 0.5*2/1 + 0.5*2/2 = 1.5.
	c.Close(0.5, nil)

	ordered := c.GetOrderedResults()
	if len(ordered) != 2 {
		t.Fatalf("GetOrderedResults returned %d results, want 2", len(ordered))
	}
	if ordered[0].Title != "High" || ordered[1].Title != "Low" {
		t.Errorf("order = [%s %s], want [High Low] (score descending)", ordered[0].Title, ordered[1].Title)
	}
	if ordered[0].Score != 1.5 || ordered[1].Score != 0.5 {
		t.Errorf("scores = [%v %v], want [1.5 0.5]", ordered[0].Score, ordered[1].Score)
	}

	// The map is not mutated by GetOrderedResults.
	if len(c.mainResultsMap) != 2 {
		t.Errorf("mainResultsMap has %d entries after GetOrderedResults, want 2", len(c.mainResultsMap))
	}

	// Without a prior Close the scores are zero and the order is stable
	// but unspecified (equal scores); the method must not panic.
	c2 := NewResultContainer()
	c2.Extend("e", []*result.RawResult{result.NewMain(&result.MainResult{
		Title: "X", URL: "https://x.example", Engines: []string{"e"}, Category: "general",
	})})
	if got := c2.GetOrderedResults(); len(got) != 1 {
		t.Errorf("GetOrderedResults before Close returned %d results, want 1", len(got))
	}
}

// --- ResultContainer: redirect URL and unresponsive engines ---

// TestRedirectURL verifies the RedirectURL/SetRedirectURL accessors
// (results.py redirect_url).
func TestRedirectURL(t *testing.T) {
	c := NewResultContainer()
	if got := c.RedirectURL(); got != "" {
		t.Errorf("RedirectURL() = %q, want empty on a fresh container", got)
	}
	c.SetRedirectURL("https://redirect.example")
	if got := c.RedirectURL(); got != "https://redirect.example" {
		t.Errorf("RedirectURL() = %q, want https://redirect.example", got)
	}
}

// TestAddUnresponsiveEngine verifies AddUnresponsiveEngine
// (add_unresponsive_engine, results.py L249-255): every failure is
// recorded (display_error_messages is always true in this phase).
func TestAddUnresponsiveEngine(t *testing.T) {
	c := NewResultContainer()
	c.AddUnresponsiveEngine("ddg", "timeout")
	c.AddUnresponsiveEngine("google", "429")

	if len(c.unresponsive) != 2 {
		t.Fatalf("unresponsive has %d entries, want 2", len(c.unresponsive))
	}
	if c.unresponsive[0].Name != "ddg" || c.unresponsive[0].Reason != "timeout" {
		t.Errorf("unresponsive[0] = %+v, want {ddg timeout}", c.unresponsive[0])
	}
	if c.unresponsive[1].Name != "google" || c.unresponsive[1].Reason != "429" {
		t.Errorf("unresponsive[1] = %+v, want {google 429}", c.unresponsive[1])
	}
}

// TestReset verifies that Reset returns the container to its empty state
// (no Python counterpart; provided for test reuse).
func TestReset(t *testing.T) {
	c := NewResultContainer()
	c.Extend("e", []*result.RawResult{result.NewMain(&result.MainResult{
		Title: "T", URL: "https://x.example", Engines: []string{"e"},
	})})
	c.SetRedirectURL("https://r.example")
	c.AddUnresponsiveEngine("e", "timeout")

	c.Reset()

	if len(c.mainResultsMap) != 0 || len(c.answers) != 0 || len(c.infoboxes) != 0 ||
		len(c.corrections) != 0 || len(c.suggestions) != 0 || len(c.engineData) != 0 ||
		len(c.unresponsive) != 0 || len(c.timings) != 0 {
		t.Error("Reset left state behind")
	}
	if c.RedirectURL() != "" {
		t.Errorf("RedirectURL() = %q after Reset, want empty", c.RedirectURL())
	}
}

// --- AnswererStorage (TASK-006 part A, REQ-009) ---

// fakeAnswerer is a minimal Answerer for tests.
type fakeAnswerer struct {
	name string
	raws []*result.RawResult
}

func (f *fakeAnswerer) Name() string                                        { return f.name }
func (f *fakeAnswerer) Ask(_ context.Context, _ string) []*result.RawResult { return f.raws }

// TestAnswererStorage verifies NewAnswererStorage/Register/Ask: an empty
// storage returns nil; registered answerers contribute their results in
// any registration order; re-registering a Name replaces the answerer.
func TestAnswererStorage(t *testing.T) {
	s := NewAnswererStorage()
	if got := s.Ask(context.Background(), "query"); got != nil {
		t.Errorf("Ask on empty storage = %v, want nil", got)
	}

	a1 := &fakeAnswerer{name: "one", raws: []*result.RawResult{result.NewSuggestion("s1")}}
	a2 := &fakeAnswerer{name: "two", raws: []*result.RawResult{result.NewSuggestion("s2"), result.NewSuggestion("s3")}}
	s.Register(a1)
	s.Register(a2)

	got := s.Ask(context.Background(), "query")
	if len(got) != 3 {
		t.Fatalf("Ask returned %d results, want 3 (concatenated)", len(got))
	}

	// Re-registering "one" with no results removes its contribution.
	s.Register(&fakeAnswerer{name: "one", raws: nil})
	if got := s.Ask(context.Background(), "query"); len(got) != 2 {
		t.Errorf("Ask after replacement returned %d results, want 2", len(got))
	}
}
