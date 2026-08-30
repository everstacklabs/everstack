package memory

import (
	"context"
	"fmt"
	"testing"
)

// mockTestEmbedder implements EmbedderInterface for testing.
type mockTestEmbedder struct {
	dimension int
	callCount int
}

func (m *mockTestEmbedder) Embed(_ context.Context, _, text string) ([]float32, error) {
	m.callCount++
	vec := make([]float32, m.dimension)
	// Use text length as a distinguishing value
	if len(text) > 0 && m.dimension > 0 {
		vec[0] = float32(len(text))
	}
	return vec, nil
}

func (m *mockTestEmbedder) EmbedBatch(ctx context.Context, model string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := m.Embed(ctx, model, text)
		if err != nil {
			return nil, err
		}
		result[i] = vec
	}
	return result, nil
}

// errorEmbedder always returns an error.
type errorEmbedder struct{}

func (e *errorEmbedder) Embed(_ context.Context, _, _ string) ([]float32, error) {
	return nil, fmt.Errorf("embedding failed")
}

func (e *errorEmbedder) EmbedBatch(ctx context.Context, model string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := e.Embed(ctx, model, text)
		if err != nil {
			return nil, err
		}
		result[i] = vec
	}
	return result, nil
}

func TestEmbedBatch_Ordering(t *testing.T) {
	emb := &mockTestEmbedder{dimension: 4}
	texts := []string{"a", "bb", "ccc", "dddd", "eeeee"}

	results, err := emb.EmbedBatch(context.Background(), "model", texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != len(texts) {
		t.Fatalf("expected %d results, got %d", len(texts), len(results))
	}

	// Verify order is preserved: each result's first element should be the text length
	for i, text := range texts {
		expected := float32(len(text))
		if results[i][0] != expected {
			t.Fatalf("result[%d][0] = %f, expected %f (text=%q)", i, results[i][0], expected, text)
		}
	}
}

func TestEmbedBatch_Empty(t *testing.T) {
	emb := &mockTestEmbedder{dimension: 4}
	results, err := emb.EmbedBatch(context.Background(), "model", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestEmbedBatch_SingleText(t *testing.T) {
	emb := &mockTestEmbedder{dimension: 8}
	results, err := emb.EmbedBatch(context.Background(), "model", []string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0]) != 8 {
		t.Fatalf("expected dimension 8, got %d", len(results[0]))
	}
}

func TestEmbedBatch_Error(t *testing.T) {
	emb := &errorEmbedder{}
	_, err := emb.EmbedBatch(context.Background(), "model", []string{"test"})
	if err == nil {
		t.Fatal("expected error from embedder")
	}
}
