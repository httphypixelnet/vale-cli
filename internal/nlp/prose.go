package nlp

import (
	"sync"

	"github.com/jdkato/prose/v3/segment"
	"github.com/jdkato/prose/v3/tag"
	"github.com/jdkato/prose/v3/tokenize"
)

// TaggedWord is a word with an NLP context.
type TaggedWord struct {
	Token tag.Token
	Line  int
	Span  []int
}

// WordTokenizer splits text into words.
var WordTokenizer = NewIterTokenizer()

// segmenter is loaded on first use: the punkt model costs time and memory to
// build, and most Vale runs never segment anything.
var punktSegmenter = sync.OnceValue(func() *segment.Segmenter {
	s, err := segment.New()
	if err != nil {
		panic("nlp: loading the sentence segmentation model: " + err.Error())
	}
	return s
})

// sentenceTokenizer defers loading the punkt model until something is actually
// segmented, while keeping the `SentenceTokenizer.Segment(...)` call shape.
type sentenceTokenizer struct{}

// Segment splits text into sentences.
func (sentenceTokenizer) Segment(text string) []string {
	return punktSegmenter().SegmentText(text)
}

// SentenceTokenizer splits text into sentences.
var SentenceTokenizer sentenceTokenizer

// tagger assigns part-of-speech tags.
//
// sync.OnceValues rather than a nil check: Vale lints files concurrently, so
// a plain check-then-assign here is a data race.
var tagger = sync.OnceValues(tag.New)

// wordTokenizer splits a sentence into positioned words for tagging.
//
// prose's tokenizer rather than the Treebank one: Treebank rewrites the text
// as it splits (quotes become “ and ”), so its tokens are not substrings of
// the source and cannot carry offsets. Callers such as the `sequence` check
// need to know where a token actually is.
var wordTokenizer = sync.OnceValue(func() *tokenize.Tokenizer {
	return tokenize.New()
})

// tagText splits text into sentences, tags each one, and returns the tokens
// with offsets relative to text.
//
// Tagging is per sentence because the tagger conditions on the previous two
// tags; letting that context run across a sentence boundary would condition
// the first word of each sentence on the last word of the one before it.
func tagText(text string) []tag.Token {
	t, err := tagger()
	if err != nil {
		panic("nlp: loading the part-of-speech model: " + err.Error())
	}

	var tokens []tag.Token
	for _, sent := range punktSegmenter().Segment(text) {
		found := wordTokenizer().Tokenize(sent.Text)
		t.TagTokens(found)

		// Tokenize reported offsets within the sentence; shift them so they
		// address the text the caller passed in.
		for i := range found {
			found[i].Start += sent.Start
		}
		tokens = append(tokens, found...)
	}

	return tokens
}

// TextToTokens converts a string to a slice of tagged tokens.
//
// Tokens from the built-in tagger carry their byte offset within text, so
// text[tok.Start:tok.Start+len(tok.Text)] == tok.Text. Tokens from a remote
// NLP endpoint do not: that API returns text and tags only, so Start is zero
// throughout and callers needing positions must locate the tokens themselves.
func TextToTokens(text string, nlp *Info) []tag.Token {
	// Determine if (and how) we need to do POS tagging.
	if nlp == nil || nlp.Endpoint == "" {
		// Fall back to our internal library (English-only).
		return tagText(text)
	}
	result, err := pos(text, nlp.Lang, nlp.Endpoint)
	if err != nil {
		panic(err)
	}
	return result.Tokens
}

// TokenCache remembers the tagging of each block within one document.
//
// Every `sequence` rule tags the sentence it is given, and a style may hold
// hundreds of them -- so the same sentence was tagged once per rule. The
// result depends only on the text, so it is computed once and shared.
//
// Scoped to a document: a cache living longer would hold every sentence a run
// has ever seen, and one shared between documents would need locking on a path
// that is otherwise free of it.
type TokenCache struct {
	tagged map[string][]tag.Token
}

// Tokens returns the tagged tokens of text, tagging it only the first time.
func (c *TokenCache) Tokens(text string, info *Info) []tag.Token {
	if c == nil {
		return TextToTokens(text, info)
	}

	if toks, ok := c.tagged[text]; ok {
		return toks
	}

	toks := TextToTokens(text, info)
	if c.tagged == nil {
		c.tagged = map[string][]tag.Token{}
	}
	c.tagged[text] = toks

	return toks
}
