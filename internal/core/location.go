package core

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/errata-ai/vale/v3/internal/nlp"
)

// isPunctOnly reports whether s contains no letters or digits -- e.g. a bare
// `,`. Such matches are inherently ambiguous (they occur all over a document),
// so locating them needs the block-masking anchor below.
func isPunctOnly(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// quoteTolerantPattern escapes s for use in a regex, but lets ASCII quotes and
// apostrophes also match their "smart" Unicode variants. Spell-checking (and
// other checks) normalize `’` -> `'` etc. before matching, so the resulting
// match must still be locatable in the original, un-normalized source. See
// #1003.
func quoteTolerantPattern(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString(`['\x{2018}\x{2019}]`)
		case '"':
			b.WriteString(`["\x{201c}\x{201d}]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

// insideInlineMarkup reports whether a match sits against a backtick or a
// dash, which is how a match inside inline code is recognised.
//
// NOTE: This is a workaround for #673. Ideally we'd handle it at the AST level
// by ignoring inline code spans.
func insideInlineMarkup(ctx string, fs []int) bool {
	size := nlp.StrLen(ctx)

	start := fs[0] - 1
	end := fs[1] + 1
	if start > 0 && (ctx[start] == '`' || ctx[start] == '-') {
		return true
	} else if end < size && (ctx[end] == '`' || ctx[end] == '-') {
		return true
	}

	return false
}

// joinsWord reports whether r would sit inside a word, and so cannot stand
// beside a match that the pattern below requires to be `\b`-bounded.
//
// `_` is a word character to the regex engine, but the pattern admits it as a
// boundary of its own, so it is not one of these.
func joinsWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// directPosition confirms that `idx` is where the search below would land, so
// that the search itself can be skipped.
//
// A check knows where in its block the match was, and the block knows where it
// begins in `ctx`, so the position is arithmetic. That matters because the
// search runs once per alert over the whole context, and spelling reports
// alerts by the thousand -- the difference between linear and quadratic on a
// large document.
//
// The arithmetic rests on hints rather than promises, though: a check may
// measure against text it transformed first (spelling normalizes quotes before
// tokenizing), and `ctx` may have been masked by earlier alerts. So the answer
// is only used when the bytes there really are the match, bounded as the
// pattern requires, with nothing before it the pattern would have matched
// first. `from` is where that last check has to start -- everything earlier is
// already known to hold no match. Anything short of all three falls through to
// the search, which makes a bad hint cost time instead of correctness.
func directPosition(ctx string, idx, from int, sub string) bool {
	// Quote tolerance and the `^`/`$`/`_` alternatives make the pattern's
	// boundaries subtle in both directions. Restricting this to matches that
	// begin and end on a letter or digit and hold no quotes covers the ordinary
	// word -- what spelling reports -- and leaves the rest to the search.
	if sub == "" || from < 0 || idx < from || strings.ContainsAny(sub, `'"`) {
		return false
	}
	if first, _ := utf8.DecodeRuneInString(sub); !joinsWord(first) {
		return false
	}
	if last, _ := utf8.DecodeLastRuneInString(sub); !joinsWord(last) {
		return false
	}

	end := idx + len(sub)
	if end > len(ctx) || ctx[idx:end] != sub {
		return false
	}

	if idx > 0 {
		if r, _ := utf8.DecodeLastRuneInString(ctx[:idx]); joinsWord(r) {
			return false
		}
	}
	if end < len(ctx) {
		if r, _ := utf8.DecodeRuneInString(ctx[end:]); joinsWord(r) {
			return false
		}
	}

	// The search reports the *first* acceptable match, so this agrees with it
	// only if there is nothing earlier to find. A plain scan is enough: `sub`
	// holds no quote for the pattern to be tolerant about, so the pattern can
	// only match where `sub` itself occurs.
	return !strings.Contains(ctx[from:idx], sub)
}

// positionOf converts a match offset into the 1-based rune position reported.
func positionOf(ctx string, idx int, sub string) (int, string) {
	if strings.HasPrefix(ctx[idx:], "_") {
		idx++ // We don't want to include the underscore boundary.
	}
	return nlp.StrLen(ctx[:idx]) + 1, sub
}

// initialPosition calculates the position of a match (given by the location in
// the reference document, `loc`) in the source document (`ctx`).
//
// `at` is where `txt` begins in `ctx` if the caller knows, and -1 otherwise. It
// is only a shortcut: the position it produces is checked before being used.
func initialPosition(ctx, txt string, a Alert, at int) (int, string) {
	var idx int
	var pat *regexp.Regexp

	if a.Match == "" {
		// We have nothing to look for -- assume the rule applies to the entire
		// document (e.g., readability).
		return 1, ""
	}

	offset := strings.Index(ctx, txt)
	if offset < 0 && isPunctOnly(a.Match) {
		// `txt` may not appear verbatim in `ctx` when inline markup (e.g. a
		// code span) was stripped during extraction. For a punctuation-only
		// match -- which occurs all over the document -- anchor on the block's
		// first token so we still mask everything before it; otherwise a bare
		// `,` is located at its first occurrence anywhere. Restricted to
		// punctuation matches to preserve word-match disambiguation. See #994.
		if fields := strings.Fields(txt); len(fields) > 0 {
			offset = strings.Index(ctx, fields[0])
		}
	}
	if offset >= 0 {
		masked, _ := Substitute(ctx, ctx[:offset], '@')
		// Substitute maps rune to rune, so a prefix holding anything multi-byte
		// comes back shorter. The tail it leaves alone is what gives the block
		// its new position.
		offset = len(masked) - (len(ctx) - offset)
		ctx = masked
	}

	sub := strings.ToValidUTF8(a.Match, "")

	// Where the block starts in `ctx`, and how far the pattern is already known
	// to have nothing to match. The search above answers both at once. When it
	// came up empty -- which is what happens once an earlier alert has masked
	// part of this block, so on every alert but the first -- the caller's offset
	// still says where the block is, but nothing has ruled out the text in front
	// of it, so that has to be scanned as well.
	base, from := offset, offset
	if base < 0 {
		base, from = at, 0
	}

	if base >= 0 && len(a.Span) > 0 && a.Span[0] >= 0 {
		if pos := base + a.Span[0]; directPosition(ctx, pos, from, sub) {
			// The span the pattern would have reported, so that the inline-code
			// test below sees what it sees: both `_` alternatives are consumed
			// by the match rather than left beside it.
			fs := []int{pos, pos + len(sub)}
			if pos > 0 && ctx[pos-1] == '_' {
				fs[0]--
			}
			if fs[1] < len(ctx) && ctx[fs[1]] == '_' {
				fs[1]++
			}
			if !insideInlineMarkup(ctx, fs) {
				return positionOf(ctx, pos, sub)
			}
		}
	}

	pat = regexp.MustCompile(`(?:^|\b|_)` + quoteTolerantPattern(sub) + `(?:_|\b|$)`)

	// Only the first acceptable match is used, so the search stops at the
	// first one and widens only if that turns out to be rejected below. This
	// runs once per alert over the whole context, so scanning past a match
	// nothing reads is the difference between linear and quadratic on a
	// document with many alerts -- which spelling produces by the thousand.
	fsi := pat.FindAllStringIndex(ctx, 1)
	if len(fsi) > 0 && !insideInlineMarkup(ctx, fsi[0]) {
		return positionOf(ctx, fsi[0][0], sub)
	}
	if len(fsi) > 0 {
		fsi = pat.FindAllStringIndex(ctx, -1)
	}
	if len(fsi) == 0 {
		idx = strings.Index(ctx, sub)
		if idx < 0 {
			// This should only happen if we're in a scope that contains inline
			// markup (e.g., a sentence with code spans).
			return guessLocation(ctx, txt, sub)
		}
	} else {
		idx = fsi[0][0]
		// NOTE: This is a workaround for #673.
		//
		// In cases where we have more than one match, we skip any that look
		// like they're inside inline code (e.g., `code`).
		//
		// This is a bit of a hack: ideally, we'd handle this at the AST level
		// by ignoring these inline code spans.
		//
		// TODO: What about `scope: raw`?
		size := nlp.StrLen(ctx)
		for _, fs := range fsi {
			start := fs[0] - 1
			end := fs[1] + 1
			if start > 0 && (ctx[start] == '`' || ctx[start] == '-') {
				continue
			} else if end < size && (ctx[end] == '`' || ctx[end] == '-') {
				continue
			}
			idx = fs[0]
			break
		}
	}

	if strings.HasPrefix(ctx[idx:], "_") {
		idx++ // We don't want to include the underscore boundary.
	}

	return nlp.StrLen(ctx[:idx]) + 1, sub
}

func guessLocation(ctx, sub, match string) (int, string) {
	target := ""
	for _, s := range nlp.SentenceTokenizer.Segment(sub) {
		if s == match || strings.Index(s, match) > 0 {
			target = s
		}
	}

	if target == "" {
		return -1, sub
	}

	tokens := nlp.WordTokenizer.Tokenize(target)
	for _, text := range strings.Split(ctx, "\n") {
		if allStringsInString(tokens, text) {
			return strings.Index(ctx, text) + 1, text
		}
	}

	return -1, sub
}

func allStringsInString(subs []string, s string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
