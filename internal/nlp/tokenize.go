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
		switch {
		case hasAnyPrefix(token, t.prefixes):
			// Remove prefixes -- e.g., $100 -> [$, 100].
			token = token[1:]
		case hasAnySuffix(token, t.suffixes):
			// Remove suffixes -- e.g., Well) -> [Well, )].
			token = token[:len(token)-1]
		default:
			tokens = addToken(token, tokens)
		}
	}

	return tokens
}

// Tokenize splits a sentence into a slice of words.
func (t *IterTokenizer) Tokenize(text string) []string {
	tokens, _ := t.tokenize(text, false)
	return tokens
}

// TokenizeWithOffsets splits a sentence into words and reports where each one
// begins, as a byte index into `text`.
//
// An offset is -1 when the word cannot be pointed at: the tokenizer normalizes
// curly quotes and entities before splitting, so a word holding one is not a
// substring of the text it came from. Callers must treat -1 as "unknown"
// rather than as a position.
//
// This exists so that a check can report where in its block a match was. The
// alternative is for the match to be located afterwards by searching the
// context for its text, which costs a scan of the whole document per alert --
// and spelling produces alerts by the thousand.
func (t *IterTokenizer) TokenizeWithOffsets(text string) ([]string, []int) {
	return t.tokenize(text, true)
}

// tokenize walks the whitespace-delimited runs of text and splits each one.
//
// Sanitizing happens per run rather than over the whole text: the replacements
// neither introduce nor remove whitespace, so the runs are the same either way,
// and doing it here keeps each run's position in the original text -- which is
// what an offset has to be measured against.
func (t *IterTokenizer) tokenize(text string, locate bool) ([]string, []int) {
	var tokens []string
	var offsets []int

	white, length := false, len(text)
	start, index := 0, 0

	cache := map[string][]string{}
	split := func(span string) []string {
		if toks, found := cache[span]; found {
			return toks
		}
		toks := t.doSplit(t.sanitizer.Replace(span))
		cache[span] = toks
		return toks
	}

	emit := func(from, to int) {
		span := text[from:to]
		for _, tok := range split(span) {
			tokens = append(tokens, tok)
			if !locate {
				continue
			}
			// Within a single run there is nothing to confuse the search: the
			// token is the run minus whatever the splitter stripped off its
			// ends.
			at := -1
			if i := strings.Index(span, tok); i >= 0 {
				at = from + i
			}
			offsets = append(offsets, at)
		}
	}

	for index <= length {
		uc, size := utf8.DecodeRuneInString(text[index:])
		if size == 0 {
			break
		} else if index == 0 {
			white = unicode.IsSpace(uc)
		}
		if unicode.IsSpace(uc) != white {
			if start < index {
				emit(start, index)
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
		emit(start, index)
	}

	return tokens, offsets
}
