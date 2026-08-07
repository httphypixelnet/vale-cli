package code

import (
	"regexp"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/smacker/go-tree-sitter/elm"
)

// Haskell is parsed with the Elm grammar: the two languages share their
// comment syntax (`--`, `{- -}`), and comments are extras that survive error
// recovery, which is all comment extraction needs.
func Haskell() *Language {
	return &Language{
		Delims: regexp.MustCompile(`\{-\|?|-\}|--`),
		Parser: elm.GetLanguage(),
		Queries: []core.Scope{
			{Name: "", Expr: "(line_comment) @comment", Type: ""},
			{Name: "", Expr: "(block_comment) @comment", Type: ""},
		},
		Padding: func(s string) int {
			return computePadding(s, []string{"--", "{-"})
		},
	}
}
