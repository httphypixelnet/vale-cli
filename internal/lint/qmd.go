package lint

import (
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

// Quarto is Pandoc Markdown plus knitr- and Jupyter-style code cells. The
// cells already read as fenced code and code spans, so what needs parsing is
// the Pandoc layer: fenced divs, attributes, and shortcodes. See #793.
//
// A fenced div's classes become class scopes (`class.callout-note`) for
// everything inside it, and its fence lines are markup. Attributes and
// shortcodes render as nothing at all.

// Quarto configuration: Markdown, plus the Pandoc constructs.
var goldQmd = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Footnote,
		mathExtension{},
		quartoExtension{},
	),
	goldmark.WithRendererOptions(
		grh.WithUnsafe(),
	),
)

type quartoExtension struct{}

func (quartoExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(
		// Ahead of the paragraph parser (1000); ':' has no built-in owner.
		util.Prioritized(&quartoDivParser{}, 990),
	))
	m.Parser().AddOptions(parser.WithInlineParsers(
		// '{' has no built-in owner.
		util.Prioritized(&quartoInlineParser{}, 100),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(quartoRenderer{}, 1),
	))
}

var (
	// {{< shortcode ... >}}
	quartoShortcode = regexp.MustCompile(`^\{\{<[^\n]*?>\}\}`)
	// {#id .class key="val"} -- an identifier or class attribute.
	quartoAttrs = regexp.MustCompile(`^\{[#.][^{}\n]*\}`)
	// Any braced attributes directly after a `]`.
	quartoAttrsAfter = regexp.MustCompile(`^\{[^{}\n]*\}`)
)

// quartoFence reads a fenced-div line: at least three colons, then -- for an
// opener -- attributes or a bare name. It reports whether the line is a
// fence at all, and what follows the colons.
func quartoFence(line []byte, pos int) (string, bool) {
	if pos < 0 || pos >= len(line) || line[pos] != ':' {
		return "", false
	}
	i := pos
	for i < len(line) && line[i] == ':' {
		i++
	}
	if i-pos < 3 {
		return "", false
	}
	return strings.TrimSpace(string(line[i:])), true
}

// quartoClasses reads the class names out of a div opener's attributes:
// `{.callout-note title="x"}` and the bare-word form `warning` alike.
func quartoClasses(rest string) []string {
	rest = strings.TrimSpace(rest)

	var found []string
	if strings.HasPrefix(rest, "{") {
		for _, f := range strings.Fields(strings.Trim(rest, "{}")) {
			if name, ok := strings.CutPrefix(f, "."); ok {
				found = append(found, name)
			}
		}
		return found
	}

	if f := strings.Fields(rest); len(f) > 0 {
		found = append(found, f[0])
	}
	return found
}

// A quartoDiv is one fenced div.
type quartoDiv struct {
	ast.BaseBlock

	classes []string
	closed  bool
}

var kindQuartoDiv = ast.NewNodeKind("QuartoDiv")

func (n *quartoDiv) Kind() ast.NodeKind { return kindQuartoDiv }
func (n *quartoDiv) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// openDescendant returns the deepest still-open div under n, or nil.
func (n *quartoDiv) openDescendant() *quartoDiv {
	if child, ok := n.LastChild().(*quartoDiv); ok && !child.closed {
		if deeper := child.openDescendant(); deeper != nil {
			return deeper
		}
		return child
	}
	return nil
}

type quartoDivParser struct{}

func (*quartoDivParser) Trigger() []byte {
	return []byte{':'}
}

func (*quartoDivParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()

	rest, ok := quartoFence(line, pc.BlockOffset())
	if !ok {
		return nil, parser.NoChildren
	}
	if rest == "" {
		// A stray closer with no open div: markup, nothing more.
		reader.Advance(segment.Len() - 1)
		return &quartoDiv{closed: true}, parser.NoChildren
	}

	node := &quartoDiv{classes: quartoClasses(rest)}
	reader.Advance(segment.Len() - 1)
	return node, parser.HasChildren
}

func (*quartoDivParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n := node.(*quartoDiv) //nolint:errcheck // only quartoDiv is opened
	if n.closed {
		return parser.Close
	}

	line, segment := reader.PeekLine()
	w, pos := util.IndentWidth(line, reader.LineOffset())
	rest, ok := quartoFence(line, pos)
	if w > 3 || !ok || rest != "" {
		// Not a closer; an opener is a child and parses on its own.
		return parser.Continue | parser.HasChildren
	}

	// A bare fence closes the innermost open div. When that is a descendant,
	// the line is passed down the chain instead of taken here.
	if n.openDescendant() != nil {
		return parser.Continue | parser.HasChildren
	}

	newline := 1
	if line[len(line)-1] != '\n' {
		newline = 0
	}
	reader.Advance(segment.Stop - segment.Start - newline + segment.Padding)
	return parser.Close
}

func (*quartoDivParser) Close(node ast.Node, _ text.Reader, _ parser.Context) {
	if n, ok := node.(*quartoDiv); ok {
		n.closed = true
	}
}

func (*quartoDivParser) CanInterruptParagraph() bool { return true }
func (*quartoDivParser) CanAcceptIndentedLine() bool { return false }

// A quartoInline is inline Pandoc syntax with nothing to lint: a shortcode,
// or an attribute set.
type quartoInline struct {
	ast.BaseInline
}

var kindQuartoInline = ast.NewNodeKind("QuartoInline")

func (n *quartoInline) Kind() ast.NodeKind { return kindQuartoInline }
func (n *quartoInline) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type quartoInlineParser struct{}

func (*quartoInlineParser) Trigger() []byte {
	return []byte{'{'}
}

func (*quartoInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()

	// {{< shortcode >}}
	if m := quartoShortcode.Find(line); m != nil {
		block.Advance(len(m))
		return &quartoInline{}
	}
	// {#id} / {.class} -- a heading's or span's attributes.
	if m := quartoAttrs.Find(line); m != nil {
		block.Advance(len(m))
		return &quartoInline{}
	}
	// [text]{lang="fr"} -- any attributes directly after a bracket.
	if block.PrecendingCharacter() == ']' {
		if m := quartoAttrsAfter.Find(line); m != nil {
			block.Advance(len(m))
			return &quartoInline{}
		}
	}

	return nil
}

type quartoRenderer struct{}

func (quartoRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindQuartoDiv, renderQuartoDiv)
	reg.Register(kindQuartoInline, renderMystNothing)
}

func renderQuartoDiv(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n, ok := node.(*quartoDiv)
	if !ok || len(n.classes) == 0 && n.ChildCount() == 0 {
		return ast.WalkContinue, nil
	}

	if entering {
		_, _ = w.WriteString(`<div class="`)
		_, _ = w.Write(util.EscapeHTML([]byte(strings.Join(n.classes, " "))))
		_, _ = w.WriteString("\">\n")
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}

// lintQuarto lints Quarto: Markdown, parsed with the Pandoc constructs.
func (l *Linter) lintQuarto(f *core.File) error {
	return l.lintMarkdownWith(f, goldQmd)
}
