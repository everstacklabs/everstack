package memory

import (
	"testing"
)

func TestNilIfEmpty_Empty(t *testing.T) {
	result := nilIfEmpty("")
	if result != nil {
		t.Fatalf("expected nil for empty string, got %v", result)
	}
}

func TestNilIfEmpty_NonEmpty(t *testing.T) {
	result := nilIfEmpty("hello")
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string type, got %T", result)
	}
	if s != "hello" {
		t.Fatalf("expected 'hello', got %q", s)
	}
}

func TestNilIfEmpty_Whitespace(t *testing.T) {
	// Whitespace is not empty — only "" is nil
	result := nilIfEmpty(" ")
	if result == nil {
		t.Fatal("expected non-nil for whitespace string")
	}
}

func TestNewRequestLogger_NilDB(t *testing.T) {
	// Should not panic when created with nil DB.
	logger := NewRequestLogger(nil)
	if logger == nil {
		t.Fatal("expected non-nil RequestLogger")
	}
	if logger.db != nil {
		t.Fatal("expected nil db on logger")
	}
}
