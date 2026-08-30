//go:build !tokenizers
// +build !tokenizers

// Package cache provides caching implementations for the gateway.
package cache

import (
	"fmt"
	"strings"
	"unicode"
)

// Tokenizer defines the interface for text tokenization
type Tokenizer interface {
	Encode(text string) ([]int, error)
	Decode(tokens []int) (string, error)
	TokenCount(text string) int
}

// BPETokenizer implements BPE (Byte Pair Encoding) tokenization
// This is a stub implementation when tokenizers library is not available
type BPETokenizer struct {
	// Stub - no actual tokenizer
}

// NewBPETokenizer creates a new BPE tokenizer from a vocabulary file
// This stub version returns an error indicating the feature is not available
func NewBPETokenizer(vocabFile string) (*BPETokenizer, error) {
	return nil, fmt.Errorf("BPE tokenizer not available: build with -tags tokenizers to enable")
}

// Encode tokenizes text into token IDs (stub implementation)
func (t *BPETokenizer) Encode(text string) ([]int, error) {
	return nil, fmt.Errorf("BPE tokenizer not available: build with -tags tokenizers to enable")
}

// Decode converts token IDs back to text (stub implementation)
func (t *BPETokenizer) Decode(tokens []int) (string, error) {
	return "", fmt.Errorf("BPE tokenizer not available: build with -tags tokenizers to enable")
}

// TokenCount returns the number of tokens in the text (stub implementation)
func (t *BPETokenizer) TokenCount(text string) int {
	// Fallback: approximate by word count
	return len(strings.Fields(text))
}

// SimpleTokenizer implements a basic whitespace-based tokenizer
type SimpleTokenizer struct{}

// NewSimpleTokenizer creates a new simple tokenizer
func NewSimpleTokenizer() *SimpleTokenizer {
	return &SimpleTokenizer{}
}

// Encode tokenizes text into token IDs using simple word splitting
func (t *SimpleTokenizer) Encode(text string) ([]int, error) {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})

	tokens := make([]int, len(words))
	for i, word := range words {
		// Simple hash-based token ID
		tokens[i] = simpleHash(word)
	}

	return tokens, nil
}

// Decode converts token IDs back to text (not reversible with simple tokenizer)
func (t *SimpleTokenizer) Decode(tokens []int) (string, error) {
	return "", fmt.Errorf("simple tokenizer does not support decoding")
}

// TokenCount returns the number of tokens in the text
func (t *SimpleTokenizer) TokenCount(text string) int {
	return len(strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}))
}

// simpleHash creates a simple hash of a string
func simpleHash(s string) int {
	hash := 0
	for _, c := range s {
		hash = hash*31 + int(c)
	}
	return hash
}
