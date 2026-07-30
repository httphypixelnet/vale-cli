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

// sentenceScope narrows a declared scope to the sentences within it. Asking
// for blocks that are never built makes the rule match nothing and say
// nothing, which is the failure #1126 reported.
func TestSentenceScope(t *testing.T) {
	cases := []struct {
		name     string
		declared []string
		want     []string
	}{
		{"unset means every sentence", nil, []string{"sentence"}},
		{"a block scope is narrowed", []string{"list"}, []string{"sentence.list"}},
		{"already a sentence scope", []string{"sentence"}, []string{"sentence"}},
		{"already narrowed", []string{"sentence.list"}, []string{"sentence.list"}},
		{"negation stays in front", []string{"~list"}, []string{"~list"}},
		{"several at once",
			[]string{"heading", "list"},
			[]string{"sentence.heading", "sentence.list"}},

		// `paragraph` names no block of its own: splitting wraps every block
		// as `paragraph.<scope>`, so it already describes what an undeclared
		// scope does. `sentence.paragraph` is built for nothing.
		{"paragraph is not narrowed further",
			[]string{"paragraph"}, []string{"sentence"}},
		{"a qualified paragraph keeps its qualifier",
			[]string{"paragraph.md"}, []string{"sentence.md"}},
		{"a scope merely starting with the word is left alone",
			[]string{"paragraphs"}, []string{"sentence.paragraphs"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sentenceScope(c.declared)
			if len(got) != len(c.want) {
				t.Fatalf("sentenceScope(%v) = %v, want %v", c.declared, got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("sentenceScope(%v)[%d] = %q, want %q",
						c.declared, i, got[i], c.want[i])
				}
			}
		})
	}
}

// The narrowed scope has to match a block that is actually built. A paragraph's
// sentences arrive as `sentence.text.<ext>`, so a rule scoped to `paragraph`
// must match that or it reports nothing at all.
func TestSequenceParagraphScopeMatchesParagraphSentences(t *testing.T) {
	paragraphSentence := nlp.NewLinedBlock(
		"", "A sentence in a paragraph.", "sentence.text.md", 1)

	for _, declared := range []string{"paragraph", "sentence"} {
		t.Run(declared, func(t *testing.T) {
			rule, err := NewSequence(testConfig(), baseCheck{
				"extends": "sequence",
				"name":    "Test.Sequence",
				"level":   "error",
				"message": "Sequence matched '%s'.",
				"scope":   []string{declared},
				"tokens": []interface{}{
					map[string]interface{}{"pattern": "in"},
					map[string]interface{}{"pattern": "a"},
				},
			}, "Test.Sequence")
			if err != nil {
				t.Fatalf("building rule: %v", err)
			}

			narrowed := rule.Fields().Scope
			if !NewScope(narrowed).Matches(paragraphSentence) {
				t.Errorf("scope %q narrowed to %v, which matches no paragraph sentence",
					declared, narrowed)
			}
		})
	}
}
