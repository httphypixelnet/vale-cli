package check

import (
	"testing"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/nlp"
)

// A sequence position names the word it accepts, so it has to match the whole
// token.
//
// The regex that finds candidate positions is deliberately unanchored -- it is
// run against the whole sentence -- and reusing it to test a token made
// `pattern: self` accept the single token `self-worth`, so a rule for
// `your self` fired on `your self-worth`.
func TestSequenceMatchesWholeTokens(t *testing.T) {
	rule, err := NewSequence(testConfig(), baseCheck{
		"extends": "sequence",
		"name":    "Test.Yourself",
		"level":   "error",
		"message": "Did you mean 'yourself'?",
		"tokens": []interface{}{
			map[string]interface{}{"pattern": "your"},
			map[string]interface{}{"pattern": "self"},
		},
	}, "Test.Yourself")
	if err != nil {
		t.Fatalf("building rule: %v", err)
	}

	cases := []struct {
		name string
		text string
		want int
	}{
		{"exact words", "Ask your self what matters.", 1},
		{"hyphenated compound", "Question your self-worth sometimes.", 0},
		{"longer word", "Consider your selfishness here.", 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &core.File{NLP: nlp.Info{}}

			alerts, rerr := rule.Run(nlp.NewBlock(c.text, c.text, "text"), f, testConfig())
			if rerr != nil {
				t.Fatalf("running rule: %v", rerr)
			}
			if len(alerts) != c.want {
				t.Errorf("%q produced %d alerts, want %d", c.text, len(alerts), c.want)
			}
		})
	}
}

func testConfig() *core.Config {
	return &core.Config{WordTemplate: wordTemplate}
}
