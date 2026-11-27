package catalog_sync

import (
	"reflect"
	"testing"
)

func TestRemoteCatalogPreservesUnsupportedParameters(t *testing.T) {
	t.Parallel()

	model := convertModelToLegacy("openai", map[string]interface{}{
		"name":                   "gpt-6.2-sol",
		"api_type":               "responses",
		"unsupported_parameters": []interface{}{"temperature", "top_p"},
	}, "")
	if got := model["unsupported_parameters"]; !reflect.DeepEqual(got, []interface{}{"temperature", "top_p"}) {
		t.Fatalf("unsupported_parameters = %#v, want [temperature top_p]", got)
	}

	plain := convertModelToLegacy("openai", map[string]interface{}{"name": "gpt-4o"}, "")
	if _, present := plain["unsupported_parameters"]; present {
		t.Fatal("conversion invented unsupported_parameters for a model without it")
	}
}
