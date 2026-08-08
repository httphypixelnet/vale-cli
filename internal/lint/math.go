package lint

import (
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	mathjax "github.com/litao91/goldmark-mathjax"
)

// mathExtension parses `$$…$$` display math and `$…$` inline math, rendering
// them as elements vale skips -- so equations aren't spell-checked as prose
// (#878, #839).
//
// The inline parser is ours rather than goldmark-mathjax's: that one takes any
// two `$` on a line as delimiters, so `$5 and $10` reads as math and the prose
// between them is silently dropped from linting. See mathInlineParser.
type mathExtension struct{}

func (mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(
		util.Prioritized(mathjax.NewMathJaxBlockParser(), 701),
	))
	m.Parser().AddOptions(parser.WithInlineParsers(
		// '$' has no built-in owner.
		util.Prioritized(mathInlineParser{}, 500),
	))
	// Priority must be lower than goldmark-mathjax's own renderers (501/502)
	// to win, since renderers are registered in reverse-priority order with
	// later registrations overwriting earlier ones.
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(mathRenderer{}, 1),
	))
}

// kindInlineMath identifies a `$…$` span.
var kindInlineMath = ast.NewNodeKind("ValeInlineMath")

// inlineMath is a `$…$` span. Its children are the raw source segments it
// covers, which may be more than one: Pandoc lets inline math wrap across
// lines within a paragraph.
//
// The segments include the `$` delimiters. The walker masks a skipped element
// with one character per character of its text, so a mask that dropped them
// would be two short of the source and every alert later in the paragraph
// would be placed two columns early.
type inlineMath struct {
	ast.BaseInline
}

func (*inlineMath) Kind() ast.NodeKind { return kindInlineMath }

func (*inlineMath) IsRaw() bool { return true }

func (n *inlineMath) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// mathInlineParser reads `$…$` inline math under Pandoc's delimiter rules: the
// opening `$` must be followed by a non-space character, and the closing `$`
// must be preceded by one and not followed by a digit.
//
// Those rules are what separate math from money. In `$5 and $10` the second
// `$` has a space to its left, so it can't close -- the sentence stays prose.
// A rule-free reader pairs the two and hides `5 and 10` from every check.
type mathInlineParser struct{}

func (mathInlineParser) Trigger() []byte { return []byte{'$'} }

// closesMath reports whether line[i] is a `$` that closes a span: one that
// isn't escaped, has a non-space character to its left, and isn't followed by
// a digit.
func closesMath(line []byte, i int) bool {
	switch {
	case line[i] != '$' || (i > 0 && line[i-1] == '\\'):
		return false
	case i == 0 || util.IsSpace(line[i-1]):
		return false
	case i+1 < len(line) && util.IsNumeric(line[i+1]):
		return false
	default:
		return true
	}
}

func (mathInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	head, opening := block.PeekLine()

	// `$$` opens display math, which the block parser owns.
	if len(head) < 2 || head[1] == '$' || util.IsSpace(head[1]) {
		return nil
	}

	startLine, startPos := block.Position()
	block.Advance(1)

	// Where the span begins in the source: the opening `$`, which the loop
	// below has already read past.
	start := opening.Start

	node := &inlineMath{}
	for {
		line, segment := block.PeekLine()
		if line == nil {
			// The paragraph ended with the span still open, so there was no
			// math here: leave the `$` to be read as text.
			block.SetPosition(startLine, startPos)
			return nil
		}
		for i := range line {
			if !closesMath(line, i) {
				continue
			}
			// Through the closing `$`.
			node.AppendChild(node, ast.NewRawTextSegment(
				text.NewSegment(start, segment.Start+i+1)))
			block.Advance(i + 1)
			return node
		}
		node.AppendChild(node, ast.NewRawTextSegment(
			text.NewSegment(start, segment.Stop)))
		start = segment.Stop
		block.AdvanceLine()
	}
}

// mathRenderer renders math nodes as `pre` and `code` rather than the
// extension's default `<span class="math">`, so vale's walker -- which skips
// both -- excludes them from linting.
type mathRenderer struct{}

func (mathRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(mathjax.KindMathBlock, renderMathBlock)
	reg.Register(kindInlineMath, renderInlineMath)
}

func renderMathBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n, ok := node.(*mathjax.MathBlock)
	if !ok {
		return ast.WalkContinue, nil
	}
	if entering {
		_, _ = w.WriteString("<pre>")
		for i := 0; i < n.Lines().Len(); i++ {
			line := n.Lines().At(i)
			_, _ = w.Write(line.Value(source))
		}
	} else {
		_, _ = w.WriteString("</pre>\n")
	}
	return ast.WalkContinue, nil
}

// renderInlineMath writes the span as `code`. The body is escaped so that a
// `<` in an equation can't open a tag: the walker unescapes it again, so what
// reaches vale is the source text at its source length.
func renderInlineMath(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</code>")
		return ast.WalkContinue, nil
	}

	_, _ = w.WriteString("<code>")
	for c := node.FirstChild(); c != nil; c = c.NextSibling() {
		t, ok := c.(*ast.Text)
		if !ok {
			continue
		}
		_, _ = w.Write(util.EscapeHTML(t.Segment.Value(source)))
	}

	return ast.WalkSkipChildren, nil
}
