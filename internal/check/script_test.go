package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/stdlib"

	"github.com/errata-ai/vale/v3/internal/nlp"
)

// compileScript builds a rule's program the way NewScript does, without the
// config plumbing needed to read one off disk.
func compileScript(t *testing.T, src string) *tengo.Compiled {
	t.Helper()

	program := tengo.NewScript([]byte(src))
	program.SetImports(stdlib.GetModuleMap("text", "fmt", "math"))

	if err := program.Add("scope", ""); err != nil {
		t.Fatal(err)
	}

	compiled, err := program.Compile()
	if err != nil {
		t.Fatal(err)
	}

	return compiled
}

// A script rule is the only check whose body is arbitrary code, and it usually
// arrives inside a downloaded style package. Denying it the `os` module bounds
// what it can reach; this bounds how long it can take. Without the deadline
// this test does not fail, it hangs.
func TestScriptRunStopsAtTheTimeout(t *testing.T) {
	s := Script{
		compiled: compileScript(t, "matches := []\nfor { }"),
		path:     "Runaway.yml",
	}

	start := time.Now()
	_, err := s.Run(nlp.Block{Text: "some text to match against"}, nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a script that never returns should have been stopped")
	}
	if elapsed > tengoTimeout*3 {
		t.Errorf("took %s to give up on a %s timeout", elapsed, tengoTimeout)
	}
}

// A rule that runs away has to say which rule it was: the file may hold
// several checks, and the name is the handle a user has for switching one off.
// The deadline is restated for the same reason -- Go's own wording for it names
// a mechanism the reader has never heard of.
func TestScriptRunTimeoutNamesTheRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Runaway.yml")

	err := os.WriteFile(path,
		[]byte("extends: script\nlevel: error\nscript: |\n  for { }\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := Script{compiled: compileScript(t, "matches := []\nfor { }"), path: path}
	s.Name = "Runaway.Loop"

	_, err = s.Run(nlp.Block{Text: "some text"}, nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}

	for _, want := range []string{"Runaway.Loop", "did not finish within"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("error still reports Go's internal wording:\n%v", err)
	}
}

// The deadline must not cost a working rule its result.
func TestScriptRunReturnsMatchesWithinTheTimeout(t *testing.T) {
	src := `
text := import("text")

matches := []
idx := text.index(scope, "storage")
if idx >= 0 {
	matches = append(matches, {begin: idx, end: idx + 7})
}
`

	s := Script{
		compiled: compileScript(t, src),
		path:     "Storage.yml",
	}

	alerts, err := s.Run(nlp.Block{Text: "the storage layer"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(alerts))
	}
	if got := alerts[0].Match; got != "storage" {
		t.Errorf("matched %q, want %q", got, "storage")
	}
}
