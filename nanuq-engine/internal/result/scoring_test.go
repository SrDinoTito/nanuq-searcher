package result

import (
	"math"
	"reflect"
	"strconv"
	"testing"
)

// --- CalculateScore (searx/results.py L24-33) ---

// TestCalculateScoreSinglePosition: weight 1, position [1] -> 1/1 = 1.0.
func TestCalculateScoreSinglePosition(t *testing.T) {
	got := CalculateScore(1.0, []int{1})
	if got != 1.0 {
		t.Errorf("CalculateScore(1, [1]) = %v, want 1.0", got)
	}
}

// TestCalculateScoreMultiplePositions: weight 2, positions [1,3] ->
// 2/1 + 2/3 = 2.666...
func TestCalculateScoreMultiplePositions(t *testing.T) {
	got := CalculateScore(2.0, []int{1, 3})
	want := 2.0/1.0 + 2.0/3.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("CalculateScore(2, [1,3]) = %v, want %v", got, want)
	}
}

// TestCalculateScoreHigherPositionLowerScore: later positions contribute less
// (weight 1, position 3 -> 0.333...).
func TestCalculateScoreHigherPositionLowerScore(t *testing.T) {
	got := CalculateScore(1.0, []int{3})
	want := 1.0 / 3.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("CalculateScore(1, [3]) = %v, want %v", got, want)
	}
}

// TestCalculateScoreEmptyPositions: no positions -> score 0 (a result that
// never surfaced scores nothing).
func TestCalculateScoreEmptyPositions(t *testing.T) {
	if got := CalculateScore(1.0, nil); got != 0.0 {
		t.Errorf("CalculateScore(1, nil) = %v, want 0.0", got)
	}
}

// --- GetOrderedResults (searx/results.py L191-247) ---

// resultFixture is a tiny builder for ordering tests.
func resultFixture(name string, score float64, category string) *MainResult {
	return &MainResult{
		Title:    name,
		URL:      "https://example.org/" + name,
		Score:    score,
		Category: category,
	}
}

// names extracts the Title sequence of an ordered result slice.
func names(rs []*MainResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Title
	}
	return out
}

// TestGetOrderedResultsSortsByScoreDesc checks pass 1: results are ordered by
// score, descending.
func TestGetOrderedResultsSortsByScoreDesc(t *testing.T) {
	input := []*MainResult{
		resultFixture("low", 1.0, "general"),
		resultFixture("high", 9.0, "general"),
		resultFixture("mid", 5.0, "general"),
	}
	got := names(GetOrderedResults(input))
	want := []string{"high", "mid", "low"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("score desc order: got %v, want %v", got, want)
	}
}

// TestGetOrderedResultsStableForEqualScores: Python's sorted(..., reverse=True)
// is stable — equal scores keep input (extend) order.
func TestGetOrderedResultsStableForEqualScores(t *testing.T) {
	input := []*MainResult{
		resultFixture("first", 3.0, "general"),
		resultFixture("second", 3.0, "general"),
	}
	got := names(GetOrderedResults(input))
	want := []string{"first", "second"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("equal scores must keep input order: got %v, want %v", got, want)
	}
}

// TestGetOrderedResultsGroupsByCategory: results of different categories do
// not interleave — each category forms a consecutive cluster.
func TestGetOrderedResultsGroupsByCategory(t *testing.T) {
	input := []*MainResult{
		resultFixture("a1", 100, "general"),
		resultFixture("b1", 95, "images"),
		resultFixture("a2", 90, "general"),
		resultFixture("b2", 85, "images"),
	}
	got := names(GetOrderedResults(input))
	want := []string{"a1", "a2", "b1", "b2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("category clustering: got %v, want %v", got, want)
	}
}

// TestGetOrderedResultsImgSrcFlagSeparatesClusters: the group key includes
// the img_src flag (set when thumbnail or img_src is present) — results with
// an image and without do not share a cluster even in the same category.
func TestGetOrderedResultsImgSrcFlagSeparatesClusters(t *testing.T) {
	withImg := resultFixture("img1", 100, "general")
	withImg.Thumbnail = "https://example.org/t.png"
	noImg := resultFixture("web1", 90, "general")

	input := []*MainResult{withImg, noImg}
	got := names(GetOrderedResults(input))
	want := []string{"img1", "web1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("img_src flag grouping: got %v, want %v", got, want)
	}
}

// TestGetOrderedResultsTemplateSeparatesClusters: the group key includes the
// template — "images.html" results do not cluster with "default.html" ones.
func TestGetOrderedResultsTemplateSeparatesClusters(t *testing.T) {
	custom := resultFixture("custom", 100, "general")
	custom.Template = "images.html"
	standard := resultFixture("standard", 90, "general")

	input := []*MainResult{custom, standard}
	got := names(GetOrderedResults(input))
	want := []string{"custom", "standard"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("template grouping: got %v, want %v", got, want)
	}
}

// TestGetOrderedResultsMaxCount8 is the REQ-010 parity fixture for the
// max_count=8 rule: two categories (general vs images), 10 results each,
// interleaved by score. Following SearXNG semantics the first cluster of a
// category holds the initial result plus 8 insertions (9 items); the 10th
// result of each category starts a fresh cluster appended at the end.
//
// Expected final order (verified against searx/results.py get_ordered_results
// with the same input):
//
//	a1..a9, b1..b9, a10, b10
func TestGetOrderedResultsMaxCount8(t *testing.T) {
	var input []*MainResult
	// Interleaved by score: a(100), b(95), a(90), b(85), ... a(10), b(5).
	for i := 0; i < 10; i++ {
		aScore := float64(100 - 10*i)
		bScore := float64(95 - 10*i)
		a := resultFixture("a"+strconv.Itoa(i+1), aScore, "general")
		b := resultFixture("b"+strconv.Itoa(i+1), bScore, "images")
		input = append(input, a, b)
	}

	got := names(GetOrderedResults(input))
	want := []string{
		"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9",
		"b1", "b2", "b3", "b4", "b5", "b6", "b7", "b8", "b9",
		"a10", "b10",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("max_count=8 clustering: got %v, want %v", got, want)
	}
}

// TestGetOrderedResultsEmptyInput: no results, no panic, empty output.
func TestGetOrderedResultsEmptyInput(t *testing.T) {
	if got := GetOrderedResults(nil); len(got) != 0 {
		t.Errorf("empty input: got %v", got)
	}
}

// TestGetOrderedResultsDoesNotMutateInput: the input slice is not reordered.
func TestGetOrderedResultsDoesNotMutateInput(t *testing.T) {
	input := []*MainResult{
		resultFixture("low", 1.0, "general"),
		resultFixture("high", 9.0, "general"),
	}
	before := names(input)
	GetOrderedResults(input)
	if !reflect.DeepEqual(names(input), before) {
		t.Errorf("input slice must not be mutated: got %v, want %v", names(input), before)
	}
}
