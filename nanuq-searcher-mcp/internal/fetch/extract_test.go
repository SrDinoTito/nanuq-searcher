package fetch

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// articleBodyText is long enough to satisfy the readability char
// threshold (readabilityCharThreshold = 100) when placed inside a
// paragraph.
const articleBodyText = "This is the body of a test article. It contains enough text to satisfy the readability char threshold so that the parser treats the page as a proper article and returns a non-nil node. Keep it over one hundred characters for safety."

const testArticleHTML = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Test Article</title></head><body><article><h1>Test Article</h1><p>` + articleBodyText + `</p></article></body></html>`

// testMainNoArticleHTML has no text at all: readability yields a nil
// Node, so the EC-002 fallback must pick <main>.
const testMainNoArticleHTML = `<!DOCTYPE html><html lang="en"><head><title>Main Page</title></head><body><main><img src="https://example.com/pic.png" alt="a picture"></main></body></html>`

// testBodyOnlyHTML has no text and no <main>: the fallback must use
// <body>.
const testBodyOnlyHTML = `<!DOCTYPE html><html lang="en"><head><title>Body Page</title></head><body><img src="https://example.com/banner.png" alt="banner"></body></html>`

const testEmptyHTML = `<!DOCTYPE html><html lang="en"><head><title>Empty</title></head><body></body></html>`

// testUTF8HTML mixes multibyte UTF-8 content: Extract must keep the
// non-ASCII text intact. The document declares <meta charset="utf-8">,
// which the WHATWG decoder honors (TASK-014: Extract pre-decodes to UTF-8
// before readability, bypassing go-shiori/dom's statistical chardet).
const testUTF8HTML = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Héllo wörld</title></head><body><article><p>Héllo wörld — ünïcode contént. ` + articleBodyText + `</p></article></body></html>`

// testShortUTF8NoMetaHTML is a short (<200 bytes) UTF-8 document WITHOUT a
// declared charset, containing non-ASCII text (ñ, á, é). Regression fixture
// for TASK-014: go-shiori/dom's statistical chardet (gogs/chardet) used to
// mis-detect documents this short as windows-1252, decoding the UTF-8 bytes
// as latin-1 and producing mojibake ("La niña" → "La niÃ±a").
const testShortUTF8NoMetaHTML = `<html><body><p>La niña come café en la mañana.</p></body></html>`

// testBylineHTML exposes an author via rel="author".
const testBylineHTML = `<!DOCTYPE html><html lang="en"><head><title>Byline Story</title></head><body><article><h1>Story</h1><p><a rel="author">Jane Doe</a></p><p>` + articleBodyText + `</p></article></body></html>`

func TestExtract(t *testing.T) {
	u, err := url.Parse("https://example.com/page")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	tests := []struct {
		name        string
		body        []byte
		wantOK      bool
		wantContain []string // substrings expected in ContentHTML when wantOK
		wantTitle   string
	}{
		{
			name:        "article with paragraph",
			body:        []byte(testArticleHTML),
			wantOK:      true,
			wantContain: []string{"<p>", articleBodyText},
			wantTitle:   "Test Article",
		},
		{
			name:        "main without article falls back",
			body:        []byte(testMainNoArticleHTML),
			wantOK:      true,
			wantContain: []string{"<main", "pic.png"},
			wantTitle:   "Main Page",
		},
		{
			name:        "body only falls back to body",
			body:        []byte(testBodyOnlyHTML),
			wantOK:      true,
			wantContain: []string{"<body", "banner.png"},
			wantTitle:   "Body Page",
		},
		{
			name:   "empty page is not an error",
			body:   []byte(testEmptyHTML),
			wantOK: false,
		},
		{
			name:   "whitespace only is not an error",
			body:   []byte("   \n\t  "),
			wantOK: false,
		},
		{
			name:        "utf8 passes through untouched",
			body:        []byte(testUTF8HTML),
			wantOK:      true,
			wantContain: []string{"Héllo wörld — ünïcode contént."},
			wantTitle:   "Héllo wörld",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Extract(tt.body, u)
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if got.OK != tt.wantOK {
				t.Errorf("Extract().OK = %v, want %v", got.OK, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.Title != tt.wantTitle {
				t.Errorf("Extract().Title = %q, want %q", got.Title, tt.wantTitle)
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(got.ContentHTML, want) {
					t.Errorf("Extract().ContentHTML does not contain %q; got:\n%s", want, got.ContentHTML)
				}
			}
			if got.Length != len(got.ContentHTML) {
				t.Errorf("Extract().Length = %d, want len(ContentHTML) = %d", got.Length, len(got.ContentHTML))
			}
		})
	}
}

// TestExtractShortUTF8NoMojibake is the TASK-014 regression test: a short
// UTF-8 document without a declared charset must keep its non-ASCII text
// intact. Before the fix, go-shiori/dom's chardet mis-detected docs <~200
// bytes as windows-1252 and decoded the UTF-8 bytes as latin-1, producing
// mojibake ("La niña" became "La niÃ±a").
func TestExtractShortUTF8NoMojibake(t *testing.T) {
	u, err := url.Parse("https://example.com/corto")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if len(testShortUTF8NoMetaHTML) >= 200 {
		t.Fatalf("fixture must stay short (<200 bytes) to reproduce the chardet mis-detection; got %d bytes", len(testShortUTF8NoMetaHTML))
	}

	got, err := Extract([]byte(testShortUTF8NoMetaHTML), u)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !got.OK {
		t.Fatal("Extract().OK = false, want true")
	}
	for _, want := range []string{"niña", "café", "mañana"} {
		if !strings.Contains(got.ContentHTML, want) {
			t.Errorf("Extract().ContentHTML does not contain %q; got:\n%s", want, got.ContentHTML)
		}
	}
	for _, mojibake := range []string{"niÃ±a", "cafÃ©", "maÃ±ana", "Ã", "Â"} {
		if strings.Contains(got.ContentHTML, mojibake) {
			t.Errorf("Extract().ContentHTML contains mojibake %q; got:\n%s", mojibake, got.ContentHTML)
		}
	}
}

// TestDecodeToUTF8 verifies the WHATWG pre-decode used by Extract: legacy
// windows-1252 bytes (TASK-014, DSG-012) must be re-encoded to UTF-8 so
// go-shiori/dom's statistical chardet never sees raw non-UTF-8 bytes.
func TestDecodeToUTF8(t *testing.T) {
	// "café" in windows-1252: é is byte 0xE9 (invalid UTF-8 on its own, so
	// the WHATWG algorithm falls back to windows-1252).
	in := []byte{'c', 'a', 'f', 0xE9}
	got, err := decodeToUTF8(in)
	if err != nil {
		t.Fatalf("decodeToUTF8() error = %v", err)
	}
	if !strings.Contains(string(got), "café") {
		t.Errorf("decodeToUTF8(% x) = %q, want decoded 'café'", in, got)
	}

	// Already-valid UTF-8 must pass through unchanged.
	in = []byte("café")
	got, err = decodeToUTF8(in)
	if err != nil {
		t.Fatalf("decodeToUTF8() error = %v", err)
	}
	if string(got) != "café" {
		t.Errorf("decodeToUTF8(%q) = %q, want unchanged", in, got)
	}
}

func TestExtractByline(t *testing.T) {
	u, err := url.Parse("https://example.com/story")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	got, err := Extract([]byte(testBylineHTML), u)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !got.OK {
		t.Fatal("Extract().OK = false, want true")
	}
	if got.Byline != "Jane Doe" {
		t.Errorf("Extract().Byline = %q, want %q", got.Byline, "Jane Doe")
	}
}

func TestExtractNilPageURL(t *testing.T) {
	got, err := Extract([]byte(testArticleHTML), nil)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !got.OK {
		t.Fatal("Extract().OK = false, want true (nil pageURL must not break extraction)")
	}
	if !strings.Contains(got.ContentHTML, articleBodyText) {
		t.Errorf("Extract().ContentHTML missing article body:\n%s", got.ContentHTML)
	}
}

func TestExtractEmptyBody(t *testing.T) {
	got, err := Extract(nil, nil)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if got.OK {
		t.Error("Extract().OK = true, want false for empty body")
	}
}

func TestHasContent(t *testing.T) {
	bodyOf := func(s string) *html.Node {
		doc, err := html.Parse(strings.NewReader(s))
		if err != nil {
			t.Fatalf("html.Parse() error = %v", err)
		}
		return firstElement(doc, "body")
	}

	tests := []struct {
		name string
		html string
		want bool
	}{
		{name: "text content", html: `<body><p>Hello</p></body>`, want: true},
		{name: "whitespace only is not content", html: "<body>   \n\t  </body>", want: false},
		{name: "script and style are not content", html: `<body><script>var x = 1;</script><style>.c {}</style></body>`, want: false},
		{name: "media element counts", html: `<body><img src="x.png" alt="x"></body>`, want: true},
		{name: "empty div is not content", html: `<body><div></div></body>`, want: false},
		{name: "nested content counts", html: `<body><div><p>Hi</p></div></body>`, want: true},
		{name: "empty body is not content", html: `<body></body>`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasContent(bodyOf(tt.html)); got != tt.want {
				t.Errorf("hasContent() = %v, want %v", got, tt.want)
			}
		})
	}

	if hasContent(nil) {
		t.Error("hasContent(nil) = true, want false")
	}
}
