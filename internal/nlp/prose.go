package nlp

import (
	"strings"
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

// wordTokenizer splits a sentence into words for tagging.
var wordTokenizer = sync.OnceValue(tokenize.NewTreebankWordTokenizer)

// doTag assigns part-of-speech tags to `words`.
func doTag(words []string) []tag.Token {
	t, err := tagger()
	if err != nil {
		panic("nlp: loading the part-of-speech model: " + err.Error())
	}
	return t.Tag(words)
}

// textToWords convert raw text into a slice of words.
func textToWords(text string, nlp bool) []string {
	words := []string{}
	for _, s := range SentenceTokenizer.Segment(text) {
		if nlp {
			words = append(words, wordTokenizer().Tokenize(s)...)
		} else {
			words = append(words, strings.Fields(s)...)
		}
	}

	return words
}

// TextToTokens converts a string to a slice of tokens.
func TextToTokens(text string, nlp *Info) []tag.Token {
	// Determine if (and how) we need to do POS tagging.
	if nlp == nil || nlp.Endpoint == "" {
		// Fall back to our internal library (English-only).
		return doTag(textToWords(text, true))
	}
	result, err := pos(text, nlp.Lang, nlp.Endpoint)
	if err != nil {
		panic(err)
	}
	return result.Tokens
}
