package lint

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/system"
)

// reStructuredText configuration.
//
// reCodeBlock is used to convert Sphinx-style code directives to the regular
// `::` for rst2html, including the use of runtime options (e.g., :caption:).
var reCodeBlock = regexp.MustCompile(`\.\. (?:raw|code(?:-block)?):: *(?:[\w-]+)?(?:\s+:\w+:.*)*`)

// We replace custom directives with `.. code::`.
//
// See https://github.com/errata-ai/vale/v2/issues/119.
var reSphinx = regexp.MustCompile(`.. (?:glossary|contents)::`)
var rstArgs = []string{
	"--quiet",
	"--halt=5",
	"--report=5",
	"--link-stylesheet",
	"--no-file-insertion",
	"--no-toc-backlinks",
	"--no-footnote-backlinks",
	"--no-section-numbering",
}

// Converting a document with Docutils takes a few milliseconds; starting
// Python and importing Docutils takes seventy. Vale paid the latter once per
// file, so on a corpus of reStructuredText nearly all of the time went to
// starting the same interpreter over and over.
//
// rstServer keeps one interpreter up and converts documents as they arrive. It
// parses the flags through Docutils' own command-line handling rather than
// mapping them to settings by hand, so the pooled settings cannot drift from
// what a per-file rst2html invocation would use.
const rstServer = `import sys
from docutils.core import Publisher, publish_string

_pub = Publisher()
_pub.set_components("standalone", "restructuredtext", "html4css1")
_pub.process_command_line(argv=sys.argv[1:])
SETTINGS = _pub.settings

buf = sys.stdin.buffer
out = sys.stdout.buffer

while True:
    header = buf.readline()
    if not header:
        break
    n = int(header)
    if n < 0:
        break
    doc = buf.read(n) if n else b""
    try:
        b = publish_string(
            source=doc.decode("utf-8"),
            source_path="<stdin>",
            writer_name="html4css1",
            settings=SETTINGS,
        )
        out.write(b"ok %d\n" % len(b))
        out.write(b)
    except Exception as e:
        m = str(e).encode("utf-8", "replace")
        out.write(b"err %d\n" % len(m))
        out.write(m)
    out.flush()`

var (
	rstOnce   sync.Once
	rstDirect []string // <python> -c <server>, or nil
)

// rstInterpreter finds the Python that can import Docutils.
//
// It is not necessarily the `python3` on PATH: rst2html is installed with a
// shebang naming the interpreter of the environment Docutils was installed
// into, and on a machine with several Pythons the one on PATH often cannot
// import it. So the script is asked which interpreter it runs on.
func rstInterpreter(exe string) string {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}

	file, err := os.Open(resolved)
	if err != nil {
		return ""
	}
	defer file.Close()

	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "#!") {
		return ""
	}

	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "#!"))
	if len(fields) == 0 {
		return ""
	}

	// `#!/usr/bin/env python3` names the interpreter in the second field.
	if filepath.Base(fields[0]) == "env" {
		if len(fields) < 2 {
			return ""
		}
		return system.Which([]string{fields[1]})
	}

	return fields[0]
}

// rstFastPath returns the argv prefix for converting through a long-lived
// interpreter, or nil when that could not be established.
func rstFastPath(exe string) []string {
	rstOnce.Do(func() {
		python := rstInterpreter(exe)
		if python == "" {
			return
		}

		candidate := []string{python, "-c", rstServer}

		// Trust it only after a document has made the round trip. The probe
		// carries a non-ASCII character on purpose: a mismatched default
		// encoding is what broke the first version of the AsciiDoc pool, and
		// an ASCII-only probe would have passed anyway.
		probe, err := startExtProc(candidate, rstArgs)
		if err != nil {
			return
		}
		defer probe.close()

		got, err := probe.convert("naïve body\n")
		if err != nil || !strings.Contains(got, "naïve body") {
			return
		}

		rstDirect = candidate
	})

	return rstDirect
}

func (l *Linter) lintRST(f *core.File) error {
	var html string

	rst2html := system.Which([]string{
		"rst2html", "rst2html.py", "rst2html-3", "rst2html-3.py"})

	if rst2html == "" {
		return core.NewE100("lintRST", errors.New("rst2html not found"))
	}

	err := l.lintMetadata(f)
	if err != nil {
		return err
	}

	s, err := l.Transform(f)
	if err != nil {
		return err
	}

	s = reSphinx.ReplaceAllString(s, ".. code::")
	s = reCodeBlock.ReplaceAllString(s, "::")

	html, err = l.callRst(s, rst2html)
	if err != nil {
		return core.NewE100(f.Path, err)
	}

	return l.lintHTMLTokens(f, []byte(html), 0)
}

// callRst converts one document, over a pooled interpreter when Docutils can
// be reached directly.
func (l *Linter) callRst(text, exe string) (string, error) {
	if direct := rstFastPath(exe); direct != nil {
		l.rstOnce.Do(func() {
			pool, err := newProcPool(direct, rstArgs, adocConcurrency)
			if err == nil {
				l.rst = pool
			}
		})

		if l.rst != nil {
			html, err := l.rst.convert(text, direct, rstArgs)
			if err != nil {
				return "", err
			}
			return rstBody(html), nil
		}
	}

	html, err := system.ExecuteWithInput(exe, text, rstArgs...)
	if err != nil {
		return "", err
	}

	return rstBody(html), nil
}

// rstBody takes the document body out of a full rst2html page.
func rstBody(html string) string {
	html = strings.ReplaceAll(html, "\r", "")

	bodyStart := strings.Index(html, "<body>\n")
	if bodyStart < 0 {
		bodyStart = -7
	}
	bodyEnd := strings.Index(html, "\n</body>")
	if bodyEnd < 0 || bodyEnd >= len(html) {
		bodyEnd = len(html) - 1
		if bodyEnd < 0 {
			bodyEnd = 0
		}
	}

	return html[bodyStart+7 : bodyEnd]
}
