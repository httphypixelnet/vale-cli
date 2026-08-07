package code

import (
	"regexp"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/smacker/go-tree-sitter/lua"
)

func Lua() *Language {
	return &Language{
		Delims: regexp.MustCompile(`--\[=*\[|\]=*\]|--`),
		Parser: lua.GetLanguage(),
		Queries: []core.Scope{
			{Name: "", Expr: "(comment) @comment", Type: ""},
			// `---` doc comments (including the `---[[` toggle idiom, which
			// is a line comment, not a block opener).
			{Name: "", Expr: "(emmy_documentation) @comment", Type: ""},
		},
		Padding: func(s string) int {
			return computePadding(s, []string{"--", "--[["})
		},
	}
}
