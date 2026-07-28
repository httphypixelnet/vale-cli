package check

import "testing"

// runeSpanToBytes underpins every anchored alert; an off-by-one here
// mislocates findings rather than failing loudly.
func TestRuneSpanToBytes(t *testing.T) {
	cases := []struct {
		name     string
		s        string
		from, to int
		want     string
		ok       bool
	}{
		{"ascii start", "hello world", 0, 5, "hello", true},
		{"ascii middle", "hello world", 6, 11, "world", true},
		{"ascii to end", "hello", 0, 5, "hello", true},
		{"empty span", "hello", 2, 2, "", true},
		{"multibyte before", "héllo wörld", 6, 11, "wörld", true},
		{"multibyte inside", "héllo", 0, 5, "héllo", true},
		{"emoji", "a 🎉 b", 2, 3, "🎉", true},
		{"cjk", "漢字テスト", 0, 2, "漢字", true},
		{"whole string", "naïve", 0, 5, "naïve", true},
		{"negative", "abc", -1, 2, "", false},
		{"reversed", "abc", 2, 1, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lo, hi, ok := runeSpanToBytes(c.s, c.from, c.to)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if got := c.s[lo:hi]; got != c.want {
				t.Errorf("s[%d:%d] = %q, want %q", lo, hi, got, c.want)
			}
		})
	}
}

// The conversion must agree with the rune slicing it replaces, for every span
// of a multi-byte string.
func TestRuneSpanToBytesMatchesRuneSlicing(t *testing.T) {
	for _, s := range []string{
		"hello world",
		"héllo wörld",
		"漢字テストです",
		"a 🎉 b 😀 c",
		"naïve café résumé",
		"",
	} {
		runes := []rune(s)
		for from := 0; from <= len(runes); from++ {
			for to := from; to <= len(runes); to++ {
				lo, hi, ok := runeSpanToBytes(s, from, to)
				if !ok {
					t.Fatalf("%q [%d:%d]: not ok", s, from, to)
				}
				want := string(runes[from:to])
				if got := s[lo:hi]; got != want {
					t.Errorf("%q [%d:%d] = %q, want %q", s, from, to, got, want)
				}
			}
		}
	}
}
