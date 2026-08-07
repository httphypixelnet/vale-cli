package lint

import (
	"bytes"
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	grh "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/errata-ai/vale/v3/internal/core"
)

// MDX is CommonMark plus ESM statements, JSX elements, and JavaScript
// expressions. None of them hold prose: each is parsed whole -- children
// included -- and rendered as inline or fenced code, which the walker skips.
// A `{/* ... */}` flow comment becomes an HTML comment instead, so
// comment-based configuration keeps working.
//
// This replaces shelling out to mdx2vast, whose output this reproduces: an
// MDX node became `<code class="mdxNode <type>">` -- `<pre><code>` when it
// spanned lines -- and everything else ordinary Markdown.

// MDX configuration: Markdown, plus the MDX constructs.
var goldMdx = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Footnote,
		mathExtension{},
		mdxExtension{},
	),
	goldmark.WithRendererOptions(
		grh.WithUnsafe(),
	),
)

type mdxExtension struct{}

func (mdxExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(
		// Ahead of the indented-code parser (500): MDX removed indented code
		// from the grammar, so four leading spaces are an ordinary paragraph.
		util.Prioritized(&mdxIndentParser{}, 499),
		// Ahead of the HTML block parser (900), which would otherwise claim
		// a JSX element, and the paragraph parser (1000).
		util.Prioritized(&mdxEsmParser{}, 880),
		util.Prioritized(&mdxFlowExprParser{}, 881),
		util.Prioritized(&mdxJsxFlowParser{}, 882),
	))
	m.Parser().AddOptions(parser.WithInlineParsers(
		// Behind the autolink parser (300), so `<https://...>` stays a link,
		// and ahead of the raw-HTML parser (400), which would otherwise
		// claim an inline JSX tag.
		util.Prioritized(&mdxInlineParser{}, 350),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(mdxRenderer{}, 1),
	))
}

// An mdxScan tracks nesting through JavaScript-ish source: one depth for
// every kind of bracket, with strings, template literals, and comments
// recognized so that a bracket inside them doesn't count.
//
// It is an approximation -- a regex literal holding a bracket would fool
// it -- but it covers the JavaScript that appears in documentation.
type mdxScan struct {
	depth   int
	quote   byte // ' " ` or 0
	comment bool // inside /* ... */
}

// scan processes one line, returning how far the state carried.
func (s *mdxScan) scan(line []byte) {
	for i := 0; i < len(line); i++ {
		c := line[i]

		if s.comment {
			if c == '*' && i+1 < len(line) && line[i+1] == '/' {
				s.comment = false
				i++
			}
			continue
		}
		if s.quote != 0 {
			if c == '\\' {
				i++
			} else if c == s.quote {
				s.quote = 0
			}
			continue
		}

		switch c {
		case '\'', '"', '`':
			s.quote = c
		case '/':
			if i+1 < len(line) {
				if line[i+1] == '*' {
					s.comment = true
					i++
				} else if line[i+1] == '/' {
					return // a line comment runs to the end
				}
			}
		case '{', '(', '[':
			s.depth++
		case '}', ')', ']':
			s.depth--
		}
	}
}

// An mdxBlock is one flow-level MDX node: an ESM block, a flow expression,
// or a JSX element. It renders as code -- or, for a `{/* ... */}` comment,
// as an HTML comment.
type mdxBlock struct {
	ast.BaseBlock

	typ      string
	finished bool

	// scan carries the node's parsing state across lines; what it holds
	// depends on typ.
	scan mdxScan
	jsx  mdxJsxScan
}

var kindMdxBlock = ast.NewNodeKind("MdxBlock")

func (n *mdxBlock) Kind() ast.NodeKind { return kindMdxBlock }
func (n *mdxBlock) IsRaw() bool        { return true }
func (n *mdxBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// source reassembles the node's text, without the trailing newline.
func (n *mdxBlock) source(src []byte) []byte {
	var buf bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		buf.Write(line.Value(src))
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// consume appends the current line to the node and moves past it.
func mdxConsume(n *mdxBlock, reader text.Reader) {
	_, segment := reader.PeekLine()
	seg := text.NewSegment(segment.Start, segment.Stop)
	seg.ForceNewline = true
	n.Lines().Append(seg)
	reader.Advance(segment.Len() - 1)
}

// An mdxIndentParser reads an indented chunk as the paragraph MDX says it
// is -- the grammar has no indented code blocks.
type mdxIndentParser struct{}

func (*mdxIndentParser) Trigger() []byte { return nil }

func (*mdxIndentParser) Open(_ ast.Node, reader text.Reader, _ parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	if w, _ := util.IndentWidth(line, reader.LineOffset()); w < 4 || util.IsBlank(line) {
		return nil, parser.NoChildren
	}

	node := ast.NewParagraph()
	node.Lines().Append(segment.TrimLeftSpace(reader.Source()))
	reader.Advance(segment.Len() - 1)
	return node, parser.NoChildren
}

func (*mdxIndentParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	line, segment := reader.PeekLine()
	if util.IsBlank(line) {
		return parser.Close
	}
	node.Lines().Append(segment.TrimLeftSpace(reader.Source()))
	reader.Advance(segment.Len() - 1)
	return parser.Continue | parser.NoChildren
}

func (*mdxIndentParser) Close(node ast.Node, reader text.Reader, _ parser.Context) {
	lines := node.Lines()
	if length := lines.Len(); length != 0 {
		last := lines.At(length - 1)
		lines.Set(length-1, last.TrimRightSpace(reader.Source()))
	}
}

func (*mdxIndentParser) CanInterruptParagraph() bool { return false }
func (*mdxIndentParser) CanAcceptIndentedLine() bool { return true }

// mdxEsm matches the start of an ESM statement.
var mdxEsm = regexp.MustCompile(`^(?:import|export)\b`)

// An mdxEsmParser reads a block of import/export statements. Contiguous
// statements are one node, matching how MDX reads them.
type mdxEsmParser struct{}

func (*mdxEsmParser) Trigger() []byte {
	return []byte{'i', 'e'}
}

func (*mdxEsmParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos != 0 || !mdxEsm.Match(line) {
		return nil, parser.NoChildren
	}

	node := &mdxBlock{typ: "mdxjsEsm"}
	node.scan.scan(line)
	node.finished = node.scan.depth <= 0
	mdxConsume(node, reader)

	return node, parser.NoChildren
}

func (*mdxEsmParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n := node.(*mdxBlock) //nolint:errcheck // only mdxBlock is opened
	line, _ := reader.PeekLine()

	if n.finished {
		// Another statement directly below joins the block.
		if !mdxEsm.Match(line) {
			return parser.Close
		}
		n.scan = mdxScan{}
	}

	n.scan.scan(line)
	n.finished = n.scan.depth <= 0 && !n.scan.comment && n.scan.quote == 0
	mdxConsume(n, reader)

	return parser.Continue | parser.NoChildren
}

func (*mdxEsmParser) Close(ast.Node, text.Reader, parser.Context) {}

func (*mdxEsmParser) CanInterruptParagraph() bool { return false }
func (*mdxEsmParser) CanAcceptIndentedLine() bool { return false }

// An mdxFlowExprParser reads a `{...}` expression standing on its own.
type mdxFlowExprParser struct{}

func (*mdxFlowExprParser) Trigger() []byte {
	return []byte{'{'}
}

func (*mdxFlowExprParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || pos >= len(line) || line[pos] != '{' {
		return nil, parser.NoChildren
	}

	node := &mdxBlock{typ: "mdxFlowExpression"}
	node.scan.scan(line[pos:])
	node.finished = node.scan.depth <= 0 && !node.scan.comment
	mdxConsume(node, reader)

	return node, parser.NoChildren
}

func (*mdxFlowExprParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n := node.(*mdxBlock) //nolint:errcheck // only mdxBlock is opened
	if n.finished {
		return parser.Close
	}

	line, _ := reader.PeekLine()
	n.scan.scan(line)
	n.finished = n.scan.depth <= 0 && !n.scan.comment
	mdxConsume(n, reader)

	return parser.Continue | parser.NoChildren
}

func (*mdxFlowExprParser) Close(ast.Node, text.Reader, parser.Context) {}

func (*mdxFlowExprParser) CanInterruptParagraph() bool { return true }
func (*mdxFlowExprParser) CanAcceptIndentedLine() bool { return false }

// An mdxJsxScan walks a JSX element to its end: tags are pushed and popped,
// and `{...}` expressions -- in attributes or children -- are handed to an
// mdxScan so their contents can't end a tag or the element.
type mdxJsxScan struct {
	stack []string

	// mode is where the scan is: 0 children text, 1 inside a tag's name or
	// attributes, 2 inside a JavaScript expression.
	mode int

	js mdxScan

	// wasInTag remembers whether the active expression began inside a tag,
	// so its end returns the scan to the right mode.
	wasInTag bool

	name    []byte // the tag being read
	named   bool   // the name is complete
	closing bool   // the tag is </...>
	selfEnd bool   // the tag ends with />
	quote   byte   // inside an attribute string

	begun bool // at least one tag has been read
	done  bool // the element is complete
}

// tagName reports whether c can appear in a JSX element name -- which allows
// member expressions (`myComponents.thisOne`) and, at the start, a fragment's
// empty name.
func mdxTagName(c byte) bool {
	return c == '.' || c == '-' || c == '_' || c == '$' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// scan processes one line of the element.
func (s *mdxJsxScan) scan(line []byte) {
	for i := 0; i < len(line); i++ {
		c := line[i]

		switch s.mode {
		case 2: // expression
			// Scan a single character so the mdxScan's own state (strings,
			// comments) applies; its depth began at 1 for the opening brace.
			s.js.scan(line[i : i+1])
			if s.js.depth <= 0 && !s.js.comment && s.js.quote == 0 {
				if s.wasInTag {
					s.mode = 1
				} else {
					s.mode = 0
				}
			}
		case 1: // tag
			if s.quote != 0 {
				if c == s.quote {
					s.quote = 0
				}
				continue
			}
			switch {
			case !s.named && mdxTagName(c):
				s.name = append(s.name, c)
			case !s.named:
				s.named = true
				i-- // reprocess as an attribute character
			case c == '\'' || c == '"':
				s.quote = c
			case c == '{':
				s.js = mdxScan{depth: 1}
				s.wasInTag = true
				s.mode = 2
			case c == '/':
				s.selfEnd = true
			case c == '>':
				s.endTag()
			default:
				// an attribute character
			}
		default: // children
			switch c {
			case '<':
				s.mode = 1
				s.name = nil
				s.named = false
				s.closing = false
				s.selfEnd = false
				if i+1 < len(line) && line[i+1] == '/' {
					s.closing = true
					i++
				}
			case '{':
				s.js = mdxScan{depth: 1}
				s.wasInTag = false
				s.mode = 2
			}
		}

		if s.done {
			return
		}
	}
}

func (s *mdxJsxScan) endTag() {
	name := string(s.name)
	switch {
	case s.closing:
		if n := len(s.stack); n > 0 {
			s.stack = s.stack[:n-1]
		}
	case s.selfEnd:
		// opens and closes at once
	default:
		s.stack = append(s.stack, name)
	}

	s.begun = true
	s.mode = 0
	if len(s.stack) == 0 {
		s.done = true
	}
}

// opensJsx reports whether line[pos:] begins a JSX tag: `<` followed by a
// name, a fragment's `>`, or a closing `/`. `<!`, `<?`, and autolink-style
// `<scheme:` starts are left to other parsers.
func opensJsx(line []byte, pos int) bool {
	if pos >= len(line) || line[pos] != '<' {
		return false
	}
	rest := line[pos+1:]
	if len(rest) == 0 {
		return false
	}
	if rest[0] == '>' || rest[0] == '/' {
		return true
	}
	if !mdxTagName(rest[0]) {
		return false
	}
	// A scheme (`https:`) or an email (`user@host`) means an autolink.
	for _, c := range rest {
		if c == ' ' || c == '\t' || c == '>' || c == '/' || c == '\n' || c == '{' {
			return true
		}
		if !mdxTagName(c) {
			return false
		}
	}
	return true
}

// An mdxJsxFlowParser reads a block-level JSX element, children and all.
type mdxJsxFlowParser struct{}

func (*mdxJsxFlowParser) Trigger() []byte {
	return []byte{'<'}
}

func (*mdxJsxFlowParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || !opensJsx(line, pos) {
		return nil, parser.NoChildren
	}

	node := &mdxBlock{typ: "mdxJsxFlowElement"}
	node.jsx.scan(line[pos:])
	node.finished = node.jsx.done
	mdxConsume(node, reader)

	return node, parser.NoChildren
}

func (*mdxJsxFlowParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n := node.(*mdxBlock) //nolint:errcheck // only mdxBlock is opened
	if n.finished {
		return parser.Close
	}

	line, _ := reader.PeekLine()
	n.jsx.scan(line)
	n.finished = n.jsx.done
	mdxConsume(n, reader)

	return parser.Continue | parser.NoChildren
}

func (*mdxJsxFlowParser) Close(ast.Node, text.Reader, parser.Context) {}

func (*mdxJsxFlowParser) CanInterruptParagraph() bool { return true }
func (*mdxJsxFlowParser) CanAcceptIndentedLine() bool { return false }

// An mdxInline is an inline MDX node: a text expression or a JSX text
// element, rendered as a code span.
type mdxInline struct {
	ast.BaseInline

	typ  string
	text []byte
}

var kindMdxInline = ast.NewNodeKind("MdxInline")

func (n *mdxInline) Kind() ast.NodeKind { return kindMdxInline }
func (n *mdxInline) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type mdxInlineParser struct{}

func (*mdxInlineParser) Trigger() []byte {
	return []byte{'{', '<'}
}

func (*mdxInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) == 0 {
		return nil
	}

	if line[0] == '{' {
		return mdxParseInline(block, "mdxTextExpression", func() *mdxJsxScan {
			return nil
		})
	}
	if opensJsx(line, 0) {
		return mdxParseInline(block, "mdxJsxTextElement", func() *mdxJsxScan {
			return &mdxJsxScan{}
		})
	}
	return nil
}

// mdxParseInline consumes an expression or element that may span lines
// within its paragraph, returning nil -- with the reader restored -- when it
// never ends.
func mdxParseInline(block text.Reader, typ string, newJsx func() *mdxJsxScan) ast.Node {
	l, pos := block.Position()

	var collected []byte
	js := mdxScan{}
	jsx := newJsx()

	for {
		line, _ := block.PeekLine()
		if line == nil {
			block.SetPosition(l, pos)
			return nil
		}

		var done bool
		if jsx != nil {
			jsx.scan(line)
			done = jsx.done
		} else {
			js.scan(line)
			done = js.depth <= 0 && !js.comment && js.quote == 0
		}

		if done {
			// Find how much of the line the construct actually used: rescan
			// from a fresh state to the closing position.
			used := mdxEndOn(line, jsx != nil, collected)
			collected = append(collected, line[:used]...)
			block.Advance(used)
			break
		}

		collected = append(collected, line...)
		block.AdvanceLine()
	}

	return &mdxInline{typ: typ, text: bytes.TrimRight(collected, "\n")}
}

// mdxEndOn returns the offset just past the construct's closing character on
// the line that completes it, by rescanning that line with the state carried
// in from the earlier lines.
func mdxEndOn(line []byte, isJsx bool, carried []byte) int {
	if isJsx {
		s := mdxJsxScan{}
		s.scan(carried)
		for i := 1; i <= len(line); i++ {
			t := s
			t.stack = append([]string(nil), s.stack...)
			t.name = append([]byte(nil), s.name...)
			t.scan(line[:i])
			if t.done {
				return i
			}
		}
		return len(line)
	}

	s := mdxScan{}
	s.scan(carried)
	for i := 0; i < len(line); i++ {
		s.scan(line[i : i+1])
		if s.depth <= 0 && !s.comment && s.quote == 0 {
			return i + 1
		}
	}
	return len(line)
}

type mdxRenderer struct{}

func (mdxRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMdxBlock, renderMdxBlock)
	reg.Register(kindMdxInline, renderMdxInline)
}

// isMdxComment reports whether source is a `{/* ... */}` comment.
func isMdxComment(source []byte) bool {
	return bytes.HasPrefix(source, []byte("{/*")) && bytes.HasSuffix(source, []byte("*/}"))
}

func renderMdxBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n, ok := node.(*mdxBlock)
	if !ok || !entering {
		return ast.WalkContinue, nil
	}

	src := n.source(source)
	if n.typ == "mdxFlowExpression" && isMdxComment(src) {
		_, _ = w.WriteString("<!--")
		_, _ = w.Write(src[3 : len(src)-3])
		_, _ = w.WriteString("-->\n")
		return ast.WalkContinue, nil
	}

	if bytes.ContainsRune(src, '\n') {
		_, _ = w.WriteString(`<pre><code class="mdxNode ` + n.typ + `">`)
		_, _ = w.Write(util.EscapeHTML(src))
		_, _ = w.WriteString("</code></pre>\n")
	} else {
		_, _ = w.WriteString(`<code class="mdxNode ` + n.typ + `">`)
		_, _ = w.Write(util.EscapeHTML(src))
		_, _ = w.WriteString("</code>\n")
	}
	return ast.WalkContinue, nil
}

func renderMdxInline(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n, ok := node.(*mdxInline)
	if !ok || !entering {
		return ast.WalkContinue, nil
	}

	_, _ = w.WriteString(`<code class="mdxNode ` + n.typ + `">`)
	_, _ = w.Write(util.EscapeHTML(n.text))
	_, _ = w.WriteString("</code>")
	return ast.WalkContinue, nil
}

// lintMDX lints MDX: Markdown, parsed with the MDX constructs.
func (l *Linter) lintMDX(f *core.File) error {
	return l.lintMarkdownWith(f, goldMdx)
}
