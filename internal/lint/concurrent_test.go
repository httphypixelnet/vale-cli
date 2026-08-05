package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/errata-ai/vale/v3/internal/core"
)

// withFloor forces every block onto one path or the other.
func withFloor(t *testing.T, floor int) {
	t.Helper()

	old := parallelFloor
	parallelFloor = floor
	t.Cleanup(func() { parallelFloor = old })
}

// sample is prose long enough to clear the real floor, with several rules
// firing on the same line so that ordering has something to get wrong. The
// misspellings are deliberate: `spelling` is on the serial pass and the rest
// are not, so the merge has to interleave the two correctly.
func sample() string {
	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, "Use your cellphone or your smart phone to reachout (%d). ", i)
		b.WriteString("The abandonment of the plan was intentional; ")
		//nolint:dupword // the repeat is what Vale.Repetition fires on
		b.WriteString("we will utilize the mispelled word and and repeat it.\n\n")
	}
	return b.String()
}

// demoLinter lints with the fixture style, which has rules of both kinds.
func demoLinter(t *testing.T) *Linter {
	t.Helper()

	cfg, err := core.NewConfig(&core.CLIFlags{IgnoreGlobal: true})
	if err != nil {
		t.Fatal(err)
	}

	cfg.AddStylesPath("../../testdata/styles")
	cfg.GBaseStyles = []string{"demo", "Vale"}
	cfg.MinAlertLevel = 0
	cfg.Flags.InExt = ".txt"

	linter, err := NewLinter(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return linter
}

func alertLines(files []*core.File) []string {
	var out []string
	for _, f := range files {
		for _, a := range f.Alerts {
			out = append(out, fmt.Sprintf("%d:%d:%s:%s:%s",
				a.Line, a.Span[0], a.Check, a.Severity, a.Match))
		}
	}
	return out
}

func lintSample(t *testing.T, floor int) []string {
	t.Helper()

	withFloor(t, floor)

	files, err := demoLinter(t).LintString(sample())
	if err != nil {
		t.Fatal(err)
	}

	return alertLines(files)
}

// The concurrent path has to agree with the serial one on every alert and on
// the order they arrive in, since which rule reaches a span first decides what
// the reader is told about it.
func TestConcurrentMatchesSerial(t *testing.T) {
	serial := lintSample(t, 1<<30) // every block below the floor
	concurrent := lintSample(t, 0) // every block above it

	if len(serial) == 0 {
		t.Fatal("the sample raised no alerts, so this proves nothing")
	}

	if len(serial) != len(concurrent) {
		t.Fatalf("serial found %d alerts, concurrent %d", len(serial), len(concurrent))
	}

	for i := range serial {
		if serial[i] != concurrent[i] {
			t.Fatalf("alert %d differs:\n serial     %s\n concurrent %s",
				i, serial[i], concurrent[i])
		}
	}
}

// Rules finish in whatever order the scheduler chooses, so the same input has
// to come back in the same order every time rather than merely most times.
func TestConcurrentIsDeterministic(t *testing.T) {
	first := lintSample(t, 0)
	if len(first) == 0 {
		t.Fatal("the sample raised no alerts, so this proves nothing")
	}

	for i := range 12 {
		got := lintSample(t, 0)
		if len(got) != len(first) {
			t.Fatalf("run %d found %d alerts, the first found %d", i, len(got), len(first))
		}
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("run %d differs at alert %d:\n first %s\n got   %s",
					i, j, first[j], got[j])
			}
		}
	}
}

// A rule that reads the file cannot run concurrently with one that writes it.
// Adding an extension point should mean deciding which it is, so a new one
// that nobody classified fails here rather than racing in the field.
func TestEveryExtensionPointIsClassified(t *testing.T) {
	// Kept by hand rather than read from `check`: the point is to make an
	// unclassified addition fail, which reading the list would defeat.
	serial := map[string]bool{
		"conditional": true, // appends to f.Sequences
		"consistency": true, // appends to f.Sequences
		"metric":      true, // reads the file's counts
		"sequence":    true, // tags through the file's cache
		"spelling":    true, // holds a dictionary of its own
	}

	points := []string{
		"capitalization", "conditional", "consistency", "existence",
		"metric", "occurrence", "readability", "repetition", "script",
		"sequence", "spelling", "substitution",
	}

	for _, p := range points {
		if concurrentKinds[p] == serial[p] {
			t.Errorf("%q is %s", p, map[bool]string{
				true:  "both concurrent and serial",
				false: "neither concurrent nor serial",
			}[concurrentKinds[p]])
		}
	}

	for p := range concurrentKinds {
		if !contains(points, p) {
			t.Errorf("%q is marked concurrent but is not an extension point", p)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
