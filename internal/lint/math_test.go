package lint

import (
	"bytes"
	"testing"
)

// TestMathInline pins Pandoc's delimiter rules for `$…$`: the opening `$` must
// be followed by a non-space character, and the closing `$` must be preceded
// by one and not followed by a digit.
//
// The rules exist to keep money out of math. Every case that renders as `code`
// is text vale will not check; every case that stays plain is text it will.
func TestMathInline(t *testing.T) {
	cases := []struct {
		description string
		content     string
		expected    string
	}{
		{
			description: "math",
			content:     "A test $g_i$ here.",
			expected:    "<p>A test <code>$g_i$</code> here.</p>\n",
		},
		{
			description: "math with spaces inside",
			content:     "A test $g_i = g(p)_i = p_i$ here.",
			expected:    "<p>A test <code>$g_i = g(p)_i = p_i$</code> here.</p>\n",
		},
		{
			description: "currency",
			content:     "It costs $5 and $10 today.",
			expected:    "<p>It costs $5 and $10 today.</p>\n",
		},
		{
			description: "currency glued to a word",
			content:     "Between US$5 and CA$10 there.",
			expected:    "<p>Between US$5 and CA$10 there.</p>\n",
		},
		{
			description: "unclosed",
			content:     "A lone $5 in a sentence.",
			expected:    "<p>A lone $5 in a sentence.</p>\n",
		},
		{
			description: "space after the opening delimiter",
			content:     "A test $ g_i$ here.",
			expected:    "<p>A test $ g_i$ here.</p>\n",
		},
		{
			description: "wrapped across lines",
			content:     "A test $g_i =\ng(p)_i$ here.",
			expected:    "<p>A test <code>$g_i =\ng(p)_i$</code> here.</p>\n",
		},
		{
			description: "escaped delimiter",
			content:     "It costs \\$5 and \\$10 today.",
			expected:    "<p>It costs $5 and $10 today.</p>\n",
		},
		{
			description: "angle bracket inside math",
			content:     "The bound $a < b$ holds.",
			expected:    "<p>The bound <code>$a &lt; b$</code> holds.</p>\n",
		},
		{
			description: "display math is a block",
			content:     "Intro.\n\n$$\ng_i = g(p)_i\n$$\n",
			expected:    "<p>Intro.</p>\n<pre>g_i = g(p)_i\n</pre>\n",
		},
		{
			description: "code span is untouched",
			content:     "A test `$g_i$` here.",
			expected:    "<p>A test <code>$g_i$</code> here.</p>\n",
		},
	}

	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {
			var buf bytes.Buffer
			if err := goldQmd.Convert([]byte(c.content), &buf); err != nil {
				t.Fatalf("Convert returned an error: %s", err)
			}
			if got := buf.String(); got != c.expected {
				t.Fatalf("Expected %q, but got %q", c.expected, got)
			}
		})
	}
}

// TestMathDisplay pins the closing rules for `$$…$$` display math: any line
// ending with an unescaped `$$` -- optionally trailed by a `{…}` attribute --
// closes the block, and a blank line bounds an unclosed one. Every form here
// is a single DisplayMath node to Pandoc, so the prose around it must render
// as `p` for vale to lint (#1148).
func TestMathDisplay(t *testing.T) {
	cases := []struct {
		description string
		content     string
		expected    string
	}{
		{
			description: "closer alone on its line",
			content:     "Intro.\n\n$$\nx = 1\n$$\n\nAfter.",
			expected:    "<p>Intro.</p>\n<pre>x = 1\n</pre>\n<p>After.</p>\n",
		},
		{
			description: "opening and closing on one line",
			content:     "Intro.\n\n$$x = 1$$\n\nAfter.",
			expected:    "<p>Intro.</p>\n<pre>x = 1</pre>\n<p>After.</p>\n",
		},
		{
			description: "closer ending a content line",
			content:     "Intro.\n\n$$\\begin{aligned}\nx &= 1\n\\end{aligned}$$\n\nAfter.",
			expected:    "<p>Intro.</p>\n<pre>\\begin{aligned}\nx &= 1\n\\end{aligned}</pre>\n<p>After.</p>\n",
		},
		{
			description: "label after the closer",
			content:     "Intro.\n\n$$\nx = 1\n$$ {#eq-foo}\n\nAfter.",
			expected:    "<p>Intro.</p>\n<pre>x = 1\n</pre>\n<p>After.</p>\n",
		},
		{
			description: "label after a one-line block",
			content:     "Intro.\n\n$$x = 1$$ {#eq-foo}\n\nAfter.",
			expected:    "<p>Intro.</p>\n<pre>x = 1</pre>\n<p>After.</p>\n",
		},
		{
			description: "content on the opening line",
			content:     "Intro.\n\n$$ x =\n1\n$$\n\nAfter.",
			expected:    "<p>Intro.</p>\n<pre> x =\n1\n</pre>\n<p>After.</p>\n",
		},
		{
			description: "unclosed block ends at a blank line",
			content:     "Intro.\n\n$$\nx = 1\n\nAfter.",
			expected:    "<p>Intro.</p>\n<pre>x = 1\n</pre>\n<p>After.</p>\n",
		},
		{
			description: "escaped dollars do not close",
			content:     "Intro.\n\n$$\nx = \\$$\n$$\n\nAfter.",
			expected:    "<p>Intro.</p>\n<pre>x = \\$$\n</pre>\n<p>After.</p>\n",
		},
		{
			// goldmark can open a block on a line before closing the one that
			// ended there, so per-block state can't live in a context key: two
			// adjacent blocks crashed goldmark-mathjax's parser outright.
			description: "back-to-back blocks",
			content:     "$$\nx = 1\n$$\n$$\ny = 2\n$$\n\nAfter.",
			expected:    "<pre>x = 1\n</pre>\n<pre>y = 2\n</pre>\n<p>After.</p>\n",
		},
		{
			description: "one-line block followed by another block",
			content:     "$$x = 1$$\n$$\ny = 2\n$$\n\nAfter.",
			expected:    "<pre>x = 1</pre>\n<pre>y = 2\n</pre>\n<p>After.</p>\n",
		},
	}

	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {
			var buf bytes.Buffer
			if err := goldQmd.Convert([]byte(c.content), &buf); err != nil {
				t.Fatalf("Convert returned an error: %s", err)
			}
			if got := buf.String(); got != c.expected {
				t.Fatalf("Expected %q, but got %q", c.expected, got)
			}
		})
	}
}
