package lint

import (
	"strings"
	"testing"
)

// TestQdocHTML pins what the QDoc converter makes of each construct: prose
// is kept word-for-word, commands become the elements they mean, and topic
// or meta commands are markup with nothing to lint. See #784.
func TestQdocHTML(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   []string
		absent []string
	}{
		{
			"comment delimiters are markup",
			"/*!\n    Prose here.\n*/\n",
			[]string{"<p>    Prose here.</p>"},
			[]string{"/*!", "*/"},
		},
		{
			"title and sections are headings",
			"\\title The Page Title\n\n\\section1 First Level\n\n\\section2 Second Level\n",
			[]string{"<h1>The Page Title</h1>", "<h2>First Level</h2>", "<h3>Second Level</h3>"},
			nil,
		},
		{
			"topic commands are markup",
			"\\page overview.html\n\\class QWidget\n\\fn void QWidget::show()\n\nProse here.\n",
			[]string{"<p>Prose here.</p>"},
			[]string{"overview", "QWidget", "show"},
		},
		{
			"brief and note are classed paragraphs",
			"\\brief One brief sentence.\n\n\\note A note sentence.\n",
			[]string{
				`<div class="brief">`, "One brief sentence.",
				`<div class="note">`, "A note sentence.",
			},
			nil,
		},
		{
			"code runs to endcode and is skipped",
			"\\code\n    int x = 0;\n\\endcode\n\nProse here.\n",
			[]string{"<pre><code>", "<p>Prose here.</p>"},
			[]string{"int x"},
		},
		{
			"omit drops its content",
			"Before.\n\n\\omit\nHidden prose.\n\\endomit\n\nAfter.\n",
			[]string{"<p>Before.</p>", "<p>After.</p>"},
			[]string{"Hidden"},
		},
		{
			"c and a are code spans",
			"Use the \\c QWidget class with the \\a parent argument.\n",
			[]string{"<code>QWidget</code>", "<code>parent</code>"},
			[]string{"\\c", "\\a"},
		},
		{
			"b and e keep their text",
			"See \\b {bold words} and \\e {emphasized words}.\n",
			[]string{"<strong>bold words</strong>", "<em>emphasized words</em>"},
			[]string{"\\b", "\\e", "{"},
		},
		{
			"a bare link keeps its target as text",
			"See \\l QWidget for details.\n",
			[]string{`<a href="#">QWidget</a>`},
			[]string{"\\l"},
		},
		{
			"a two-part link keeps its text, not its target",
			"See \\l {target-page.html} {the linked text} for details.\n",
			[]string{`<a href="#">the linked text</a>`},
			[]string{"target-page"},
		},
		{
			"lists become lists",
			"\\list\n    \\li First item text.\n    \\li Second item text.\n\\endlist\n",
			[]string{"<ul>", "<li>First item text.", "<li>Second item text.", "</ul>"},
			[]string{"\\li"},
		},
		{
			"tables have header and body cells",
			"\\table\n    \\header\n        \\li Name\n        \\li Meaning\n    \\row\n        \\li \\c Value\n        \\li Cell prose.\n\\endtable\n",
			[]string{"<table>", "<th>Name", "<th>Meaning", "<td><code>Value</code>", "<td>Cell prose."},
			[]string{"\\li"},
		},
		{
			"a value's constant is code, its description prose",
			"\\value AlignLeft Aligns with the left edge.\n",
			[]string{"<code>AlignLeft</code>", "Aligns with the left edge."},
			[]string{"\\value"},
		},
		{
			"an image's caption is prose, its file markup",
			"\\image diagram.png A caption sentence.\n",
			[]string{"<figcaption>A caption sentence.</figcaption>"},
			[]string{"diagram.png"},
		},
		{
			"sa lines are markup",
			"Prose here.\n\n\\sa QWidget, QObject\n",
			[]string{"<p>Prose here.</p>"},
			[]string{"QWidget"},
		},
		{
			"an unknown inline command is masked, its text kept",
			"The \\unknowncmd rest of the sentence.\n",
			[]string{"The  rest of the sentence."},
			[]string{"unknowncmd"},
		},
		{
			"a command may share the opening delimiter's line",
			"/*! \\page overview.html\n    \\title The Page Title\n*/\n",
			[]string{"<h1>The Page Title</h1>"},
			[]string{"/*!", "*/", "overview"},
		},
		{
			"prose may share the opening delimiter's line",
			"/*! One brief sentence. */\n",
			[]string{"One brief sentence."},
			[]string{"/*!", "*/"},
		},
		{
			"adjacent comment blocks are separate paragraphs",
			"/*!\n    First block.\n*/\n\n/*!\n    Second block.\n*/\n",
			[]string{"<p>    First block.</p>", "<p>    Second block.</p>"},
			nil,
		},
		{
			"build-system and type commands are markup",
			"\\qmlenum Mode\n\\qmlsingletontype Theme\n\\typealias Alias\n" +
				"\\nativetype QString\n\\cmakepackage Qt6\n\\cmakecomponent Widgets\n" +
				"\\cmaketargetitem Target\n\\qtcmakepackage Core\n" +
				"\\qtcmaketargetitem Item\n\\qtvariable widgets\n" +
				"\\tableofcontents auto\n\\dontdocument (Hidden)\n\nProse here.\n",
			[]string{"<p>Prose here.</p>"},
			[]string{"Mode", "Theme", "Alias", "QString", "Qt6", "Widgets",
				"Target", "Core", "Item", "widgets", "auto", "Hidden"},
		},
		{
			"a comparison block's command lines are markup",
			"\\compareswith equality QAnyStringView\n\\endcompareswith\n\nProse here.\n",
			[]string{"<p>Prose here.</p>"},
			[]string{"QAnyStringView"},
		},
		{
			"a conditional's expression is markup, its branches prose",
			"\\if defined(onlinedocs)\nOnline prose.\n\\else\nOffline prose.\n\\endif\n",
			[]string{"Online prose.", "Offline prose."},
			[]string{"onlinedocs"},
		},
		{
			"an escaped backslash is text, not a command",
			"Write \\\\c to show the command.\n",
			[]string{`Write \c to show the command.`},
			[]string{"<code>"},
		},
		{
			"a unicode code point is markup",
			"A bullet \\unicode 0x2022 sits here.\n",
			[]string{"A bullet", "sits here."},
			[]string{"0x2022"},
		},
		{
			"a span's attribute is markup, its text prose",
			"See \\span {class=\"vrheader\"} {the spanned words} here.\n",
			[]string{"<span>the spanned words</span>", "here."},
			[]string{"vrheader", "class="},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html := qdocToHTML(c.in)

			for _, want := range c.want {
				if !strings.Contains(html, want) {
					t.Errorf("missing %q in:\n%s", want, html)
				}
			}
			for _, absent := range c.absent {
				if strings.Contains(html, absent) {
					t.Errorf("unwanted %q in:\n%s", absent, html)
				}
			}
		})
	}
}
