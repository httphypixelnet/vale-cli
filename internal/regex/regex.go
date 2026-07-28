// Package regex adapts github.com/dlclark/regexp2 to the API Vale uses.
//
// Vale needs a backtracking engine: user-authored rules rely on lookarounds
// and backreferences, which regexp/syntax rejects outright. regexp2 provides
// that, but its matching methods return errors and its Find surface is
// narrower than the standard library's, so this package supplies the missing
// convenience layer.
//
// # Offsets are rune-based
//
// FindAllStringIndex and friends report positions in the *rune* slice, not
// byte offsets. Callers converting to byte positions must keep doing so; see
// core.re2Loc.
//
// # Errors
//
// regexp2 can fail at match time, most often by exceeding its backtracking
// budget on a pathological pattern. The wrappers here panic in that case,
// matching the behaviour Vale has always had. Anything that should degrade
// gracefully needs to call the error-returning method directly.
package regex

import (
	"github.com/dlclark/regexp2/v2"
)

// Match is a single regular-expression match.
type Match = regexp2.Match

// Regexp is a compiled regular expression.
//
// It embeds *regexp2.Regexp, so the engine's own API — String, Replace,
// ReplaceFunc, MatchString, FindStringMatch, FindNextMatch — is available
// directly.
type Regexp struct {
	*regexp2.Regexp
}

// String returns the source text of the pattern.
//
// The zero value is usable and reports "". Vale relies on that: an unset
// pattern is represented by &Regexp{} and detected with String() == "", so
// this must not dereference the embedded pointer.
func (re *Regexp) String() string {
	if re == nil || re.Regexp == nil {
		return ""
	}
	return re.Regexp.String()
}

// IsZero reports whether re carries no pattern.
func (re *Regexp) IsZero() bool {
	return re == nil || re.Regexp == nil
}

// Compile parses a regular expression in RE2 compatibility mode.
func Compile(expr string) (*Regexp, error) {
	re, err := regexp2.Compile(expr, regexp2.RE2)
	if err != nil {
		return nil, err
	}
	return &Regexp{Regexp: re}, nil
}

// MustCompile is Compile but panics on an invalid pattern. It is meant for
// patterns fixed at compile time.
func MustCompile(expr string) *Regexp {
	re, err := Compile(expr)
	if err != nil {
		panic(err)
	}
	return re
}

// MatchString reports whether s contains a match of pattern.
func MatchString(pattern, s string) (bool, error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchStringStd(s), nil
}

// MatchStringStd reports whether s contains a match, panicking if the match
// itself fails.
func (re *Regexp) MatchStringStd(s string) bool {
	matched, err := re.MatchString(s)
	if err != nil {
		panic(err)
	}
	return matched
}

// FindAllString returns up to n successive matches, or all of them if n < 0.
//
// A nil return means no match.
func (re *Regexp) FindAllString(s string, n int) []string {
	var result []string
	re.eachMatch(s, n, func(m *regexp2.Match) {
		result = append(result, m.Group.String())
	})
	return result
}

// FindAllStringMatches returns every successive match.
func (re *Regexp) FindAllStringMatches(s string) []*Match {
	var result []*Match
	re.eachMatch(s, -1, func(m *regexp2.Match) {
		result = append(result, m)
	})
	return result
}

// FindAllStringIndex returns the rune-index bounds of up to n successive
// matches, or all of them if n < 0.
//
// A nil return means no match.
func (re *Regexp) FindAllStringIndex(s string, n int) [][]int {
	var result [][]int
	re.eachMatch(s, n, func(m *regexp2.Match) {
		result = append(result, []int{
			m.Group.RuneIndex,
			m.Group.RuneIndex + m.Group.RuneLength,
		})
	})
	return result
}

// FindAllStringSubmatch returns up to n successive matches and their
// submatches, or all of them if n < 0.
//
// A nil return means no match.
func (re *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	var result [][]string
	re.eachMatch(s, n, func(m *regexp2.Match) {
		groups := m.Groups()
		subs := make([]string, 0, len(groups))
		for i := range groups {
			subs = append(subs, groups[i].String())
		}
		result = append(result, subs)
	})
	return result
}

// FindAllStringSubmatchIndex returns the rune-index bounds of up to n
// successive matches and their submatches, or all of them if n < 0.
//
// A group that did not participate in the match is reported as -1, -1, the
// same convention the standard library uses.
//
// A nil return means no match.
func (re *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	var result [][]int
	re.eachMatch(s, n, func(m *regexp2.Match) {
		groups := m.Groups()
		subs := make([]int, 0, len(groups)*2)
		for i := range groups {
			g := &groups[i]
			idx, length := g.RuneIndex, g.RuneLength
			if idx+length == 0 {
				idx = -1
			}
			subs = append(subs, idx, idx+length)
		}
		result = append(result, subs)
	})
	return result
}

// SubexpNames returns the names of the parenthesized subexpressions.
//
// The name for the first sub-expression is names[1], so for a match slice m
// the name for m[i] is SubexpNames()[i]. The expression as a whole cannot be
// named, so names[0] is always empty.
func (re *Regexp) SubexpNames() []string {
	names := re.GetGroupNames()
	result := make([]string, 0, len(names))
	for i, name := range names {
		if i == 0 {
			result = append(result, "")
			continue
		}
		result = append(result, name)
	}
	return result
}

// Split slices s around each match, returning the pieces between them.
//
// n limits the number of pieces; n < 0 means no limit. A pattern that does
// not match returns s unchanged, as a single element.
func (re *Regexp) Split(s string, n int) []string {
	if n == 0 {
		return []string{s}
	}
	parts, err := re.Regexp.Split(s, n)
	if err != nil {
		panic(err)
	}
	return parts
}

// eachMatch walks up to n matches, calling fn for each. n < 0 means all.
//
// Centralised so every Find* variant shares one iteration and one error
// policy, rather than repeating the walk with subtly different bounds.
func (re *Regexp) eachMatch(s string, n int, fn func(*regexp2.Match)) {
	if n == 0 {
		return
	}

	m, err := re.FindStringMatch(s)
	if err != nil {
		panic(err)
	}

	count := 0
	for m != nil {
		fn(m)
		count++
		if n > 0 && count >= n {
			return
		}

		m, err = re.FindNextMatch(m)
		if err != nil {
			panic(err)
		}
	}
}
