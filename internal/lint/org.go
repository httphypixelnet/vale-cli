package lint

import (
	"regexp"
	"strings"

	"github.com/niklasfasching/go-org/org"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/nlp"
)

var orgConverter = org.New()

var orgExample = "\n#+BEGIN_EXAMPLE\n$1\n#+END_EXAMPLE\n"

var reOrgAttribute = regexp.MustCompile(`(#(?:\+| )[^\s]+:.+)`)
var reOrgProps = regexp.MustCompile(`(:PROPERTIES:\n.+\n:END:)`)
var reOrgSrc = regexp.MustCompile(`(?i)#\+BEGIN_SRC .+`)

type ExtendedHTMLWriter struct {
	*org.HTMLWriter
}

func (w *ExtendedHTMLWriter) WriteComment(n org.Comment) {
	w.HTMLWriter.WriteString("<!-- ")
	w.HTMLWriter.WriteString(n.Content)
	w.HTMLWriter.WriteString(" -->\n")
}

func (l *Linter) lintOrg(f *core.File) error {
	// A writer per file: `org.HTMLWriter` accumulates its output in an embedded
	// `strings.Builder` that nothing resets, so a shared one hands each file
	// every earlier file's HTML as well. See #1129.
	writer := org.NewHTMLWriter()
	writer.ExtendingWriter = &ExtendedHTMLWriter{writer}

	old := f.Content

	s := reOrgAttribute.ReplaceAllString(f.Content, "\n=$1=\n")
	s = reOrgProps.ReplaceAllString(s, orgExample)

	f.Content = s

	err := l.lintMetadata(f)
	if err != nil {
		return err
	}

	s, err = l.Transform(f)
	if err != nil {
		return err
	}
	f.Content = old

	// We don't want to find matches in `begin_src` lines.
	body := reOrgSrc.ReplaceAllStringFunc(f.Content, func(m string) string {
		return strings.Repeat("*", nlp.StrLen(m))
	})

	doc := orgConverter.Parse(strings.NewReader(s), f.Path)
	// We don't want to introduce any *new* content into our HTML,
	// so we clear the outline.
	doc.Outline.Children = nil

	html, err := doc.Write(writer)
	if err != nil {
		return err
	}

	f.Content = body
	return l.lintHTMLTokens(f, []byte(html), 0)
}
