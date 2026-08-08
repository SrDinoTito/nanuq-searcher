package search

import (
	"testing"
	"time"

	"nanuq-engine/internal/result"
)

// --- ResultContainer read accessors (TASK-012b, CA-003) ---

// TestAccessors verifies the public read accessors Answers, Corrections,
// Infoboxes, Suggestions and Timings: they return the payloads collected
// by Extend/AddTiming, following the lock+copy pattern (a fresh, non-nil
// empty slice is returned for untouched collections).
func TestAccessors(t *testing.T) {
	c := NewResultContainer()
	c.Extend("e", []*result.RawResult{
		result.NewAnswer(&result.AnswerSet{
			Answers: []result.Answer{{Title: "A", Content: "C"}},
		}),
		result.NewCorrection("did you mean X"),
		result.NewSuggestion("try also Y"),
		result.NewInfobox(&result.Infobox{Title: "IB", Content: "IB content"}),
	})
	c.AddTiming("e", 5*time.Millisecond)

	if got := c.Answers(); len(got) != 1 || got[0].Title != "A" || got[0].Content != "C" {
		t.Errorf("Answers() = %+v, want [{A C}]", got)
	}
	if got := c.Corrections(); len(got) != 1 || got[0] != "did you mean X" {
		t.Errorf("Corrections() = %v, want [did you mean X]", got)
	}
	if got := c.Suggestions(); len(got) != 1 || got[0] != "try also Y" {
		t.Errorf("Suggestions() = %v, want [try also Y]", got)
	}
	if got := c.Infoboxes(); len(got) != 1 || got[0].Title != "IB" || got[0].Content != "IB content" {
		t.Errorf("Infoboxes() = %+v, want [{IB IB content}]", got)
	}
	if got := c.Timings(); len(got) != 1 || got[0].EngineName != "e" || got[0].Duration != 5*time.Millisecond {
		t.Errorf("Timings() = %+v, want [{e 5ms}]", got)
	}

	// A fresh container yields empty (non-nil) slices for every accessor.
	c2 := NewResultContainer()
	if got := c2.Answers(); got == nil || len(got) != 0 {
		t.Errorf("Answers() on fresh container = %v, want empty non-nil slice", got)
	}
	if got := c2.Corrections(); got == nil || len(got) != 0 {
		t.Errorf("Corrections() on fresh container = %v, want empty non-nil slice", got)
	}
	if got := c2.Suggestions(); got == nil || len(got) != 0 {
		t.Errorf("Suggestions() on fresh container = %v, want empty non-nil slice", got)
	}
	if got := c2.Infoboxes(); got == nil || len(got) != 0 {
		t.Errorf("Infoboxes() on fresh container = %v, want empty non-nil slice", got)
	}
	if got := c2.Timings(); got == nil || len(got) != 0 {
		t.Errorf("Timings() on fresh container = %v, want empty non-nil slice", got)
	}
}

// TestAccessorsReturnCopy verifies the lock+copy contract: mutating the
// returned slice must not leak back into the container's internal state.
func TestAccessorsReturnCopy(t *testing.T) {
	c := NewResultContainer()
	c.Extend("e", []*result.RawResult{
		result.NewAnswer(&result.AnswerSet{
			Answers: []result.Answer{{Title: "orig"}},
		}),
		result.NewCorrection("orig"),
		result.NewSuggestion("orig"),
		result.NewInfobox(&result.Infobox{Title: "orig"}),
	})
	c.AddTiming("e", 1*time.Millisecond)

	c.Answers()[0].Title = "mutated"
	c.Corrections()[0] = "mutated"
	c.Suggestions()[0] = "mutated"
	c.Infoboxes()[0].Title = "mutated"
	c.Timings()[0] = Timing{EngineName: "mutated", Duration: 0}

	if got := c.Answers(); got[0].Title != "orig" {
		t.Errorf("Answers() = %+v, want [orig] (caller mutation must not leak)", got)
	}
	if got := c.Corrections(); got[0] != "orig" {
		t.Errorf("Corrections() = %v, want [orig] (caller mutation must not leak)", got)
	}
	if got := c.Suggestions(); got[0] != "orig" {
		t.Errorf("Suggestions() = %v, want [orig] (caller mutation must not leak)", got)
	}
	if got := c.Infoboxes(); got[0].Title != "orig" {
		t.Errorf("Infoboxes() = %+v, want [orig] (caller mutation must not leak)", got)
	}
	if got := c.Timings(); got[0].EngineName != "e" || got[0].Duration != 1*time.Millisecond {
		t.Errorf("Timings() = %+v, want [{e 1ms}] (caller mutation must not leak)", got)
	}
}
