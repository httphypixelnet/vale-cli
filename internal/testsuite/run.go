package testsuite

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/lint"
)

// A Result is what running one Case produced.
type Result struct {
	Case Case

	// Got is the alert output, one `line:col:check:message` per line. There is
	// no path: the document was a string.
	Got string

	// Reason says what went wrong, and is empty when the case passed. Err is
	// set instead when the case could not be run at all -- a rule that does
	// not compile is not a failing assertion.
	Reason string
	Err    error
}

// Failed reports whether the case did not pass, for either reason.
func (r Result) Failed() bool { return r.Reason != "" || r.Err != nil }

// A Runner lints the cases in a configuration.
type Runner struct {
	flags *core.CLIFlags

	// project is the linter a `vale` run in this directory would use. It is
	// built once and reused: loading a configuration costs more than linting
	// the handful of lines a case holds.
	project *lint.Linter
}

// NewRunner prepares to run cases against the configuration in `flags`.
func NewRunner(flags *core.CLIFlags) *Runner {
	return &Runner{flags: flags}
}

// Run lints one case and compares what came back.
func (r *Runner) Run(c Case) Result {
	got, err := r.lint(c)
	if err != nil {
		return Result{Case: c, Err: err}
	}

	return Result{Case: c, Got: got, Reason: compare(c, got)}
}

// lint converts a case's input and returns its alerts, one per line.
func (r *Runner) lint(c Case) (string, error) {
	linter, err := r.linterFor(c)
	if err != nil {
		return "", err
	}

	linter.Manager.Config.Flags.InExt = c.Ext()

	linted, err := linter.LintString(c.Input)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	for _, f := range linted {
		for _, a := range f.SortedAlerts() {
			fmt.Fprintf(&out, "%d:%d:%s:%s\n", a.Line, a.Span[0], a.Check, a.Message)
		}
	}

	return out.String(), nil
}

// linterFor returns the linter a case runs under.
func (r *Runner) linterFor(c Case) (*lint.Linter, error) {
	if c.Rule != "" {
		return isolate(c)
	}

	if r.project == nil {
		cfg, err := core.ReadPipeline(r.flags, false)
		if err != nil {
			return nil, err
		}

		r.project, err = lint.NewLinter(cfg)
		if err != nil {
			return nil, err
		}
	}

	return r.project, nil
}

// isolate builds a linter holding one rule and nothing else.
//
// The rule keeps the name a project run would give it -- the style is the
// directory it sits in -- so a case can be moved between the two modes without
// its `want` changing.
func isolate(c Case) (*lint.Linter, error) {
	path := c.Rule
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(c.Path), path)
	}

	style := filepath.Base(filepath.Dir(path))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	cfg, err := core.NewConfig(&core.CLIFlags{})
	if err != nil {
		return nil, err
	}

	// Every severity, since an isolated rule is being asked what it matches
	// and not whether a run would show it.
	cfg.MinAlertLevel = 0
	cfg.GBaseStyles = []string{style}

	linter, err := lint.NewLinter(cfg)
	if err != nil {
		return nil, err
	}

	if err = linter.Manager.AddRuleFromFile(style+"."+name, path); err != nil {
		return nil, err
	}

	return linter, nil
}

// compare returns why a case failed, or "" if it passed.
func compare(c Case, got string) string {
	if c.Want != nil {
		if want := strings.TrimSpace(*c.Want); strings.TrimSpace(got) != want {
			return "output does not match"
		}
	}

	if c.Contains != "" && !strings.Contains(got, c.Contains) {
		return fmt.Sprintf("output does not contain %q", c.Contains)
	}

	for _, absent := range c.Absent {
		if strings.Contains(got, absent) {
			return fmt.Sprintf("output contains %q", absent)
		}
	}

	return ""
}
