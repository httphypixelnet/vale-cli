package lint

import (
	"bytes"
	"strings"
	"testing"
)

// TestMdxHTML pins what the MDX extension makes of each construct: ESM,
// expressions, tags, and self-closing elements are code the walker skips,
// while an element's children stay Markdown -- a flow element is a div and
// an inline element a span, each classed with the element's name. A
// `{/* ... */}` flow comment is an HTML comment so comment-based
// configuration works, and everything else is ordinary Markdown.
func TestMdxHTML(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   []string
		absent []string
	}{
		{
			"contiguous ESM is one skipped block",
			"import {A} from './a.js'\nimport b from './b.js'\n\nProse here.\n",
			[]string{`<pre><code class="mdxNode mdxjsEsm">`, "<p>Prose here.</p>"},
			[]string{"<p>import"},
		},
		{
			"a multiline export ends when its braces do",
			"export function A() {\n  return 1\n}\n\nProse here.\n",
			[]string{`<pre><code class="mdxNode mdxjsEsm">`, "<p>Prose here.</p>"},
			[]string{"<p>export"},
		},
		{
			"an arrow export with JSX and strings",
			"export const L = p => <span style={{color: 'red'}} {...p} />\n\nProse here.\n",
			[]string{`<code class="mdxNode mdxjsEsm">`, "<p>Prose here.</p>"},
			[]string{"<p>export"},
		},
		{
			"a flow comment is a comment",
			"{/* vale off */}\n\nProse here.\n",
			[]string{"<!-- vale off -->", "<p>Prose here.</p>"},
			[]string{"mdxNode"},
		},
		{
			"a flow expression is skipped",
			"{(function () {\n  return 'a { in a string'\n})()}\n\nProse here.\n",
			[]string{`<pre><code class="mdxNode mdxFlowExpression">`, "<p>Prose here.</p>"},
			[]string{"<p>{("},
		},
		{
			"a self-closing element with expression attributes",
			"<Chart data={population} label={'a > b'} />\n\nProse here.\n",
			[]string{`<code class="mdxNode mdxJsxFlowElement">`, "<p>Prose here.</p>"},
			[]string{"<p><Chart", "<chart"},
		},
		{
			"an element's children are prose in a classed div",
			"<div className=\"note\">\n  > Some notable things!\n</div>\n\nProse here.\n",
			[]string{`<div class="div">`, "<blockquote>", "</div>", "<p>Prose here.</p>"},
			[]string{"mdxNode", "note"},
		},
		{
			"multiline attributes",
			"<Component\n  open\n  x={1}\n  icon={<Icon />}\n/>\n\nProse here.\n",
			[]string{`<pre><code class="mdxNode mdxJsxFlowElement">`, "<p>Prose here.</p>"},
			[]string{"<p><Component"},
		},
		{
			"inline JSX children stay prose",
			"An <External>XXX</External> component TODO.\n",
			[]string{`An <span class="External">XXX</span> component TODO.`},
			[]string{"mdxNode"},
		},
		{
			"a member-expression component",
			"Use the <myComponents.thisOne /> component.\n",
			[]string{`<code class="mdxNode mdxJsxTextElement">`},
			[]string{"<mycomponents.thisone"},
		},
		{
			"an inline expression is a code span",
			"Two is: {Math.PI * 2}, TODO\n",
			[]string{`Two is: <code class="mdxNode mdxTextExpression">{Math.PI * 2}</code>, TODO`},
			nil,
		},
		{
			"a heading attribute is a code span",
			"## Some Markdown {#initial-setup}\n",
			[]string{"Some Markdown", `<code class="mdxNode mdxTextExpression">{#initial-setup}</code>`},
			nil,
		},
		{
			"autolinks are still links",
			"See <https://example.com/config> for details.\n",
			[]string{`<a href="https://example.com/config">`},
			[]string{"mdxNode"},
		},
		{
			"indented lines are a paragraph, not code",
			"Intro.\n\n    XXX = False\n    more here\n",
			[]string{"<p>XXX = False\nmore here</p>"},
			[]string{"<pre><code>XXX"},
		},
		{
			"a fragment's children are prose",
			"<>\nInside a fragment.\n</>\n\nProse here.\n",
			[]string{"<div>\n<p>Inside a fragment.</p>\n</div>", "<p>Prose here.</p>"},
			[]string{"mdxNode"},
		},
		{
			"a nested same-name element on one line stays code",
			"<Box>\n  <Box>inner</Box>\n</Box>\n\nProse here.\n",
			[]string{`<div class="Box">`, `<code class="mdxNode mdxJsxFlowElement">`, "<p>Prose here.</p>"},
			[]string{"<p>inner"},
		},
		{
			"nested same-name elements close in order",
			"<Box>\n\nOuter start.\n\n<Box>\n\nInner text.\n\n</Box>\n\nOuter end.\n\n</Box>\n\nProse here.\n",
			[]string{"<p>Outer start.</p>", "<p>Inner text.</p>", "<p>Outer end.</p>", "<p>Prose here.</p>"},
			[]string{"mdxNode"},
		},
		{
			"a container's attributes are not prose",
			"<AstroAside type=\"info\" x={a > b}>\n  Renders inside a colored box.\n</AstroAside>\n\nProse here.\n",
			[]string{`<div class="AstroAside">`, "<p>Renders inside a colored box.</p>", "<p>Prose here.</p>"},
			[]string{"info", "a &gt; b"},
		},
		{
			"markdown children keep their structure",
			"<Steps>\n\n1. First point here.\n\n2. Second point here.\n\n</Steps>\n\nProse here.\n",
			[]string{`<div class="Steps">`, "<ol>", "<li>", "First point here.", "<p>Prose here.</p>"},
			[]string{"mdxNode"},
		},
		{
			"a JSX tag quoted in a fence doesn't close the element",
			"<Steps>\n\n```\n</Steps>\n```\n\nStill inside.\n\n</Steps>\n\nProse here.\n",
			[]string{"<p>Still inside.</p>", "<p>Prose here.</p>"},
			[]string{`<p>&lt;/Steps&gt;`},
		},
		{
			"a member-expression container's dots become hyphens",
			"<my.Component>\n\nText here.\n\n</my.Component>\n\nProse here.\n",
			[]string{`<div class="my-Component">`, "<p>Text here.</p>", "<p>Prose here.</p>"},
			[]string{"my.Component<"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := goldMdx.Convert([]byte(c.in), &buf); err != nil {
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
