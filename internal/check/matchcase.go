package check

import (
	"strings"
	"unicode"

	"github.com/errata-ai/vale/v3/internal/core"
)

// matchCase adapts replacement text to the case of the text it replaces.
//
// A rule's replacement is written one way -- `A-OK` -- but the text it
// corrects may be lower case, capitalized or shouted. Substituting the literal
// leaves `I am A-OK with the plan`, so a rule that means "spell this phrase
// correctly" has to carry the case of what it found.
//
// Only uniform casing is inferred. Text that mixes cases in its own way
// (`iPhone`, `McDonald`) is left alone, because there is no rule to read off
// it and guessing would corrupt a deliberate spelling.
func matchCase(replacement, observed string) string {
	// Case varies across a phrase -- `BLU ray` is shouted then not, `Cross
	// Platform` capitalizes both halves -- so it is read word by word.
	if out, ok := matchWordwise(replacement, observed); ok {
		return out
	}

	switch caseOf(observed) {
	case caseLower:
		return strings.ToLower(replacement)
	case caseUpper:
		return strings.ToUpper(replacement)
	case caseTitle:
		return core.CapFirst(strings.ToLower(replacement))
	case caseNone:
	}

	return replacement
}

// matchWordwise re-cases the replacement word by word, from the text it
// replaces.
//
// The two rarely have the same length -- `to backout` becomes `to back out`,
// and `look in to when` becomes `look into when` -- but the words a rule
// leaves alone sit at the edges unchanged. Aligning those, and treating what
// is left in the middle as one block, carries the case across without needing
// to know which words the rule meant to rewrite.
//
// Separators come from the replacement, so `blu-ray` stays hyphenated even
// though the `BLU ray` it corrects is not.
func matchWordwise(replacement, observed string) (string, bool) {
	rw, seps := words(replacement)
	ow, _ := words(observed)

	if len(rw) == 0 || len(ow) == 0 {
		return "", false
	}

	// Words identical on both sides are ones the rule kept; they anchor the
	// alignment.
	head := 0
	for head < len(rw) && head < len(ow) && strings.EqualFold(rw[head], ow[head]) {
		head++
	}

	tail := 0
	for tail < len(rw)-head && tail < len(ow)-head &&
		strings.EqualFold(rw[len(rw)-1-tail], ow[len(ow)-1-tail]) {
		tail++
	}

	// What is left in the middle is the rewrite itself. Its case comes from
	// the whole of the middle it replaces, since the words do not correspond.
	midWords := ow[head : len(ow)-tail]
	mid := caseOf(strings.Join(midWords, " "))

	// A rule can insert a word rather than change one -- `there an` becoming
	// `there is an` -- leaving no text to read the case from. The word it is
	// inserted before answers for it: `THERE AN` gives `THERE IS AN`, while
	// `Some the` gives `Some of the`. The following word is the one to ask,
	// because a capital on the preceding word may only mean the sentence
	// started there.
	if len(midWords) == 0 {
		switch {
		case tail > 0:
			mid = caseOf(ow[len(ow)-tail])
		case head > 0:
			mid = caseOf(ow[head-1])
		}
	}

	// Title case describes a phrase, not each word in it: `Bad rep` becoming
	// `poor showing` gives `Poor showing`, not `Poor Showing`. Only when every
	// word it replaces is capitalized does each word get a capital.
	midFirst := true
	perWord := mid == caseTitle && allTitle(strings.Join(midWords, " "))

	var b strings.Builder
	for i, w := range rw {
		b.WriteString(seps[i])

		c := mid
		switch {
		case i < head:
			c = caseOf(ow[i])
		case i >= len(rw)-tail:
			c = caseOf(ow[len(ow)-(len(rw)-i)])
		}

		inMid := i >= head && i < len(rw)-tail
		if c == caseTitle && inMid && !perWord && !midFirst {
			c = caseLower
		}
		if inMid {
			midFirst = false
		}

		switch c {
		case caseLower:
			b.WriteString(strings.ToLower(w))
		case caseUpper:
			b.WriteString(strings.ToUpper(w))
		case caseTitle:
			b.WriteString(core.CapFirst(strings.ToLower(w)))
		case caseNone:
			b.WriteString(w)
		}
	}
	b.WriteString(seps[len(rw)])

	return b.String(), true
}

// inWord reports whether position i sits between two letters.
func inWord(runes []rune, i int) bool {
	return i > 0 && i+1 < len(runes) &&
		unicode.IsLetter(runes[i-1]) && unicode.IsLetter(runes[i+1])
}

func isApostrophe(r rune) bool {
	return r == '\'' || r == '\u2019'
}

// words splits s into runs of letters, and the separators around them. The
// separators are returned with one more entry than the words, so the string can
// be rebuilt as sep[0] + word[0] + sep[1] + ... + sep[len(words)].
func words(s string) ([]string, []string) {
	var (
		out  []string
		seps []string
		cur  strings.Builder
		sep  strings.Builder
	)

	runes := []rune(s)
	for i, r := range runes {
		// An apostrophe inside a word belongs to it: splitting `you're` into
		// `you` and `re` makes it two words, and a two-word phrase it replaces
		// then fails to line up.
		if unicode.IsLetter(r) || (isApostrophe(r) && inWord(runes, i)) {
			if sep.Len() > 0 || cur.Len() == 0 {
				seps = append(seps, sep.String())
				sep.Reset()
			}
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
		sep.WriteRune(r)
	}

	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	seps = append(seps, sep.String())

	return out, seps
}

type casing int

const (
	caseNone casing = iota
	caseLower
	caseUpper
	caseTitle
)

// caseOf classifies text by the only three patterns that can be reproduced in
// a different string.
func caseOf(s string) casing {
	var (
		letters, upper, lower int
		firstUpper            bool
		seenLetter            bool
	)

	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		switch {
		case unicode.IsUpper(r):
			upper++
			if !seenLetter {
				firstUpper = true
			}
		case unicode.IsLower(r):
			lower++
		}
		seenLetter = true
	}

	switch {
	case letters == 0:
		return caseNone
	case upper == 0:
		return caseLower
	case lower == 0:
		// A single capital is a capitalized word, not a shouted one; treating
		// "A" as upper case would shout every replacement it stands in for.
		if letters == 1 {
			return caseTitle
		}
		return caseUpper
	case firstUpper && upper == 1:
		return caseTitle
	}

	return caseNone
}

// allTitle reports whether every word in s is capitalized, as a heading or a
// proper name would be. caseOf sees only one string and cannot tell `Cross
// Platform` from `McDonald`.
func allTitle(s string) bool {
	w, _ := words(s)
	if len(w) < 2 {
		return false
	}
	for _, x := range w {
		if caseOf(x) != caseTitle {
			return false
		}
	}
	return true
}

// recase applies matchCase to every suggestion a rule offers.
func recase(params []string, observed string) []string {
	out := make([]string, len(params))
	for i, p := range params {
		out[i] = matchCase(p, observed)
	}
	return out
}
