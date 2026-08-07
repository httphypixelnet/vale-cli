package check

import (
	"fmt"
	"testing"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/nlp"
)

func TestSelectors(t *testing.T) {
	s1 := Selector{Value: []string{"text.comment.line.py"}}
	s2 := Selector{Value: []string{"text.comment"}}
	// s3 := Selector{Value: "text.comment.line.rb"}

	sec := []string{"text", "comment", "line", "py"}
	if !core.AllStringsInSlice(sec, s1.Sections()) {
		t.Errorf("expected = %v, got = %v", sec, s1.Sections())
	}

	if s2.Has("py") {
		t.Errorf("expected `false`, got `true`")
	}

	for _, part := range s1.Sections() {
		if !s1.Has(part) {
			t.Errorf("expected `true`, got `false`")
		}
	}
}

// TestScopeMatches pins which blocks a rule's declared scope reaches.
//
// Matching is by containment: a rule matches a block when every section of the
// rule's scope appears somewhere in the block's. The block scopes below are the
// ones the linter actually builds -- `text.<element>` from the parser,
// `paragraph.<scope>` from splitting, `sentence.<scope>` from segmentation --
// so each row is one cell of the scope/element matrix. See #1124 and #1132.
func TestScopeMatches(t *testing.T) {
	cases := []struct {
		rule  []string
		block string
		want  bool
	}{
		// `paragraph` names splitting's wrapper, carried only by body text.
		{[]string{"paragraph"}, "paragraph.text.md", true},
		{[]string{"paragraph"}, "text.md", false},
		{[]string{"paragraph"}, "text.heading.h2.md", false},
		{[]string{"paragraph"}, "text.table.cell.md", false},
		{[]string{"paragraph"}, "text.list.md", false},
		{[]string{"paragraph"}, "text.blockquote.md", false},
		{[]string{"paragraph"}, "sentence.text.md", false},

		// `sentence` names segmentation's wrapper, carried by every kind of
		// prose -- which is what #1124 fixed.
		{[]string{"sentence"}, "sentence.text.md", true},
		{[]string{"sentence"}, "sentence.text.heading.h2.md", true},
		{[]string{"sentence"}, "sentence.text.list.md", true},
		{[]string{"sentence"}, "text.md", false},
		{[]string{"sentence"}, "paragraph.text.md", false},

		// Element scopes reach their element in every wrapper, and no other.
		{[]string{"heading"}, "text.heading.h2.md", true},
		{[]string{"heading"}, "sentence.text.heading.h2.md", true},
		{[]string{"heading.h2"}, "text.heading.h2.md", true},
		{[]string{"heading.h3"}, "text.heading.h2.md", false},
		{[]string{"heading"}, "text.md", false},
		{[]string{"table.header"}, "text.table.header.md", true},
		{[]string{"table.header"}, "text.table.cell.md", false},
		{[]string{"table.cell"}, "text.table.cell.md", true},
		{[]string{"table"}, "text.table.header.md", true},
		{[]string{"table"}, "text.table.cell.md", true},
		{[]string{"list"}, "text.list.md", true},
		{[]string{"list"}, "text.md", false},
		{[]string{"blockquote"}, "text.blockquote.md", true},
		{[]string{"blockquote"}, "text.md", false},

		// Inline and raw scopes are siblings of `text`, not children of it:
		// an ordinary rule must not run a second time over each fragment.
		{[]string{"link"}, "link.md", true},
		{[]string{"link"}, "text.md", false},
		{[]string{"code"}, "code.md", true},
		{[]string{"raw"}, "raw.md", true},
		{[]string{"raw"}, "text.md", false},
		{[]string{"text"}, "link.md", false},
		{[]string{"text"}, "raw.md", false},

		// Negation excludes an element and keeps everything else.
		{[]string{"~blockquote"}, "text.md", true},
		{[]string{"~blockquote"}, "text.blockquote.md", false},
		{[]string{"~blockquote & ~heading"}, "text.md", true},
		{[]string{"~blockquote & ~heading"}, "text.heading.h2.md", false},
		{[]string{"~blockquote & ~heading"}, "text.blockquote.md", false},

		// Several scopes are a union.
		{[]string{"heading", "list"}, "text.list.md", true},
		{[]string{"heading", "list"}, "text.blockquote.md", false},
	}

	for _, c := range cases {
		name := fmt.Sprintf("%v vs %s", c.rule, c.block)
		t.Run(name, func(t *testing.T) {
			blk := nlp.NewLinedBlock("", "text", c.block, 1)
			if got := NewScope(c.rule).Matches(blk); got != c.want {
				t.Errorf("NewScope(%v).Matches(%q) = %v, want %v",
					c.rule, c.block, got, c.want)
			}
		})
	}
}
