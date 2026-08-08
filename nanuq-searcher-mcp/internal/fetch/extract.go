package fetch

import (
	"bytes"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// readabilityCharThreshold is the minimum amount of text (in characters)
// that go-readability requires for a candidate to be treated as readable.
//
// The library default is 500, which is too aggressive for the short pages
// a search tool commonly encounters. 100 keeps behaviour predictable for
// small documents while still skipping degenerate ones. A document whose
// total extractable text is zero still yields an Article with a nil Node
// (and a nil error), which is what triggers the EC-002 fallback.
const readabilityCharThreshold = 100

// voidElements is the set of HTML elements that never have children, so
// an element node with no children only counts as "content" when it is
// one of these (e.g. <img>, <br>).
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// Extracted is the result of a readability extraction pass.
//
// ContentHTML holds the rendered HTML of the extracted article (or, in
// fallback mode, the raw HTML of <main> or <body>). It is always HTML,
// never markdown: the markdown conversion is owned by TASK-009. Length is
// the byte length of ContentHTML. OK reports whether any content could be
// extracted at all; when it is false the tool layer is expected to fall
// back to full-page handling (REQ-008 / EC-002).
type Extracted struct {
	Title       string
	ContentHTML string
	Length      int
	Byline      string
	OK          bool
}

// Extract runs readability over body and returns the extracted article.
//
// pageURL is used by readability to resolve relative URLs; it may be nil
// (the library handles that safely). body is the raw response bytes captured
// by the fetch stage (fetch.Response.Body).
//
// Charset handling: Extract decodes body to UTF-8 itself before running
// readability, using the WHATWG encoding algorithm
// (golang.org/x/net/html/charset): BOM, then a <meta> prescan, then a UTF-8
// validity heuristic, defaulting to windows-1252. This is deliberate: the
// readability Parser.Parse entry point re-decodes the raw bytes through
// go-shiori/dom.Parse, which uses a purely statistical chardet detector
// (github.com/gogs/chardet) that mis-detects short UTF-8 documents
// (< ~200 bytes, no declared charset) as windows-1252, corrupting non-ASCII
// text (e.g. "ñ" → "Ã±"). By decoding first and handing readability an
// already-parsed *html.Node (Parser.ParseDocument), the dom.Parse re-encoding
// is bypassed entirely. decodeToUTF8 never mis-detects valid UTF-8, so short
// UTF-8 pages keep their accents intact (TASK-014).
//
// Per EC-002, Extract never returns a non-nil error: a page with no
// extractable content is reported as Extracted{OK: false} so the caller
// can decide to use full-page mode.
func Extract(body []byte, pageURL *url.URL) (Extracted, error) {
	if len(body) == 0 {
		return Extracted{OK: false}, nil
	}

	// Decode to UTF-8 first (see docstring). On a decoding failure the bytes
	// are not text we can reason about; report no content rather than
	// feeding unreadable bytes to readability.
	utf8Body, err := decodeToUTF8(body)
	if err != nil {
		return Extracted{OK: false}, nil
	}

	// Parse ourselves and hand readability the already-decoded document so
	// it never re-decodes through go-shiori/dom's chardet.
	doc, err := html.Parse(bytes.NewReader(utf8Body))
	if err == nil {
		ps := readability.NewParser()
		ps.CharThresholds = readabilityCharThreshold
		article, aerr := ps.ParseDocument(doc, pageURL)
		if aerr == nil {
			if content, ok := renderArticle(article); ok {
				return Extracted{
					Title:       article.Title(),
					ContentHTML: content,
					Length:      len(content),
					Byline:      article.Byline(),
					OK:          true,
				}, nil
			}
		}
	}

	// EC-002 fallback: readability failed or produced nothing usable.
	// Parse the (decoded) document ourselves and take <main> or <body> as
	// raw HTML.
	return fallbackExtract(utf8Body), nil
}

// decodeToUTF8 decodes raw response bytes to UTF-8. When the raw bytes are
// already valid UTF-8 they are returned unchanged: a windows-1252 document
// with high bytes is never valid UTF-8, so this is a safe fast path. This
// also covers a golang.org/x/net/html/charset quirk where a short input
// ending in a complete trailing multi-byte rune is trimmed before the UTF-8
// heuristic and falls through to windows-1252.
//
// For non-UTF-8 input, the WHATWG encoding algorithm
// (golang.org/x/net/html/charset) runs its full detection chain: BOM, then a
// <meta> charset prescan, then a UTF-8 validity heuristic, defaulting to
// windows-1252. Unlike go-shiori/dom's chardet, this never mis-detects valid
// UTF-8 documents as latin-1, which is what prevents the short-page mojibake
// bug (TASK-014, DSG-012).
func decodeToUTF8(body []byte) ([]byte, error) {
	if utf8.Valid(body) {
		return body, nil
	}
	r, err := charset.NewReader(bytes.NewReader(body), "")
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// renderArticle renders the readability result to an HTML string,
// returning ok=false when the article is empty (Article.Node is nil, or
// the rendered output is blank).
func renderArticle(article readability.Article) (string, bool) {
	if article.Node == nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := article.RenderHTML(&buf); err != nil {
		return "", false
	}
	content := buf.String()
	if strings.TrimSpace(content) == "" {
		return "", false
	}
	return content, true
}

// fallbackExtract implements the EC-002 fallback: it parses body with
// golang.org/x/net/html and returns the raw HTML of the first usable
// <main> element, or failing that of <body>. If nothing usable exists it
// returns Extracted{OK: false} (no error).
func fallbackExtract(body []byte) Extracted {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return Extracted{OK: false}
	}
	title := documentTitle(doc)

	for _, tag := range []string{"main", "body"} {
		if el := firstElement(doc, tag); el != nil && hasContent(el) {
			if content := renderNode(el); content != "" {
				return Extracted{
					Title:       title,
					ContentHTML: content,
					Length:      len(content),
					OK:          true,
				}
			}
		}
	}
	return Extracted{Title: title, OK: false}
}

// documentTitle returns the trimmed text of the first <title> element in
// doc, or "" when there is none.
func documentTitle(doc *html.Node) string {
	for n := range doc.Descendants() {
		if n.Type == html.ElementNode && n.Data == "title" {
			return strings.TrimSpace(textContent(n))
		}
	}
	return ""
}

// textContent returns the concatenated text of n's descendants.
func textContent(n *html.Node) string {
	var b strings.Builder
	for c := range n.Descendants() {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

// firstElement returns the first element with the given tag in
// depth-first order, checking n itself before its children.
func firstElement(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := firstElement(c, tag); found != nil {
			return found
		}
	}
	return nil
}

// hasContent reports whether n contains any meaningful content: a
// non-whitespace text node, or an element that carries content (media
// elements like <img> count). Comments, whitespace and script/style
// content do not count, so an empty <body> or a body holding only scripts
// is treated as content-free.
func hasContent(n *html.Node) bool {
	if n == nil {
		return false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if strings.TrimSpace(c.Data) != "" {
				return true
			}
		case html.ElementNode:
			if c.Data == "script" || c.Data == "style" {
				continue
			}
			if c.FirstChild == nil {
				if voidElements[c.Data] {
					return true
				}
				continue
			}
			if hasContent(c) {
				return true
			}
		}
	}
	return false
}

// renderNode renders n and its subtree to an HTML string, or "" when
// rendering fails.
func renderNode(n *html.Node) string {
	var buf bytes.Buffer
	if err := html.Render(&buf, n); err != nil {
		return ""
	}
	return buf.String()
}
