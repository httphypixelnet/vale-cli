package lint

import (
	"strings"
	"testing"

	"github.com/errata-ai/vale/v3/internal/core"
)

func Test_applyPatterns(t *testing.T) {
	cases := []struct {
		description string
		conf        core.Config
		exts        extensionConfig
		content     string
		expected    string
	}{
		{
			description: "MDX comment in markdown, custom comment delimiter",
			conf: core.Config{
				CommentDelimiters: map[string][2]string{
					".md": {"{/*", "*/}"},
				},
			},
			exts: extensionConfig{Normed: ".md", Real: ".md"},
			content: `
This is the intro pagragraph.

{/* This is a comment */}
`,
			expected: strings.ReplaceAll(`
This is the intro pagragraph.

<!-- This is a comment -->
`, "@", "`"),
		},
		{
			description: "MDX comment in markdown, no custom comment delimiter",
			conf:        core.Config{},
			exts:        extensionConfig{Normed: ".md", Real: ".md"},
			content: `
This is the intro pagragraph.

{/* This is a comment */}
`,
			expected: strings.ReplaceAll(`
This is the intro pagragraph.

{/* This is a comment */}
`, "@", "`"),
		},
		{
			description: "multiline MDX comment in markdown, custom comment delimiter",
			conf: core.Config{
				CommentDelimiters: map[string][2]string{
					".md": {"{/*", "*/}"},
				},
			},
			exts: extensionConfig{Normed: ".md", Real: ".md"},
			content: `
This is the intro pagragraph.

{/*
This is a comment
*/}
`,
			expected: strings.ReplaceAll(`
This is the intro pagragraph.

<!--
This is a comment
-->
`, "@", "`"),
		},
		{
			description: "token ignore in cc file",
			content:     "Call \\c func to start the process.",
			conf: core.Config{
				TokenIgnores: map[string][]string{
					"*.cc": {`(\\c \w+)`},
				},
				Formats: map[string]string{
					"cc": "md",
				},
			},
			exts:     extensionConfig{Normed: ".md", Real: ".cc"},
			expected: "Call `\\c func` to start the process.",
		},
		{
			description: "token ignore in a path-scoped section",
			content:     "A test $g_i = p_i$ here.",
			conf: core.Config{
				TokenIgnores: map[string][]string{
					"tutorials/*.md": {`(\$+[^\n$]+\$+)`},
				},
			},
			exts: extensionConfig{
				Normed:   ".md",
				Real:     ".md",
				RealPath: "tutorials/intro.md",
			},
			expected: "A test `$g_i = p_i$` here.",
		},
		{
			description: "path-scoped section keyed on the mapped extension",
			content:     "A test $g_i = p_i$ here.",
			conf: core.Config{
				TokenIgnores: map[string][]string{
					"tutorials/*.md": {`(\$+[^\n$]+\$+)`},
				},
				Formats: map[string]string{
					"qmd": "md",
				},
			},
			exts: extensionConfig{
				Normed:   ".md",
				Real:     ".qmd",
				RealPath: "tutorials/intro.qmd",
			},
			expected: "A test $g_i = p_i$ here.",
		},
		{
			description: "token ignore in a path-scoped section that doesn't match",
			content:     "A test $g_i = p_i$ here.",
			conf: core.Config{
				TokenIgnores: map[string][]string{
					"tutorials/*.md": {`(\$+[^\n$]+\$+)`},
				},
			},
			exts: extensionConfig{
				Normed:   ".md",
				Real:     ".md",
				RealPath: "guides/intro.md",
			},
			expected: "A test $g_i = p_i$ here.",
		},
		{
			description: "block ignore in a path-scoped section",
			content:     "Intro.\n\nBEGIN\nskipped\nEND\n",
			conf: core.Config{
				BlockIgnores: map[string][]string{
					"docs/**/*.md": {`(?s)(BEGIN.*?END)`},
				},
			},
			exts: extensionConfig{
				Normed:   ".md",
				Real:     ".md",
				RealPath: "docs/src/a.md",
			},
			expected: "Intro.\n\n\n```\nBEGIN\nskipped\nEND\n```\n\n",
		},
	}

	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {
			s, err := applyPatterns(&c.conf, c.exts, c.content)
			if err != nil {
				t.Fatalf("applyPatterns returned an error: %s", err)
			} else if s != c.expected {
				t.Fatalf("Expected '%s', but got '%s'", c.expected, s)
			}
		})
	}
}

func Test_applyPatterns_errors(t *testing.T) {
	cases := []struct {
		description string
		conf        core.Config
		exts        extensionConfig
		content     string
		expectedErr string
	}{
		{
			description: "only one delimiter",
			conf: core.Config{
				CommentDelimiters: map[string][2]string{
					".md": {"{/*", ""},
				},
			},
			exts: extensionConfig{Normed: ".md", Real: ".md"},
			content: `
This is the intro pagragraph.

{/* This is a comment */}
`,
			expectedErr: "",
		},
	}
	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {
			_, err := applyPatterns(&c.conf, c.exts, c.content)
			if !strings.Contains(err.Error(), c.expectedErr) {
				t.Fatalf("Expected '%s', but got '%s'", c.expectedErr, err.Error())
			}
		})
	}
}

// TODO: Test for expected errors resulting from applyPatterns
