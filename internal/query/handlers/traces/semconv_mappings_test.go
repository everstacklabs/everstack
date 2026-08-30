package traces

import (
	"strings"
	"testing"
)

func TestWithExtra_FiltersUnsafeKeys(t *testing.T) {
	base := []string{"llm.model"}
	got := withExtra(base, []string{"my.model", "x'] OR '1'='1", "has space", "ok_key.2"})
	// base preserved + only safe extras appended
	want := []string{"llm.model", "my.model", "ok_key.2"}
	if len(got) != len(want) {
		t.Fatalf("withExtra = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("withExtra[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWithExtra_EmptyReturnsBase(t *testing.T) {
	base := []string{"a", "b"}
	if got := withExtra(base, nil); len(got) != 2 {
		t.Fatalf("withExtra(base, nil) = %v, want base", got)
	}
}

func TestModelSQL_AppendsExtra(t *testing.T) {
	plain := modelSQL()
	withTenant := modelSQL("my_app.model_id")
	if !strings.Contains(withTenant, "my_app.model_id") {
		t.Fatalf("modelSQL(extra) = %q, want it to include the tenant key", withTenant)
	}
	if strings.Contains(plain, "my_app.model_id") {
		t.Fatalf("modelSQL() should not include a tenant key")
	}
	// An unsafe extra is dropped, leaving the fragment identical to plain.
	if got := modelSQL("bad'; drop"); got != plain {
		t.Fatalf("modelSQL(unsafe) = %q, want identical to plain %q", got, plain)
	}
}
