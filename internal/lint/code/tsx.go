package code

import (
	"regexp"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
)

func Tsx() *Language {
	return &Language{
		Delims:  regexp.MustCompile(`//|/\*|\*/`),
		Prefix:  cStylePrefix,
		Parser:  tsx.GetLanguage(),
		Queries: []core.Scope{{Name: "", Expr: "(comment) @comment", Type: ""}},
		Padding: cStyle,
	}
}
