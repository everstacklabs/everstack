package memory

import (
	"database/sql"
	"testing"
	"time"
)

func TestFormatPgVector_Empty(t *testing.T) {
	got := formatPgVector(nil)
	if got != "[]" {
		t.Fatalf("expected '[]', got %q", got)
	}
}

func TestFormatPgVector_Single(t *testing.T) {
	got := formatPgVector([]float32{1.5})
	if got != "[1.500000]" {
		t.Fatalf("expected '[1.500000]', got %q", got)
	}
}

func TestFormatPgVector_Multiple(t *testing.T) {
	got := formatPgVector([]float32{0.1, 0.2, 0.3})
	expected := "[0.100000,0.200000,0.300000]"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatPgVector_Precision(t *testing.T) {
	got := formatPgVector([]float32{1.0 / 3.0})
	// float32(1/3) ≈ 0.333333
	if got != "[0.333333]" {
		t.Fatalf("expected '[0.333333]', got %q", got)
	}
}

func TestCollectionRow_ToCollection_ValidMetadata(t *testing.T) {
	row := collectionRow{
		ID:                 "id-1",
		TenantID:           "tenant-1",
		Name:               "test",
		Description:        sql.NullString{String: "desc", Valid: true},
		EmbeddingModel:     "text-embedding-3-small",
		EmbeddingDimension: 1536,
		DistanceMetric:     "cosine",
		Metadata:           []byte(`{"key":"value"}`),
		DocumentCount:      5,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	c := row.toCollection()
	if c.ID != "id-1" {
		t.Fatalf("expected ID 'id-1', got %q", c.ID)
	}
	if c.Description != "desc" {
		t.Fatalf("expected Description 'desc', got %q", c.Description)
	}
	if c.Metadata["key"] != "value" {
		t.Fatalf("expected metadata key=value, got %v", c.Metadata)
	}
	if c.DistanceMetric != DistanceCosine {
		t.Fatalf("expected cosine, got %q", c.DistanceMetric)
	}
	if c.DocumentCount != 5 {
		t.Fatalf("expected 5, got %d", c.DocumentCount)
	}
}

func TestCollectionRow_ToCollection_EmptyMetadata(t *testing.T) {
	row := collectionRow{
		ID:       "id-2",
		Name:     "empty-meta",
		Metadata: nil,
	}

	c := row.toCollection()
	if c.Metadata != nil {
		t.Fatalf("expected nil metadata, got %v", c.Metadata)
	}
}

func TestCollectionRow_ToCollection_NullDescription(t *testing.T) {
	row := collectionRow{
		ID:          "id-3",
		Name:        "null-desc",
		Description: sql.NullString{Valid: false},
	}

	c := row.toCollection()
	if c.Description != "" {
		t.Fatalf("expected empty description, got %q", c.Description)
	}
}
