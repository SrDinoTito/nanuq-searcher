package markdown

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html/charset"
)

const (
	// defaultMaxBytes is the DSG-006/EC-008 cap on the converted markdown
	// output, applied when ConvertHTML receives maxBytes == 0. It mirrors the
	// fetch stage's body limit (REQ-008).
	defaultMaxBytes = 2 << 20 // 2 MiB

	// truncNoteFormat is the visible marker appended when the converted
	// markdown is truncated (DSG-006/EC-008). Args: original byte length and
	// the maxBytes cap — e.g. "\n\n_[truncado: 24000 bytes > 200]_\n".
	truncNoteFormat = "\n\n_[truncado: %d bytes > %d]_\n"
)

// ConvertHTML converts an HTML document body to GitHub Flavored Markdown
// (DSG-006 step 3, EC-008): CommonMark rendering with GFM tables, fenced
// code blocks and strikethrough enabled via the html-to-markdown v2 plugins.
//
// The body is expected to be the raw bytes captured by the fetch stage
// (fetch.Response.Body), which are NOT yet UTF-8 decoded. charsetLabel is the
// detected charset name (fetch.Response.Charset); the body is decoded to
// UTF-8 with the WHATWG encoding algorithm before conversion, so the returned
// markdown is always valid UTF-8.
//
// maxBytes caps the returned markdown in bytes (default 2 MiB when 0;
// negative values are rejected as configuration bugs). When the converted
// markdown exceeds the cap it is cut so the output never exceeds maxBytes,
// and a visible note is appended: "\n\n_[truncado: N bytes > max]_\n".
//
// The HTML <title> is intentionally not extracted here: the title/frontmatter
// concern belongs to the tool layer (TASK-010).
func ConvertHTML(body []byte, charsetLabel string, maxBytes int) (string, error) {
	if maxBytes < 0 {
		return "", fmt.Errorf("markdown: maxBytes must be >= 0, got %d", maxBytes)
	}
	if maxBytes == 0 {
		maxBytes = defaultMaxBytes
	}
	if len(body) == 0 {
		return "", nil
	}

	utf8HTML, err := decodeToUTF8(body, charsetLabel)
	if err != nil {
		return "", err
	}

	// GFM plugin set: base (node removal + whitespace collapse) + commonmark
	// (headings, lists, links, images, fenced code) + table + strikethrough.
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(),
			strikethrough.NewStrikethroughPlugin(),
		),
	)

	md, err := conv.ConvertString(utf8HTML)
	if err != nil {
		return "", fmt.Errorf("markdown: converting html: %w", err)
	}

	return truncate(md, maxBytes), nil
}

// decodeToUTF8 decodes body (raw bytes from the fetch stage) to a UTF-8
// string using the WHATWG encoding algorithm (golang.org/x/net/html/charset):
// BOM, then the charsetLabel content-type parameter, then a <meta> prescan,
// then a UTF-8 heuristic, defaulting to windows-1252. An empty or unknown
// label degrades to the same sniffing the fetch stage uses for detection, so
// decoding never fails on a bad label.
func decodeToUTF8(body []byte, charsetLabel string) (string, error) {
	contentType := "text/html"
	if label := strings.TrimSpace(charsetLabel); label != "" {
		contentType += "; charset=" + label
	}
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return "", fmt.Errorf("markdown: creating charset reader: %w", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("markdown: decoding body to utf-8: %w", err)
	}
	return string(out), nil
}

// truncate caps md at maxBytes (DSG-006/EC-008). When md fits it is returned
// unchanged. Otherwise the content is cut to leave room for a visible
// truncation note, and the result is guaranteed to be valid UTF-8 and never
// longer than maxBytes.
func truncate(md string, maxBytes int) string {
	b := []byte(md)
	if len(b) <= maxBytes {
		return md
	}

	note := []byte(fmt.Sprintf(truncNoteFormat, len(b), maxBytes))
	contentBudget := maxBytes - len(note)
	if contentBudget <= 0 {
		// The note alone does not fit; emit as much of it as possible.
		if len(note) > maxBytes {
			return string(note[:maxBytes])
		}
		return string(note)
	}

	out := make([]byte, 0, maxBytes)
	out = append(out, cutAtRuneBoundary(b, contentBudget)...)
	out = append(out, note...)
	return string(out)
}

// cutAtRuneBoundary returns b[:n] trimmed so it ends on a UTF-8 rune
// boundary. A naive byte cut can split a multi-byte rune; dropping the
// incomplete trailing rune keeps the output valid UTF-8 while never
// exceeding n bytes.
func cutAtRuneBoundary(b []byte, n int) []byte {
	if n >= len(b) {
		return b
	}
	cut := b[:n]
	for len(cut) > 0 {
		r, size := utf8.DecodeLastRune(cut)
		if r != utf8.RuneError || size > 1 {
			return cut
		}
		cut = cut[:len(cut)-1]
	}
	return cut
}
