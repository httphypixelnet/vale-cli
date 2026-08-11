package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/adrg/strutil/metrics"
	"github.com/pterm/pterm"

	"github.com/errata-ai/vale/v3/internal/system"
)

// maxCommandDistance is how far a word may be from a command and still be
// taken for a misspelling of it.
//
// Two edits catches the transpositions and dropped letters that typing
// produces -- `ls-confg`, `snyc` -- without reaching so far that an ordinary
// short sentence looks like a command.
const maxCommandDistance = 2

// didYouMean returns the command `arg` was probably meant to be.
//
// `vale "some text in a string"` is a documented way to lint prose, so an
// argument Vale doesn't recognize cannot simply be rejected: most of them are
// the document. Only a single bare word that is a near miss for a command
// qualifies, which leaves every real use of that form alone.
func didYouMean(arg string) (string, bool) {
	if strings.ContainsAny(arg, " \t\n") || system.FileExists(arg) || system.IsDir(arg) {
		return "", false
	}

	metric := metrics.NewLevenshtein()

	best, distance := "", maxCommandDistance+1
	for name, cmd := range commands {
		if cmd.Hidden {
			continue
		}

		// A tie goes to the name that sorts first, so the suggestion doesn't
		// depend on map ordering.
		if d := metric.Distance(arg, name); d < distance ||
			(d == distance && name < best) {
			best, distance = name, d
		}
	}

	if distance > maxCommandDistance {
		return "", false
	}

	return best, true
}

// reportUnknownCommand explains a mistyped command and how to recover.
func reportUnknownCommand(arg, suggestion string) {
	fmt.Fprintf(os.Stderr, "%s unknown command %s\n\n",
		pterm.Red("Error:"), pterm.Bold.Sprint(arg))

	fmt.Fprintf(os.Stderr, "Did you mean %s?\n\n", pterm.Bold.Sprint(suggestion))

	names := make([]string, 0, len(commands))
	for name, cmd := range commands {
		if !cmd.Hidden {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	fmt.Fprintf(os.Stderr, "Commands: %s\n", strings.Join(names, ", "))
	fmt.Fprintf(os.Stderr, "Run %s for more.\n", toCodeStyle("vale --help"))
}
