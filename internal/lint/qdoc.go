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
// The command each one ends at is not always `\end<X>`: the code variants all
// close on `\endcode`, and `\oldcode` runs through `\newcode` because both
// halves are code.
var qdocVerbatim = map[string]string{
	"badcode": "endcode",
	"code":    "endcode",
	"css":     "endcss",
	"js":      "endjs",
	"newcode": "endcode",
	"oldcode": "endcode",
	"qml":     "endqml",
	"raw":     "endraw",
}

// qdocSkipLine names the topic, context, quoting, build-system, and
// conditional commands whose whole line is markup: identifiers, file paths,
// cross-references, and expressions, not prose.
var qdocSkipLine = map[string]struct{}{
	"abstract":            {},
	"annotatedlist":       {},
	"attribution":         {},
	"class":               {},
	"cmakecomponent":      {},
	"cmakepackage":        {},
	"cmaketargetitem":     {},
	"codeline":            {},
	"compares":            {},
	"compareswith":        {},
	"contentspage":        {},
	"default":             {},
	"dontdocument":        {},
	"dots":                {},
	"else":                {},
	"endcompareswith":     {},
	"endif":               {},
	"enum":                {},
	"example":             {},
	"externalpage":        {},
	"fn":                  {},
	"generatelist":        {},
	"group":               {},
	"headerfile":          {},
	"if":                  {},
	"include":             {},
	"indexpage":           {},
	"inheaderfile":        {},
	"ingroup":             {},
	"inherits":            {},
	"inmodule":            {},
	"inqmlmodule":         {},
	"instantiates":        {},
	"internal":            {},
	"macro":               {},
	"meta":                {},
	"module":              {},
	"modulestate":         {},
	"namespace":           {},
	"nativetype":          {},
	"nextpage":            {},
	"noautolist":          {},
	"nonreentrant":        {},
	"notranslate":         {},
	"omitvalue":           {},
	"overload":            {},
	"page":                {},
	"preliminary":         {},
	"previouspage":        {},
	"printline":           {},
	"printto":             {},
	"printuntil":          {},
	"property":            {},
	"qmlabstract":         {},
	"qmlattachedproperty": {},
	"qmlattachedsignal":   {},
	"qmlbasictype":        {},
	"qmlclass":            {},
	"qmldefault":          {},
	"qmlenum":             {},
	"qmlenumeratorsfrom":  {},
	"qmlmethod":           {},
	"qmlmodule":           {},
	"qmlproperty":         {},
	"qmlsignal":           {},
	"qmlsingletontype":    {},
	"qmltype":             {},
	"qmlvaluetype":        {},
	"qtcmakepackage":      {},
	"qtcmaketargetitem":   {},
	"qtvariable":          {},
	"quotefile":           {},
	"quotefromfile":       {},
	"readonly":            {},
	"reentrant":           {},
	"reimp":               {},
	"relates":             {},
	"required":            {},
	"sa":                  {},
	"since":               {},
	"sincelist":           {},
	"skipline":            {},
	"skipto":              {},
	"skipuntil":           {},
	"snippet":             {},
	"startpage":           {},
	"tableofcontents":     {},
	"threadsafe":          {},
	"toc":                 {},
	"tocentry":            {},
	"typealias":           {},
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
	// within its paragraph. `\\` is an escaped backslash, matched first so
	// that the command that follows one is read as text.
	qdocInlineCmd = regexp.MustCompile(`\\\\|\\([a-zA-Z0-9]+)\s*(\[[^\]\n]*\]\s*)?(\{[^{}]*\}|[^\s{][^\s]*)?`)
	// The braced {text} that may follow a braced `\l` target or `\span`
	// attribute.
	qdocLinkText = regexp.MustCompile(`^\s*\{[^{}]*\}`)
	// A `\div` or `\span` attribute list: `{class="header"}`.
	qdocClassAttr = regexp.MustCompile(`class\s*=\s*"([^"]*)"`)
	// A table cell's `{rows,cols}` span, which precedes the cell's prose.
	qdocCellSpan = regexp.MustCompile(`^\{\d+\s*,\s*\d+\}\s*`)
	// The `[since]` qualifier that may open a `\deprecated` line.
	qdocSinceQual = regexp.MustCompile(`^\[[^\]\n]*\]\s*`)
)

// qdocClass returns the class named by a `\div` or `\span` attribute list, or
// the argument itself when it is a bare name -- `\div {note}`. An unusable
// class is dropped rather than guessed at, leaving an unclassed element.
func qdocClass(arg string) string {
	arg = qdocArg(arg)
	if m := qdocClassAttr.FindStringSubmatch(arg); m != nil {
		return m[1]
	} else if strings.ContainsAny(arg, `="' `) {
		return ""
	}
	return arg
}

// meta writes one piece of the document's machine-readable content -- an
// anchor name, an image's file name -- under the `meta` scope, where a rule
// about naming can reach it and a style about prose cannot.
func (c *qdocConv) meta(kind, value string) {
	if value == "" {
		return
	}
	c.html.WriteString(`<data class="` + kind + `">` + qdocEsc(value) + "</data>\n")
}

// qdocOpenDiv writes a `<div>` carrying `arg`'s class, if it names one.
func (c *qdocConv) openDiv(arg string) {
	c.flush()
	if class := qdocClass(arg); class != "" {
		c.html.WriteString(`<div class="` + class + "\">\n")
		return
	}
	c.html.WriteString("<div>\n")
}

// qdocInlineNames names the inline commands, so that one starting a line is
// not mistaken for a block command.
var qdocInlineNames = map[string]struct{}{
	"a": {}, "b": {}, "bold": {}, "c": {}, "e": {}, "i": {}, "l": {},
	"sub": {}, "sup": {}, "tt": {}, "uicontrol": {}, "underline": {},
}

// qdocEsc makes text safe to write into the HTML the walker reads.
//
// QDoc prose is kept word-for-word, and Qt's prose talks about markup: a
// sentence explaining that a file "must be included using a <script> tag"
// carries a real tag. Written straight through, it opens an element in the
// converted document -- and `<script>` in particular puts the tokenizer into
// raw-text mode, so the rest of the page is swallowed looking for a close
// that never comes. The tokenizer decodes these again, so the text a rule
// sees, and the text located in the source, are unchanged.
var qdocEscAll = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

// An HTML comment, which survives escaping: `vale off` and `vale on` reach
// the walker as comments, and a QDoc file writes them the same way any other
// markup does.
var qdocHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)

func qdocEsc(text string) string {
	spans := qdocHTMLComment.FindAllStringIndex(text, -1)
	if spans == nil {
		return qdocEscAll(text)
	}

	var out strings.Builder
	last := 0
	for _, s := range spans {
		out.WriteString(qdocEscAll(text[last:s[0]]))
		out.WriteString(text[s[0]:s[1]])
		last = s[1]
	}
	out.WriteString(qdocEscAll(text[last:]))

	return out.String()
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
			out.WriteString(qdocEsc(text))
			break
		}
		out.WriteString(qdocEsc(text[:loc[0]]))

		if loc[2] < 0 {
			// `\\` is a literal backslash, not a command.
			out.WriteString(`\`)
			text = text[loc[1]:]
			continue
		}

		name := text[loc[2]:loc[3]]
		arg := ""
		if loc[6] >= 0 {
			arg = text[loc[6]:loc[7]]
		}
		rest := text[loc[1]:]

		switch name {
		case "c", "tt", "a":
			out.WriteString("<code>" + qdocEsc(qdocArg(arg)) + "</code>")
		case "b", "bold", "uicontrol":
			out.WriteString("<strong>" + qdocEsc(qdocArg(arg)) + "</strong>")
		case "e", "i":
			out.WriteString("<em>" + qdocEsc(qdocArg(arg)) + "</em>")
		case "underline":
			out.WriteString("<u>" + qdocEsc(qdocArg(arg)) + "</u>")
		case "sub", "sup":
			out.WriteString("<" + name + ">" + qdocEsc(qdocArg(arg)) + "</" + name + ">")
		case "l":
			label := qdocArg(arg)
			if strings.HasPrefix(arg, "{") {
				if m := qdocLinkText.FindString(rest); m != "" {
					label = qdocArg(strings.TrimSpace(m))
					rest = rest[len(m):]
				}
			}
			out.WriteString(`<a href="#">` + qdocEsc(label) + "</a>")
		case "span":
			// `\span {class="x"} {text}`: the attribute names a class a rule
			// can be scoped to, the braced text that follows is prose.
			label := ""
			if m := qdocLinkText.FindString(rest); m != "" {
				label = qdocArg(strings.TrimSpace(m))
				rest = rest[len(m):]
			}
			if class := qdocClass(arg); class != "" {
				out.WriteString(`<span class="` + class + `">` + qdocEsc(label) + "</span>")
			} else {
				out.WriteString("<span>" + qdocEsc(label) + "</span>")
			}
		case "image", "inlineimage":
			// The argument is a file name; a caption, if any, follows as
			// ordinary prose.
		case "unicode":
			// The argument is a code point, not prose.
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

	// inComment says whether the reader is inside a `/*! ... */` block, the
	// only place QDoc looks. A comment body extracted from a source file
	// starts inside one; a whole file starts outside.
	inComment bool
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

	// A table cell may open with a `{rows,cols}` span, which is markup.
	rest = qdocCellSpan.ReplaceAllString(rest, "")

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

// qdocBlankDelims blanks the comment delimiters of a standalone .qdoc file,
// which a command or its prose may share a line with: `/*! \page index.html`.
// They are replaced by spaces rather than removed so that a column in the
// converted line is still a column in the source.
func qdocBlankDelims(raw string) string {
	if i := strings.Index(raw, "/*!"); i >= 0 && strings.TrimSpace(raw[:i]) == "" {
		raw = raw[:i] + "   " + raw[i+3:]
	}
	if i := strings.LastIndex(raw, "*/"); i >= 0 && strings.TrimSpace(raw[i+2:]) == "" {
		raw = raw[:i] + "  " + raw[i+2:]
	}
	return raw
}

// endsVerbatim reports whether name closes the open verbatim block. Both the
// command QDoc documents and the symmetrical `\end<X>` are accepted, so a
// source that writes `\endbadcode` is not read as unterminated.
func (c *qdocConv) endsVerbatim(name string) bool {
	return name == qdocVerbatim[c.verbatim] || name == "end"+c.verbatim
}

// opensComment reports whether the line starts a `/*!` documentation comment.
func qdocOpensComment(raw string) bool {
	i := strings.Index(raw, "/*!")
	return i >= 0 && strings.TrimSpace(raw[:i]) == ""
}

// line reads one line of a QDoc source, tracking which of them the reader is
// meant to see at all.
//
// QDoc documents a project from its `/*! ... */` comments and nothing else.
// A `.qdoc` file is a file of those comments, and what sits between them --
// a licence header, a `//! [name]` snippet whose body is shell or C++ -- is
// no more prose than the code in a `.cpp` file is.
func (c *qdocConv) line(raw string) {
	closes := strings.HasSuffix(strings.TrimSpace(raw), "*/")

	// A verbatim or omitted block cannot outlive the comment holding it. An
	// unterminated one -- a `\code` whose `\endcode` was never written, or a
	// spelling this converter does not know -- would otherwise swallow the
	// rest of the file.
	if closes {
		if c.verbatim != "" {
			c.html.WriteString("</code></pre>\n")
			c.verbatim = ""
		}
		c.omitted = false
	}

	if c.inComment || qdocOpensComment(raw) {
		c.inComment = true
		c.content(qdocBlankDelims(raw))
	}

	if closes {
		c.inComment = false
		c.closeDiv()
	}
}

func (c *qdocConv) content(raw string) { //nolint:gocyclo // one case per command family
	trimmed := strings.TrimSpace(raw)

	if c.verbatim != "" {
		if m := qdocBlockCmd.FindStringSubmatch(raw); m != nil && c.endsVerbatim(m[1]) {
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

	// A `//` line inside a comment is a snippet marker -- `//! [intro]` --
	// or code, never prose. QDoc renders nothing for it, so it does not end
	// the paragraph it sits in the middle of either.
	if strings.HasPrefix(trimmed, "//") {
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
		c.verbatim = name
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
	case name == "subtitle":
		c.flush()
		c.html.WriteString(`<h2 class="subtitle">` + qdocInline(rest) + "</h2>\n")
	case name == "deprecated":
		// `\deprecated [6.0] Use \l Foo instead.`: the version is markup,
		// the replacement advice that may follow it is prose.
		c.flush()
		if rest = qdocSinceQual.ReplaceAllString(rest, ""); rest != "" {
			c.para = append(c.para, rest)
		}
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
			c.html.WriteString("<p><code>" + qdocEsc(fields[0]) + "</code> " +
				qdocInline(strings.Join(fields[1:], " ")) + "</p>\n")
		}
	case name == "quotation":
		c.flush()
		c.html.WriteString("<blockquote>\n")
	case name == "endquotation":
		c.flush()
		c.html.WriteString("</blockquote>\n")
	case name == "div":
		c.openDiv(rest)
	case name == "details":
		// `\details {Summary text}`: the summary is prose of its own.
		c.flush()
		c.html.WriteString(`<div class="details">` + "\n")
		if summary := qdocArg(rest); summary != "" {
			c.html.WriteString(`<p class="summary">` + qdocInline(summary) + "</p>\n")
		}
	case name == "enddiv", name == "enddetails":
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
	case name == "target", name == "keyword":
		// The anchor a link points at: a name, reachable under `meta` for a
		// rule about naming and invisible to one about prose.
		c.flush()
		c.meta("anchor", qdocArg(rest))
	case name == "image", name == "inlineimage":
		// `\image file.png An optional caption of prose.`
		c.flush()
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			c.meta("image", fields[0])
		}
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

// qdocToHTML converts a whole QDoc source: the `/*! ... */` comments in it
// are the document, and everything else is code.
func qdocToHTML(content string) string { return qdocConvert(content, false) }

// qdocFragmentToHTML converts the body of a single comment, already extracted
// from a source file, so there is no `/*!` left to wait for.
func qdocFragmentToHTML(content string) string { return qdocConvert(content, true) }

func qdocConvert(content string, inComment bool) string {
	conv := &qdocConv{inComment: inComment}
	for _, line := range strings.Split(content, "\n") {
		conv.line(line)
	}
	conv.closeDiv()
	conv.flush()
	return conv.html.String()
}

// lintQDoc lints a QDoc source: Qt's documentation markup.
//
// A `.qdocinc` is the exception. It is an include -- the body of a comment,
// pulled into a document by `\include` -- so its markup is not wrapped in
// `/*! ... */`, and reading it as a `.qdoc` file leaves the whole thing
// unread while waiting for a comment to open. Starting inside one reads both
// shapes: a `/*!` still opens a comment where the file writes one. See #784.
func (l *Linter) lintQDoc(f *core.File) error {
	if f.RealExt == ".qdocinc" {
		return l.lintQDocWith(f, qdocFragmentToHTML)
	}
	return l.lintQDocWith(f, qdocToHTML)
}

// lintQDocFragment lints one QDoc comment lifted out of a code file.
func (l *Linter) lintQDocFragment(f *core.File) error {
	return l.lintQDocWith(f, qdocFragmentToHTML)
}

func (l *Linter) lintQDocWith(f *core.File, convert func(string) string) error {
	err := l.lintMetadata(f)
	if err != nil {
		return err
	}

	s, err := l.Transform(f)
	if err != nil {
		return err
	}

	return l.lintHTMLTokens(f, []byte(convert(s)), 0)
}
