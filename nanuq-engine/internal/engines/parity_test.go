package engines

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"nanuq-engine/internal/result"
)

// Golden parity fixtures for the SearXNG scoring/ordering/serialization port
// (REQ-010, REQ-NF-006, DECISION-011, CA-005). The expected values in
// testdata/parity_fixtures.json are deterministic and were computed by hand
// from the reference algorithm documented in searx/results.py (calculate_score
// L17-38, get_ordered_results L191-247, ResultContainer L183-189) — they
// encapsulate the Python behavior, so no Python runtime is needed in CI
// (REQ-NF-006). The Go side under test is internal/result (TASK-004/TASK-006
// ports, verified by internal/result scoring_test.go and as_dict_test.go).
//
// A note on get_ordered_results clustering: a cluster holds its initial result
// plus max_count=8 inserts (results.py L199, L224-244) — i.e. NINE results per
// cluster. The tenth same-group result finds count == 0 and starts a new
// cluster appended at the end. The fixtures below encode this real Python
// behavior (see the max_count8_* cases).

// parityFixtures is the decoded shape of testdata/parity_fixtures.json.
type parityFixtures struct {
	CalculateScore []struct {
		Weight    float64 `json:"weight"`
		Positions []int   `json:"positions"`
		Want      float64 `json:"want"`
	} `json:"calculate_score"`
	Ordered []struct {
		Name    string `json:"name"`
		Results []struct {
			Title     string  `json:"title"`
			URL       string  `json:"url"`
			Category  string  `json:"category"`
			Template  string  `json:"template"`
			ImgSrc    string  `json:"img_src"`
			Score     float64 `json:"score"`
			Positions []int   `json:"positions"`
		} `json:"results"`
		Want []string `json:"want"`
	} `json:"ordered"`
	AsDict struct {
		Result struct {
			Title     string   `json:"title"`
			Content   string   `json:"content"`
			URL       string   `json:"url"`
			Thumbnail string   `json:"thumbnail"`
			ImgSrc    string   `json:"img_src"`
			Engines   []string `json:"engines"`
			Score     float64  `json:"score"`
			Category  string   `json:"category"`
			Positions []int    `json:"positions"`
			Priority  int      `json:"priority"`
			Template  string   `json:"template"`
		} `json:"result"`
		Want map[string]any `json:"want"`
	} `json:"as_dict"`
}

// loadParityFixtures reads and decodes testdata/parity_fixtures.json, failing
// the test on any I/O or decode error.
func loadParityFixtures(t *testing.T) *parityFixtures {
	t.Helper()
	data, err := os.ReadFile("testdata/parity_fixtures.json")
	if err != nil {
		t.Fatalf("read parity fixtures: %v", err)
	}
	var fx parityFixtures
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("decode parity fixtures: %v", err)
	}
	return &fx
}

// TestCalculateScoreParity checks CalculateScore against the golden fixture
// values, which were hand-computed from searx/results.py L17-38 (calculate_score:
// score += weight / position). Tolerance is ±0.01 per CA-005.
func TestCalculateScoreParity(t *testing.T) {
	fx := loadParityFixtures(t)
	if len(fx.CalculateScore) == 0 {
		t.Fatal("fixture has no calculate_score cases")
	}
	for _, c := range fx.CalculateScore {
		got := result.CalculateScore(c.Weight, c.Positions)
		if math.Abs(got-c.Want) > 0.01 {
			t.Errorf("CalculateScore(weight=%v, positions=%v) = %v, want %v (±0.01, results.py L17-38)",
				c.Weight, c.Positions, got, c.Want)
		}
	}
}

// TestOrderedParity checks GetOrderedResults against the golden URL orders,
// hand-computed from searx/results.py L191-247 (get_ordered_results: stable
// sort by score desc, cluster by category:template:img_src with max_count=8 and
// max_distance=20). The input slice must not be mutated (scoring.go copies it).
func TestOrderedParity(t *testing.T) {
	fx := loadParityFixtures(t)
	if len(fx.Ordered) == 0 {
		t.Fatal("fixture has no ordered cases")
	}
	for _, c := range fx.Ordered {
		t.Run(c.Name, func(t *testing.T) {
			rs := make([]*result.MainResult, 0, len(c.Results))
			for _, r := range c.Results {
				rs = append(rs, &result.MainResult{
					Title:     r.Title,
					URL:       r.URL,
					Category:  r.Category,
					Template:  r.Template,
					ImgSrc:    r.ImgSrc,
					Score:     r.Score,
					Positions: r.Positions,
				})
			}
			before := urlOrder(rs)

			got := result.GetOrderedResults(rs)

			if !sameStringOrder(urlOrder(got), c.Want) {
				t.Errorf("GetOrderedResults order = %v, want %v (results.py L191-247)",
					urlOrder(got), c.Want)
			}
			// no-mutation contract: the input slice keeps its original order.
			if !sameStringOrder(before, urlOrder(rs)) {
				t.Errorf("GetOrderedResults mutated its input: before %v, after %v",
					before, urlOrder(rs))
			}
		})
	}
}

// TestAsDictParity checks MainResult.AsDict against the golden snake_case map:
// exactly the 12 keys {title, content, url, thumbnail, img_src, engines, score,
// category, positions, priority, template, parsed_url} (REQ-018). Empty fields
// are NOT omitted, and parsed_url is the urllib.ParseResult shape
// [scheme, netloc, path, params, query, fragment] — params always "" (Go
// net/url has no semicolon-params component, as_dict.go parsedURL).
func TestAsDictParity(t *testing.T) {
	fx := loadParityFixtures(t)
	if len(fx.AsDict.Want) == 0 {
		t.Fatal("fixture has no as_dict case")
	}
	r := fx.AsDict.Result
	m := &result.MainResult{
		Title:     r.Title,
		Content:   r.Content,
		URL:       r.URL,
		Thumbnail: r.Thumbnail,
		ImgSrc:    r.ImgSrc,
		Engines:   r.Engines,
		Score:     r.Score,
		Category:  r.Category,
		Positions: r.Positions,
		Priority:  r.Priority,
		Template:  r.Template,
	}
	got := m.AsDict()
	if len(got) != 12 || len(fx.AsDict.Want) != 12 {
		t.Fatalf("AsDict() returned %d keys, fixture want has %d (must be 12)", len(got), len(fx.AsDict.Want))
	}
	for key, wantVal := range fx.AsDict.Want {
		if !jsonEqual(got[key], wantVal) {
			t.Errorf("AsDict()[%q] = %#v, want %#v (REQ-018)", key, got[key], wantVal)
		}
	}
}

// urlOrder extracts the URL of each result in slice order.
func urlOrder(rs []*result.MainResult) []string {
	urls := make([]string, 0, len(rs))
	for _, r := range rs {
		if r == nil {
			urls = append(urls, "<nil>")
			continue
		}
		urls = append(urls, r.URL)
	}
	return urls
}

// sameStringOrder reports whether two string slices are equal element-wise.
func sameStringOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// jsonEqual compares a Go value produced by AsDict against a value decoded
// from JSON (want). JSON numbers decode to float64, so int/float64 got values
// are compared numerically; JSON arrays decode to []any, which is compared
// element-wise against []any (parsed_url), []string (engines) or []int
// (positions). No reflection is used (project convention).
func jsonEqual(got, want any) bool {
	switch w := want.(type) {
	case nil:
		return got == nil
	case string:
		g, ok := got.(string)
		return ok && g == w
	case float64:
		switch g := got.(type) {
		case float64:
			return g == w
		case int:
			return float64(g) == w
		}
		return false
	case []any:
		switch g := got.(type) {
		case []any:
			if len(g) != len(w) {
				return false
			}
			for i := range w {
				if !jsonEqual(g[i], w[i]) {
					return false
				}
			}
			return true
		case []string:
			if len(g) != len(w) {
				return false
			}
			for i := range w {
				if !jsonEqual(g[i], w[i]) {
					return false
				}
			}
			return true
		case []int:
			if len(g) != len(w) {
				return false
			}
			for i := range w {
				if !jsonEqual(g[i], w[i]) {
					return false
				}
			}
			return true
		}
		return false
	}
	return false
}
