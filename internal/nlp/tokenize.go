package nlp

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// IterTokenizer extracts words from a sentence.
//
// This is a word extractor for spell checking and location matching, not a
// linguistic tokenizer: it keeps contractions whole ("Don't" stays one token)
// and drops standalone punctuation. prose's tokenizer splits the other way, so
// the two are not interchangeable here.
type IterTokenizer struct {
	specialRE *regexp.Regexp
	sanitizer *strings.Replacer
	suffixes  []string
	prefixes  []string
	emoticons map[string]int
}

// NewIterTokenizer creates a new IterTokenizer.
func NewIterTokenizer() *IterTokenizer {
	return &IterTokenizer{
		emoticons: emoticons,
		prefixes:  prefixes,
		sanitizer: sanitizer,
		specialRE: internalRE,
		suffixes:  suffixes,
	}
}

func addToken(s string, toks []string) []string {
	if !allNonLetter(s) {
		toks = append(toks, s)
	}
	return toks
}

func (t *IterTokenizer) isSpecial(token string) bool {
	_, found := t.emoticons[token]
	return found || t.specialRE.MatchString(token)
}

func (t *IterTokenizer) doSplit(token string) []string {
	var tokens []string

	last := 0
	for token != "" && StrLen(token) != last {
		if t.isSpecial(token) {
			// We've found a special case (e.g., an emoticon) -- so, we add it as a token without
			// any further processing.
			tokens = addToken(token, tokens)
			break
		}
		last = StrLen(token)
		if hasAnyPrefix(token, t.prefixes) {
			// Remove prefixes -- e.g., $100 -> [$, 100].
			token = token[1:]
		} else if hasAnySuffix(token, t.suffixes) {
			// Remove suffixes -- e.g., Well) -> [Well, )].
			token = token[:len(token)-1]
		} else {
			tokens = addToken(token, tokens)
		}
	}

	return tokens
}

// Tokenize splits a sentence into a slice of words.
func (t *IterTokenizer) Tokenize(text string) []string {
	var tokens []string

	clean, white := t.sanitizer.Replace(text), false
	length := len(clean)

	start, index := 0, 0
	cache := map[string][]string{}
	for index <= length {
		uc, size := utf8.DecodeRuneInString(clean[index:])
		if size == 0 {
			break
		} else if index == 0 {
			white = unicode.IsSpace(uc)
		}
		if unicode.IsSpace(uc) != white {
			if start < index {
				span := clean[start:index]
				if toks, found := cache[span]; found {
					tokens = append(tokens, toks...)
				} else {
					toks = t.doSplit(span)
					cache[span] = toks
					tokens = append(tokens, toks...)
				}
			}
			if uc == ' ' {
				start = index + 1
			} else {
				start = index
			}
			white = !white
		}
		index += size
	}

	if start < index {
		tokens = append(tokens, t.doSplit(clean[start:index])...)
	}

	return tokens
}
