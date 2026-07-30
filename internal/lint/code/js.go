package code

import (
	"regexp"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/smacker/go-tree-sitter/javascript"
)

func JavaScript() *Language {
	return &Language{
		Delims: regexp.MustCompile(`//|/\*\*?|\*/`),
		Prefix: cStylePrefix,
		Parser: javascript.GetLanguage(),
		// NOTE: a Cutset of " *" was tried here and cannot work: it makes the
		// dedent read `*` as indentation, which eats a Markdown list, and the
		// alert adjustment has no way to know a non-whitespace character was
		// removed. Prefix above handles the decoration instead.
		Queries: []core.Scope{{Name: "", Expr: "(comment) @comment", Type: ""}},
		Padding: cStyle,
	}
}
