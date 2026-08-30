package v1

import (
	"testing"
)

func TestModelFreshnessUsesCatalogAdditionMetadata(t *testing.T) {
	t.Parallel()

	if got := modelFreshness("2.4.0"); got != "new" {
		t.Fatalf("latest catalog addition freshness = %q, want new", got)
	}
	if got := modelFreshness(""); got != "stable" {
		t.Fatalf("existing model freshness = %q, want stable", got)
	}
}
