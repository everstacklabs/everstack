package openai

import "testing"

func TestSanitizeMetadata(t *testing.T) {
	if sanitizeMetadata(nil) != nil { t.Fatal("nil in -> nil out") }
	if sanitizeMetadata(map[string]interface{}{}) != nil { t.Fatal("empty -> nil") }

	// nested object (the bug) becomes a JSON string, not an object
	out := sanitizeMetadata(map[string]interface{}{
		"everstack.source": "playground",
		"nested":           map[string]interface{}{"a": 1},
		"num":              float64(3),
		"drop":             nil,
	})
	if out["everstack.source"] != "playground" { t.Errorf("string passthrough: %#v", out["everstack.source"]) }
	if _, ok := out["nested"].(string); !ok { t.Errorf("nested must be stringified, got %T", out["nested"]) }
	if out["num"] != "3" { t.Errorf("num=%v", out["num"]) }
	if _, ok := out["drop"]; ok { t.Error("nil value should be dropped") }
	for _, v := range out {
		if _, ok := v.(string); !ok { t.Errorf("all values must be strings, got %T", v) }
	}
}
