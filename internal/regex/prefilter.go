package regex

import (
	"regexp/syntax"
	"strings"
)

// required finds literals a subject must contain for the pattern to match.
//
// Running a rule's regular expression is far more expensive than searching for
// a word, and almost every rule is asked about text it cannot match: linting an
// 82 KB file with a 545-rule style ran the engine 1.2 million times to produce
// 274 alerts, a hit rate of 0.022%. A cheap test that rules a pattern out
// before the engine sees it removes nearly all of that.
//
// The result is a disjunction: the subject must contain at least one of the
// returned literals. An empty result means no useful test could be derived and
// the pattern has to be run.
//
// Correctness rests on only ever returning literals that are genuinely
// required. Anything uncertain -- a pattern that will not parse, an optional
// branch, a character class -- yields nothing, so the pattern runs as before. A
// prefilter can then only skip work, never change a result.
func Required(expr string) []string {
	re, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		// Lookarounds and backreferences land here: regexp2 accepts them and
		// regexp/syntax does not. No filter, so no behaviour change.
		return nil
	}

	lits := literals(re.Simplify())
	if len(lits) == 0 {
		return nil
	}

	out := make([]string, 0, len(lits))
	for _, l := range lits {
		// A single character rules almost nothing out and costs a scan of the
		// subject to discover it.
		if len(l) < 2 {
			return nil
		}
		out = append(out, strings.ToLower(l))
	}

	return out
}

// literals returns a set of strings, one of which any match must contain.
func literals(re *syntax.Regexp) []string {
	switch re.Op {
	case syntax.OpLiteral:
		if re.Flags&syntax.FoldCase != 0 {
			// Folded literals are compared lower-case by the caller, which is
			// what the subject is lower-cased for.
			return []string{strings.ToLower(string(re.Rune))}
		}
		return []string{string(re.Rune)}

	case syntax.OpCapture:
		return literals(re.Sub[0])

	case syntax.OpConcat:
		// Every part must match, so any one part's requirement is the whole
		// expression's. The longest rules out the most.
		var best []string
		for _, sub := range re.Sub {
			if got := literals(sub); len(got) > 0 && Weight(got) > Weight(best) {
				best = got
			}
		}
		return best

	case syntax.OpAlternate:
		// One branch matches, so a requirement holds only if every branch
		// supplies one; the subject must contain some member of the union.
		var all []string
		for _, sub := range re.Sub {
			got := literals(sub)
			if len(got) == 0 {
				return nil
			}
			all = append(all, got...)
		}
		return all

	case syntax.OpPlus, syntax.OpRepeat:
		// One repetition is mandatory when Min > 0; OpPlus always is.
		if re.Op == syntax.OpPlus || re.Min > 0 {
			return literals(re.Sub[0])
		}
		return nil
	}

	// OpStar, OpQuest, character classes, anchors and anything else: no
	// literal is guaranteed.
	return nil
}

// weight scores a literal set by its weakest member, since the subject only has
// to contain one of them.
func Weight(lits []string) int {
	if len(lits) == 0 {
		return 0
	}
	shortest := len(lits[0])
	for _, l := range lits[1:] {
		if len(l) < shortest {
			shortest = len(l)
		}
	}
	return shortest
}
