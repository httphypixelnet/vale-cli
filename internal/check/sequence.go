package check

import (
	"fmt"
	"strings"

	rx "github.com/errata-ai/vale/v3/internal/regex"
	"github.com/jdkato/prose/v3/tag"
	"github.com/mitchellh/mapstructure"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/nlp"
)

// NLPToken represents a token of text with NLP-related attributes.
type NLPToken struct {
	Pattern string
	Tag     string
	Skip    int

	// UPOS matches a universal part-of-speech tag -- NOUN, VERB, ADJ and so
	// on -- rather than a Penn Treebank one.
	//
	// It compiles down to the equivalent Penn tags, plus a word constraint for
	// the few categories Penn cannot express (see upos.go). Rules written
	// against universal tags are portable; rules written against Penn tags are
	// more precise.
	UPOS string

	// Target narrows the alert to this token alone.
	//
	// Without it a match spans every token in the sequence. Marking one lets a
	// rule require surrounding context while pointing at only the part the
	// writer should change -- "flag the space, but only between these two
	// words".
	Target bool

	re *rx.Regexp

	// wordRe narrows a universal tag to the words it can apply to.
	//
	// Kept apart from `re` because that one doubles as the anchor and is run
	// against the whole sentence; this is only ever tested against a single
	// token's text.
	wordRe *rx.Regexp

	Negate   bool
	optional bool
	start    bool
	end      bool
}

// Sequence looks for a user-defined sequence of tokens.
type Sequence struct {
	Definition   `mapstructure:",squash"`
	Tokens       []NLPToken
	Ignorecase   bool
	needsTagging bool
}

// NewSequence creates a new rule from the provided `baseCheck`.
func NewSequence(cfg *core.Config, generic baseCheck, path string) (Sequence, error) {
	rule := Sequence{}

	err := makeTokens(&rule, generic)
	if err != nil {
		return rule, readStructureError(err, path)
	}

	err = decodeRule(generic, &rule)
	if err != nil {
		return rule, readStructureError(err, path)
	}

	err = checkScopes(rule.Scope, path)
	if err != nil {
		return rule, err
	}

	for i, token := range rule.Tokens {
		if token.UPOS != "" {
			if token.Tag != "" {
				return rule, core.NewE201FromPosition(
					"a token cannot set both `tag` and `upos`", path, 1)
			}

			pattern, uerr := uposTagPattern(token.UPOS)
			if uerr != nil {
				return rule, core.NewE201FromPosition(uerr.Error(), path, 1)
			}
			rule.Tokens[i].Tag = pattern
			token.Tag = pattern

			// A category Penn cannot express on its own also constrains the
			// word. Only applied when the rule did not ask for a pattern of
			// its own, which is the more specific request.
			if token.Pattern == "" {
				if words := uposWordPattern(token.UPOS); words != "" {
					wre, werr := rx.Compile(words)
					if werr != nil {
						return rule, core.NewE201FromPosition(werr.Error(), path, 1)
					}
					rule.Tokens[i].wordRe = wre
				}
			}
		}

		if !rule.needsTagging && token.Tag != "" {
			rule.needsTagging = true
		}

		if token.Pattern != "" {
			regex := makeRegexp(
				cfg.WordTemplate,
				rule.Ignorecase,
				func() bool { return false },
				func() string { return "" },
				false)
			regex = fmt.Sprintf(regex, token.Pattern)

			re, errc := rx.Compile(regex)
			if errc != nil {
				return rule, core.NewE201FromPosition(errc.Error(), path, 1)
			}
			rule.Tokens[i].re = re
		}
	}

	rule.Definition.Scope = []string{"sentence"}
	return rule, nil
}

// Fields provides access to the rule definition.
func (s Sequence) Fields() Definition {
	return s.Definition
}

// Pattern is the internal regex pattern used by this rule.
func (s Sequence) Pattern() string {
	return ""
}

func makeTokens(s *Sequence, generic baseCheck) error {
	for _, token := range generic["tokens"].([]interface{}) {
		tok := NLPToken{}
		if err := mapstructure.WeakDecode(token, &tok); err != nil {
			return err
		}

		tok.optional = true
		for i := tok.Skip; i > 0; i-- {
			tok.start = false
			if i == tok.Skip {
				tok.start = true
			}
			s.Tokens = append(s.Tokens, tok)
		}

		if tok.Pattern != "" || tok.Tag != "" || tok.UPOS != "" {
			tok.optional = false
			tok.end = true
			s.Tokens = append(s.Tokens, tok)
		}
	}

	delete(generic, "tokens")
	return nil
}

func tokensMatch(token NLPToken, word tag.Token) bool {
	failedTag, err := rx.MatchString(token.Tag, word.Tag)
	if err != nil {
		// FIXME: return the error instead ...
		panic(err)
	}

	failedTag = failedTag == token.Negate
	failedTok := token.re != nil && token.re.MatchStringStd(word.Text) == token.Negate

	// A universal tag that Penn cannot express also restricts which words
	// qualify -- `upos: AUX` is "a verb, and one of these words".
	if token.wordRe != nil && token.wordRe.MatchStringStd(word.Text) == token.Negate {
		return false
	}

	if (token.Pattern == "" && failedTag) ||
		(token.Tag == "" && failedTok) ||
		(token.Tag != "" && token.Pattern != "") && (failedTag || failedTok) {
		return false
	}

	return true
}

// match describes one sequence hit: the matched words' text, the index of the
// anchor word, and the range of word indices the match covers.
//
// The index range is what lets Run report where the match actually is. The
// text alone is not enough: the same sequence can occur more than once, and
// re-joining the words does not reproduce the source when the spacing is
// irregular.
type match struct {
	text  []string
	index int
	lo    int
	hi    int

	// wordAt maps a position in the expanded token slice to the word it
	// matched, so a targeted token can be resolved back to its span.
	wordAt map[int]int
}

func (m match) ok() bool { return len(m.text) > 0 && m.lo >= 0 && m.hi >= m.lo }

func sequenceMatches(idx int, chk Sequence, target NLPToken, words []tag.Token, history []int) match {
	var text []string

	toks := chk.Tokens

	sizeT := len(toks)
	sizeW := len(words)
	index := 0
	lo, hi := -1, -1
	wordAt := map[int]int{}

	for jdx, tok := range words {
		if tokensMatch(target, tok) && !core.IntInSlice(jdx, history) {
			index = jdx
			// We've found our context.
			//
			// The *first* token with a `pattern` becomes the anchor of our
			// search. From there, we must check both its left- and right-hand
			// sides to ensure the sequence matches.
			if idx > 0 {
				// Check the left-end of the sequence:
				//
				// If the anchor is the first token, then there's no left-hand
				// side to check -- hence, `idx > 0`.
				for i := 1; idx-i >= 0; i++ {
					if jdx-i < 0 {
						return match{index: index, lo: -1, hi: -1}
					}
					tok := toks[idx-i]

					word := words[jdx-i]
					text = append([]string{word.Text}, text...)
					lo = jdx - i
					wordAt[idx-i] = jdx - i

					// NOTE: We have to perform this conversion because the token slice is made
					// with the right-hand orientation in mind. For example,
					//
					// optional (start), optional, required (end) -> required, optional, optional
					//
					// (from right to left).
					if tok.Skip > 0 {
						tok.optional = (tok.optional || tok.end) && !tok.start
					}

					mat := tokensMatch(tok, word)
					if !mat && !tok.optional {
						return match{index: index, lo: -1, hi: -1}
					} else if mat && tok.optional {
						break
					}
				}
			}
			if idx < sizeT {
				// Check the right-end of the sequence
				//
				// If the anchor is the last token, then there's no right-hand
				// side to check.
				for i := 0; idx+i < sizeT; i++ {
					if jdx+i >= sizeW {
						return match{index: index, lo: -1, hi: -1}
					}
					tok := toks[idx+i]

					word := words[jdx+i]
					text = append(text, word.Text)
					if lo < 0 || jdx+i < lo {
						lo = jdx + i
					}
					hi = jdx + i
					wordAt[idx+i] = jdx + i

					mat := tokensMatch(tok, word)
					if !mat && !tok.optional {
						return match{index: index, lo: -1, hi: -1}
					} else if mat && tok.optional {
						break
					}
				}
			}
			break
		}
	}

	return match{text: text, index: index, lo: lo, hi: hi, wordAt: wordAt}
}

func stepsToString(steps []string) string {
	var sb strings.Builder

	for i, step := range steps {
		switch {
		case step == "." || step == "," || step == ":" || step == ";" || step == "!" || step == "?" || step == "'" || step == `"` || step == ")":
			// No space before punctuation or closing parenthesis
			sb.WriteString(step)
		case step == "(":
			// No space before or after an opening parenthesis
			if i > 0 && sb.Len() > 0 {
				lastChar := sb.String()[sb.Len()-1]
				if lastChar != ' ' {
					sb.WriteString(" ")
				}
			}
			sb.WriteString(step)
		case strings.HasPrefix(step, "'"):
			// If the step starts with an apostrophe, attach it without space
			sb.WriteString(step)
		default:
			// Otherwise, add space before the word
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(step)
		}
	}

	return strings.TrimSpace(sb.String())
}

// locate returns the span of a match within txt, plus the matched text.
//
// When the tokens carry offsets, the span is taken straight from them, so it
// is exact even when the sequence occurs more than once or the source spacing
// is irregular. Without offsets we fall back to rebuilding the text and
// searching for it, which is what Vale did before tokens had positions; that
// path returns nil rather than a negative span when the search fails.
func (s Sequence) locate(txt string, words []tag.Token, m match, positioned bool) ([]int, string) {
	if positioned && m.hi < len(words) {
		lo, hi := m.lo, m.hi
		if tlo, thi, ok := s.targetRange(m); ok {
			lo, hi = tlo, thi
		}

		start := words[lo].Start
		end := words[hi].Start + len(words[hi].Text)
		if start >= 0 && end <= len(txt) && start < end {
			return []int{start, end}, txt[start:end]
		}
	}

	seq := stepsToString(m.text)
	if ssp := strings.Index(txt, seq); ssp >= 0 {
		return []int{ssp, ssp + len(seq)}, seq
	}
	return nil, seq
}

// targetRange returns the span of words covered by the rule's `target`
// tokens.
//
// Several tokens may be marked, in which case the range runs from the first to
// the last -- a target of two words reports both, not just one. Unmarked
// tokens between them are included, since the result has to be a single
// contiguous span.
func (s Sequence) targetRange(m match) (int, int, bool) {
	lo, hi := -1, -1
	for i := range s.Tokens {
		if !s.Tokens[i].Target {
			continue
		}
		w, ok := m.wordAt[i]
		if !ok {
			// A targeted token that matched nothing -- an optional or skipped
			// one. Narrowing to a range we cannot fully resolve would report
			// the wrong span, so fall back to the whole match.
			return 0, 0, false
		}
		if lo < 0 || w < lo {
			lo = w
		}
		if w > hi {
			hi = w
		}
	}
	if lo < 0 {
		return 0, 0, false
	}
	return lo, hi, true
}

// Run looks for the user-defined sequence of tokens.
func (s Sequence) Run(blk nlp.Block, f *core.File, _ *core.Config) ([]core.Alert, error) {
	var alerts []core.Alert
	var offset []string
	var history []int

	// This is *always* sentence-scoped.
	words := nlp.TextToTokens(blk.Text, &f.NLP)

	// A remote NLP endpoint returns text and tags only, so we have no offsets
	// to work from and have to fall back to locating the match by its text.
	positioned := f.NLP.Endpoint == ""

	txt := blk.Text
	idx, tok, ok := s.anchor()
	if ok {
		{
			// Each candidate position for the anchor is one possible
			// violation. A `pattern` anchor enumerates them by searching the
			// text; a tag-only anchor has nothing to search for, so we let
			// sequenceMatches walk the words and stop when it runs out.
			for _, loc := range s.candidates(txt, tok, len(words)) {
				// These are all possible violations in `txt`:
				m := sequenceMatches(idx, s, tok, words, history)
				history = append(history, m.index)

				if m.ok() {
					span, seq := s.locate(txt, words, m, positioned)
					if span == nil {
						// We matched but cannot say where; reporting a bogus
						// span is worse than reporting nothing.
						continue
					}

					// When the block knows where it sits in the document, hand
					// back an absolute offset. Otherwise the span is
					// block-relative and has to be located by searching, which
					// resolves every repeat of a sentence to the first one.
					absolute := blk.Offset >= 0
					if absolute {
						span = []int{blk.Offset + span[0], blk.Offset + span[1]}
					}

					action := s.Action
					if s.MatchCase && action.Name == "replace" {
						action.Params = recase(action.Params, seq)
					}

					a := core.Alert{
						Check: s.Name, Severity: s.Level, Link: s.Link,
						Span: span, Hide: false, HasByteOffsets: absolute,
						Match: seq, Action: action}

					a.Message, a.Description = formatMessages(s.Message,
						s.Description, m.text...)
					a.Offset = offset

					alerts = append(alerts, a)
					offset = []string{}
				} else if loc != nil {
					converted, err := re2Loc(txt, loc)
					if err != nil {
						return alerts, err
					}
					offset = append(offset, converted)
				}
			}
		}
	}

	return alerts, nil
}

// anchor picks the token the search starts from.
//
// A `pattern` token is preferred because it can be located in the text
// directly. Failing that any tagged token will do -- without this, a rule made
// only of tags matched nothing at all, silently.
func (s Sequence) anchor() (int, NLPToken, bool) {
	for i, tok := range s.Tokens {
		if !tok.Negate && tok.Pattern != "" {
			return i, tok, true
		}
	}
	for i, tok := range s.Tokens {
		if !tok.Negate && tok.Tag != "" {
			return i, tok, true
		}
	}
	return 0, NLPToken{}, false
}

// candidates returns one entry per position the anchor might occupy.
//
// For a `pattern` anchor each entry is the match's location in the text, which
// the caller reports as an offset when the surrounding sequence does not pan
// out. A tag-only anchor has no such location, so the entries are nil and the
// count simply bounds how many times to try.
func (s Sequence) candidates(txt string, tok NLPToken, words int) [][]int {
	if tok.re != nil {
		return tok.re.FindAllStringIndex(txt, -1)
	}
	return make([][]int, words)
}
