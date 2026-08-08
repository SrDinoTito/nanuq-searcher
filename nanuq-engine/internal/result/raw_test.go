package result

import (
	"reflect"
	"testing"
)

// TestRawResultHelpers verifies the discriminated-union construction
// (DSG-004, REQ-011): every helper sets the right Kind and payload slot so
// the processor (TASK-006) can switch on Kind in extend().
func TestRawResultHelpers(t *testing.T) {
	main := NewMain(&MainResult{Title: "t", URL: "https://example.org/"})
	if main.Kind != KindMain || main.Main == nil || main.Main.Title != "t" {
		t.Errorf("NewMain: got %+v", main)
	}

	as := NewAnswer(&AnswerSet{Answers: []Answer{{Title: "a"}}})
	if as.Kind != KindAnswer || as.Answer == nil || len(as.Answer.Answers) != 1 {
		t.Errorf("NewAnswer: got %+v", as)
	}

	ib := NewInfobox(&Infobox{Title: "i"})
	if ib.Kind != KindInfobox || ib.Infobox == nil || ib.Infobox.Title != "i" {
		t.Errorf("NewInfobox: got %+v", ib)
	}

	corr := NewCorrection("did you mean X?")
	if corr.Kind != KindCorrection || corr.Str == nil || *corr.Str != "did you mean X?" {
		t.Errorf("NewCorrection: got %+v", corr)
	}

	sugg := NewSuggestion("suggestion")
	if sugg.Kind != KindSuggestion || sugg.Str == nil || *sugg.Str != "suggestion" {
		t.Errorf("NewSuggestion: got %+v", sugg)
	}

	ed := NewEngineData("duckduckgo", map[string]string{"key": "value"})
	if ed.Kind != KindEngineData || ed.Str == nil || *ed.Str != "duckduckgo" {
		t.Errorf("NewEngineData must carry the engine name in Str: got %+v", ed)
	}
	if ed.Data == nil {
		t.Errorf("NewEngineData must carry the payload in Data: got %+v", ed)
	}

	kv := NewKeyValue("Radius", "6371 km")
	if kv.Kind != KindKeyValue {
		t.Errorf("NewKeyValue kind: got %v, want KindKeyValue", kv.Kind)
	}
	if k, ok := kv.Data.(KeyValue); !ok || k.Key != "Radius" || k.Value != "6371 km" {
		t.Errorf("NewKeyValue payload: got %+v", kv.Data)
	}

	code := NewCode("sum", "snippet", "go", "func sum() {}")
	if code.Kind != KindCode {
		t.Errorf("NewCode kind: got %v", code.Kind)
	}
	if c, ok := code.Data.(CodeResult); !ok || c.Language != "go" || c.Code != "func sum() {}" {
		t.Errorf("NewCode payload: got %+v", code.Data)
	}

	paper := NewPaper("Paper", "abstract", "https://example.org/paper", "Einstein")
	if paper.Kind != KindPaper {
		t.Errorf("NewPaper kind: got %v", paper.Kind)
	}
	if p, ok := paper.Data.(PaperResult); !ok || p.Authors != "Einstein" {
		t.Errorf("NewPaper payload: got %+v", paper.Data)
	}

	file := NewFile("File", "desc", "https://example.org/file.pdf")
	if file.Kind != KindFile {
		t.Errorf("NewFile kind: got %v", file.Kind)
	}
	if f, ok := file.Data.(FileResult); !ok || f.URL != "https://example.org/file.pdf" {
		t.Errorf("NewFile payload: got %+v", file.Data)
	}

	img := NewImage(&Image{ThumbnailSrc: "https://example.org/t.png"})
	if img.Kind != KindImage {
		t.Errorf("NewImage kind: got %v", img.Kind)
	}
	if im, ok := img.Data.(*Image); !ok || im.ThumbnailSrc != "https://example.org/t.png" {
		t.Errorf("NewImage payload: got %+v", img.Data)
	}

	tr := NewTranslations(&Translations{Translations: []string{"hola"}, Source: "en", Target: "es"})
	if tr.Kind != KindTranslations {
		t.Errorf("NewTranslations kind: got %v", tr.Kind)
	}
	if tt, ok := tr.Data.(*Translations); !ok || tt.Source != "en" || tt.Target != "es" {
		t.Errorf("NewTranslations payload: got %+v", tr.Data)
	}

	w := NewWeather(&WeatherAnswer{Temperature: "20C", Condition: "sunny"})
	if w.Kind != KindWeather {
		t.Errorf("NewWeather kind: got %v", w.Kind)
	}
	if ww, ok := w.Data.(*WeatherAnswer); !ok || ww.Temperature != "20C" || ww.Condition != "sunny" {
		t.Errorf("NewWeather payload: got %+v", w.Data)
	}
}

// TestRawResultKindsOrder pins DECISION-004: the Kind constants follow the
// spec order main|answer|infobox|engine_data|correction|suggestion|keyvalue|
// code|paper|file|image|translations|weather.
func TestRawResultKindsOrder(t *testing.T) {
	want := []RawKind{
		KindMain, KindAnswer, KindInfobox, KindEngineData, KindCorrection,
		KindSuggestion, KindKeyValue, KindCode, KindPaper, KindFile,
		KindImage, KindTranslations, KindWeather,
	}
	for i, k := range want {
		if k != RawKind(i) {
			t.Errorf("RawKind %v must equal iota value %d (DECISION-004 order)", k, i)
		}
	}
}

// TestRawResultPointerPayloads ensures the pointer payloads are distinct
// references (not accidentally shared across RawResults).
func TestRawResultPointerPayloads(t *testing.T) {
	m1 := &MainResult{Title: "one"}
	m2 := &MainResult{Title: "two"}
	r1 := NewMain(m1)
	r2 := NewMain(m2)
	if r1.Main == r2.Main {
		t.Error("RawResult payload pointers must be distinct per result")
	}
	if !reflect.DeepEqual(r1.Main, m1) {
		t.Errorf("r1 payload: got %+v, want %+v", r1.Main, m1)
	}
}
