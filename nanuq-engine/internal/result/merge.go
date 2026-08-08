package result

import (
	"net/url"
	"strings"
)

// This file implements result merging / deduplication (REQ-009). Two results
// that share a URL are fused into one — a faithful port of SearXNG
// merge_two_main_results (searx/results.py L332-356) with the extensions the
// spec requires: positions are appended and the higher score wins.

// Merge fuses b into a and returns a. It is the SearXNG merge of two results
// that share the same URL (REQ-009, searx/results.py L332-356):
//
//   - content: the longer snippet wins
//   - title:   the longer headline wins
//   - engines: set union of the engine names (a first, then any new from b)
//   - empty a fields are filled from b (defaults_from semantics, incl. URL
//     scheme upgrade http -> https when only b is secure)
//   - positions: b's positions are appended to a's (the container recomputes
//     the score from the full position list — TASK-006)
//   - score:    the higher of the two is kept
//
// The mutation style mirrors the Python origin object: the receiver a is
// modified in place and returned, so callers can write
// `merged := Merge(a, b)` and keep using `merged`.
func Merge(a, b *MainResult) *MainResult {
	// content: keep the one with more text
	// ref: searx/results.py L337-338
	if len(b.Content) > len(a.Content) {
		a.Content = b.Content
	}

	// title: keep the one with more text
	// ref: searx/results.py L340-341
	if len(b.Title) > len(a.Title) {
		a.Title = b.Title
	}

	// engines: set union, a first
	// ref: searx/results.py L346-347 (origin.engines.add(other.engine or ""))
	for _, e := range b.Engines {
		if !containsString(a.Engines, e) {
			a.Engines = append(a.Engines, e)
		}
	}

	// defaults_from(b): fill a's unset / empty / zero fields from b
	// ref: searx/results.py L343-345 + defaults_from (empty string or None is
	// treated as unset)
	if a.URL == "" {
		a.URL = b.URL
	}
	if a.Thumbnail == "" {
		a.Thumbnail = b.Thumbnail
	}
	if a.ImgSrc == "" {
		a.ImgSrc = b.ImgSrc
	}
	if a.Category == "" {
		a.Category = b.Category
	}
	if a.Template == "" {
		a.Template = b.Template
	}
	// Priority is an int; 0 (PriorityDefault) is the unset value.
	if a.Priority == 0 {
		a.Priority = b.Priority
	}

	// URL scheme upgrade: when a is plain http and b is secure (https, ftps,
	// ...), adopt b's scheme (ref: searx/results.py L349-353).
	a.URL = upgradedURL(a.URL, b.URL)

	// positions: append b's positions to a's — the score is recomputed by the
	// container from the merged position list (REQ-009, EC-006).
	// ref: searx/results.py _merge_main_result (merged.positions.append(...))
	a.Positions = append(a.Positions, b.Positions...)

	// score: keep the higher one (spec extension — Python recomputes the score
	// in close(); the container will do the same over the merged positions).
	if b.Score > a.Score {
		a.Score = b.Score
	}

	return a
}

// upgradedURL returns a upgraded with the scheme of b when b uses a secure
// scheme and a does not (http -> https upgrade, ref: searx/results.py
// L349-353). When either URL fails to parse, a is returned unchanged.
func upgradedURL(a, b string) string {
	if a == "" || b == "" {
		return a
	}
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil || ua.Scheme == "" {
		return a
	}
	// Python: origin.parsed_url.scheme.endswith("s"); "http" -> false,
	// "https"/"ftps" -> true.
	if strings.HasSuffix(ua.Scheme, "s") {
		return a
	}
	if !strings.HasSuffix(ub.Scheme, "s") {
		return a
	}
	ua.Scheme = ub.Scheme
	return ua.String()
}

// containsString reports whether s is present in list.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
