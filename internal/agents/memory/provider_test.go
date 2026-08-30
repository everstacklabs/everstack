package memory

import "testing"

func TestParseMemoryConfig_Nil(t *testing.T) {
	cfg := ParseMemoryConfig(map[string]interface{}{})
	if cfg != nil {
		t.Error("expected nil for missing memory key")
	}
}

func TestParseMemoryConfig_Disabled(t *testing.T) {
	cfg := ParseMemoryConfig(map[string]interface{}{
		"memory": map[string]interface{}{
			"enabled": false,
		},
	})
	if cfg != nil {
		t.Error("expected nil for disabled memory")
	}
}

func TestParseMemoryConfig_Enabled(t *testing.T) {
	cfg := ParseMemoryConfig(map[string]interface{}{
		"memory": map[string]interface{}{
			"enabled":              true,
			"scope":               "user",
			"auto_retrieve":       true,
			"auto_retrieve_top_k": float64(20),
			"auto_extract":        false,
		},
	})
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Scope != MemoryScopeUser {
		t.Errorf("scope = %q, want user", cfg.Scope)
	}
	if cfg.AutoRetrieveTopK != 20 {
		t.Errorf("topK = %d, want 20", cfg.AutoRetrieveTopK)
	}
	if cfg.AutoExtract != false {
		t.Error("auto_extract should be false")
	}
}

func TestParseMemoryConfig_Collections(t *testing.T) {
	cfg := ParseMemoryConfig(map[string]interface{}{
		"memory": map[string]interface{}{
			"enabled":     true,
			"collections": []interface{}{"col-1", "col-2"},
		},
	})
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.Collections) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(cfg.Collections))
	}
	if cfg.Collections[0] != "col-1" || cfg.Collections[1] != "col-2" {
		t.Errorf("collections = %v, want [col-1, col-2]", cfg.Collections)
	}
}

func TestParseMemoryConfig_Defaults(t *testing.T) {
	cfg := ParseMemoryConfig(map[string]interface{}{
		"memory": map[string]interface{}{
			"enabled": true,
		},
	})
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Scope != MemoryScopeAgent {
		t.Errorf("default scope = %q, want agent", cfg.Scope)
	}
	if cfg.AutoRetrieveTopK != 10 {
		t.Errorf("default topK = %d, want 10", cfg.AutoRetrieveTopK)
	}
	if !cfg.AutoRetrieve {
		t.Error("default auto_retrieve should be true")
	}
	if !cfg.AutoExtract {
		t.Error("default auto_extract should be true")
	}
}
