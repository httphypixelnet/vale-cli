package code

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

func toJSON(comments []Comment) string {
	j, _ := json.MarshalIndent(comments, "", "    ")
	return string(j)
}

func cStyle(s string) int {
	return computePadding(s, []string{"/*", "//"})
}

// cStylePrefix matches the ` *` that decorates each line of a JSDoc, Javadoc
// or Doxygen block.
//
// The `*` only counts as decoration when whitespace or the end of the line
// follows it. `*emphasis*` and `*item` are content, and a pattern that took
// them for decoration would quietly delete the markup it was meant to expose.
var cStylePrefix = regexp.MustCompile(`^([ \t]*\*)(?:[ \t]|$)`)

func computePadding(s string, makers []string) int {
	padding := 0

	for _, m := range makers {
		if strings.HasPrefix(s, m) {
			l := utf8.RuneCountInString(m)

			padding = l
			for i, r := range s {
				if i < l {
					continue
				}

				if r == ' ' {
					padding++
				} else {
					break
				}
			}
		}
	}

	return padding
}
