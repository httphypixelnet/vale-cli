package regex

import (
	"strings"
	"testing"
)

func TestWithoutLookarounds(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{"none", `\bfoo\b`, `\bfoo\b`, false},
		{"lookbehind", `(?<=\bthe\s)foo`, `foo`, true},
		{"negative lookbehind", `(?<!\bthe\s)foo`, `foo`, true},
		{"lookahead", `foo(?=\sbar)`, `foo`, true},
		{"negative lookahead", `foo(?!\sbar)`, `foo`, true},
		{"both ends", `(?<=a)foo(?!b)`, `foo`, true},
		{"nested group inside", `(?<=(?:a|b)\s)foo`, `foo`, true},
		{"escaped paren inside", `(?<=\(x\))foo`, `foo`, true},
		{"keeps ordinary groups", `(?:a|b)foo`, `(?:a|b)foo`, false},
		{"keeps named group", `(?P<x>a)foo`, `(?P<x>a)foo`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := withoutLookarounds(tt.in)
			if got != tt.want || changed != tt.changed {
				t.Errorf("withoutLookarounds(%q) = (%q, %v), want (%q, %v)",
					tt.in, got, changed, tt.want, tt.changed)
			}
		})
	}
}

// The filter may only ever skip work. A literal it reports as required must be
// present in every string the pattern matches, or a real alert disappears.
func TestRequiredThroughLookaroundsStaysSound(t *testing.T) {
	cases := []struct {
		expr    string
		matches []string
	}{
		{`(?<=\bfascinated\s)(?:about|above)`, []string{"fascinated about", "fascinated above"}},
		{`\bself[-\s]+(?!harm\b)(?:censor|censors)`, []string{"self-censor", "self censors"}},
		{`(?i)\b(?:do|does)\s+(?=a lot)`, []string{"do a lot", "does a lot"}},
		{`foo(?!bar)`, []string{"foo", "foobaz"}},
	}

	for _, c := range cases {
		lits := Required(c.expr)
		if len(lits) == 0 {
			continue // no claim made, nothing to check
		}
		for _, subject := range c.matches {
			lowered := strings.ToLower(subject)
			ok := false
			for _, lit := range lits {
				if strings.Contains(lowered, lit) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%q: filter %v rejects %q, which the pattern matches",
					c.expr, lits, subject)
			}
		}
	}
}
