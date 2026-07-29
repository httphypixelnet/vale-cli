package core

import (
	"testing"

	"github.com/errata-ai/vale/v3/internal/nlp"
)

// A punctuation-only match in a block whose text was altered by inline markup
// (e.g. a stripped code span) must still be located within that block, not at
// the first occurrence anywhere in the document -- see #994.
func TestInitialPositionPunctAnchor(t *testing.T) {
	// ctx keeps the raw code span; txt is the extracted sentence without it.
	ctx := "Test, line.\nLine, with, four, commas, `yes`.\n"
	txt := "Line, with, four, commas, yes."

	pos, sub := initialPosition(ctx, txt, Alert{Match: ","})
	// The comma belongs to the second sentence (after "Line"), not the first
	// comma in "Test,". Position is 1-based rune count.
	if pos != 17 {
		t.Errorf("pos = %d, want 17 (second sentence)", pos)
	}
	if sub != "," {
		t.Errorf("sub = %q, want %q", sub, ",")
	}
}

func TestIsPunctOnly(t *testing.T) {
	cases := map[string]bool{
		",":      true,
		"...":    true,
		"":       false,
		"hav":    false,
		"that's": false,
		"OAuth2": false,
	}
	for in, want := range cases {
		if got := isPunctOnly(in); got != want {
			t.Errorf("isPunctOnly(%q) = %v, want %v", in, got, want)
		}
	}
}

// A match whose smart apostrophe/quote was normalized to ASCII (as
// spell-checking does) must still be located in the original source. Before
// the fix, the straight-apostrophe match couldn't be found in smart-apostrophe
// text, so the alert was dropped -- see #1003.
func TestInitialPositionSmartApostrophe(t *testing.T) {
	straight := "The toolkit's plugin." // a.Match is always normalized
	smart := "The toolkit’s plugin."    // source keeps the smart form

	tests := []struct {
		name string
		ctx  string
	}{
		{"straight source", straight},
		{"smart source", smart},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, sub := initialPosition(tt.ctx, tt.ctx, Alert{Match: "toolkit's"})
			if pos != 5 {
				t.Errorf("pos = %d, want 5", pos)
			}
			if sub != "toolkit's" {
				t.Errorf("sub = %q, want %q", sub, "toolkit's")
			}
		})
	}
}

// locByScan is the previous implementation: count newlines from the start of
// the document on every call. The indexed version has to agree with it exactly.
func locByScan(ctx string, begin, end, pad int) (int, []int) {
	line := 1
	lineStart := 0

	for i := 0; i < begin && i < len(ctx); i++ {
		if ctx[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}

	col := nlp.StrLen(ctx[lineStart:begin]) + 1 + pad
	matchLen := nlp.StrLen(ctx[begin:end])

	span := []int{col, col + matchLen - 1}
	if span[1] <= 0 {
		span[1] = 1
	}

	return line, span
}

func TestLocFromByteOffsetMatchesScan(t *testing.T) {
	inputs := []string{
		"",
		"one line",
		"a\nb\nc",
		"\n\n\nleading blanks",
		"trailing\n",
		"héllo\nwörld\n日本語\n👍🏽 emoji",
		"win\r\nline\r\nendings",
	}

	f := &File{}
	for _, ctx := range inputs {
		starts := f.lineStarts(ctx)
		for begin := 0; begin <= len(ctx); begin++ {
			for end := begin; end <= len(ctx); end++ {
				for _, pad := range []int{0, 3} {
					wantLine, wantSpan := locByScan(ctx, begin, end, pad)
					gotLine, gotSpan := locFromByteOffset(ctx, starts, begin, end, pad)

					if gotLine != wantLine {
						t.Fatalf("ctx=%q begin=%d: line = %d, want %d",
							ctx, begin, gotLine, wantLine)
					}
					if gotSpan[0] != wantSpan[0] || gotSpan[1] != wantSpan[1] {
						t.Fatalf("ctx=%q begin=%d end=%d: span = %v, want %v",
							ctx, begin, end, gotSpan, wantSpan)
					}
				}
			}
		}
	}
}

// The index is cached per context; a different context must rebuild it.
func TestLineStartsRebuildsOnNewContext(t *testing.T) {
	f := &File{}

	if got := f.lineStarts("a\nb"); len(got) != 2 {
		t.Fatalf("lineStarts = %v, want 2 entries", got)
	}
	if got := f.lineStarts("a\nb\nc\nd"); len(got) != 4 {
		t.Fatalf("lineStarts after change = %v, want 4 entries", got)
	}
	if got := f.lineStarts("a\nb"); len(got) != 2 {
		t.Fatalf("lineStarts back = %v, want 2 entries", got)
	}
}
