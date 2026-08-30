package v1

import "testing"

func TestParseGeneratedDatasetItemsValidArray(t *testing.T) {
	items, err := parseGeneratedDatasetItems(`[{"input":{"prompt":"hello"},"expected_output":{"answer":"world"}}]`)
	if err != nil {
		t.Fatalf("parseGeneratedDatasetItems returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	input, ok := items[0].Input.(map[string]interface{})
	if !ok || input["prompt"] != "hello" {
		t.Fatalf("unexpected input: %#v", items[0].Input)
	}
}

func TestParseGeneratedDatasetItemsStripsFence(t *testing.T) {
	items, err := parseGeneratedDatasetItems("```json\n[{\"input\":{\"prompt\":\"hello\"},\"expected_output\":{\"answer\":\"world\"}}]\n```")
	if err != nil {
		t.Fatalf("parseGeneratedDatasetItems returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}

func TestParseGeneratedDatasetItemsMalformed(t *testing.T) {
	if _, err := parseGeneratedDatasetItems(`{"items":`); err == nil {
		t.Fatal("expected malformed JSON to return an error")
	}
}
