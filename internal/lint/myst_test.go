package lint

import (
	"bytes"
	"strings"
	"testing"
)

// TestMystHTML pins what the MyST extension makes of each construct: prose
// directives become classed divs the walker lints (and scopes), literal
// directives become skipped <pre> blocks, and pure markup -- targets, roles,
// comments, options -- renders as nothing at all. See #667.
func TestMystHTML(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   []string
		absent []string
	}{
		{
			"target labels are markup",
			"(contributing-volto-label)=\n\nSome prose.\n",
			[]string{"<p>Some prose.</p>"},
			[]string{"volto", "label"},
		},
		{
			"a role keeps its span, loses its braces",
			"Read the {term}`server-side rendering` glossary entry.\n",
			[]string{"Read the <code>server-side rendering</code> glossary entry."},
			[]string{"{term}", "term}"},
		},
		{
			"substitutions are markup",
			"Version {{ version }} is out.\n",
			[]string{"Version  is out."},
			[]string{"version }}"},
		},
		{
			"inline attributes keep their text",
			"Some [styled text]{.customclass} here.\n",
			[]string{"styled text"},
			[]string{"customclass"},
		},
		{
			"comments and block breaks are markup",
			"Before.\n\n% a comment with a misteak\n\n+++ {\"meta\": 1}\n\nAfter.\n",
			[]string{"<p>Before.</p>", "<p>After.</p>"},
			[]string{"misteak", "meta"},
		},
		{
			"an attributes line is markup",
			"{.bg-primary}\n\nStyled paragraph.\n",
			[]string{"<p>Styled paragraph.</p>"},
			[]string{"bg-primary"},
		},
		{
			"a prose directive is a classed div",
			"```{note}\n:class: dropdown\n\nAdmonition prose here.\n```\n",
			[]string{`<div class="note">`, "<p>Admonition prose here.</p>", "</div>"},
			[]string{"dropdown", ":class:"},
		},
		{
			"a colon fence is the same directive",
			":::{warning}\nColon prose here.\n:::\n",
			[]string{`<div class="warning">`, "<p>Colon prose here.</p>"},
			[]string{":::"},
		},
		{
			"YAML options are markup",
			"```{figure} img.png\n---\nheight: 150px\nname: fig-target\n---\nThe caption is prose.\n```\n",
			[]string{`<div class="figure">`, "<p>The caption is prose.</p>"},
			[]string{"150px", "fig-target"},
		},
		{
			"a literal directive stays skipped",
			"```{code-block} python\nimport volto\n```\n",
			[]string{"<pre>", "import volto"},
			[]string{"<p>"},
		},
		{
			"a literal colon directive stays skipped",
			":::{math}\na^2 + b^2\n:::\n",
			[]string{"<pre>", "a^2 + b^2"},
			[]string{"<p>"},
		},
		{
			"directives nest",
			"`````{note}\nOuter prose.\n\n````{tab-set}\n```{tab-item} Label\nInner prose.\n```\n````\n`````\n",
			[]string{
				`<div class="note">`, `<div class="tab-set">`,
				`<div class="tab-item">`, "<p>Inner prose.</p>",
			},
			nil,
		},
		{
			"a plain code fence is untouched",
			"```python\nimport volto\n```\n",
			[]string{"<pre><code class=\"language-python\">import volto"},
			nil,
		},
		{
			"a bare colon fence is markup",
			":::\nDiv prose.\n:::\n",
			[]string{"<p>Div prose.</p>"},
			[]string{":::"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := goldMyst.Convert([]byte(c.in), &buf); err != nil {
				t.Fatal(err)
			}
			html := buf.String()

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
