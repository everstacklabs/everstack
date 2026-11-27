package telemetry

import "testing"

// Meta's Muse models and Tencent's Hy models are first-party names, so a span
// that carries only the model still attributes to the lab that serves it.
func TestExtractProviderFirstPartyModelNames(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"muse-spark-1.3": "meta",
		"muse-ember-1.0": "meta",
		"hy4":            "tencent",
		"hy4-flash":      "tencent",
		"hy3":            "tencent",
	}
	for model, want := range cases {
		if got := ExtractProvider(model); got != want {
			t.Errorf("ExtractProvider(%q) = %q, want %q", model, got, want)
		}
	}
}
