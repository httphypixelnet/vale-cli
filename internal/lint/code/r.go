package code

import (
	"regexp"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/smacker/go-tree-sitter/bash"
)

// R (and Perl, which shares the normed extension) is parsed with the Bash
// grammar: all three languages use `#` line comments, and comments are extras
// that survive error recovery, which is all comment extraction needs.
func R() *Language {
	return &Language{
		Delims:  regexp.MustCompile(`#'|#`),
		Parser:  bash.GetLanguage(),
		Queries: []core.Scope{{Name: "", Expr: "(comment) @comment", Type: ""}},
		Padding: func(s string) int {
			return computePadding(s, []string{"#", "#'"})
		},
	}
}
