package catalog

import "testing"

func TestDeprecatedPublishedModelConstantsRemainCompatible(t *testing.T) {
	legacyIDs := map[string]string{
		Openai_GPT4:            "deprecated",
		AzureOpenai_GPT41:      "deprecated",
		Qwen_Qwen2572bInstruct: "stable",
	}
	for modelID, wantStatus := range legacyIDs {
		if !IsValid(modelID) {
			t.Errorf("published model ID %q disappeared from the generated catalog", modelID)
		}
		metadata, ok := GetMetadata(modelID)
		if !ok || metadata.Status != wantStatus {
			t.Errorf("metadata for %q = %#v, %v; want status %q", modelID, metadata, ok, wantStatus)
		}
	}
}
