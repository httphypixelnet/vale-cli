package lint

import (
	"bytes"
	"strings"
	"testing"
)

// TestMdxHTML pins what the MDX extension makes of each construct: ESM,
// expressions, and JSX -- children included -- are code the walker skips, a
// `{/* ... */}` flow comment is an HTML comment so comment-based
// configuration works, and everything else is ordinary Markdown. This
// reproduces what mdx2vast produced, without the external process.
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
			"an element's children are not prose",
			"<div className=\"note\">\n  > Some notable things!\n</div>\n\nProse here.\n",
			[]string{`<pre><code class="mdxNode mdxJsxFlowElement">`, "<p>Prose here.</p>"},
			[]string{"<blockquote>"},
		},
		{
			"multiline attributes",
			"<Component\n  open\n  x={1}\n  icon={<Icon />}\n/>\n\nProse here.\n",
			[]string{`<pre><code class="mdxNode mdxJsxFlowElement">`, "<p>Prose here.</p>"},
			[]string{"<p><Component"},
		},
		{
			"inline JSX is a code span",
			"An <External>XXX</External> component TODO.\n",
			[]string{`An <code class="mdxNode mdxJsxTextElement">&lt;External&gt;XXX&lt;/External&gt;</code> component TODO.`},
			nil,
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
			"a fragment",
			"<>\nInside a fragment.\n</>\n\nProse here.\n",
			[]string{`<pre><code class="mdxNode mdxJsxFlowElement">`, "<p>Prose here.</p>"},
			[]string{"<p>Inside"},
		},
		{
			"nested same-name elements",
			"<Box>\n  <Box>inner</Box>\n</Box>\n\nProse here.\n",
			[]string{`<pre><code class="mdxNode mdxJsxFlowElement">`, "<p>Prose here.</p>"},
			[]string{"<p>inner"},
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
