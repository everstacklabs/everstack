package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestAllocateConstantNamesPreservesPublishedBinding(t *testing.T) {
	cat := catalog{Providers: map[string]provider{
		"qwen": {Models: []model{
			{Name: "qwen2.5-72b-instruct", Status: "deprecated"},
			{Name: "qwen2-5-72b-instruct", Status: "stable"},
		}},
	}}

	got, err := allocateConstantNames(cat, []string{"qwen"})
	if err != nil {
		t.Fatalf("allocateConstantNames() error = %v", err)
	}
	if constant := got[modelRef{providerID: "qwen", modelName: "qwen2.5-72b-instruct"}]; constant != "Qwen_Qwen2572bInstruct" {
		t.Fatalf("published constant = %q, want Qwen_Qwen2572bInstruct", constant)
	}

	modelID := "@qwen/qwen2-5-72b-instruct"
	digest := sha256.Sum256([]byte(modelID))
	want := fmt.Sprintf("Qwen_Qwen2572bInstruct_%X", digest[:4])
	if constant := got[modelRef{providerID: "qwen", modelName: "qwen2-5-72b-instruct"}]; constant != want {
		t.Fatalf("colliding constant = %q, want %q", constant, want)
	}

	again, err := allocateConstantNames(cat, []string{"qwen"})
	if err != nil {
		t.Fatalf("second allocateConstantNames() error = %v", err)
	}
	if again[modelRef{providerID: "qwen", modelName: "qwen2-5-72b-instruct"}] != want {
		t.Fatalf("collision suffix was not deterministic: %#v", again)
	}
}

func TestAllocateConstantNamesRejectsUnmappedCollision(t *testing.T) {
	cat := collisionCatalog("foo")
	_, err := allocateConstantNames(cat, []string{"foo"})
	if err == nil || !strings.Contains(err.Error(), "add its published binding") {
		t.Fatalf("error = %v, want missing-binding error", err)
	}
}

func TestAllocateConstantNamesRejectsStaleBinding(t *testing.T) {
	const base = "Foo_AB"
	preservedConstantBindings[base] = "@foo/missing"
	t.Cleanup(func() { delete(preservedConstantBindings, base) })

	_, err := allocateConstantNames(collisionCatalog("foo"), []string{"foo"})
	if err == nil || !strings.Contains(err.Error(), "does not match any colliding model") {
		t.Fatalf("error = %v, want stale-binding error", err)
	}
}

func collisionCatalog(providerID string) catalog {
	return catalog{Providers: map[string]provider{
		providerID: {Models: []model{{Name: "a-b"}, {Name: "a.b"}}},
	}}
}
