package testsuite

import "strings"

// An Op is what a diff line did.
type Op byte

const (
	// Same is a line both sides have.
	Same Op = ' '
	// Del is a line only the expected output has.
	Del Op = '-'
	// Add is a line only the actual output has.
	Add Op = '+'
)

// A Line is one line of a diff.
type Line struct {
	Op   Op
	Text string
}

// Diff compares want against got, line by line.
//
// A failing case is nearly always a line that moved by a column or a message
// that was reworded, and neither reads as two blocks of text quoted one after
// the other. Callers render the result: what color a `-` is, and how far it
// is indented, is a question about where it is being printed.
func Diff(want, got string) []Line {
	w, g := split(want), split(got)
	common := lcs(w, g)

	var out []Line
	var i, j int

	for _, c := range common {
		for i < len(w) && w[i] != c {
			out = append(out, Line{Del, w[i]})
			i++
		}
		for j < len(g) && g[j] != c {
			out = append(out, Line{Add, g[j]})
			j++
		}
		out = append(out, Line{Same, c})
		i, j = i+1, j+1
	}

	for ; i < len(w); i++ {
		out = append(out, Line{Del, w[i]})
	}
	for ; j < len(g); j++ {
		out = append(out, Line{Add, g[j]})
	}

	return out
}

// split returns the lines of s, without the trailing newline that would
// otherwise show up as a line neither side has.
func split(s string) []string {
	if s = strings.TrimRight(s, "\n"); s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lcs returns the longest common subsequence of a and b.
func lcs(a, b []string) []string {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}

	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}

	var out []string
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i, j = i+1, j+1
		case table[i+1][j] >= table[i][j+1]:
			i++
		default:
			j++
		}
	}

	return out
}
