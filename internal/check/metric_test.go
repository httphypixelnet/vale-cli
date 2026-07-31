package check

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A formula is substituted into the boilerplate, so one carrying its own
// parentheses could read as several statements. Only a single expression is a
// formula this rule can evaluate.
func TestEvalMathRejectsNonExpressions(t *testing.T) {
	notExpressions := []string{
		// Closes the substitution's parenthesis and reopens it, so what
		// arrives is three statements rather than one expression.
		"0); for { } ; x := (0",
		"0); x := 1; y := (0",
		// A statement rather than an expression.
		"x := 1",
	}

	for _, expr := range notExpressions {
		t.Run(expr, func(t *testing.T) {
			_, err := evalMath(context.Background(), expr, map[string]interface{}{})
			if err == nil {
				t.Fatalf("%q was accepted", expr)
			}
			if strings.Contains(err.Error(), "deadline") {
				t.Errorf("%q was evaluated and then timed out; it should have "+
					"been refused before that: %v", expr, err)
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

// `condition` is substituted the same way, after the computed value, so it
// needs the same check.
func TestEvalMathGuardsTheConditionPath(t *testing.T) {
	// What Metric.Run builds: the result, then the rule's condition.
	good := "12.500000 > 10"
	if _, err := evalMath(context.Background(), good, map[string]interface{}{}); err != nil {
		t.Errorf("rejected a valid condition: %v", err)
	}

	bad := "12.500000 > 0); for { } ; x := (0"
	_, err := evalMath(context.Background(), bad, map[string]interface{}{})
	if err == nil {
		t.Fatal("a condition that is not a single expression was accepted")
	}
	if strings.Contains(err.Error(), "deadline") {
		t.Errorf("the condition was evaluated rather than refused: %v", err)
	}
}

// The check has to happen before evaluation. A formula stopped by the deadline
// was still evaluated, which is a slower answer and a worse message.
func TestEvalMathRejectsWithoutRunning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), tengoTimeout)
	defer cancel()

	start := time.Now()
	if _, err := evalMath(ctx, "0); for { } ; x := (0", map[string]interface{}{}); err == nil {
		t.Fatal("expected the expression to be rejected")
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, so it was evaluated and timed out rather than refused",
			elapsed)
	}
}
