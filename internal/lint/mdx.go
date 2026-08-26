package lint

import (
	"bytes"
	"regexp"
	"strings"

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
// expressions. The JavaScript -- ESM, expressions, tags and their
// attributes, self-closing elements -- holds no prose and renders as inline
// or fenced code, which the walker skips. A JSX element's children are
// Markdown, though, just as MDX itself reads them: a flow element becomes a
// div classed with the element's name, so its content is linted and a rule
// can reach (or a user can ignore) it by class, and an inline element
// becomes a span the same way. A `{/* ... */}` flow comment becomes an HTML
// comment instead, so comment-based configuration keeps working.

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

	// scan carries the node's parsing state across lines.
	scan mdxScan
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

// clone copies the scan, its slices included, so a caller can probe ahead
// without committing.
func (s *mdxJsxScan) clone() mdxJsxScan {
	t := *s
	t.stack = append([]string(nil), s.stack...)
	t.name = append([]byte(nil), s.name...)
	return t
}

// mdxTagEnd returns the offset just past the line's first completed tag,
// scanning from the carried state, or -1 when the tag needs more lines. On
// success the carried state is advanced to that offset.
//
// The scan reads a character ahead of itself (`</`, `/*`), so each prefix is
// probed whole from a copy rather than a character at a time.
func mdxTagEnd(line []byte, carried *mdxJsxScan) int {
	for i := 1; i <= len(line); i++ {
		t := carried.clone()
		t.scan(line[:i])
		if t.begun && t.mode == 0 {
			*carried = t
			return i
		}
	}
	return -1
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

// An mdxJsxContainer is a flow JSX element whose children are Markdown, per
// MDX's own grammar: only the tags, attributes, and expressions are
// JavaScript. It renders as a div classed with the element's name.
//
// An element whose open tag spans lines but that turns out to be childless
// (a multiline `<Component ... />`) collects its source in raw and renders
// as code, the way a single-line element does.
type mdxJsxContainer struct {
	ast.BaseBlock

	name    string
	jsx     mdxJsxScan   // carries open-tag state across lines
	raw     bytes.Buffer // the source read while pending
	pending bool         // still inside the open tag
	rawOnly bool         // finished childless; render raw as code

	// depth counts same-name elements opened on child lines, so a nested
	// element's close tag isn't taken for this one's; fenced guards the
	// count against JSX quoted in a code fence.
	depth  int
	fenced bool
}

var kindMdxJsxContainer = ast.NewNodeKind("MdxJsxContainer")

func (n *mdxJsxContainer) Kind() ast.NodeKind { return kindMdxJsxContainer }
func (n *mdxJsxContainer) IsRaw() bool        { return n.rawOnly }
func (n *mdxJsxContainer) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// mdxIsCloseTag reports whether line is exactly a closing tag for name.
func mdxIsCloseTag(line []byte, name string) bool {
	if !bytes.HasPrefix(line, []byte("</")) {
		return false
	}
	rest := line[2:]
	if !bytes.HasPrefix(rest, []byte(name)) {
		return false
	}
	rest = bytes.TrimLeft(rest[len(name):], " \t")
	return len(rest) == 1 && rest[0] == '>'
}

// mdxNetOpens counts the name's tags opened minus closed on a child line, so
// a same-name element handled by a descendant parser is not mistaken for
// this one's close. It reads one line at a time -- a tag broken across lines
// or a `>` quoted in an attribute can miscount -- which covers the JSX that
// appears in documentation.
func mdxNetOpens(line []byte, name string) int {
	if name == "" {
		return 0
	}

	net := 0
	tag := []byte(name)

	for i := 0; i+1 < len(line); i++ {
		if line[i] != '<' {
			continue
		}
		j := i + 1
		closing := line[j] == '/'
		if closing {
			j++
		}
		if !bytes.HasPrefix(line[j:], tag) {
			continue
		}
		k := j + len(tag)
		if k < len(line) && mdxTagName(line[k]) {
			continue // a longer name
		}
		if closing {
			net--
			continue
		}
		end := bytes.IndexByte(line[k:], '>')
		if end < 0 {
			net++ // the tag spans lines
		} else if end == 0 || line[k+end-1] != '/' {
			net++
		}
	}
	return net
}

// An mdxJsxFlowParser reads a block-level JSX element. An element opened and
// closed on one line -- self-closing or otherwise -- is a code node, and an
// element with lines of its own is a container whose children keep parsing
// as Markdown.
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

	var s mdxJsxScan
	end := mdxTagEnd(line[pos:], &s)

	if end < 0 {
		// The open tag itself spans lines.
		node := &mdxJsxContainer{pending: true}
		node.jsx.scan(line[pos:])
		node.raw.Write(line[pos:])
		mdxAdvanceLine(reader, line)
		return node, parser.HasChildren
	}

	if s.done || mdxDoneOnLine(s, line[pos+end:]) {
		// The whole element sits on this line: keep it as code, the shape
		// mdx2vast gave it.
		node := &mdxBlock{typ: "mdxJsxFlowElement", finished: true}
		mdxConsume(node, reader)
		return node, parser.NoChildren
	}

	node := &mdxJsxContainer{name: s.stack[len(s.stack)-1]}
	reader.Advance(pos + end)
	return node, parser.HasChildren
}

// mdxDoneOnLine reports whether the element completes in the rest of its
// first line.
func mdxDoneOnLine(s mdxJsxScan, rest []byte) bool {
	t := s.clone()
	t.scan(rest)
	return t.done
}

// mdxAdvanceLine consumes the rest of the current line.
func mdxAdvanceLine(reader text.Reader, line []byte) {
	reader.Advance(len(line) - 1)
}

func (*mdxJsxFlowParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n, ok := node.(*mdxJsxContainer)
	if !ok {
		// The single-line code form closes itself.
		return parser.Close
	}

	line, _ := reader.PeekLine()

	if n.pending {
		end := mdxTagEnd(line, &n.jsx)
		if end < 0 {
			n.jsx.scan(line)
			n.raw.Write(line)
			mdxAdvanceLine(reader, line)
			return parser.Continue | parser.HasChildren
		}
		if n.jsx.done {
			// Childless after all: a multiline self-closing element.
			n.raw.Write(bytes.TrimRight(line, "\n"))
			n.rawOnly = true
			mdxAdvanceLine(reader, line)
			return parser.Close
		}
		n.pending = false
		n.name = n.jsx.stack[len(n.jsx.stack)-1]
		reader.Advance(end)
		return parser.Continue | parser.HasChildren
	}

	trimmed := bytes.TrimSpace(line)

	if bytes.HasPrefix(trimmed, []byte("```")) || bytes.HasPrefix(trimmed, []byte("~~~")) {
		n.fenced = !n.fenced
	} else if !n.fenced {
		if mdxIsCloseTag(trimmed, n.name) {
			if n.depth == 0 {
				mdxAdvanceLine(reader, line)
				return parser.Close
			}
			n.depth--
		} else {
			n.depth += mdxNetOpens(line, n.name)
		}
	}

	return parser.Continue | parser.HasChildren
}

func (*mdxJsxFlowParser) Close(ast.Node, text.Reader, parser.Context) {}

func (*mdxJsxFlowParser) CanInterruptParagraph() bool { return true }
func (*mdxJsxFlowParser) CanAcceptIndentedLine() bool { return false }

// An mdxInline is an inline MDX node: a text expression, a self-closing JSX
// element -- both rendered as code spans -- or one tag of a JSX element
// whose children stay inline prose, rendered as a span so the element's
// name reaches rules as a class.
type mdxInline struct {
	ast.BaseInline

	typ  string
	text []byte

	form int    // 0 code, 1 an open tag, 2 a close tag
	name string // the tag's name, for form 1
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

// mdxParseInline consumes an expression or one JSX tag that may span lines
// within its paragraph, returning nil -- with the reader restored -- when it
// never ends. An element's children are left inline, so they keep parsing
// as prose; only the tag itself becomes a node.
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

		if jsx != nil {
			end := mdxTagEnd(line, jsx)
			if end < 0 {
				jsx.scan(line)
				collected = append(collected, line...)
				block.AdvanceLine()
				continue
			}
			collected = append(collected, line[:end]...)
			block.Advance(end)
			return mdxInlineTag(collected, jsx)
		}

		js.scan(line)
		if js.depth <= 0 && !js.comment && js.quote == 0 {
			// Find how much of the line the expression actually used: rescan
			// from a fresh state to the closing position.
			used := mdxEndOn(line, collected)
			collected = append(collected, line[:used]...)
			block.Advance(used)
			return &mdxInline{typ: typ, text: bytes.TrimRight(collected, "\n")}
		}

		collected = append(collected, line...)
		block.AdvanceLine()
	}
}

// mdxInlineTag builds the node for one completed inline tag: an open tag is
// a span whose children follow as prose, a close tag ends one, and a
// self-closing element stays a code span.
func mdxInlineTag(collected []byte, jsx *mdxJsxScan) ast.Node {
	node := &mdxInline{typ: "mdxJsxTextElement", text: bytes.TrimRight(collected, "\n")}

	switch {
	case bytes.HasPrefix(collected, []byte("</")):
		node.form = 2
	case !jsx.done:
		node.form = 1
		node.name = jsx.stack[len(jsx.stack)-1]
	}
	return node
}

// mdxEndOn returns the offset just past the expression's closing brace on
// the line that completes it, by rescanning that line with the state carried
// in from the earlier lines.
func mdxEndOn(line []byte, carried []byte) int {
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
	reg.Register(kindMdxJsxContainer, renderMdxContainer)
	reg.Register(kindMdxInline, renderMdxInline)
}

// mdxClassName renders an element name as a class: a member expression's
// dots become hyphens, since a dot separates scope parts.
func mdxClassName(name string) string {
	return strings.ReplaceAll(name, ".", "-")
}

func renderMdxContainer(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n, ok := node.(*mdxJsxContainer)
	if !ok {
		return ast.WalkContinue, nil
	}

	if n.rawOnly {
		if entering {
			_, _ = w.WriteString(`<pre><code class="mdxNode mdxJsxFlowElement">`)
			_, _ = w.Write(util.EscapeHTML(bytes.TrimRight(n.raw.Bytes(), "\n")))
			_, _ = w.WriteString("</code></pre>\n")
		}
		return ast.WalkContinue, nil
	}

	if !entering {
		_, _ = w.WriteString("</div>\n")
	} else if cls := mdxClassName(n.name); cls != "" {
		_, _ = w.WriteString(`<div class="` + cls + `">` + "\n")
	} else {
		_, _ = w.WriteString("<div>\n")
	}
	return ast.WalkContinue, nil
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

	switch n.form {
	case 1:
		if cls := mdxClassName(n.name); cls != "" {
			_, _ = w.WriteString(`<span class="` + cls + `">`)
		} else {
			_, _ = w.WriteString("<span>")
		}
	case 2:
		_, _ = w.WriteString("</span>")
	default:
		_, _ = w.WriteString(`<code class="mdxNode ` + n.typ + `">`)
		_, _ = w.Write(util.EscapeHTML(n.text))
		_, _ = w.WriteString("</code>")
	}
	return ast.WalkContinue, nil
}

// lintMDX lints MDX: Markdown, parsed with the MDX constructs.
func (l *Linter) lintMDX(f *core.File) error {
	return l.lintMarkdownWith(f, goldMdx)
}
