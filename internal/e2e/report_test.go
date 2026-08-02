package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Colors are used only when the output is a terminal that asked for them.
var (
	dim, bold, red, green, cyan, reset string
)

func init() {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return
	}
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	dim, bold, reset = "\x1b[2m", "\x1b[1m", "\x1b[0m"
	red, green, cyan = "\x1b[31m", "\x1b[32m", "\x1b[36m"
}

// Progress: a mark per case as it finishes, then a summary -- the shape of
// `cucumber --format progress`, which this suite replaced.
//
// NOTE: `go test` discards a passing package's output, so the marks surface
// when a case fails or under `-v`. On a clean run you get Go's own `ok` line.
type tally struct {
	sync.Mutex
	declared                     int // scenarios in the files, before -run filters
	passed, failed, skipped, col int
	checked, broke               int // individual assertions, and those that failed
	suites                       map[string]bool
	tools                        map[string]bool // converters found to be missing
}

// count records the assertions a scenario made.
func count(checked, failed int) {
	progress.Lock()
	defer progress.Unlock()

	progress.checked += checked
	progress.broke += failed
}

var progress tally

// announce states what's about to run, so the marks below it have a scale.
func announce(suites []*suite) {
	progress.Lock()
	defer progress.Unlock()

	for _, s := range suites {
		progress.declared += len(s.Cases)
	}

	fmt.Printf("\n%s%d scenarios in %d suites%s\n\n",
		bold, progress.declared, len(suites), reset)
}

// mark draws one character of the progress line. Call with the lock held.
func (t *tally) mark(s string) {
	if testing.Verbose() {
		return // `go test -v` already narrates every case
	}

	fmt.Print(s)

	if t.col++; t.col == 60 {
		t.col = 0
		fmt.Println()
	}
}

// seen records that a suite contributed a case. Call with the lock held.
func (t *tally) seen(suite string) {
	if t.suites == nil {
		t.suites = map[string]bool{}
	}
	t.suites[suite] = true
}

// tick records a finished case.
func tick(suite string, ok bool) {
	progress.Lock()
	defer progress.Unlock()

	progress.seen(suite)

	if ok {
		progress.passed++
		progress.mark(green + "." + reset)
	} else {
		progress.failed++
		progress.mark(bold + red + "F" + reset)
	}
}

// skip records a case that can't run here for want of a converter.
func skip(suite string, absent []string) {
	progress.Lock()
	defer progress.Unlock()

	progress.seen(suite)
	progress.skipped++

	if progress.tools == nil {
		progress.tools = map[string]bool{}
	}
	for _, name := range absent {
		progress.tools[name] = true
	}

	progress.mark(dim + "s" + reset)
}

// summarize closes out the progress display.
func summarize(d time.Duration) {
	progress.Lock()
	defer progress.Unlock()

	total := progress.passed + progress.failed + progress.skipped
	if total == 0 {
		return
	}

	counts := fmt.Sprintf("%s%d passed%s", green, progress.passed, reset)
	if progress.failed > 0 {
		counts += fmt.Sprintf(", %s%s%d failed%s", bold, red, progress.failed, reset)
	}
	if progress.skipped > 0 {
		counts += fmt.Sprintf(", %s%d skipped%s", dim, progress.skipped, reset)
	}

	lead := "\n\n"
	if testing.Verbose() {
		lead = "\n"
	}

	// `total` is what actually ran; say so plainly when `-run` filtered the
	// suite down, so the count can't be mistaken for the whole of it.
	ran := fmt.Sprintf("%d", total)
	if total < progress.declared {
		ran = fmt.Sprintf("%d of %d", total, progress.declared)
	}

	fmt.Printf("%s%s scenarios — %s %s(%.1fs)%s\n",
		lead, ran, counts, dim, d.Seconds(), reset)

	if progress.checked > 0 {
		asserts := fmt.Sprintf("%s%d passed%s", green, progress.checked-progress.broke, reset)
		if progress.broke > 0 {
			asserts += fmt.Sprintf(", %s%s%d failed%s", bold, red, progress.broke, reset)
		}
		fmt.Printf("%d assertions — %s\n", progress.checked, asserts)
	}

	if len(progress.tools) > 0 {
		missing := make([]string, 0, len(progress.tools))
		for name := range progress.tools {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		fmt.Printf("%snot installed: %s — see .github/CONTRIBUTING.md%s\n",
			dim, strings.Join(missing, ", "), reset)
	}

	fmt.Println()
}

// report prints a failure: what was asked of Vale, where, and how the result
// differed.
//
//	✗ lint/css — output does not match
//
//	  run   vale test.css
//	  in    testdata/fixtures/formats
//	  from  testdata/e2e/lint.yaml
//
//	    test.css:1:4:vale.Annotations:'TODO' left in text
//	  - test.css:7:19:vale.Annotations:'XXX' left in text
//	  + test.css:7:19:vale.Annotations:'FIXME' left in text
func report(t *testing.T, s *suite, tc testCase, reason, body string) {
	t.Helper()

	var b strings.Builder

	fmt.Fprintf(&b, "\n%s%s✗ %s/%s%s %s— %s%s\n\n",
		bold, red, s.Name, tc.Name, reset, dim, reason, reset)

	if tc.About != "" {
		fmt.Fprintf(&b, "  %sabout%s %s\n", dim, reset, tc.About)
	}

	fmt.Fprintf(&b, "  %srun%s   vale %s\n", dim, reset, strings.Join(tc.Args, " "))
	fmt.Fprintf(&b, "  %sin%s    %s\n", dim, reset, rel(s.workdir(tc)))
	if tc.Stdin != "" {
		fmt.Fprintf(&b, "  %sstdin%s %s\n", dim, reset, tc.Stdin)
	}
	fmt.Fprintf(&b, "  %sfrom%s  %s\n\n", dim, reset, rel(s.Path))

	b.WriteString(body)
	t.Error(b.String())
}

// diffLines renders a line diff of want against got.
func diffLines(want, got string) string {
	w, g := lines(want), lines(got)
	common := lcs(w, g)

	var b strings.Builder
	var i, j int

	for _, c := range common {
		for i < len(w) && w[i] != c {
			fmt.Fprintf(&b, "  %s- %s%s\n", red, w[i], reset)
			i++
		}
		for j < len(g) && g[j] != c {
			fmt.Fprintf(&b, "  %s+ %s%s\n", green, g[j], reset)
			j++
		}
		fmt.Fprintf(&b, "  %s  %s%s\n", dim, c, reset)
		i, j = i+1, j+1
	}
	for ; i < len(w); i++ {
		fmt.Fprintf(&b, "  %s- %s%s\n", red, w[i], reset)
	}
	for ; j < len(g); j++ {
		fmt.Fprintf(&b, "  %s+ %s%s\n", green, g[j], reset)
	}

	fmt.Fprintf(&b, "\n  %s%s- expected   %s+ actual%s\n", dim, red, green, reset)

	return b.String()
}

// indent shows output verbatim, for the assertions a diff wouldn't clarify.
func indent(s string) string {
	if s == "" {
		return fmt.Sprintf("  %s(no output)%s\n", dim, reset)
	}

	var b strings.Builder
	for _, line := range lines(s) {
		fmt.Fprintf(&b, "  %s│%s %s\n", cyan, reset, line)
	}

	return b.String()
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lcs returns the longest common subsequence of a and b.
func lcs(a, b []string) []string {
	n := make([][]int, len(a)+1)
	for i := range n {
		n[i] = make([]int, len(b)+1)
	}

	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				n[i][j] = n[i+1][j+1] + 1
			} else {
				n[i][j] = max(n[i+1][j], n[i][j+1])
			}
		}
	}

	var out []string
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i, j = i+1, j+1
		case n[i+1][j] >= n[i][j+1]:
			i++
		default:
			j++
		}
	}

	return out
}

// rel shortens a path for display, relative to the repository root.
func rel(path string) string {
	root := filepath.Dir(testdata)
	if r, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(r)
	}
	return path
}
