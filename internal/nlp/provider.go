package nlp

import (
	"strings"
)

type segmenter func(string) []string

// maxOffsetScan bounds the work resolveOffset will do to place a block whose
// offset was not recorded.
const maxOffsetScan = 8 << 10

// A Block represents a section of text.
type Block struct {
	Context string // parent content - e.g., sentence -> paragraph
	Line    int    // line of the block
	Scope   string // section selector
	Parent  string // parent (fully-qualfied) selector
	Text    string // text content

	// Offset is where Text begins within Context, or -1 when that is not
	// known.
	//
	// Checks that can report byte offsets need this to place a match: without
	// it, a match has to be located by searching Context for its text, which
	// finds the first occurrence rather than the one that matched. A sentence
	// repeated in a document is the common case.
	Offset int
}

// NewBlock makes a new Block with prepared text and a Selector.
func NewBlock(ctx, txt, sel string) Block {
	return NewLinedBlock(ctx, txt, sel, -1)
}

// NewLinedBlock creates a Block with an already-known location.
//
// The block's offset is 0 when it *is* its own context, and otherwise unknown;
// callers that carve a block out of a larger one should set Offset themselves.
func NewLinedBlock(ctx, txt, sel string, line int) Block {
	offset := -1
	if ctx == "" {
		ctx = txt
		offset = 0
	}

	return Block{
		Context: ctx,
		Text:    txt,
		Scope:   sel,
		Parent:  sel,
		Line:    line,
		Offset:  offset}
}

// at returns a copy of blk positioned at the given offset within its context.
func (b Block) at(offset int) Block {
	b.Offset = offset
	return b
}

// resolveOffset returns where Text sits within Context.
//
// Blocks built from markup are handed a context they were carved out of but
// not told where, so their offset starts out unknown. It can still be
// recovered when Text occurs exactly once -- and only then: with several
// occurrences there is no way to tell which one this block is, and guessing
// would reintroduce the mislocation this is meant to prevent.
func (b *Block) resolveOffset() int {
	if b.Offset >= 0 {
		return b.Offset
	}
	if b.Text == b.Context {
		return 0
	}

	// Recovering the offset means scanning the context, once per block. On a
	// large document that is quadratic, so give up past a threshold and let
	// the caller fall back to locating alerts by search. Blocks carved out of
	// markup have contexts of a paragraph or so; a context this large means
	// the offset was not threaded through, which is the real fix.
	if len(b.Context) > maxOffsetScan {
		return -1
	}

	// Find the first occurrence, then look for a second starting just past it.
	// strings.Count would scan the whole context to completion; this stops at
	// the second hit, and skips the search entirely once it is clear there is
	// no first one.
	first := strings.Index(b.Context, b.Text)
	if first < 0 {
		return -1
	}
	if strings.Contains(b.Context[first+len(b.Text):], b.Text) {
		return -1
	}
	return first
}

// Info handles NLP-related tasks.
//
// Assigning this on a per-file basis allows us to handle multi-language
// projects -- one file might be `en` while another is `ja`, for example.
type Info struct {
	Lang         string // Language of the file.
	Endpoint     string // API endpoint (optional); TODO: should this be per-file?
	Scope        string // The file's ext scope.
	Tagging      bool   // Does the file need POS tagging?
	Segmentation bool   // Does the file need sentence segmentation?
	Splitting    bool   // Does the file need paragraph splitting?
}

// An NLP provider is a library to implements part-of-speech tagging, sentence
// segmentation, and word tokenization.
//
// The default implementation is the pure-Go prose library, but the goal is to
// allow (fairly) seamless integration with non-Go libraries too (such as
// spaCy).
func (n *Info) Compute(block *Block) ([]Block, error) {
	seg := SentenceTokenizer.Segment
	if n.Endpoint != "" && n.Lang != "en" {
		// We only use external segmentation for non-English text since prose
		// (our native library) is more efficient.
		seg = func(text string) []string {
			ret, err := doSegment(text, n.Lang, n.Endpoint)
			if err != nil {
				panic(err)
			}
			return ret.Sents
		}
	}
	return n.doNLP(block, seg)
}

// offsetOf locates piece within blk.Text and returns its offset in blk's
// context, or -1 if it cannot be placed.
//
// cursor advances past each piece so that repeated text resolves to successive
// occurrences rather than always the first -- which is the whole point of
// tracking offsets instead of searching for them later.
func offsetOf(blk *Block, base int, piece string, cursor *int) int {
	if base < 0 || *cursor > len(blk.Text) {
		return -1
	}

	i := strings.Index(blk.Text[*cursor:], piece)
	if i < 0 {
		// A segmenter that rewrites text (a remote endpoint, say) can return
		// something that is not a substring of the input.
		return -1
	}

	start := *cursor + i
	*cursor = start + len(piece)

	return base + start
}

func (n *Info) doNLP(blk *Block, seg segmenter) ([]Block, error) {
	blks := []Block{}

	ctx := blk.Context
	idx := blk.Line
	base := blk.resolveOffset()

	if n.Splitting {
		cursor := 0
		for _, p := range strings.SplitAfter(blk.Text, "\n\n") {
			b := NewLinedBlock(ctx, p, "paragraph."+blk.Scope, idx)
			blks = append(blks, b.at(offsetOf(blk, base, p, &cursor)))
		}
	}

	if n.Segmentation {
		cursor := 0
		for _, s := range seg(blk.Text) {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			b := NewLinedBlock(ctx, s, "sentence."+blk.Scope, idx)
			blks = append(blks, b.at(offsetOf(blk, base, s, &cursor)))
		}
	}

	// The block itself, which is what most rules run against. It needs the
	// offset as much as its children do.
	blks = append(
		blks, NewLinedBlock(ctx, blk.Text, blk.Scope, idx).at(base))

	return blks, nil
}
