// Package cache provides caching implementations for the gateway.
package cache

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"unicode"
)

// MinHashConfig configures the MinHash signature generator.
type MinHashConfig struct {
	// NumHashes is the number of hash functions to use (more = more accurate, slower)
	// Typical values: 64, 128, 256
	NumHashes int

	// ShingleSize is the number of words per shingle (n-gram size)
	// Typical values: 2-4
	ShingleSize int

	// SimilarityThreshold is the minimum Jaccard similarity to consider a match
	// Range: 0.0 to 1.0 (0.8 = 80% similar)
	SimilarityThreshold float64
}

// DefaultMinHashConfig returns sensible defaults for semantic caching.
// Note: MinHash is best suited for near-duplicate detection (typos, rewordings).
// For true semantic similarity, consider using embedding-based approaches.
func DefaultMinHashConfig() MinHashConfig {
	return MinHashConfig{
		NumHashes:           128,
		ShingleSize:         2,    // 2-grams work better for short queries
		SimilarityThreshold: 0.25, // 25% Jaccard similarity for better recall on short queries
	}
}

// MinHasher generates MinHash signatures for text similarity comparison.
// MinHash is a probabilistic algorithm for estimating Jaccard similarity
// between sets (in this case, sets of word shingles).
type MinHasher struct {
	numHashes   int
	shingleSize int
	threshold   float64

	// Pre-computed hash coefficients for reproducible hashing
	// Each hash function is: h(x) = (a*x + b) mod prime
	coeffA []uint64
	coeffB []uint64
	prime  uint64
}

// NewMinHasher creates a new MinHash signature generator.
func NewMinHasher(cfg MinHashConfig) *MinHasher {
	if cfg.NumHashes <= 0 {
		cfg.NumHashes = 128
	}
	if cfg.ShingleSize <= 0 {
		cfg.ShingleSize = 3
	}
	if cfg.SimilarityThreshold <= 0 || cfg.SimilarityThreshold > 1 {
		cfg.SimilarityThreshold = 0.85
	}

	m := &MinHasher{
		numHashes:   cfg.NumHashes,
		shingleSize: cfg.ShingleSize,
		threshold:   cfg.SimilarityThreshold,
		prime:       4294967311, // Large prime > 2^32
		coeffA:      make([]uint64, cfg.NumHashes),
		coeffB:      make([]uint64, cfg.NumHashes),
	}

	// Generate deterministic hash coefficients
	// Using FNV hash of index for reproducibility
	for i := 0; i < cfg.NumHashes; i++ {
		h := fnv.New64a()
		h.Write([]byte{byte(i), byte(i >> 8), 'a'})
		m.coeffA[i] = h.Sum64()

		h.Reset()
		h.Write([]byte{byte(i), byte(i >> 8), 'b'})
		m.coeffB[i] = h.Sum64()
	}

	return m
}

// preprocessQuery normalizes the query for better matching.
// It lowercases, removes punctuation, and normalizes whitespace.
func preprocessQuery(text string) string {
	// 1. Lowercase for case-insensitive matching
	text = strings.ToLower(text)

	// 2. Remove punctuation (keep letters, numbers, spaces)
	text = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) {
			return r
		}
		return -1
	}, text)

	// 3. Normalize whitespace
	text = strings.Join(strings.Fields(text), " ")

	return text
}

// Signature computes the MinHash signature for a text string.
// The signature is a fixed-size array of hash values that can be used
// to estimate similarity without storing the original text.
func (m *MinHasher) Signature(text string) []uint64 {
	// Preprocess query for better matching
	text = preprocessQuery(text)

	// Normalize and tokenize
	tokens := m.tokenize(text)
	if len(tokens) == 0 {
		return make([]uint64, m.numHashes)
	}

	// Generate shingles (n-grams of words)
	shingles := m.shingle(tokens)
	if len(shingles) == 0 {
		return make([]uint64, m.numHashes)
	}

	// Compute MinHash signature
	sig := make([]uint64, m.numHashes)
	for i := range sig {
		sig[i] = math.MaxUint64
	}

	for _, shingle := range shingles {
		// Hash the shingle
		h := fnv.New64a()
		h.Write([]byte(shingle))
		shingleHash := h.Sum64()

		// Apply each hash function and keep minimum
		for i := 0; i < m.numHashes; i++ {
			hashVal := (m.coeffA[i]*shingleHash + m.coeffB[i]) % m.prime
			if hashVal < sig[i] {
				sig[i] = hashVal
			}
		}
	}

	return sig
}

// EstimateSimilarity estimates the Jaccard similarity between two signatures.
// Returns a value between 0.0 (completely different) and 1.0 (identical).
func (m *MinHasher) EstimateSimilarity(sig1, sig2 []uint64) float64 {
	if len(sig1) != len(sig2) || len(sig1) == 0 {
		return 0
	}

	matches := 0
	for i := range sig1 {
		if sig1[i] == sig2[i] {
			matches++
		}
	}

	return float64(matches) / float64(len(sig1))
}

// IsSimilar returns true if the estimated similarity exceeds the threshold.
func (m *MinHasher) IsSimilar(sig1, sig2 []uint64) bool {
	return m.EstimateSimilarity(sig1, sig2) >= m.threshold
}

// Threshold returns the configured similarity threshold.
func (m *MinHasher) Threshold() float64 {
	return m.threshold
}

// tokenize converts text to lowercase tokens, removing punctuation and stopwords.
func (m *MinHasher) tokenize(text string) []string {
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
			if !isStopword(token) && len(token) > 1 {
				tokens = append(tokens, token)
			}
			current.Reset()
		}
	}

	// Don't forget the last token
	if current.Len() > 0 {
		token := current.String()
		if !isStopword(token) && len(token) > 1 {
			tokens = append(tokens, token)
		}
	}

	return tokens
}

// shingle generates n-grams of words from tokens.
// It includes individual words (unigrams) to handle word order variations,
// plus n-grams for phrase matching.
func (m *MinHasher) shingle(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}

	// Estimate capacity: unigrams + bigrams + n-grams
	capacity := len(tokens) * 2
	shingles := make([]string, 0, capacity)

	// Always include individual words (unigrams) for word overlap detection
	// This helps with word order variations like "capital of France" vs "France's capital"
	for _, token := range tokens {
		shingles = append(shingles, token)
	}

	// Add bigrams (2-grams) for phrase detection
	if len(tokens) >= 2 {
		for i := 0; i < len(tokens)-1; i++ {
			shingles = append(shingles, tokens[i]+" "+tokens[i+1])
		}
	}

	// Add configured n-grams (if shingleSize > 2)
	if m.shingleSize > 2 && len(tokens) >= m.shingleSize {
		for i := 0; i <= len(tokens)-m.shingleSize; i++ {
			shingle := strings.Join(tokens[i:i+m.shingleSize], " ")
			shingles = append(shingles, shingle)
		}
	}

	return shingles
}

// Common English stopwords to filter out
var stopwords = map[string]bool{
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

func isStopword(word string) bool {
	return stopwords[word]
}

// LSHIndex provides Locality Sensitive Hashing for fast similarity search.
// It divides signatures into bands and uses hash tables for each band.
type LSHIndex struct {
	numBands    int
	rowsPerBand int
	buckets     []map[uint64][]int // band -> hash -> entry indices
}

// NewLSHIndex creates a new LSH index for fast similarity search.
// The number of bands controls the trade-off between precision and recall.
// More bands = higher recall (finds more similar items) but more false positives.
func NewLSHIndex(numHashes int, numBands int) *LSHIndex {
	if numBands <= 0 || numBands > numHashes {
		// Default: sqrt(numHashes) bands gives good balance
		numBands = int(math.Sqrt(float64(numHashes)))
		if numBands < 1 {
			numBands = 1
		}
	}

	rowsPerBand := numHashes / numBands

	buckets := make([]map[uint64][]int, numBands)
	for i := range buckets {
		buckets[i] = make(map[uint64][]int)
	}

	return &LSHIndex{
		numBands:    numBands,
		rowsPerBand: rowsPerBand,
		buckets:     buckets,
	}
}

// Add adds a signature to the index with the given entry index.
func (l *LSHIndex) Add(sig []uint64, entryIndex int) {
	for band := 0; band < l.numBands; band++ {
		start := band * l.rowsPerBand
		end := start + l.rowsPerBand
		if end > len(sig) {
			end = len(sig)
		}

		// Hash the band portion of the signature
		bandHash := l.hashBand(sig[start:end])
		l.buckets[band][bandHash] = append(l.buckets[band][bandHash], entryIndex)
	}
}

// Query finds candidate entry indices that might be similar to the query signature.
// The returned indices should be verified with actual similarity computation.
func (l *LSHIndex) Query(sig []uint64) []int {
	candidateSet := make(map[int]bool)

	for band := 0; band < l.numBands; band++ {
		start := band * l.rowsPerBand
		end := start + l.rowsPerBand
		if end > len(sig) {
			end = len(sig)
		}

		bandHash := l.hashBand(sig[start:end])
		if indices, ok := l.buckets[band][bandHash]; ok {
			for _, idx := range indices {
				candidateSet[idx] = true
			}
		}
	}

	// Convert set to sorted slice
	candidates := make([]int, 0, len(candidateSet))
	for idx := range candidateSet {
		candidates = append(candidates, idx)
	}
	sort.Ints(candidates)

	return candidates
}

// Remove removes an entry from the index.
// Note: This is O(n) where n is the number of entries in affected buckets.
func (l *LSHIndex) Remove(sig []uint64, entryIndex int) {
	for band := 0; band < l.numBands; band++ {
		start := band * l.rowsPerBand
		end := start + l.rowsPerBand
		if end > len(sig) {
			end = len(sig)
		}

		bandHash := l.hashBand(sig[start:end])
		if indices, ok := l.buckets[band][bandHash]; ok {
			// Remove the entry index
			newIndices := make([]int, 0, len(indices)-1)
			for _, idx := range indices {
				if idx != entryIndex {
					newIndices = append(newIndices, idx)
				}
			}
			if len(newIndices) == 0 {
				delete(l.buckets[band], bandHash)
			} else {
				l.buckets[band][bandHash] = newIndices
			}
		}
	}
}

// Clear removes all entries from the index.
func (l *LSHIndex) Clear() {
	for i := range l.buckets {
		l.buckets[i] = make(map[uint64][]int)
	}
}

// hashBand computes a hash for a portion of the signature.
func (l *LSHIndex) hashBand(band []uint64) uint64 {
	h := fnv.New64a()
	for _, v := range band {
		b := []byte{
			byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24),
			byte(v >> 32), byte(v >> 40), byte(v >> 48), byte(v >> 56),
		}
		h.Write(b)
	}
	return h.Sum64()
}
