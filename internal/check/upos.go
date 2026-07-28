package check

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Universal POS tags, expressed in terms of the Penn Treebank tags Vale's
// tagger actually produces.
//
// Penn is the finer tagset, so most of this is a straight widening: NOUN is
// "NN or NNS", ADJ is "JJ, JJR or JJS". Three categories are not expressible
// that way, because the distinction Penn omits is syntactic rather than
// morphological:
//
//   - Penn has no AUX at all. "have" is VBP whether it is the main verb of
//     "I have a car" or the auxiliary of "I have eaten".
//   - IN covers both ADP and SCONJ: "after dinner" and "after we ate".
//   - TO is PART in "to eat" but ADP in "to the store".
//
// Those are approximated with a word list alongside the tag; see uposWords.
// The approximation is stated in the docs rather than hidden, because a rule
// author needs to know that `upos: AUX` means "an auxiliary-looking verb", not
// "an auxiliary".
var uposTags = map[string]string{
	"ADJ":   `JJ|JJR|JJS`,
	"ADP":   `IN|RP`,
	"ADV":   `RB|RBR|RBS|WRB`,
	"AUX":   `MD|VB|VBD|VBG|VBN|VBP|VBZ`,
	"CCONJ": `CC`,
	"DET":   `DT|PDT|WDT|WP\$`,
	"INTJ":  `UH`,
	"NOUN":  `NN|NNS`,
	"NUM":   `CD`,
	"PART":  `TO|RP|POS`,
	"PRON":  `PRP|PRP\$|WP|EX`,
	"PROPN": `NNP|NNPS`,
	"PUNCT": `[.,:]|''|` + "``" + `|-LRB-|-RRB-|\(|\)`,
	"SCONJ": `IN|WRB`,
	"SYM":   `SYM|\$|#`,
	"VERB":  `VB|VBD|VBG|VBN|VBP|VBZ`,
	"X":     `FW|LS`,
}

// uposWords narrows the categories Penn cannot distinguish on its own.
//
// A token must carry both the tag and one of these words to match. Without
// this, `upos: AUX` would match every verb in the document.
var uposWords = map[string][]string{
	"AUX": {
		"am", "is", "are", "was", "were", "be", "been", "being",
		"have", "has", "had", "having",
		"do", "does", "did", "doing",
		"will", "would", "shall", "should", "can", "could",
		"may", "might", "must", "ought",
		"'s", "'re", "'m", "'ve", "'ll", "'d",
	},
	"SCONJ": {
		"after", "although", "as", "because", "before", "even", "if",
		"since", "so", "than", "that", "though", "unless", "until",
		"when", "whenever", "where", "whereas", "wherever", "whether",
		"while", "why", "how",
	},
}

// uposTagPattern returns the Penn-tag regex for a universal tag.
func uposTagPattern(name string) (string, error) {
	pattern, ok := uposTags[strings.ToUpper(name)]
	if !ok {
		return "", fmt.Errorf("unknown universal POS tag %q; expected one of %s",
			name, strings.Join(uposNames(), ", "))
	}
	return "^(?:" + pattern + ")$", nil
}

// uposWordPattern returns the word-level constraint for a universal tag, or
// "" when the tag needs none.
func uposWordPattern(name string) string {
	words, ok := uposWords[strings.ToUpper(name)]
	if !ok {
		return ""
	}

	escaped := make([]string, 0, len(words))
	for _, w := range words {
		escaped = append(escaped, regexp.QuoteMeta(w))
	}

	return "^(?:" + strings.Join(escaped, "|") + ")$"
}

// uposNames lists the supported tags, sorted, for error messages.
func uposNames() []string {
	names := make([]string, 0, len(uposTags))
	for name := range uposTags {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
