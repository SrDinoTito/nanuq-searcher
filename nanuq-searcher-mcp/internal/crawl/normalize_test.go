package crawl

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already normal", "http://example.com/a/b?q=1", "http://example.com/a/b?q=1"},
		{"uppercase host", "HTTP://EXAMPLE.COM/Path", "http://example.com/Path"},
		{"uppercase scheme", "HTTPS://Example.com/x", "https://example.com/x"},
		{"strip http default port", "http://example.com:80/a", "http://example.com/a"},
		{"strip https default port", "https://example.com:443/a", "https://example.com/a"},
		{"keep non-default port", "http://example.com:8080/a", "http://example.com:8080/a"},
		{"http on 443 keeps port", "http://example.com:443/a", "http://example.com:443/a"},
		{"https on 80 keeps port", "https://example.com:80/a", "https://example.com:80/a"},
		{"strip fragment", "http://example.com/a#section", "http://example.com/a"},
		{"keep query strip fragment", "http://example.com/a?q=1#frag", "http://example.com/a?q=1"},
		{"uppercase host port query", "http://EXAMPLE.COM:8080/A?X=1", "http://example.com:8080/A?X=1"},
		{"root only", "https://example.com", "https://example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeURL(tc.in)
			if err != nil {
				t.Fatalf("NormalizeURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeURLErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"missing scheme", "example.com/x"},
		{"missing host", "http:///x"},
		{"not a url", "://bad"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := NormalizeURL(tc.in); err == nil {
				t.Errorf("NormalizeURL(%q) = %q, want error", tc.in, got)
			}
		})
	}
}

func TestIsSameHost(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"same host case-insensitive", "http://Example.com/a", "http://example.com/b", true},
		{"https default port ignored", "https://example.com:443/x", "https://example.com/y", true},
		{"http default port ignored", "http://example.com:80/x", "http://example.com/y", true},
		{"scheme ignored", "http://example.com/x", "https://example.com/x", true},
		{"http on 443 differs", "http://example.com:443/x", "http://example.com/y", false},
		{"non-default port differs", "http://example.com/a", "http://example.com:8080/b", false},
		{"different host", "http://a.com/x", "http://b.com/x", false},
		{"unparsable false", "://bad", "http://example.com/x", false},
		{"missing host false", "http:///x", "http://example.com/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSameHost(tc.a, tc.b); got != tc.want {
				t.Errorf("IsSameHost(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
