//go:build tokenizers
// +build tokenizers

// Package cache provides caching implementations for the gateway.
package cache

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/daulet/tokenizers"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Tokenizer defines the interface for text tokenization
type Tokenizer interface {
	Encode(text string) ([]int, error)
	Decode(tokens []int) (string, error)
	TokenCount(text string) int
}

// BPETokenizer implements BPE (Byte Pair Encoding) tokenization
type BPETokenizer struct {
	tokenizer *tokenizers.Tokenizer
}

// NewBPETokenizer creates a new BPE tokenizer from a vocabulary file
func NewBPETokenizer(vocabFile string) (*BPETokenizer, error) {
	if vocabFile == "" {
		return nil, fmt.Errorf("vocab file path is required for BPE tokenizer")
	}

	// Load tokenizer from file
	tk, err := tokenizers.FromFile(vocabFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load BPE tokenizer from %s: %w", vocabFile, err)
	}

	logger.WithFields("vocab_file", vocabFile).Info("BPE tokenizer loaded successfully")

	return &BPETokenizer{tokenizer: tk}, nil
}

// Encode converts text to token IDs
func (t *BPETokenizer) Encode(text string) ([]int, error) {
	encoding, err := t.tokenizer.Encode(text, false)
	if err != nil {
		return nil, fmt.Errorf("failed to encode text: %w", err)
	}
	// Convert []uint32 to []int
	ids := make([]int, len(encoding))
	for i, id := range encoding {
		ids[i] = int(id)
	}
	return ids, nil
}

// Decode converts token IDs back to text
func (t *BPETokenizer) Decode(tokens []int) (string, error) {
	// Convert []int to []uint32
	ids := make([]uint32, len(tokens))
	for i, token := range tokens {
		ids[i] = uint32(token)
	}
	text := t.tokenizer.Decode(ids, false)
	return text, nil
}

// TokenCount returns the number of tokens in the text
func (t *BPETokenizer) TokenCount(text string) int {
	encoding, err := t.tokenizer.Encode(text, false)
	if err != nil {
		return 0
	}
	return len(encoding)
}

// SimpleTokenizer implements a simple word-based tokenizer as fallback
// This is based on the existing implementation in minhash.go
type SimpleTokenizer struct {
	stopwords map[string]bool
}

// NewSimpleTokenizer creates a new simple tokenizer
func NewSimpleTokenizer() *SimpleTokenizer {
	return &SimpleTokenizer{
		stopwords: defaultStopwords(),
	}
}

// Encode converts text to token IDs (simple word-based)
func (t *SimpleTokenizer) Encode(text string) ([]int, error) {
	tokens := t.tokenize(text)
	ids := make([]int, len(tokens))
	for i, token := range tokens {
		// Simple hash-based ID generation
		ids[i] = simpleHash(token)
	}
	return ids, nil
}

// Decode converts token IDs back to text (not fully reversible for simple tokenizer)
func (t *SimpleTokenizer) Decode(tokens []int) (string, error) {
	// Simple tokenizer doesn't support true decoding
	return "", fmt.Errorf("decode not supported for simple tokenizer")
}

// TokenCount returns the number of tokens in the text
func (t *SimpleTokenizer) TokenCount(text string) int {
	return len(t.tokenize(text))
}

// tokenize converts text to lowercase tokens, removing punctuation and stopwords
func (t *SimpleTokenizer) tokenize(text string) []string {
	// Convert to lowercase
	text = strings.ToLower(text)

	// Split on whitespace and punctuation
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else if current.Len() > 0 {
			token := current.String()
			if !t.stopwords[token] && len(token) > 1 {
				tokens = append(tokens, token)
			}
			current.Reset()
		}
	}

	// Don't forget the last token
	if current.Len() > 0 {
		token := current.String()
		if !t.stopwords[token] && len(token) > 1 {
			tokens = append(tokens, token)
		}
	}

	return tokens
}

// simpleHash generates a simple hash for a string
func simpleHash(s string) int {
	hash := 0
	for _, c := range s {
		hash = hash*31 + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return hash
}

// defaultStopwords returns common English stopwords
func defaultStopwords() map[string]bool {
	return map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true,
		"but": true, "is": true, "are": true, "was": true, "were": true,
		"be": true, "been": true, "being": true, "have": true, "has": true,
		"had": true, "do": true, "does": true, "did": true, "will": true,
		"would": true, "could": true, "should": true, "may": true, "might": true,
		"must": true, "can": true, "to": true, "of": true, "in": true,
		"for": true, "on": true, "with": true, "at": true, "by": true,
		"from": true, "as": true, "into": true, "through": true, "during": true,
		"before": true, "after": true, "above": true, "below": true, "between": true,
		"under": true, "again": true, "further": true, "then": true, "once": true,
		"here": true, "there": true, "when": true, "where": true, "why": true,
		"how": true, "all": true, "each": true, "few": true, "more": true,
		"most": true, "other": true, "some": true, "such": true, "no": true,
		"nor": true, "not": true, "only": true, "own": true, "same": true,
		"so": true, "than": true, "too": true, "very": true, "just": true,
		"also": true, "now": true, "i": true, "me": true, "my": true,
		"we": true, "our": true, "you": true, "your": true, "he": true,
		"him": true, "his": true, "she": true, "her": true, "it": true,
		"its": true, "they": true, "them": true, "their": true, "what": true,
		"which": true, "who": true, "whom": true, "this": true, "that": true,
		"these": true, "those": true, "am": true, "if": true, "because": true,
		"about": true, "against": true, "while": true, "both": true, "any": true,
	}
}
