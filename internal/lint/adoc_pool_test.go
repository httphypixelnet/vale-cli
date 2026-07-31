package lint

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// A pool with no owner is a pool nothing stops. The CLI gets away with it --
// exiting reaps the children -- but the language server lints many times in one
// process, and every run would leave its interpreters behind.
func TestAdocPoolDoesNotLeakProcesses(t *testing.T) {
	if adocFastPath() == nil {
		t.Skip("asciidoctor not resolvable on this machine")
	}

	before := rubyProcessCount(t)

	linter, err := initLinter()
	if err != nil {
		t.Fatal(err)
	}

	// A real AsciiDoc file, so the run actually starts the pool -- a path that
	// lints nothing would pass this test without testing anything.
	linted, err := linter.Lint([]string{"../../testdata/fixtures/formats/test.adoc"}, "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(linted) == 0 {
		t.Fatal("nothing linted; the pool was never started")
	}
	if linter.adoc != nil {
		t.Error("pool outlived the run")
	}

	// Give the OS a moment to reap.
	runtime.Gosched()
	if after := rubyProcessCount(t); after > before {
		t.Errorf("leaked processes: %d before, %d after", before, after)
	}
}

// The pool has to survive a document Asciidoctor rejects: the process stays up
// and later files still convert, rather than the run losing its warm pool.
func TestAdocPoolSurvivesABadDocument(t *testing.T) {
	argv := adocFastPath()
	if argv == nil {
		t.Skip("asciidoctor not resolvable on this machine")
	}

	pool, err := newAdocPool(argv, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.stop()

	// A byte sequence Asciidoctor refuses to read as a document.
	if _, err = pool.convert("\xff\xfe binary", argv, nil); err == nil {
		t.Log("bad document was accepted; the recovery path is untested here")
	}

	got, err := pool.convert("= T\n\nstill working\n", argv, nil)
	if err != nil {
		t.Fatalf("pool did not recover: %v", err)
	}
	if !strings.Contains(got, "still working") {
		t.Errorf("unexpected output after recovery: %q", got)
	}
}

func rubyProcessCount(t *testing.T) int {
	t.Helper()

	out, err := exec.Command("ps", "-eo", "comm").Output()
	if err != nil {
		t.Skip("ps unavailable")
	}
	return strings.Count(string(out), "ruby")
}
