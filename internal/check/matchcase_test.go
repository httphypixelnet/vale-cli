package check

import (
	"strings"
	"testing"
)

func TestMatchCase(t *testing.T) {
	cases := []struct {
		name           string
		repl, observed string
		want           string
	}{
		{"lower stays lower", "A-OK", "a ok", "a-ok"},
		{"shouted stays shouted", "A-OK", "A OK", "A-OK"},
		{"capitalized keeps one capital", "A-OK", "A ok", "A-ok"},
		{"already correct", "there", "their", "there"},
		{"sentence start", "there", "Their", "There"},
		{"shouted word", "there", "THEIR", "THERE"},
		{"phrase capitalized", "aware of", "Aware about", "Aware of"},

		// A lone capital is a capitalized word, not a shouted one: "A" must
		// not turn every replacement it stands in for into upper case.
		{"single letter", "and", "A", "And"},

		// Irregular casing carries no rule that can be copied onto another
		// string, so the replacement is left as the rule wrote it.
		{"internal capitals", "iPhone", "IPhone", "iPhone"},
		{"mixed case", "macOS", "MaC oS", "macOS"},
		{"no letters", "replacement", "123", "replacement"},
		{"empty", "replacement", "", "replacement"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchCase(c.repl, c.observed); got != c.want {
				t.Errorf("matchCase(%q, %q) = %q, want %q",
					c.repl, c.observed, got, c.want)
			}
		})
	}
}

// Every suggestion a rule offers has to be re-cased, not just the first.
func TestRecaseAllOptions(t *testing.T) {
	got := recase([]string{"bad rap", "poor showing"}, "Bad rep")

	want := []string{"Bad rap", "Poor showing"}
	if len(got) != len(want) {
		t.Fatalf("recase returned %d options, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("option %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Case is read from the text that was found, so a rule whose replacement is
// already correctly cased is not disturbed by it.
func TestMatchCaseLeavesProperNounsAlone(t *testing.T) {
	if got := matchCase("JavaScript", "javascript"); got != "javascript" {
		t.Errorf("matchCase = %q; lower-case input lower-cases the suggestion,"+
			" which is why matchcase is opt-in", got)
	}
}

// Case can vary across a phrase, and the replacement should carry that through
// rather than flattening it.
func TestMatchCaseWordwise(t *testing.T) {
	cases := []struct {
		name           string
		repl, observed string
		want           string
	}{
		{"shouted then not", "blu-ray", "BLU ray", "BLU-ray"},
		{"both capitalized", "cross-platform", "Cross Platform", "Cross-Platform"},
		{"uniform lower unchanged", "cross-platform", "cross platform", "cross-platform"},
		{"uniform shouted", "cross-platform", "CROSS PLATFORM", "CROSS-PLATFORM"},

		// Differing word counts give nothing to line up, so the phrase is
		// judged as a whole.
		{"count mismatch", "a lot", "Alot", "A lot"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchCase(c.repl, c.observed); got != c.want {
				t.Errorf("matchCase(%q, %q) = %q, want %q",
					c.repl, c.observed, got, c.want)
			}
		})
	}
}

// Rebuilding a phrase must not lose or move its punctuation.
func TestWordsRoundTrip(t *testing.T) {
	for _, s := range []string{
		"", "hello", "blu-ray", "  spaced  out  ", "a's and b's",
		"...", "Cross-Platform!", "héllo wörld",
	} {
		w, seps := words(s)

		var b strings.Builder
		for i, x := range w {
			b.WriteString(seps[i])
			b.WriteString(x)
		}
		b.WriteString(seps[len(w)])

		if got := b.String(); got != s {
			t.Errorf("words(%q) rebuilt as %q", s, got)
		}
	}
}
