// Package result defines the raw engine output types consumed by the search
// pipeline.
//
// Engines produce typed results (MainResult, Image, AnswerSet, Infobox, ...)
// wrapped in a RawResult discriminated union (DSG-004, REQ-011); the
// processor (TASK-006) switches on RawResult.Kind to extend the search
// container. Result merging (REQ-009), snake_case serialization (DSG-014,
// REQ-018) and SearXNG-compatible scoring (DSG-010, REQ-010) live in this
// package too.
//
// The model is a faithful port of the SearXNG result_types package
// (msgspec.Structs), simplified to plain Go structs — no reflection is used
// anywhere.
package result

// MainResult is the standard web-search result (SearXNG
// result_types.MainResult, template "default.html"). The container merges
// results that share a URL (REQ-009) and the JSON handler serializes them via
// AsDict (DSG-014, REQ-018).
type MainResult struct {
	// Title is the result headline.
	Title string

	// Content is the result snippet / description.
	Content string

	// URL is the result link.
	URL string

	// Thumbnail is the thumbnail image URL (may be empty for web results).
	Thumbnail string

	// ImgSrc is the image source URL (SearXNG img_src), used by image results
	// and as part of the ordering group key (DSG-010).
	ImgSrc string

	// Engines is the ordered set of engine names that produced this result
	// (deduplicated on merge, REQ-009).
	Engines []string

	// Score is the relevance score computed by CalculateScore (DSG-010). It
	// is set by the container when a result is added (TASK-006).
	Score float64

	// Category is the engine category that produced the result (general,
	// images, news, ...). It is part of the ordering group key.
	Category string

	// Positions is the list of container-level positions where this result
	// appeared (SearXNG result['positions']). Each entry contributes
	// weight/position to the score.
	Positions []int

	// Priority ranks the result above others: 0 = default, 1 = low,
	// 2 = high (SearXNG priority "", "low", "high"). The container applies
	// it when calculating scores (TASK-006).
	Priority int

	// Template is the rendering template name, "default.html" for plain web
	// results (SearXNG default).
	Template string
}

// Image is an image-search result (SearXNG result_types.Image,
// template "images.html"). It is a sibling of MainResult, not a subclass —
// Go carries it in RawResult.Data.
type Image struct {
	// ThumbnailSrc is the thumbnail URL.
	ThumbnailSrc string

	// Resolution is the main image resolution, e.g. "1920x1080".
	Resolution string

	// ImgFormat is the image format, e.g. "JPEG".
	ImgFormat string

	// Formats lists the alternative resolutions/URLs of the image.
	Formats []ImageRef
}

// ImageRef is one alternative format of an Image.
type ImageRef struct {
	// URL of this image format.
	URL string

	// Resolution of this image format, e.g. "640x480".
	Resolution string
}

// AnswerSet aggregates the answer-type results of one query (SearXNG
// result_types.AnswerSet): simple answers, translations, weather, plus any
// infoboxes produced. Answers are deduplicated by the container (TASK-006).
type AnswerSet struct {
	// Answers are the simple text answers.
	Answers []Answer

	// Infoboxes are the structured knowledge-panel boxes.
	Infoboxes []Infobox
}

// Answer is a short, direct answer to the query (SearXNG result_types.Answer,
// template "answer/legacy.html").
type Answer struct {
	// Title of the answer, e.g. the resolved entity name.
	Title string

	// Content of the answer, e.g. the computed value.
	Content string
}

// Infobox is a structured knowledge-panel entry (SearXNG result_types.Infobox,
// template "infobox.html").
type Infobox struct {
	// Title of the infobox entity.
	Title string

	// Content is the infobox description.
	Content string

	// URLs are the external links shown in the infobox.
	URLs []string

	// Attributes are the key/value pairs of the infobox table.
	Attributes []KeyValue

	// ImgSrc is the infobox image URL.
	ImgSrc string
}

// KeyValue is a single key/value attribute row (used by Infobox).
type KeyValue struct {
	// Key of the attribute, e.g. "Area".
	Key string

	// Value of the attribute, e.g. "68,696 km2".
	Value string
}

// CodeResult is a code-snippet result (SearXNG result_types.Code,
// template "code.html").
type CodeResult struct {
	// Title of the code result.
	Title string

	// Content is the code description / snippet context.
	Content string

	// Language is the programming language name, e.g. "go".
	Language string

	// Code is the code snippet itself.
	Code string
}

// PaperResult is an academic-paper result (SearXNG result_types.Paper,
// template "paper.html").
type PaperResult struct {
	// Title of the paper.
	Title string

	// Content is the paper abstract / description.
	Content string

	// URL of the paper.
	URL string

	// Authors is the author list (comma separated).
	Authors string
}

// FileResult is a file-download result (SearXNG result_types.File,
// template "file.html").
type FileResult struct {
	// Title of the file result.
	Title string

	// Content is the file description.
	Content string

	// URL of the file.
	URL string
}

// Translations is a translation answer (SearXNG result_types.Translations,
// template "answer/translations.html").
type Translations struct {
	// Translations is the list of translated items (word or phrase + info).
	Translations []string

	// Source is the source language, e.g. "en".
	Source string

	// Target is the target language, e.g. "es".
	Target string
}

// WeatherAnswer is a weather answer (SearXNG result_types.WeatherAnswer,
// template "answer/weather.html"). This is a minimal port — the full current/
// forecasts/service model is TASK-004's simplification per the spec.
type WeatherAnswer struct {
	// Temperature in the given Units.
	Temperature string

	// Condition is a human-readable weather description.
	Condition string

	// Location is the place the weather applies to.
	Location string

	// Units is the temperature unit scale, e.g. "metric" or "imperial".
	Units string
}
