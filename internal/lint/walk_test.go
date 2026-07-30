package lint

import (
	"testing"
)

func TestSubInplace(t *testing.T) {
	cases := []struct {
		desc     string
		ctx      string
		sub      string
		want     string
		expected bool
	}{
		{
			desc:     "simple word",
			ctx:      "the quick fox",
			sub:      "quick",
			want:     "the @@@@@ fox",
			expected: true,
		},
		{
			desc:     "only first occurrence",
			ctx:      "foo foo", //nolint:dupword // intentional repeat
			sub:      "foo",
			want:     "@@@ foo",
			expected: true,
		},
		{
			desc:     "not found",
			ctx:      "hello",
			sub:      "world",
			want:     "hello",
			expected: false,
		},
		{
			desc:     "whole context equals sub",
			ctx:      "/",
			sub:      "/",
			want:     "@",
			expected: true,
		},
		{
			desc:     "repeated symbols (see #1099)",
			ctx:      "////",
			sub:      "////",
			want:     "@@@@",
			expected: true,
		},
		{
			desc:     "newlines are preserved",
			ctx:      "a\nb",
			sub:      "a\nb",
			want:     "@\n@",
			expected: true,
		},
		{
			desc:     "multi-byte runes are preserved",
			ctx:      "café",
			sub:      "café",
			want:     "@@@é",
			expected: true,
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			buf := []byte(c.ctx)

			found := subInplace(buf, c.sub, '@')
			if found != c.expected {
				t.Fatalf("found = %v; want %v", found, c.expected)
			}

			if got := string(buf); got != c.want {
				t.Fatalf("result = %q; want %q", got, c.want)
			}

			// The mask is length-preserving: positions in the context must
			// remain stable so that later lookups still line up.
			if len(buf) != len(c.ctx) {
				t.Fatalf("length changed: got %d, want %d", len(buf), len(c.ctx))
			}
		})
	}
}

// TestSubInplaceReadOnlyBacking guards against the regression in #1099, where
// the walker aliased a (potentially read-only) string backing array and then
// wrote to it. newWalker now copies the content, so the buffer handed to
// subInplace is always writable -- even when f.Content is a constant.
func TestSubInplaceReadOnlyBacking(t *testing.T) {
	const constContent = "////" // lives in the binary's read-only data.

	buf := []byte(constContent)
	if !subInplace(buf, constContent, '@') {
		t.Fatal("expected a match")
	}

	if string(buf) != "@@@@" {
		t.Fatalf("result = %q; want %q", string(buf), "@@@@")
	}
}

func TestScopeSafe(t *testing.T) {
	cases := map[string]bool{
		"title":           true,
		"admonitionblock": true,
		"sect-level1":     true,
		"h2":              true,
		"":                false,
		"a.b":             false, // `.` separates scope parts
		"a&b":             false, // `&` combines them
		"~a":              false, // `~` negates one
	}
	for in, want := range cases {
		if got := scopeSafe(in); got != want {
			t.Errorf("scopeSafe(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWalkerClasses(t *testing.T) {
	tests := []struct {
		name string
		cls  []string
		want []string
	}{
		{"none", []string{"", ""}, nil},
		{"one", []string{"", "title"}, []string{"title"}},
		{"multi-valued", []string{"admonitionblock note"}, []string{"admonitionblock", "note"}},
		{"repeats collapse", []string{"title", "title"}, []string{"title"}},
		{"unsafe dropped", []string{"a.b good"}, []string{"good"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &walker{clsHistory: tt.cls}
			got := w.classes()
			if len(got) != len(tt.want) {
				t.Fatalf("classes() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("classes() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}
