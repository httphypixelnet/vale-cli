package code

import (
	"regexp"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/smacker/go-tree-sitter/php"
)

func PHP() *Language {
	return &Language{
		Delims:  regexp.MustCompile(`//|/\*\*?|\*/|#`),
		Prefix:  cStylePrefix,
		Parser:  php.GetLanguage(),
		Queries: []core.Scope{{Name: "", Expr: "(comment) @comment", Type: ""}},
		Padding: func(s string) int {
			return computePadding(s, []string{"//", "/*", "#"})
		},
	}
}
