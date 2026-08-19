package lint

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// mathExtension parses `$$…$$` display math and `$…$` inline math, rendering
// them as elements vale skips -- so equations aren't spell-checked as prose
// (#878, #839).
//
// Both parsers are ours rather than goldmark-mathjax's: its inline parser
// takes any two `$` on a line as delimiters, so `$5 and $10` reads as math
// and the prose between them is silently dropped from linting; its block
// parser closes only on a line that is nothing but `$$`, so every other
// Pandoc closing form left the block open and consumed the rest of the file
// (#1148). See mathInlineParser and mathBlockParser.
type mathExtension struct{}

func (mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(
		util.Prioritized(mathBlockParser{}, 701),
	))
	m.Parser().AddOptions(parser.WithInlineParsers(
		// '$' has no built-in owner.
		util.Prioritized(mathInlineParser{}, 500),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(mathRenderer{}, 1),
	))
}

// kindDisplayMath identifies a `$$…$$` block.
var kindDisplayMath = ast.NewNodeKind("ValeDisplayMath")

// displayMath is a `$$…$$` block. It carries its own parsing state: goldmark
// can open the next block on a line before closing the one that ended there,
// so state shared through a context key -- as goldmark-mathjax's parser keeps
// it -- is wiped by the old block's Close while the new block still needs it.
// Back-to-back display math crashed on exactly that.
type displayMath struct {
	ast.BaseBlock

	indent int
	closed bool
}

func (*displayMath) Kind() ast.NodeKind { return kindDisplayMath }

func (*displayMath) IsRaw() bool { return true }

func (n *displayMath) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// mathBlockParser reads `$$…$$` display math as a block. A `$$` at the start
// of a line opens it, and any line that ends with an unescaped `$$` closes it
// -- alone on the line, ending a content line (`\end{aligned}$$`), on the
// opening line itself (`$$x=1$$`), or trailed by a `{…}` attribute, which is
// where Pandoc and Quarto put cross-reference labels (`$$ {#eq-foo}`).
//
// A blank line also closes the block: Pandoc won't carry display math across
// one, so a `$$` that is never closed masks at most its own paragraph rather
// than the remainder of the document (#1148).
type mathBlockParser struct{}

func (mathBlockParser) Trigger() []byte { return []byte{'$'} }

func (mathBlockParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || line[pos] != '$' {
		return nil, parser.NoChildren
	}
	i := pos
	for i < len(line) && line[i] == '$' {
		i++
	}
	if i-pos < 2 {
		return nil, parser.NoChildren
	}

	node := &displayMath{indent: pos}

	if end := mathCloserIndex(line[i:]); end >= 0 {
		// The block opens and closes on this line: `$$x=1$$`.
		node.closed = true
		if end > 0 {
			node.Lines().Append(text.NewSegment(segment.Start+i, segment.Start+i+end))
		}
	} else if !util.IsBlank(line[i:]) {
		// Content on the opening line: `$$ x = 1`.
		node.Lines().Append(text.NewSegment(segment.Start+i, segment.Stop))
	}
	return node, parser.NoChildren
}

func (mathBlockParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n, ok := node.(*displayMath)
	if !ok || n.closed {
		return parser.Close
	}

	line, segment := reader.PeekLine()
	if util.IsBlank(line) {
		return parser.Close
	}

	if end := mathCloserIndex(line); end >= 0 {
		if !util.IsBlank(line[:end]) {
			node.Lines().Append(text.NewSegment(segment.Start, segment.Start+end))
		}
		reader.Advance(segment.Stop - segment.Start - segment.Padding)
		return parser.Close
	}

	pos, padding := util.DedentPosition(line, 0, n.indent)
	node.Lines().Append(text.NewSegmentPadding(segment.Start+pos, segment.Stop, padding))
	reader.AdvanceAndSetPadding(segment.Stop-segment.Start-pos-1, padding)
	return parser.Continue | parser.NoChildren
}

func (mathBlockParser) Close(_ ast.Node, _ text.Reader, _ parser.Context) {}

func (mathBlockParser) CanInterruptParagraph() bool { return true }

func (mathBlockParser) CanAcceptIndentedLine() bool { return false }

// mathCloserIndex returns the index of the `$$` that closes display math on
// this line: one that ends the line, allowing trailing whitespace and at most
// one `{…}` attribute after it. It returns -1 when the line closes nothing,
// including when the `$$` is escaped.
func mathCloserIndex(line []byte) int {
	end := len(line)
	for end > 0 && util.IsSpace(line[end-1]) {
		end--
	}
	if end > 0 && line[end-1] == '}' {
		open := bytes.LastIndexByte(line[:end-1], '{')
		if open < 0 {
			return -1
		}
		end = open
		for end > 0 && util.IsSpace(line[end-1]) {
			end--
		}
	}
	if end < 2 || line[end-1] != '$' || line[end-2] != '$' {
		return -1
	}
	if end > 2 && line[end-3] == '\\' {
		return -1
	}
	return end - 2
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

// mathRenderer renders math nodes as `pre` and `code`, so vale's walker --
// which skips both -- excludes them from linting.
type mathRenderer struct{}

func (mathRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindDisplayMath, renderMathBlock)
	reg.Register(kindInlineMath, renderInlineMath)
}

func renderMathBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n, ok := node.(*displayMath)
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
