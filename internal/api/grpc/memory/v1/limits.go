package v1

import (
	"fmt"

	"github.com/everstacklabs/everstack/internal/memory"
)

const (
	defaultDocumentChunkSize = 512
	minDocumentChunkSize     = 64
	maxDocumentsPerCall      = 100
	maxDocumentContentBytes  = 8 << 20
	maxDocumentChunksPerCall = 4096

	// maxQueryTopK bounds how many results one query may ask for.
	maxQueryTopK = 200
	// maxMemoryRequestBytes bounds a REST request body before it is decoded.
	// It leaves room above maxDocumentContentBytes for JSON escaping and
	// metadata.
	maxMemoryRequestBytes = 16 << 20
)

// clampTopK applies the default and the ceiling to a requested top_k.
func clampTopK(topK int) int {
	if topK <= 0 {
		return 5
	}
	if topK > maxQueryTopK {
		return maxQueryTopK
	}
	return topK
}

type addDocumentsPlan struct {
	chunks [][]string
}

func planAddDocuments(contents []string, requestedChunkSize int) (*addDocumentsPlan, error) {
	if len(contents) > maxDocumentsPerCall {
		return nil, fmt.Errorf("documents per call cannot exceed %d", maxDocumentsPerCall)
	}

	chunkSize := requestedChunkSize
	if chunkSize == 0 {
		chunkSize = defaultDocumentChunkSize
	} else if chunkSize < minDocumentChunkSize {
		return nil, fmt.Errorf("chunk_size must be at least %d", minDocumentChunkSize)
	}

	plan := &addDocumentsPlan{
		chunks: make([][]string, len(contents)),
	}
	totalBytes := 0
	totalChunks := 0
	for i, content := range contents {
		if len(content) > maxDocumentContentBytes-totalBytes {
			return nil, fmt.Errorf("total document content cannot exceed %d bytes per call", maxDocumentContentBytes)
		}
		totalBytes += len(content)

		chunks := memory.ChunkText(content, chunkSize)
		if len(chunks) > maxDocumentChunksPerCall-totalChunks {
			return nil, fmt.Errorf("chunks per call cannot exceed %d", maxDocumentChunksPerCall)
		}
		totalChunks += len(chunks)
		plan.chunks[i] = chunks
	}

	return plan, nil
}
