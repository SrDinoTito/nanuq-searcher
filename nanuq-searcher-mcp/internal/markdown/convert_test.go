package markdown

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestConvertHTML(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		maxBytes int
		wantErr  bool
		contains []string
	}{
		{
			name: "empty body",
			body: "",
		},
		{
			name:     "link",
			body:     `<a href="https://go.dev/">Go</a>`,
			contains: []string{"[Go](https://go.dev/)"},
		},
		{
			name:     "image",
			body:     `<img src="/logo.png" alt="logo">`,
			contains: []string{"![logo](/logo.png)"},
		},
		{
			name:     "heading and bold",
			body:     `<h1>Title</h1><p>Hello <strong>world</strong></p>`,
			contains: []string{"# Title", "**world**"},
		},
		{
			name:     "fenced code",
			body:     "<pre><code>fmt.Println(\"hi\")\n</code></pre>",
			contains: []string{"```", `fmt.Println("hi")`},
		},
		{
			name:     "code with language",
			body:     "<pre><code class=\"language-go\">package main\n</code></pre>",
			contains: []string{"```go\npackage main"},
		},
		{
			name: "gfm table",
			body: `<table><thead><tr><th>A</th><th>B</th></tr></thead>` +
				`<tbody><tr><td>1</td><td>2</td></tr></tbody></table>`,
			contains: []string{"|", "---", "A", "B", "1", "2"},
		},
		{
			name:     "negative maxBytes",
			body:     `<p>hi</p>`,
			maxBytes: -1,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertHTML([]byte(tt.body), "", tt.maxBytes)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ConvertHTML() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ConvertHTML() unexpected error: %v", err)
			}
			if tt.body == "" {
				if got != "" {
					t.Fatalf("ConvertHTML() empty body = %q, want %q", got, "")
				}
				return
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("ConvertHTML() output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestConvertHTMLCharsetISO885915(t *testing.T) {
	// é = 0xE9, € = 0xA4 in ISO-8859-15. Input is NOT valid UTF-8.
	body := []byte("<p>Caf\xe9 \xa4uro</p>")
	got, err := ConvertHTML(body, "iso-8859-15", 0)
	if err != nil {
		t.Fatalf("ConvertHTML() unexpected error: %v", err)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("ConvertHTML() output is not valid UTF-8: %q", got)
	}
	if !strings.Contains(got, "Café €uro") {
		t.Errorf("ConvertHTML() output %q missing decoded \"Café €uro\"", got)
	}
}

func TestConvertHTMLCharsetEmptyLabelUTF8(t *testing.T) {
	// Valid UTF-8 input with an empty label must round-trip untouched.
	body := []byte("<p>Héllo wörld</p>")
	got, err := ConvertHTML(body, "", 0)
	if err != nil {
		t.Fatalf("ConvertHTML() unexpected error: %v", err)
	}
	if !strings.Contains(got, "Héllo wörld") {
		t.Errorf("ConvertHTML() output %q missing \"Héllo wörld\"", got)
	}
}

func TestConvertHTMLDefaultMaxBytes(t *testing.T) {
	// maxBytes == 0 falls back to the 2 MiB default; this input is far below
	// it, so no truncation note appears.
	body := "<p>hi " + strings.Repeat("there ", 100) + "</p>"
	got, err := ConvertHTML([]byte(body), "", 0)
	if err != nil {
		t.Fatalf("ConvertHTML() unexpected error: %v", err)
	}
	if !strings.Contains(got, "hi") {
		t.Errorf("ConvertHTML() output %q missing content", got)
	}
	if strings.Contains(got, "truncado") {
		t.Errorf("ConvertHTML() unexpectedly truncated: %q", got)
	}
}

func TestConvertHTMLTruncateNote(t *testing.T) {
	body := "<p>" + strings.Repeat("lorem ipsum ", 2000) + "</p>"

	full, err := ConvertHTML([]byte(body), "", 1<<20)
	if err != nil {
		t.Fatalf("ConvertHTML() full conversion error: %v", err)
	}

	const max = 200
	got, err := ConvertHTML([]byte(body), "", max)
	if err != nil {
		t.Fatalf("ConvertHTML() truncated conversion error: %v", err)
	}
	if len([]byte(got)) != max {
		t.Fatalf("ConvertHTML() truncated output length = %d, want exactly %d", len([]byte(got)), max)
	}
	if !strings.HasSuffix(got, "]_\n") {
		t.Errorf("ConvertHTML() truncated output %q does not end with the truncation note", got)
	}
	if !strings.Contains(got, "_[truncado: ") {
		t.Errorf("ConvertHTML() truncated output %q missing truncation note marker", got)
	}
	wantNote := fmt.Sprintf("_[truncado: %d bytes > %d]_", len([]byte(full)), max)
	if !strings.Contains(got, wantNote) {
		t.Errorf("ConvertHTML() truncated output %q missing note %q", got, wantNote)
	}
}

func TestConvertHTMLTruncateKeepsValidUTF8(t *testing.T) {
	// Multi-byte UTF-8 runes (é = 2 bytes) crossing the cut point must not be
	// split into invalid sequences at any of these caps.
	body := "<p>" + strings.Repeat("é", 300) + "</p>"
	for _, max := range []int{51, 64, 90, 128} {
		got, err := ConvertHTML([]byte(body), "", max)
		if err != nil {
			t.Fatalf("ConvertHTML() maxBytes=%d error: %v", max, err)
		}
		if l := len([]byte(got)); l > max {
			t.Errorf("ConvertHTML() maxBytes=%d output length %d exceeds cap", max, l)
		}
		if !utf8.ValidString(got) {
			t.Errorf("ConvertHTML() maxBytes=%d output is not valid UTF-8: %q", max, got)
		}
		if !strings.Contains(got, "truncado") {
			t.Errorf("ConvertHTML() maxBytes=%d output missing truncation note: %q", max, got)
		}
	}
}

func TestConvertHTMLTruncateNoteDoesNotFit(t *testing.T) {
	// maxBytes so small the note alone cannot fit: emit as much of the note
	// as possible (budget <= 0 branch).
	body := "<p>" + strings.Repeat("x", 10000) + "</p>"
	const max = 10
	got, err := ConvertHTML([]byte(body), "", max)
	if err != nil {
		t.Fatalf("ConvertHTML() error: %v", err)
	}
	if len([]byte(got)) != max {
		t.Fatalf("ConvertHTML() output length = %d, want exactly %d", len([]byte(got)), max)
	}
	if !strings.HasPrefix(got, "\n\n_[trunc") {
		t.Errorf("ConvertHTML() output %q does not start with the truncation note", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("ConvertHTML() output is not valid UTF-8: %q", got)
	}
}

func TestCutAtRuneBoundary(t *testing.T) {
	b := []byte("aé") // 'a' = 1 byte, 'é' = 2 bytes (0xC3 0xA9)
	tests := []struct {
		n    int
		want string
	}{
		{n: 1, want: "a"},
		{n: 2, want: "a"}, // cut in the middle of é -> drop the partial rune
		{n: 3, want: "aé"},
		{n: 10, want: "aé"},
	}
	for _, tt := range tests {
		got := string(cutAtRuneBoundary(b, tt.n))
		if got != tt.want {
			t.Errorf("cutAtRuneBoundary(%q, %d) = %q, want %q", b, tt.n, got, tt.want)
		}
	}
}
