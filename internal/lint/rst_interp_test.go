package lint

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/errata-ai/vale/v3/internal/system"
)

// write creates an executable script and returns its path.
func write(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return path
}

// The interpreter is read out of a shebang, which is a claim the file makes
// about itself rather than something Vale can verify. Anything but Python has
// to come back empty: the caller's next move is to hand it the Docutils server
// source, and a shell reads that as a script. See #1137.
func TestRSTInterpreterOnlyNamesPython(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		shebang string
		python  bool
	}{
		{"pyenv shim", "#!/usr/bin/env bash\nset -e\nexec pyenv exec rst2html \"$@\"\n", false},
		{"sh wrapper", "#!/bin/sh\nexec /usr/bin/rst2html \"$@\"\n", false},
		{"env perl", "#!/usr/bin/env perl\n", false},
		{"env with no argument", "#!/usr/bin/env\n", false},
		{"no shebang", "not a script\n", false},
		{"empty file", "", false},
		{"absolute python", "#!/usr/bin/python3\n", true},
		{"versioned python", "#!/usr/local/bin/python3.12\n", true},
		{"pypy", "#!/usr/bin/pypy3\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rstInterpreter(write(t, dir, "rst2html", tt.shebang))

			if !tt.python {
				if got != "" {
					t.Errorf("rstInterpreter = %q; want \"\"", got)
				}
				return
			}
			if !rePython.MatchString(filepath.Base(got)) {
				t.Errorf("rstInterpreter = %q; want a Python", got)
			}
		})
	}
}

// `#!/usr/bin/env python3` names the interpreter in the second field, and that
// field is a bare name -- it has to be resolved on PATH, not returned as-is.
func TestRSTInterpreterResolvesEnvPython(t *testing.T) {
	if system.Which([]string{"python3", "python"}) == "" {
		t.Skip("no python on PATH")
	}

	got := rstInterpreter(write(t, t.TempDir(), "rst2html", "#!/usr/bin/env python3\n"))
	if got != "" && !filepath.IsAbs(got) {
		t.Errorf("rstInterpreter = %q; want an absolute path", got)
	}
}

// The failure in #1137 was not a wrong answer -- the probe caught that and Vale
// fell back to spawning rst2html per file, so the linting was correct. It was
// what running the probe did on the way: `bash -c` on the Docutils source runs
// its first line, `import sys`, as a shell command, and `import` is
// ImageMagick's screenshot tool.
//
// So this asserts on the side effect, not the return value. `import` here
// records that it ran.
func TestRSTProbeRunsNothingThroughAShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no shebang handling on Windows")
	} else if system.Which([]string{"bash"}) == "" {
		t.Skip("bash not installed")
	}

	dir := t.TempDir()
	sentinel := filepath.Join(dir, "ran")

	// A wrapper of the shape a version manager installs: a Bourne-Again shell
	// script under the name of the tool it forwards to.
	shim := write(t, dir, "rst2html", "#!/usr/bin/env bash\nset -e\nexit 0\n")

	// Every word on the source's first line is a command a shell would try.
	for _, name := range []string{"import", "sys"} {
		write(t, dir, name, "#!/bin/sh\necho ran > "+sentinel+"\n")
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	rstProbe(shim)

	if _, err := os.Stat(sentinel); err == nil {
		t.Error("the Docutils source was executed by a shell")
	}
}
