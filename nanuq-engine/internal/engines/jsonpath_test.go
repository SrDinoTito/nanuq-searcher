package engines

import (
	"reflect"
	"testing"
)

func TestJSONPathParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"simple", "a/b/c", []string{"a", "b", "c"}},
		{"single", "url", []string{"url"}},
		{"empty parts skipped", "a//b/", []string{"a", "b"}},
		{"leading slash", "/a/b", []string{"a", "b"}},
		{"empty string", "", []string{}},
		{"brackets kept", "a/[]/b", []string{"a", "[]", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Parse(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Parse(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIsIterable(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want bool
	}{
		{"object", map[string]any{}, true},
		{"array", []any{}, true},
		{"string not iterable", "str", false},
		{"int not iterable", 42, false},
		{"nil not iterable", nil, false},
		{"bool not iterable", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsIterable(c.in); got != c.want {
				t.Fatalf("IsIterable(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestJSONPathQuery(t *testing.T) {
	data := map[string]any{
		"a": map[string]any{
			"b":      "value",
			"list":   []any{"x", "y"},
			"nested": []any{map[string]any{"k": "n1"}, map[string]any{"k": "n2"}},
		},
		"arr":    []any{map[string]any{"k": 1}, map[string]any{"k": 2}},
		"scalar": 42,
	}

	t.Run("map descent", func(t *testing.T) {
		v, ok := Query(data, Parse("a/b"))
		if !ok {
			t.Fatal("a/b should match")
		}
		if v != "value" {
			t.Fatalf("a/b = %v, want value", v)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		if v, ok := Query(data, Parse("a/missing")); ok {
			t.Fatalf("a/missing should not match, got %v", v)
		}
	})

	t.Run("array returned as-is", func(t *testing.T) {
		v, ok := Query(data, Parse("arr"))
		if !ok {
			t.Fatal("arr should match")
		}
		arr, isArr := v.([]any)
		if !isArr || len(arr) != 2 {
			t.Fatalf("arr = %v, want 2-element array", v)
		}
	})

	t.Run("array iterate concatenates", func(t *testing.T) {
		v, ok := Query(data, Parse("arr/k"))
		if !ok {
			t.Fatal("arr/k should match")
		}
		want := []any{1, 2}
		if !reflect.DeepEqual(v, want) {
			t.Fatalf("arr/k = %v, want %v", v, want)
		}
	})

	t.Run("array index", func(t *testing.T) {
		v, ok := Query(data, Parse("arr/0/k"))
		if !ok || v != 1 {
			t.Fatalf("arr/0/k = %v (ok=%v), want 1", v, ok)
		}
	})

	t.Run("array index out of range", func(t *testing.T) {
		if v, ok := Query(data, Parse("arr/5/k")); ok {
			t.Fatalf("arr/5/k should not match, got %v", v)
		}
	})

	t.Run("nested array iterate", func(t *testing.T) {
		v, ok := Query(data, Parse("a/nested/k"))
		if !ok {
			t.Fatal("a/nested/k should match")
		}
		want := []any{"n1", "n2"}
		if !reflect.DeepEqual(v, want) {
			t.Fatalf("a/nested/k = %v, want %v", v, want)
		}
	})

	t.Run("bracket token iterates", func(t *testing.T) {
		v, ok := Query(data, Parse("a/nested/[]/k"))
		if !ok {
			t.Fatal("a/nested/[]/k should match")
		}
		want := []any{"n1", "n2"}
		if !reflect.DeepEqual(v, want) {
			t.Fatalf("a/nested/[]/k = %v, want %v", v, want)
		}
	})

	t.Run("bracket token at end returns array", func(t *testing.T) {
		v, ok := Query(data, Parse("arr/[]"))
		if !ok {
			t.Fatal("arr/[] should match")
		}
		if _, isArr := v.([]any); !isArr {
			t.Fatalf("arr/[] = %v, want array", v)
		}
	})

	t.Run("scalar with pending tokens", func(t *testing.T) {
		if v, ok := Query(data, Parse("scalar/x")); ok {
			t.Fatalf("scalar/x should not match, got %v", v)
		}
	})

	t.Run("scalar direct", func(t *testing.T) {
		v, ok := Query(data, Parse("scalar"))
		if !ok || v != 42 {
			t.Fatalf("scalar = %v (ok=%v), want 42", v, ok)
		}
	})

	t.Run("empty tokens", func(t *testing.T) {
		if v, ok := Query(data, nil); ok {
			t.Fatalf("empty tokens should not match, got %v", v)
		}
	})

	t.Run("query on scalar", func(t *testing.T) {
		if v, ok := Query(42, Parse("k")); ok {
			t.Fatalf("query on scalar should not match, got %v", v)
		}
	})
}

func TestJSONPathIterate(t *testing.T) {
	data := map[string]any{
		"results": []any{
			map[string]any{"url": "u1", "title": "t1"},
			map[string]any{"url": "u2", "title": "t2"},
		},
	}

	v, ok := Query(data, Parse("results"))
	if !ok {
		t.Fatal("results should match")
	}
	arr, isArr := v.([]any)
	if !isArr || len(arr) != 2 {
		t.Fatalf("results = %v, want 2-element array", v)
	}

	wantURLs := []string{"u1", "u2"}
	for i, el := range arr {
		urlVal, ok := Query(el, Parse("url"))
		if !ok || urlVal != wantURLs[i] {
			t.Fatalf("results[%d]/url = %v (ok=%v), want %q", i, urlVal, ok, wantURLs[i])
		}
	}
}
