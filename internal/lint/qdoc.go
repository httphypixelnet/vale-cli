package lint

import (
	"regexp"
	"strings"

	"github.com/errata-ai/vale/v3/internal/core"
)

// QDoc is Qt's documentation markup: LaTeX-style `\commands` inside `/*! ...
// */` comment blocks, in C++ sources or standalone `.qdoc` files. See #784.
//
// qdocToHTML converts it to the HTML the walker reads. Prose is kept
// word-for-word so an alert can be located in the source by search; commands
// become the elements they mean -- sections are headings, `\c` is a code
// span, `\note` is a classed div -- and topic or meta commands are markup
// with nothing to lint.

// qdocVerbatim names the commands whose content runs to a matching `\end<X>`
// and is not prose.
var qdocVerbatim = map[string]struct{}{
	"badcode": {},
	"code":    {},
	"css":     {},
	"js":      {},
	"qml":     {},
	"raw":     {},
}

// qdocSkipLine names the topic, context, and quoting commands whose whole
// line is markup: identifiers, file paths, and cross-references, not prose.
var qdocSkipLine = map[string]struct{}{
	"annotatedlist":       {},
	"class":               {},
	"codeline":            {},
	"contentspage":        {},
	"dots":                {},
	"enum":                {},
	"example":             {},
	"externalpage":        {},
	"fn":                  {},
	"generatelist":        {},
	"group":               {},
	"headerfile":          {},
	"include":             {},
	"indexpage":           {},
	"ingroup":             {},
	"inherits":            {},
	"inmodule":            {},
	"inqmlmodule":         {},
	"instantiates":        {},
	"internal":            {},
	"keyword":             {},
	"macro":               {},
	"meta":                {},
	"module":              {},
	"namespace":           {},
	"nextpage":            {},
	"noautolist":          {},
	"nonreentrant":        {},
	"overload":            {},
	"page":                {},
	"previouspage":        {},
	"printline":           {},
	"printto":             {},
	"printuntil":          {},
	"property":            {},
	"qmlattachedproperty": {},
	"qmlattachedsignal":   {},
	"qmlbasictype":        {},
	"qmlclass":            {},
	"qmlmethod":           {},
	"qmlmodule":           {},
	"qmlproperty":         {},
	"qmlsignal":           {},
	"qmltype":             {},
	"qmlvaluetype":        {},
	"quotefile":           {},
	"quotefromfile":       {},
	"reentrant":           {},
	"reimp":               {},
	"relates":             {},
	"sa":                  {},
	"since":               {},
	"sincelist":           {},
	"skipline":            {},
	"skipto":              {},
	"skipuntil":           {},
	"snippet":             {},
	"startpage":           {},
	"target":              {},
	"threadsafe":          {},
	"typedef":             {},
	"variable":            {},
	"wrapper":             {},
}

// qdocDivs names the commands that open a classed paragraph -- prose that a
// rule can target by class -- running to the next blank line.
var qdocDivs = map[string]struct{}{
	"brief":     {},
	"important": {},
	"note":      {},
	"warning":   {},
}

var (
	// A block command at the start of a line: \section1, \li, \endlist, ...
	qdocBlockCmd = regexp.MustCompile(`^\s*\\([a-zA-Z0-9]+)\s*(.*)$`)
	// An inline command and its argument: `\c word` or `\c {some words}`,
	// with an optional [qualifier] for `\l`. A braced argument may wrap
	// within its paragraph.
	qdocInlineCmd = regexp.MustCompile(`\\([a-zA-Z0-9]+)\s*(\[[^\]\n]*\]\s*)?(\{[^{}]*\}|[^\s{][^\s]*)?`)
	// The braced {text} that may follow a braced `\l` target.
	qdocLinkText = regexp.MustCompile(`^\s*\{[^{}]*\}`)
)

// qdocInlineNames names the inline commands, so that one starting a line is
// not mistaken for a block command.
var qdocInlineNames = map[string]struct{}{
	"a": {}, "b": {}, "c": {}, "e": {}, "l": {}, "sub": {}, "sup": {},
	"tt": {}, "uicontrol": {}, "underline": {},
}

// qdocArg strips the braces from a command argument.
func qdocArg(arg string) string {
	if strings.HasPrefix(arg, "{") && strings.HasSuffix(arg, "}") {
		return strings.TrimSpace(arg[1 : len(arg)-1])
	}
	return arg
}

// qdocInline rewrites a line's inline commands: `\c` and `\a` become code
// spans, `\b` and friends keep their text in the matching tag, `\l` keeps a
// link's text, and any other command is markup, removed with its meaning.
func qdocInline(text string) string {
	var out strings.Builder

	for {
		loc := qdocInlineCmd.FindStringSubmatchIndex(text)
		if loc == nil {
			out.WriteString(text)
			break
		}
		out.WriteString(text[:loc[0]])

		name := text[loc[2]:loc[3]]
		arg := ""
		if loc[6] >= 0 {
			arg = text[loc[6]:loc[7]]
		}
		rest := text[loc[1]:]

		switch name {
		case "c", "tt", "a":
			out.WriteString("<code>" + qdocArg(arg) + "</code>")
		case "b", "uicontrol":
			out.WriteString("<strong>" + qdocArg(arg) + "</strong>")
		case "e":
			out.WriteString("<em>" + qdocArg(arg) + "</em>")
		case "underline":
			out.WriteString("<u>" + qdocArg(arg) + "</u>")
		case "sub", "sup":
			out.WriteString("<" + name + ">" + qdocArg(arg) + "</" + name + ">")
		case "l":
			label := qdocArg(arg)
			if strings.HasPrefix(arg, "{") {
				if m := qdocLinkText.FindString(rest); m != "" {
					label = qdocArg(strings.TrimSpace(m))
					rest = rest[len(m):]
				}
			}
			out.WriteString(`<a href="#">` + label + "</a>")
		case "image", "inlineimage":
			// The argument is a file name; a caption, if any, follows as
			// ordinary prose.
		default:
			// An unknown command is markup; whatever followed it is prose.
			rest = text[loc[3]:]
		}

		text = rest
	}

	return out.String()
}

// qdocContext is one open `\list` or `\table`.
type qdocContext struct {
	kind   string // "list" or "table"
	inItem bool
	header bool
}

// qdocConv converts one QDoc document.
type qdocConv struct {
	html strings.Builder
	para []string
	div  string // an open \note / \brief / ... paragraph's class

	stack    []qdocContext
	verbatim string // the \end command that closes the open verbatim block
	omitted  bool
}

func (c *qdocConv) flush() {
	if len(c.para) == 0 {
		return
	}
	text := qdocInline(strings.Join(c.para, "\n"))
	c.para = nil

	if n := len(c.stack); n > 0 && c.stack[n-1].inItem {
		c.html.WriteString(text + "\n")
		return
	}
	c.html.WriteString("<p>" + text + "</p>\n")
}

// closeDiv ends an open \note-style paragraph.
func (c *qdocConv) closeDiv() {
	c.flush()
	if c.div != "" {
		c.html.WriteString("</div>\n")
		c.div = ""
	}
}

// item opens a `\li` -- a list item, or a table cell in the current row.
func (c *qdocConv) item(rest string) {
	n := len(c.stack)
	if n == 0 {
		c.para = append(c.para, rest)
		return
	}

	c.endItem()
	top := &c.stack[n-1]
	top.inItem = true

	tag := "li"
	if top.kind == "table" {
		tag = "td"
		if top.header {
			tag = "th"
		}
	}
	c.html.WriteString("<" + tag + ">")
	if rest != "" {
		c.para = append(c.para, rest)
	}
}

func (c *qdocConv) endItem() {
	n := len(c.stack)
	if n == 0 || !c.stack[n-1].inItem {
		return
	}
	c.flush()
	top := &c.stack[n-1]

	tag := "li"
	if top.kind == "table" {
		tag = "td"
		if top.header {
			tag = "th"
		}
	}
	c.html.WriteString("</" + tag + ">\n")
	top.inItem = false
}

func (c *qdocConv) line(raw string) { //nolint:gocyclo // one case per command family
	trimmed := strings.TrimSpace(raw)

	// Comment delimiters, in both standalone .qdoc files and sources.
	if trimmed == "/*!" || trimmed == "*/" || strings.HasPrefix(trimmed, "/*!") && strings.HasSuffix(trimmed, "*/") {
		return
	}

	if c.verbatim != "" {
		if m := qdocBlockCmd.FindStringSubmatch(raw); m != nil && m[1] == c.verbatim {
			c.verbatim = ""
			c.html.WriteString("</code></pre>\n")
		}
		// The content itself is dropped: it was never prose.
		return
	}
	if c.omitted {
		if m := qdocBlockCmd.FindStringSubmatch(raw); m != nil && m[1] == "endomit" {
			c.omitted = false
		}
		return
	}

	if trimmed == "" {
		c.closeDiv()
		c.flush()
		return
	}

	m := qdocBlockCmd.FindStringSubmatch(raw)
	if m == nil {
		c.para = append(c.para, raw)
		return
	}
	name, rest := m[1], strings.TrimSpace(m[2])
	if _, inline := qdocInlineNames[name]; inline {
		c.para = append(c.para, raw)
		return
	}

	switch {
	case name == "omit":
		c.flush()
		c.omitted = true
	case func() bool { _, ok := qdocVerbatim[name]; return ok }():
		c.flush()
		c.verbatim = "end" + name
		c.html.WriteString("<pre><code>")
	case func() bool { _, ok := qdocSkipLine[name]; return ok }():
		c.flush()
	case func() bool { _, ok := qdocDivs[name]; return ok }():
		c.closeDiv()
		c.div = name
		c.html.WriteString(`<div class="` + name + "\">\n")
		if rest != "" {
			c.para = append(c.para, rest)
		}
	case name == "title":
		c.flush()
		c.html.WriteString("<h1>" + qdocInline(rest) + "</h1>\n")
	case strings.HasPrefix(name, "section") && len(name) == 8 && name[7] >= '1' && name[7] <= '4':
		c.closeDiv()
		level := string('1' + name[7] - '0')
		c.html.WriteString("<h" + level + ">" + qdocInline(rest) + "</h" + level + ">\n")
	case name == "list":
		c.flush()
		c.stack = append(c.stack, qdocContext{kind: "list"})
		c.html.WriteString("<ul>\n")
	case name == "endlist":
		c.endItem()
		if n := len(c.stack); n > 0 {
			c.stack = c.stack[:n-1]
		}
		c.html.WriteString("</ul>\n")
	case name == "table":
		c.flush()
		c.stack = append(c.stack, qdocContext{kind: "table"})
		c.html.WriteString("<table>\n")
	case name == "endtable":
		c.endItem()
		if n := len(c.stack); n > 0 {
			c.stack = c.stack[:n-1]
		}
		c.html.WriteString("</table>\n")
	case name == "header", name == "row":
		c.endItem()
		if n := len(c.stack); n > 0 {
			c.stack[n-1].header = name == "header"
		}
		c.html.WriteString("<tr>\n")
	case name == "li", name == "o":
		c.item(rest)
	case name == "value":
		// \value ConstantName The description is prose.
		c.flush()
		fields := strings.Fields(rest)
		if len(fields) > 1 {
			c.html.WriteString("<p><code>" + fields[0] + "</code> " +
				qdocInline(strings.Join(fields[1:], " ")) + "</p>\n")
		}
	case name == "quotation":
		c.flush()
		c.html.WriteString("<blockquote>\n")
	case name == "endquotation":
		c.flush()
		c.html.WriteString("</blockquote>\n")
	case name == "div":
		c.flush()
		c.html.WriteString("<div>\n")
	case name == "enddiv":
		c.flush()
		c.html.WriteString("</div>\n")
	case name == "legalese":
		c.flush()
		c.html.WriteString(`<div class="legalese">` + "\n")
	case name == "endlegalese":
		c.flush()
		c.html.WriteString("</div>\n")
	case name == "caption":
		c.flush()
		c.html.WriteString("<figcaption>" + qdocInline(rest) + "</figcaption>\n")
	case name == "image", name == "inlineimage":
		// `\image file.png An optional caption of prose.`
		c.flush()
		fields := strings.Fields(rest)
		if len(fields) > 1 {
			c.html.WriteString("<figcaption>" +
				qdocInline(strings.Join(fields[1:], " ")) + "</figcaption>\n")
		}
	default:
		// An unknown block command: the command is markup, the rest prose.
		if rest != "" {
			c.para = append(c.para, rest)
		}
	}
}

func qdocToHTML(content string) string {
	conv := &qdocConv{}
	for _, line := range strings.Split(content, "\n") {
		conv.line(line)
	}
	conv.closeDiv()
	conv.flush()
	return conv.html.String()
}

// lintQDoc lints QDoc: Qt's documentation markup.
func (l *Linter) lintQDoc(f *core.File) error {
	err := l.lintMetadata(f)
	if err != nil {
		return err
	}

	s, err := l.Transform(f)
	if err != nil {
		return err
	}

	return l.lintHTMLTokens(f, []byte(qdocToHTML(s)), 0)
}
