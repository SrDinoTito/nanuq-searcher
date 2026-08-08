package result

// This file implements the raw-result discriminated union (DSG-004, REQ-011,
// DECISION-004). Engines return []*RawResult from Engine.Response; the
// processor (TASK-006) switches on Kind inside extend() to route each payload
// to its container slot (main results, answers, infoboxes, corrections,
// suggestions, engine data, ...).

// RawKind discriminates the concrete payload carried by a RawResult.
//
// The values mirror DECISION-004: main, answer, infobox, engine_data,
// correction, suggestion, keyvalue, code, paper, file, image, translations,
// weather.
type RawKind int

const (
	// KindMain is a standard web result (*MainResult in RawResult.Main).
	KindMain RawKind = iota
	// KindAnswer is an answer set (*AnswerSet in RawResult.Answer).
	KindAnswer
	// KindInfobox is an infobox (*Infobox in RawResult.Infobox).
	KindInfobox
	// KindEngineData is engine-specific data (name in Str, payload in Data).
	KindEngineData
	// KindCorrection is a query-correction string (in Str).
	KindCorrection
	// KindSuggestion is a query-suggestion string (in Str).
	KindSuggestion
	// KindKeyValue is a key/value pair (KeyValue in Data).
	KindKeyValue
	// KindCode is a code-snippet result (CodeResult in Data).
	KindCode
	// KindPaper is an academic-paper result (PaperResult in Data).
	KindPaper
	// KindFile is a file-download result (FileResult in Data).
	KindFile
	// KindImage is an image result (*Image in Data).
	KindImage
	// KindTranslations is a translation answer (*Translations in Data).
	KindTranslations
	// KindWeather is a weather answer (*WeatherAnswer in Data).
	KindWeather
)

// RawResult is a tagged union produced by an engine's Response step
// (DSG-004). Exactly one payload field is meaningful for a given Kind:
//
//	KindMain        -> Main
//	KindAnswer      -> Answer
//	KindInfobox     -> Infobox
//	KindCorrection  -> Str
//	KindSuggestion  -> Str
//	KindEngineData  -> Str (engine name) + Data (payload)
//	other kinds     -> Data (typed payload, see the New* helpers)
//
// The processor (TASK-006) switches on Kind in extend().
type RawResult struct {
	// Kind discriminates the payload.
	Kind RawKind

	// Main is the *MainResult payload (KindMain).
	Main *MainResult

	// Answer is the *AnswerSet payload (KindAnswer).
	Answer *AnswerSet

	// Infobox is the *Infobox payload (KindInfobox).
	Infobox *Infobox

	// Str is a *string payload: the correction/suggestion text (KindCorrection,
	// KindSuggestion) or the engine name (KindEngineData).
	Str *string

	// Data is the typed payload for all remaining kinds (KeyValue,
	// CodeResult, PaperResult, FileResult, *Image, *Translations,
	// *WeatherAnswer) and the engine_data payload (KindEngineData).
	Data any
}

// NewMain wraps a standard web result (KindMain).
func NewMain(m *MainResult) *RawResult {
	return &RawResult{Kind: KindMain, Main: m}
}

// NewAnswer wraps an answer set (KindAnswer).
func NewAnswer(as *AnswerSet) *RawResult {
	return &RawResult{Kind: KindAnswer, Answer: as}
}

// NewInfobox wraps an infobox (KindInfobox).
func NewInfobox(ib *Infobox) *RawResult {
	return &RawResult{Kind: KindInfobox, Infobox: ib}
}

// NewCorrection wraps a query-correction string (KindCorrection).
func NewCorrection(s string) *RawResult {
	return &RawResult{Kind: KindCorrection, Str: &s}
}

// NewSuggestion wraps a query-suggestion string (KindSuggestion).
func NewSuggestion(s string) *RawResult {
	return &RawResult{Kind: KindSuggestion, Str: &s}
}

// NewEngineData wraps engine-specific data (KindEngineData). name is the
// engine instance name — the processor (TASK-006) keys the engine_data store
// by it — and data is the engine-defined payload.
func NewEngineData(name string, data any) *RawResult {
	return &RawResult{Kind: KindEngineData, Str: &name, Data: data}
}

// NewKeyValue wraps a single key/value pair (KindKeyValue).
func NewKeyValue(key, value string) *RawResult {
	return &RawResult{Kind: KindKeyValue, Data: KeyValue{Key: key, Value: value}}
}

// NewCode wraps a code-snippet result (KindCode).
func NewCode(title, content, language, code string) *RawResult {
	return &RawResult{
		Kind: KindCode,
		Data: CodeResult{Title: title, Content: content, Language: language, Code: code},
	}
}

// NewPaper wraps an academic-paper result (KindPaper).
func NewPaper(title, content, url, authors string) *RawResult {
	return &RawResult{
		Kind: KindPaper,
		Data: PaperResult{Title: title, Content: content, URL: url, Authors: authors},
	}
}

// NewFile wraps a file-download result (KindFile).
func NewFile(title, content, url string) *RawResult {
	return &RawResult{
		Kind: KindFile,
		Data: FileResult{Title: title, Content: content, URL: url},
	}
}

// NewImage wraps an image result (KindImage).
func NewImage(img *Image) *RawResult {
	return &RawResult{Kind: KindImage, Data: img}
}

// NewTranslations wraps a translation answer (KindTranslations).
func NewTranslations(t *Translations) *RawResult {
	return &RawResult{Kind: KindTranslations, Data: t}
}

// NewWeather wraps a weather answer (KindWeather).
func NewWeather(w *WeatherAnswer) *RawResult {
	return &RawResult{Kind: KindWeather, Data: w}
}
