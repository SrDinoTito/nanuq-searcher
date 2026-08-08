package result

import "net/url"

// This file implements snake_case serialization of the result model
// (DSG-014, REQ-018). The keys mirror the SearXNG JSON API contract exactly
// (CA-003): field names are lowercase snake_case and zero/empty fields are
// kept in the output — SearXNG includes every field even when empty.

// AsDict converts the result to a snake_case map matching the SearXNG JSON
// result object (REQ-018, searx webutils.get_json_response:
// results: [_.as_dict() for _ in rc.get_ordered_results()]).
//
// Contract: keys are {title, content, url, thumbnail, img_src, engines,
// score, category, positions, priority, template, parsed_url}. Empty fields
// are NOT omitted (the SearXNG JSON includes them). parsed_url mirrors
// Python's urllib.ParseResult: nil when the URL is empty, otherwise the
// [scheme, netloc, path, params, query, fragment] tuple serialized as an
// array.
func (m *MainResult) AsDict() map[string]any {
	// template defaults to "default.html" in SearXNG (result_types._base.py);
	// emit the effective value so the JSON always carries a template.
	template := m.Template
	if template == "" {
		template = "default.html"
	}

	d := map[string]any{
		"title":      m.Title,
		"content":    m.Content,
		"url":        m.URL,
		"thumbnail":  m.Thumbnail,
		"img_src":    m.ImgSrc,
		"engines":    m.Engines,
		"score":      m.Score,
		"category":   m.Category,
		"positions":  m.Positions,
		"priority":   m.Priority,
		"template":   template,
		"parsed_url": parsedURL(m.URL),
	}
	return d
}

// AsDict converts the answer to its snake_case map (REQ-018 "answers" list).
func (a *Answer) AsDict() map[string]any {
	return map[string]any{
		"title":   a.Title,
		"content": a.Content,
	}
}

// AsDict converts the infobox to its snake_case map (REQ-018 "infoboxes"
// list). Attributes are serialized as {key, value} objects.
func (ib *Infobox) AsDict() map[string]any {
	attrs := make([]map[string]any, 0, len(ib.Attributes))
	for _, kv := range ib.Attributes {
		attrs = append(attrs, map[string]any{
			"key":   kv.Key,
			"value": kv.Value,
		})
	}
	return map[string]any{
		"title":      ib.Title,
		"content":    ib.Content,
		"urls":       ib.URLs,
		"attributes": attrs,
		"img_src":    ib.ImgSrc,
	}
}

// parsedURL serializes raw into the urllib.ParseResult shape
// [scheme, netloc, path, params, query, fragment], or nil when raw is empty
// (Python: parsed_url is None for empty URLs). Go's net/url has no separate
// semicolon-params component, so params is always "" — a documented
// limitation to be pinned by the TASK-012 golden fixtures.
func parsedURL(raw string) any {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Unparseable URLs behave like empty ones in SearXNG (parsed_url stays
		// None); keep the raw URL field but emit nil for the parsed shape.
		return nil
	}
	return []any{u.Scheme, u.Host, u.Path, "", u.RawQuery, u.Fragment}
}
