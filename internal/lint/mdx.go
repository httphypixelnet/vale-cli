package lint

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/system"
)

// mdxErrorRe matches the `line:col: reason` payload that mdx2vast's underlying
// parser reports (e.g. `[3:12: Unexpected character ...]`) so we can surface a
// concise message instead of mdx2vast's raw Node stack trace. See #995.
var mdxErrorRe = regexp.MustCompile(`\[(\d+:\d+: [^\]]+)\]`)

// cleanMDXError extracts a readable parse error from mdx2vast's stderr, which
// otherwise arrives as an unhandled-rejection dump. It falls back to the first
// meaningful line, then to the raw output.
func cleanMDXError(stderr string) string {
	if m := mdxErrorRe.FindStringSubmatch(stderr); m != nil {
		return m[1]
	}
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "at ") && !strings.HasPrefix(line, "^") {
			return line
		}
	}
	return strings.TrimSpace(stderr)
}

// mdx2vast converts one document per process unless asked otherwise, and
// starting it costs about 160ms against roughly 4ms to convert -- nearly all of
// it importing the MDX toolchain. `--batch` keeps the process up and takes
// documents over the same framing the Asciidoctor pool uses.
//
// Support is probed rather than inferred from `--version`: the flag arrived in
// 0.5.0, older installs are common, and a version string is a worse test than
// asking the binary to do the thing. Anything unexpected leaves mdxDirect nil
// and each file gets its own process as before.
var (
	mdxOnce   sync.Once
	mdxDirect []string
)

func mdxFastPath() []string {
	mdxOnce.Do(func() {
		exe := system.Which([]string{"mdx2vast"})
		if exe == "" {
			return
		}

		candidate := []string{exe, "--batch"}

		probe, err := startExtProc(candidate, nil)
		if err != nil {
			return
		}
		defer probe.close()

		// Non-ASCII on purpose: encoding is what broke the equivalent path for
		// AsciiDoc, and an ASCII-only probe would not have caught it.
		got, err := probe.convert("# na\u00efve heading\n")
		if err != nil || !strings.Contains(got, "na\u00efve heading") {
			return
		}

		mdxDirect = candidate
	})

	return mdxDirect
}

func (l *Linter) lintMDX(f *core.File) error {
	var html string
	var err error

	exe := system.Which([]string{"mdx2vast"})
	if exe == "" {
		return core.NewE100("lintMDX", errors.New("mdx2vast not found"))
	}

	err = l.lintMetadata(f)
	if err != nil {
		return err
	}

	s, err := l.Transform(f)
	if err != nil {
		return err
	}

	html, err = l.callMDX(s, exe)
	if err != nil {
		return core.NewE100(f.Path,
			fmt.Errorf("failed to parse MDX: %s", cleanMDXError(err.Error())))
	}

	f.Content = prepMarkdown(f.Content)
	return l.lintHTMLTokens(f, []byte(html), 0)
}

// callMDX converts one document, over a pooled process when the installed
// mdx2vast supports it.
func (l *Linter) callMDX(text, exe string) (string, error) {
	if direct := mdxFastPath(); direct != nil {
		l.mdxOnce.Do(func() {
			pool, err := newProcPool(direct, nil, adocConcurrency)
			if err == nil {
				l.mdx = pool
			}
		})

		if l.mdx != nil {
			return l.mdx.convert(text, direct, nil)
		}
	}

	return system.ExecuteWithInput(exe, text)
}
