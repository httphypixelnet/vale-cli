package check

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A metric's formula is pasted into a Tengo program, so a formula that closes
// the parenthesis it is handed can append statements and get the whole VM. The
// timeout bounds how long that costs; this is what stops it being possible.
func TestEvalMathRejectsInjectedStatements(t *testing.T) {
	injections := []string{
		// Closes the boilerplate's parenthesis, loops, reopens it.
		"0); for { } ; x := (0",
		// The same shape without the loop: still two statements smuggled in.
		"0); x := 1; y := (0",
		// A bare statement rather than an expression.
		"x := 1",
	}

	for _, expr := range injections {
		t.Run(expr, func(t *testing.T) {
			_, err := evalMath(context.Background(), expr, map[string]interface{}{})
			if err == nil {
				t.Fatalf("%q was accepted", expr)
			}
			if strings.Contains(err.Error(), "deadline") {
				t.Errorf("%q ran and was stopped by the timeout; it should not "+
					"have compiled: %v", expr, err)
			}
		})
	}
}

// The formulas that ship with Vale have to keep working, including the
// multi-line ones and the ones calling into `math`.
func TestEvalMathAcceptsRealFormulas(t *testing.T) {
	params := map[string]interface{}{
		"words": 100.0, "sentences": 10.0, "syllables": 150.0,
		"long_words": 20.0, "polysyllabic_words": 5.0, "characters": 500.0,
	}

	formulas := []string{
		"words / sentences",
		"(words / sentences) + ((long_words * 100) / words)",
		"(0.39 * (words / sentences)) + (11.8 * (syllables / words)) - 15.59",
		"1.0430 * math.sqrt((polysyllabic_words * 30.0) / sentences) + 3.1291",
		// The block-scalar forms arrive with surrounding whitespace.
		"\n  words / sentences\n",
	}

	for _, expr := range formulas {
		t.Run(strings.TrimSpace(expr), func(t *testing.T) {
			if _, err := evalMath(context.Background(), expr, params); err != nil {
				t.Errorf("rejected a valid formula: %v", err)
			}
		})
	}
}

// `condition` is spliced in after the computed value, so it is the same hole
// by another route and has to be closed by the same check.
func TestEvalMathGuardsTheConditionPath(t *testing.T) {
	// What Metric.Run builds: the result, then the rule's condition.
	good := "12.500000 > 10"
	if _, err := evalMath(context.Background(), good, map[string]interface{}{}); err != nil {
		t.Errorf("rejected a valid condition: %v", err)
	}

	bad := "12.500000 > 0); for { } ; x := (0"
	_, err := evalMath(context.Background(), bad, map[string]interface{}{})
	if err == nil {
		t.Fatal("an injected condition was accepted")
	}
	if strings.Contains(err.Error(), "deadline") {
		t.Errorf("the injected condition ran: %v", err)
	}
}

// Rejection has to happen before execution, not by running the program and
// waiting for the deadline: a formula stopped by the timeout still ran.
func TestEvalMathRejectsWithoutRunning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), tengoTimeout)
	defer cancel()

	start := time.Now()
	if _, err := evalMath(ctx, "0); for { } ; x := (0", map[string]interface{}{}); err == nil {
		t.Fatal("expected the expression to be rejected")
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, so it was executed and timed out rather than refused",
			elapsed)
	}
}
