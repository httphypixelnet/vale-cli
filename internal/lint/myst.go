package lint

import (
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

// MyST is CommonMark plus Sphinx-style constructs -- directives, roles,
// targets, comments, and block breaks -- that a Markdown parser reads as
// prose. mystExtension parses them into shapes the walker already
// understands. See #667.
//
// A directive's content is Markdown by specification, so a prose directive
// becomes `<div class="<name>">` and its content is linted with the
// directive's name as a class scope (`text.class.note`). A directive whose
// content is literal -- code, math, raw markup -- becomes `<pre>`, which the
// walker skips. Everything else -- targets, comments, block breaks, options,
// role and attribute braces -- renders as nothing at all.

// mystLiteral names the directives whose content is not prose.
var mystLiteral = map[string]struct{}{
	"code":           {},
	"code-block":     {},
	"code-cell":      {},
	"csv-table":      {},
	"eval-rst":       {},
	"highlight":      {},
	"include":        {},
	"literalinclude": {},
	"math":           {},
	"mermaid":        {},
	"raw":            {},
	"sourcecode":     {},
	"toctree":        {},
}

var (
	// {name}, the info string that makes a fence a directive.
	mystName = regexp.MustCompile(`^\{([A-Za-z0-9:._+-]+)\}`)
	// (my-label)= on a line of its own.
	mystTarget = regexp.MustCompile(`^\(\S+\)=\s*$`)
	// {.class} / {#id} / {style=...} on a line of its own.
	mystAttrsBlock = regexp.MustCompile(`^\{[^{}\n]*\}\s*$`)
	// :key: value under a directive opener.
	mystOption = regexp.MustCompile(`^\s{0,3}:[^:\s][^:\n]*:`)
	// {role} immediately followed by a code span.
	mystRole = regexp.MustCompile("^\\{[A-Za-z][A-Za-z0-9:._+-]*\\}`")
	// {{ substitution }}
	mystSub = regexp.MustCompile(`^\{\{[^{}\n]*\}\}`)
	// The {.class} of a [text]{.class} inline attribute.
	mystAttrsInline = regexp.MustCompile(`^\{[^{}\n]*\}`)
)

// MyST configuration: Markdown, plus the MyST constructs.
var goldMyst = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Footnote,
		mathExtension{},
		mystExtension{},
	),
	goldmark.WithRendererOptions(
		grh.WithUnsafe(),
	),
)

type mystExtension struct{}

func (mystExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(
		// Ahead of the fenced-code parser (700), so a fence opening a
		// directive is read as one; a plain fence falls through to it.
		util.Prioritized(&mystDirectiveParser{}, 690),
		// Ahead of the paragraph parser (1000), which would otherwise read
		// these lines as prose.
		util.Prioritized(&mystMarkupParser{}, 990),
	))
	m.Parser().AddOptions(parser.WithInlineParsers(
		// Ahead of the emphasis parser and friends; '{' has no built-in
		// owner, so the priority is uncontested.
		util.Prioritized(&mystInlineParser{}, 100),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(mystRenderer{}, 1),
	))
}

// A mystDirective is one fenced directive: ```{name}, ~~~{name}, or :::{name}.
type mystDirective struct {
	ast.BaseBlock

	name    string
	literal bool

	marker byte
	size   int

	// opts tracks the option lines under the opener: 1 while a field list
	// may continue, 2 inside a ----delimited YAML block, 0 once content has
	// begun.
	opts int
}

var kindMystDirective = ast.NewNodeKind("MystDirective")

func (n *mystDirective) Kind() ast.NodeKind { return kindMystDirective }
func (n *mystDirective) IsRaw() bool        { return n.literal }
func (n *mystDirective) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type mystDirectiveParser struct{}

func (*mystDirectiveParser) Trigger() []byte {
	return []byte{'`', '~', ':'}
}

func (*mystDirectiveParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || pos >= len(line) {
		return nil, parser.NoChildren
	}

	marker := line[pos]
	if marker != '`' && marker != '~' && marker != ':' {
		return nil, parser.NoChildren
	}
	i := pos
	for i < len(line) && line[i] == marker {
		i++
	}
	size := i - pos
	if size < 3 {
		return nil, parser.NoChildren
	}

	rest := line[i:]
	name := mystName.FindSubmatch(rest[util.TrimLeftSpaceLength(rest):])
	if name == nil {
		// Not a directive. A bare colon fence is not Markdown either, but
		// there is no built-in parser to fall through to -- so it is treated
		// as markup and discarded, closing any open colon directive.
		if marker == ':' && util.IsBlank(rest) {
			reader.Advance(segment.Len() - 1)
			return &mystMarkup{}, parser.NoChildren
		}
		return nil, parser.NoChildren
	}

	node := &mystDirective{
		name:   string(name[1]),
		marker: marker,
		size:   size,
		opts:   1,
	}
	_, node.literal = mystLiteral[node.name]

	reader.Advance(segment.Len() - 1)
	if node.literal {
		return node, parser.NoChildren
	}
	return node, parser.HasChildren
}

func (*mystDirectiveParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n, ok := node.(*mystDirective)
	if !ok {
		// A bare colon fence: one line of markup, already consumed.
		return parser.Close
	}
	line, segment := reader.PeekLine()

	// The closing fence: at least as many markers as the opener, alone on a
	// line.
	w, pos := util.IndentWidth(line, reader.LineOffset())
	if w < 4 {
		i := pos
		for i < len(line) && line[i] == n.marker {
			i++
		}
		if i-pos >= n.size && util.IsBlank(line[i:]) {
			newline := 1
			if line[len(line)-1] != '\n' {
				newline = 0
			}
			reader.Advance(segment.Stop - segment.Start - newline + segment.Padding)
			return parser.Close
		}
	}

	// Option lines come directly after the opener and are markup, not prose.
	if n.opts == 2 {
		done := len(line) >= 3 && string(line[:3]) == "---"
		reader.Advance(segment.Len() - 1)
		if done {
			n.opts = 0
		}
		if n.literal {
			return parser.Continue | parser.NoChildren
		}
		return parser.Continue | parser.HasChildren
	}
	if n.opts == 1 {
		if len(line) >= 3 && string(line[:3]) == "---" {
			n.opts = 2
			reader.Advance(segment.Len() - 1)
			return parser.Continue | parser.HasChildren
		}
		if mystOption.Match(line) {
			reader.Advance(segment.Len() - 1)
			return parser.Continue | parser.HasChildren
		}
		n.opts = 0
	}

	if n.literal {
		seg := text.NewSegmentPadding(segment.Start, segment.Stop, segment.Padding)
		seg.ForceNewline = true
		n.Lines().Append(seg)
		reader.Advance(segment.Stop - segment.Start - 1 + segment.Padding)
		return parser.Continue | parser.NoChildren
	}
	return parser.Continue | parser.HasChildren
}

func (*mystDirectiveParser) Close(ast.Node, text.Reader, parser.Context) {}

func (*mystDirectiveParser) CanInterruptParagraph() bool { return true }
func (*mystDirectiveParser) CanAcceptIndentedLine() bool { return false }

// A mystMarkup line is MyST syntax with nothing to lint: a target, a comment,
// a block break, or an attributes line.
type mystMarkup struct {
	ast.BaseBlock
}

var kindMystMarkup = ast.NewNodeKind("MystMarkup")

func (n *mystMarkup) Kind() ast.NodeKind { return kindMystMarkup }
func (n *mystMarkup) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type mystMarkupParser struct{}

func (*mystMarkupParser) Trigger() []byte {
	return []byte{'(', '%', '+', '{'}
}

func (*mystMarkupParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || pos >= len(line) {
		return nil, parser.NoChildren
	}

	matched := false
	switch line[pos] {
	case '%':
		matched = true
	case '+':
		i := pos
		for i < len(line) && line[i] == '+' {
			i++
		}
		matched = i-pos >= 3
	case '(':
		matched = mystTarget.Match(line[pos:])
	case '{':
		matched = mystAttrsBlock.Match(line[pos:])
	}
	if !matched {
		return nil, parser.NoChildren
	}

	reader.Advance(segment.Len() - 1)
	return &mystMarkup{}, parser.NoChildren
}

func (*mystMarkupParser) Continue(ast.Node, text.Reader, parser.Context) parser.State {
	return parser.Close
}

func (*mystMarkupParser) Close(ast.Node, text.Reader, parser.Context) {}

func (*mystMarkupParser) CanInterruptParagraph() bool { return true }
func (*mystMarkupParser) CanAcceptIndentedLine() bool { return false }

// A mystInline is inline MyST syntax with nothing to lint: a role's braces, a
// substitution, or an inline attribute.
type mystInline struct {
	ast.BaseInline
}

var kindMystInline = ast.NewNodeKind("MystInline")

func (n *mystInline) Kind() ast.NodeKind { return kindMystInline }
func (n *mystInline) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type mystInlineParser struct{}

func (*mystInlineParser) Trigger() []byte {
	return []byte{'{'}
}

func (*mystInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()

	// {{ substitution }}
	if m := mystSub.Find(line); m != nil {
		block.Advance(len(m))
		return &mystInline{}
	}
	// {role}`content` -- only the braces; the code span that follows is
	// already skipped as inline code.
	if m := mystRole.Find(line); m != nil {
		block.Advance(len(m) - 1)
		return &mystInline{}
	}
	// [text]{.class} -- the braces follow the closing bracket.
	if block.PrecendingCharacter() == ']' {
		if m := mystAttrsInline.Find(line); m != nil {
			block.Advance(len(m))
			return &mystInline{}
		}
	}

	return nil
}

type mystRenderer struct{}

func (mystRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMystDirective, renderMystDirective)
	reg.Register(kindMystMarkup, renderMystNothing)
	reg.Register(kindMystInline, renderMystNothing)
}

func renderMystDirective(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n, ok := node.(*mystDirective)
	if !ok {
		return ast.WalkContinue, nil
	}

	if n.literal {
		if entering {
			_, _ = w.WriteString("<pre>")
			for i := 0; i < n.Lines().Len(); i++ {
				line := n.Lines().At(i)
				_, _ = w.Write(util.EscapeHTML(line.Value(source)))
			}
		} else {
			_, _ = w.WriteString("</pre>\n")
		}
		return ast.WalkContinue, nil
	}

	if entering {
		_, _ = w.WriteString(`<div class="`)
		_, _ = w.Write(util.EscapeHTML([]byte(n.name)))
		_, _ = w.WriteString("\">\n")
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}

func renderMystNothing(_ util.BufWriter, _ []byte, _ ast.Node, _ bool) (ast.WalkStatus, error) {
	return ast.WalkSkipChildren, nil
}

// lintMyST lints MyST: Markdown, parsed with the MyST constructs.
func (l *Linter) lintMyST(f *core.File) error {
	return l.lintMarkdownWith(f, goldMyst)
}
