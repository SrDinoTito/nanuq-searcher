// Package markdown renders the clean domain types as markdown for the MCP
// tools (DSG-002, DSG-014). Renderers only ever emit the fields of the
// clean projection (REQ-003); junk fields never appear in the output
// (AC-001).
package markdown

import (
	"fmt"
	"strings"

	"nanuq-searcher-mcp/internal/domain"
)

const (
	// maxSnippetLen caps the content snippet rendered under each hit
	// (TASK-005: ~300 chars; DSG-005 suggests ~200 — the task wins).
	maxSnippetLen = 300

	// ellipsis marks a snippet truncated at maxSnippetLen.
	ellipsis = "…"
)

// RenderSearch renders a SearchResult as clean markdown (DSG-005,
// REQ-004..007). The output always ends with a single trailing newline and
// never carries junk fields beyond the clean projection (REQ-003, AC-001).
//
// Layout:
//
//   - optional bang-redirect notice as the first line (REQ-006);
//   - "## Resultados para "<query>"" header plus a numbered hit list with
//     compact engines/score/category metadata (REQ-004), or an informative
//     "## Búsqueda vacía" block for an empty query (REQ-007 — never an
//     error, never "Sin resultados");
//   - a final "## ⚠️ Sin respuesta: ..." section listing engines that did
//     not answer (REQ-005).
func RenderSearch(res domain.SearchResult) string {
	var b strings.Builder
	if res.RedirectURL != "" {
		writeBangNotice(&b, res.RedirectURL)
	}
	if strings.TrimSpace(res.Query) == "" {
		writeEmptyQuery(&b)
	} else {
		writeResults(&b, res)
	}
	writeUnresponsive(&b, res.Unresponsive)
	return b.String()
}

// writeBangNotice emits the external-bang redirect notice (REQ-006). It is
// always the first line of the output.
func writeBangNotice(b *strings.Builder, redirectURL string) {
	fmt.Fprintf(b, "> ⚠️ Bang externo detectado → [URL](%s)\n\n", redirectURL)
}

// writeEmptyQuery emits the informative block for an empty query (REQ-007).
func writeEmptyQuery(b *strings.Builder) {
	b.WriteString("## Búsqueda vacía\n\n")
	b.WriteString("Escribe una consulta para buscar en la web (ej.: \"golang\").\n")
}

// writeResults emits the header plus the numbered hit list, or a no-results
// hint when the query matched nothing (REQ-004).
func writeResults(b *strings.Builder, res domain.SearchResult) {
	fmt.Fprintf(b, "## Resultados para %q\n\n", res.Query)
	if len(res.Hits) == 0 {
		fmt.Fprintf(b, "Sin resultados para %q.\n", res.Query)
		b.WriteString("Sugerencia: reintenta con otras palabras.\n")
		return
	}
	for i, h := range res.Hits {
		writeHit(b, i+1, h)
	}
}

// writeHit renders one numbered hit: "n. [Title](url) — snippet" followed
// by a compact metadata line (REQ-004).
func writeHit(b *strings.Builder, n int, h domain.SearchHit) {
	fmt.Fprintf(b, "%d. ", n)
	writeLink(b, h.Title, h.URL)
	if sn := snippet(h.Content, maxSnippetLen); sn != "" {
		b.WriteString(" — ")
		b.WriteString(sn)
	}
	b.WriteString("\n")
	writeMetadata(b, h)
}

// writeLink emits "[Title](url)", falling back to the URL as anchor text
// when the title is empty and to plain text when the URL is empty — the
// renderer never produces a broken markdown link.
func writeLink(b *strings.Builder, title, url string) {
	switch {
	case url == "":
		b.WriteString(title)
	case title == "":
		fmt.Fprintf(b, "[%s](%s)", url, url)
	default:
		fmt.Fprintf(b, "[%s](%s)", title, url)
	}
}

// writeMetadata emits the compact "   - engines: ... · score: ... · categoria: ..."
// line. Engines are only included when present, score always (3 decimals),
// category only when non-empty.
func writeMetadata(b *strings.Builder, h domain.SearchHit) {
	var parts []string
	if eng := joinEngines(h.Engines); eng != "" {
		parts = append(parts, "engines: "+eng)
	}
	parts = append(parts, fmt.Sprintf("score: %.3f", h.Score))
	if h.Category != "" {
		parts = append(parts, "categoria: "+h.Category)
	}
	b.WriteString("   - ")
	b.WriteString(strings.Join(parts, " · "))
	b.WriteString("\n")
}

// writeUnresponsive emits the final "## ⚠️ Sin respuesta: ..." section
// listing the engines that did not answer (REQ-005). No-op when empty.
func writeUnresponsive(b *strings.Builder, engines []string) {
	clean := make([]string, 0, len(engines))
	for _, e := range engines {
		if e = strings.TrimSpace(e); e != "" {
			clean = append(clean, e)
		}
	}
	if len(clean) == 0 {
		return
	}
	b.WriteString("\n## ⚠️ Sin respuesta: ")
	b.WriteString(strings.Join(clean, ", "))
	b.WriteString("\n\n")
	for _, e := range clean {
		b.WriteString("- ")
		b.WriteString(e)
		b.WriteString("\n")
	}
}

// snippet returns the first non-empty line of s, trimmed and collapsed to
// at most max runes with a trailing ellipsis when truncated. It returns ""
// when s has no non-empty line, so callers can omit the content line.
func snippet(s string, max int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > max {
			return string(runes[:max]) + ellipsis
		}
		return line
	}
	return ""
}

// joinEngines joins engine names with ", ", trimming whitespace and
// dropping empty entries. It returns "" for nil or empty input.
func joinEngines(engines []string) string {
	clean := make([]string, 0, len(engines))
	for _, e := range engines {
		if e = strings.TrimSpace(e); e != "" {
			clean = append(clean, e)
		}
	}
	return strings.Join(clean, ", ")
}
