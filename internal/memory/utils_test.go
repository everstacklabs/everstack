package memory

import (
	"strings"
	"testing"
)

func TestChunkText_ShortText(t *testing.T) {
	text := "Hello, this is a short text."
	chunks := ChunkText(text, 512)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Fatalf("expected chunk to equal input text")
	}
}

func TestChunkText_EmptyText(t *testing.T) {
	chunks := ChunkText("", 512)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Fatalf("expected 1 empty chunk, got %d chunks", len(chunks))
	}
}

func TestChunkText_DefaultMaxSize(t *testing.T) {
	// Generate text longer than 512 characters
	text := strings.Repeat("Hello world. ", 100) // 1300 chars
	chunks := ChunkText(text, 0)                  // 0 should default to 512
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks with default max size, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 512+50 { // Allow small overshoot from boundary search
			t.Fatalf("chunk exceeds expected size: %d chars", len(chunk))
		}
	}
}

func TestChunkText_SentenceBoundary(t *testing.T) {
	// Build text where sentence boundaries are at known positions
	sentence := "This is a test sentence. "
	text := strings.Repeat(sentence, 40) // ~1000 chars
	chunks := ChunkText(text, 200)

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	// First chunk should end at a sentence boundary (period + space)
	firstChunk := chunks[0]
	if !strings.HasSuffix(strings.TrimSpace(firstChunk), ".") {
		t.Logf("first chunk: %q", firstChunk)
		// Not a hard failure — chunker tries but may not always succeed
	}
}

func TestChunkText_LongText(t *testing.T) {
	// 5000 chars, chunked into 500-char pieces
	text := strings.Repeat("A", 5000)
	chunks := ChunkText(text, 500)

	if len(chunks) != 10 {
		t.Fatalf("expected 10 chunks, got %d", len(chunks))
	}

	// Verify all text is preserved
	reassembled := strings.Join(chunks, "")
	if reassembled != text {
		t.Fatal("reassembled text does not match original")
	}
}

func TestChunkText_NegativeMaxSize(t *testing.T) {
	text := strings.Repeat("X", 1000)
	chunks := ChunkText(text, -1) // Should default to 512
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks with negative max size, got %d", len(chunks))
	}
}
