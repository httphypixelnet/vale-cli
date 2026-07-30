package check

import (
	"math/rand"
	"strings"
	"testing"
)

// TestSpellFiltersMatchRegex checks the hand-written filters against the
// patterns they replace, over both fixed cases and random strings.
func TestSpellFiltersMatchRegex(t *testing.T) {
	cases := []string{
		"", "a", "A", "hello", "Hello", "HELLO", "helloW", "CamelCase",
		"camelCase", "XMLHttpRequest", "iOS", "IDs", "don't", "it's", "_foo",
		"foo_bar", "foo-bar", "foo.bar", "café", "naïve", "Straße", "42",
		"a1b2", "ABCd", "aBC", "aBCd", "HTTPServer", "getHTTPResponse",
		"McDonald", "O'Brien", "e.g", "U.S.A", "ZZ", "aZ", "AaB", "AaBc",
	}
	for i := 0; i < 4000; i++ {
		n := 1 + rand.Intn(12)
		var b strings.Builder
		for j := 0; j < n; j++ {
			b.WriteByte(" aAzZ_'0-.é"[rand.Intn(11)])
		}
		cases = append(cases, b.String())
	}

	for _, w := range cases {
		for i, re := range defaultFilters {
			var got bool
			switch i {
			case 0:
				got = skipsCamel(w)
			case 1:
				got = skipsTrailingCaps(w)
			case 2:
				got = skipsNonWord(w)
			}
			if want := re.MatchString(w); got != want {
				t.Fatalf("filter %d (%s) on %q: got %v, want %v",
					i, re.String(), w, got, want)
			}
		}
	}
}
