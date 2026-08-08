package markdown

import (
	"fmt"
	"sort"
	"strings"

	"nanuq-searcher-mcp/internal/domain"
)

// RenderMap renders a domain.SiteMap as a markdown tree (REQ-011, DSG-008).
// It emits the clean projection only: root URL, visit stats, host errors,
// per-page tree with depth indentation, and the H1..H3 outline headings.
// The output always ends in exactly one "\n" (repo convention).
func RenderMap(sm domain.SiteMap) string {
	return renderMap(sm, false)
}

// RenderMapContent renders a domain.SiteMap like RenderMap but additionally
// embeds each page's markdown content under a "**Contenido:**" block. The
// handler is responsible for truncating page.Content before calling this so
// the output stays bounded (see s.handleMap in internal/mcp/tools.go).
func RenderMapContent(sm domain.SiteMap) string {
	return renderMap(sm, true)
}

// renderMap is the shared implementation. When includeContent is true the
// per-page content block is emitted.
func renderMap(sm domain.SiteMap, includeContent bool) string {
	var b strings.Builder

	root := sm.RootURL
	if root == "" {
		root = "(sin URL raíz)"
	}
	fmt.Fprintf(&b, "# Mapa de %s\n\n", root)

	cancelled := "no"
	if sm.Cancelled {
		cancelled = "sí"
	}
	fmt.Fprintf(&b, "Visitas: %d · Cancelado: %s", sm.Visited, cancelled)

	if len(sm.HostErrors) > 0 {
		keys := make([]string, 0, len(sm.HostErrors))
		for k := range sm.HostErrors {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var hosts strings.Builder
		for i, k := range keys {
			if i > 0 {
				hosts.WriteString(", ")
			}
			fmt.Fprintf(&hosts, "%s (%s)", k, sm.HostErrors[k])
		}
		fmt.Fprintf(&b, " · Errores de host: %s", hosts.String())
	}
	b.WriteString("\n")

	if len(sm.Pages) > 0 {
		b.WriteString("\n")
	}
	for i := range sm.Pages {
		if i > 0 {
			b.WriteString("\n")
		}
		writeMapPage(&b, sm.Pages[i], includeContent)
	}

	return b.String()
}

// writeMapPage writes one page: its tree bullet (indented by Depth), error
// note, the H1..H3 outline headings at column 0, and optionally the content.
func writeMapPage(b *strings.Builder, p domain.Page, includeContent bool) {
	b.WriteString(strings.Repeat("  ", p.Depth))
	b.WriteString("- ")
	writeLink(b, p.Title, p.URL)

	msgs := nonEmptyStrings(p.Errors)
	if len(msgs) > 0 {
		fmt.Fprintf(b, " — error: %s", strings.Join(msgs, "; "))
	}
	b.WriteString("\n")

	// Outline headings at column 0: ATX headings tolerate a few leading
	// spaces before becoming indented code blocks, and the heading level
	// (Level+2) carries the hierarchy (the map title is H1).
	for _, h := range p.Headings {
		if h.Level < 1 || h.Level > 3 {
			continue
		}
		text := strings.TrimSpace(h.Text)
		if text == "" {
			continue
		}
		fmt.Fprintf(b, "%s %s\n", strings.Repeat("#", h.Level+2), text)
	}

	if includeContent {
		content := strings.TrimRight(p.Content, "\n")
		if strings.TrimSpace(content) != "" {
			fmt.Fprintf(b, "\n**Contenido:**\n\n%s\n", content)
		}
	}
}

// nonEmptyStrings returns the trimmed, non-empty entries of in.
func nonEmptyStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
