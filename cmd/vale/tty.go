package main

import (
	"os"

	"github.com/mattn/go-isatty"
	"github.com/pterm/pterm"
)

// isTTY reports whether f is an interactive terminal.
func isTTY(f *os.File) bool {
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// init turns styling off when nobody is there to read it.
//
// Color is for a person looking at a terminal. Redirected to a file or piped
// into another program it is noise the reader has to strip -- which Vale's own
// end-to-end suite was doing before it could compare anything. This runs
// before the flags are parsed so that a usage message printed during parsing
// is already plain.
//
// `NO_COLOR` is honored a layer down, by pterm.
func init() {
	if !isTTY(os.Stdout) {
		pterm.DisableColor()
	}
}

// configureColor applies the flags, once they are known.
func configureColor() {
	if Flags.NoColor {
		pterm.DisableColor()
	}
}
