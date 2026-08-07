package lint

import (
	"bytes"
	"strings"
	"testing"
)

// TestQuartoHTML pins what the Quarto extension makes of each construct:
// fenced divs become classed divs whose content is linted, and attributes
// and shortcodes render as nothing at all. The code cells are Markdown
// already -- fenced code and code spans -- and stay skipped. See #793.
func TestQuartoHTML(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   []string
		absent []string
	}{
		{
			"a callout is a classed div",
			"::: {.callout-note}\nCallout prose here.\n:::\n",
			[]string{`<div class="callout-note">`, "<p>Callout prose here.</p>", "</div>"},
			[]string{":::"},
		},
		{
			"attributes beyond classes are dropped",
			"::: {.callout-tip title=\"A titled tip\" #tip-1}\nTip prose.\n:::\n",
			[]string{`<div class="callout-tip">`, "<p>Tip prose.</p>"},
			[]string{"titled", "tip-1"},
		},
		{
			"a bare word is a class",
			"::: warning\nBare-word prose.\n:::\n",
			[]string{`<div class="warning">`, "<p>Bare-word prose.</p>"},
			nil,
		},
		{
			"divs nest by fence length",
			":::: {.columns}\n::: {.column width=\"40%\"}\nLeft prose.\n:::\n::: {.column width=\"60%\"}\nRight prose.\n:::\n::::\n",
			[]string{
				`<div class="columns">`, `<div class="column">`,
				"<p>Left prose.</p>", "<p>Right prose.</p>",
			},
			[]string{"40%", ":::"},
		},
		{
			"a bare fence closes the innermost div",
			"::: {.a}\nOuter prose.\n\n::: {.b}\nInner prose.\n:::\n\nStill outer.\n:::\n\nOutside.\n",
			[]string{
				`<div class="a">`, `<div class="b">`,
				"<p>Inner prose.</p>", "<p>Still outer.</p>", "<p>Outside.</p>",
			},
			[]string{":::"},
		},
		{
			"a stray closer is markup",
			"Before.\n\n:::\n\nAfter.\n",
			[]string{"<p>Before.</p>", "<p>After.</p>"},
			[]string{":::"},
		},
		{
			"a code cell stays skipped, options and all",
			"```{r}\n#| label: fig-plot\n#| fig-cap: \"A caption\"\nplot(cars)\n```\n",
			[]string{"<pre><code"},
			[]string{"<p>"},
		},
		{
			"heading attributes are markup",
			"## Overview {#sec-overview}\n\nProse here.\n",
			[]string{"Overview", "<p>Prose here.</p>"},
			[]string{"sec-overview"},
		},
		{
			"inline attributes keep their text",
			"Prose with [styled text]{.underline} in it.\n",
			[]string{"styled text"},
			[]string{"underline"},
		},
		{
			"shortcodes are markup",
			"{{< video https://example.com/v.mp4 >}}\n\nPress {{< kbd F5 >}} to run.\n",
			[]string{"Press", "to run."},
			[]string{"video", "kbd"},
		},
		{
			"an inline code cell is a code span",
			"The mean is `{python} np.mean(x)` overall.\n",
			[]string{"<code>{python} np.mean(x)</code>"},
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := goldQmd.Convert([]byte(c.in), &buf); err != nil {
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
