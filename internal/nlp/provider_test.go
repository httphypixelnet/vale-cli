package nlp

import (
	"strings"
	"testing"
)

// Compute wraps a block's paragraphs as `paragraph.<scope>` only when told the
// block holds paragraphs. A heading or a table cell is segmented like any
// other prose, but a rule scoped to `paragraph` must not reach it. See #1132.
func TestComputeSplit(t *testing.T) {
	info := Info{Lang: "en", Segmentation: true, Splitting: true}

	scopes := func(blks []Block) []string {
		found := []string{}
		for _, b := range blks {
			found = append(found, b.Scope)
		}
		return found
	}

	t.Run("a paragraph is split", func(t *testing.T) {
		blk := NewLinedBlock(
			"", "One sentence here. Two sentences here.", "text.md", 1)

		blks, err := info.Compute(&blk, true)
		if err != nil {
			t.Fatal(err)
		}

		want := []string{
			"paragraph.text.md",
			"sentence.text.md",
			"sentence.text.md",
			"text.md",
		}
		got := scopes(blks)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("scopes = %v, want %v", got, want)
		}
	})

	t.Run("a heading is not", func(t *testing.T) {
		blk := NewLinedBlock(
			"", "A heading with a sentence.", "text.heading.h2.md", 1)

		blks, err := info.Compute(&blk, false)
		if err != nil {
			t.Fatal(err)
		}

		want := []string{
			"sentence.text.heading.h2.md",
			"text.heading.h2.md",
		}
		got := scopes(blks)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("scopes = %v, want %v", got, want)
		}
	})

	t.Run("several paragraphs, several blocks", func(t *testing.T) {
		blk := NewLinedBlock(
			"", "First paragraph.\n\nSecond paragraph.", "text.md", 1)

		blks, err := info.Compute(&blk, true)
		if err != nil {
			t.Fatal(err)
		}

		count := 0
		for _, b := range blks {
			if strings.HasPrefix(b.Scope, "paragraph.") {
				count++
			}
		}
		if count != 2 {
			t.Errorf("paragraph blocks = %d, want 2", count)
		}
	})
}
