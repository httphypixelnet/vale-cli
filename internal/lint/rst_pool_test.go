package lint

import (
	"runtime"
	"strings"
	"testing"

	"github.com/errata-ai/vale/v3/internal/system"
)

const rstFixture = "../../testdata/fixtures/formats/test.rst"

func rstExecutable() string {
	return system.Which([]string{
		"rst2html", "rst2html.py", "rst2html-3", "rst2html-3.py"})
}

// The pool reuses one interpreter for every document, so its Docutils settings
// are established once instead of per file. That is the whole risk of the
// change: if the pooled settings differ at all from what a per-file rst2html
// invocation uses, Vale silently lints different HTML.
func TestRSTPoolMatchesSpawnedOutput(t *testing.T) {
	exe := rstExecutable()
	if exe == "" {
		t.Skip("rst2html not installed")
	}

	argv := rstFastPath(exe)
	if argv == nil {
		t.Skip("docutils not reachable from the rst2html interpreter")
	}

	pool, err := newProcPool(argv, rstArgs, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.stop()

	// Non-ASCII and a title on purpose: a mismatched default encoding and the
	// source name reaching <title> are both ways the two paths can diverge
	// without any visible error.
	for _, doc := range []string{
		"Title\n=====\n\nnaïve body — with punctuation.\n",
		"no title here, just a paragraph\n",
		"- a list\n- of items\n\n``literal`` and *emphasis*\n",
		// Section numbering, table-of-contents backlinks and footnote
		// backlinks are each turned off by a flag in rstArgs, and each shows
		// up inside <body>. Without a document like this the comparison
		// passes even if the pooled interpreter ignores the flags entirely.
		".. sectnum::\n\n.. contents::\n\nFirst\n=====\n\n" +
			"Body with a footnote [1]_.\n\nSecond\n======\n\nMore.\n\n.. [1] The note.\n",
	} {
		pooled, cErr := pool.convert(doc, argv, rstArgs)
		if cErr != nil {
			t.Fatalf("pooled convert failed: %v", cErr)
		}

		spawned, sErr := system.ExecuteWithInput(exe, doc, rstArgs...)
		if sErr != nil {
			t.Fatalf("spawned convert failed: %v", sErr)
		}

		if got, want := rstBody(pooled), rstBody(spawned); got != want {
			t.Errorf("pooled and spawned output differ for %q:\n pooled: %q\nspawned: %q",
				doc, got, want)
		}
	}
}

// A pool with no owner is a pool nothing stops. The CLI gets away with it --
// exiting reaps the children -- but the language server lints many times in one
// process, and every run would leave its interpreters behind.
func TestRSTPoolDoesNotLeakProcesses(t *testing.T) {
	if exe := rstExecutable(); exe == "" || rstFastPath(exe) == nil {
		t.Skip("docutils not reachable on this machine")
	}

	before := childProcessCount(t)

	linter, err := initLinter()
	if err != nil {
		t.Fatal(err)
	}

	// A real reStructuredText file, so the run actually starts the pool -- a
	// path that lints nothing would pass this test without testing anything.
	linted, err := linter.Lint([]string{rstFixture}, "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(linted) == 0 {
		t.Fatal("nothing linted; the pool was never started")
	}
	if linter.rst != nil {
		t.Error("pool outlived the run")
	}

	// Give the OS a moment to reap.
	runtime.Gosched()
	if after := childProcessCount(t); after > before {
		t.Errorf("leaked processes: %d before, %d after", before, after)
	}
}

// The pool has to survive a document Docutils rejects: later files still
// convert, rather than the run losing its warm interpreters.
func TestRSTPoolSurvivesABadDocument(t *testing.T) {
	exe := rstExecutable()
	if exe == "" {
		t.Skip("rst2html not installed")
	}

	argv := rstFastPath(exe)
	if argv == nil {
		t.Skip("docutils not reachable from the rst2html interpreter")
	}

	pool, err := newProcPool(argv, rstArgs, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.stop()

	// A malformed directive, which Docutils reports at the severity that
	// --halt=5 stops on.
	if _, err = pool.convert(".. |bad\xff| replace::\n", argv, rstArgs); err == nil {
		t.Log("bad document was accepted; the recovery path is untested here")
	}

	got, err := pool.convert("still working\n", argv, rstArgs)
	if err != nil {
		t.Fatalf("pool did not recover: %v", err)
	}
	if !strings.Contains(got, "still working") {
		t.Errorf("unexpected output after recovery: %q", got)
	}
}

// A run that lints a string owns its helper processes the same way a run that
// lints a file does. `cat page.rst | vale` and `vale "some text"` both reach
// Docutils through LintString, and both must leave nothing running behind
// them: an editor integration invokes Vale on every save.
func TestLintStringStopsHelpers(t *testing.T) {
	if rstExecutable() == "" {
		t.Skip("rst2html not installed")
	}

	linter, err := initLinter()
	if err != nil {
		t.Skip(err)
	}
	linter.Manager.Config.Flags.InExt = ".rst"

	if _, err = linter.LintString("A Title\n=======\n\nSome prose here.\n"); err != nil {
		t.Fatal(err)
	}

	if linter.rst != nil {
		t.Error("the Docutils pool outlived the run that started it")
	}
}
