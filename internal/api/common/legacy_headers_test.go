package common

import (
	"net/http"
	"testing"
)

func TestGetHeader_CanonicalWins(t *testing.T) {
	h := http.Header{}
	h.Set(EverstackApiKey, "canonical")   // x-evs-api-key
	h.Set(LegacyMFApiKey, "legacy-mf")    // x-mf-api-key
	h.Set(LegacyEverstackApiKey, "legacy-everstack")

	got := GetHTTPHeader(h, EverstackApiKey, LegacyMFApiKey, LegacyEverstackApiKey)
	if got != "canonical" {
		t.Fatalf("canonical must win when multiple names present: got %q, want %q", got, "canonical")
	}
}

func TestGetHeader_LegacyFallbackInOrder(t *testing.T) {
	tests := []struct {
		name    string
		set     map[string]string
		want    string
	}{
		{
			name: "legacy x-mf only",
			set:  map[string]string{LegacyMFApiKey: "mf-key"},
			want: "mf-key",
		},
		{
			name: "legacy x-everstack only",
			set:  map[string]string{LegacyEverstackApiKey: "everstack-key"},
			want: "everstack-key",
		},
		{
			name: "both legacy present, first legacy in arg order wins",
			set:  map[string]string{LegacyMFApiKey: "mf-key", LegacyEverstackApiKey: "everstack-key"},
			want: "mf-key",
		},
		{
			name: "none present",
			set:  map[string]string{},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.set {
				h.Set(k, v)
			}
			got := GetHTTPHeader(h, EverstackApiKey, LegacyMFApiKey, LegacyEverstackApiKey)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetHeader_CanonicalEmptyFallsThroughToLegacy(t *testing.T) {
	// Pinned semantic: an empty canonical header value is treated as absent and
	// falls through to a populated legacy header. This must NOT be "fixed" into
	// canonical-empty-means-unauthenticated, or a client that sends an empty
	// x-evs-api-key alongside a real x-mf-api-key would break.
	h := http.Header{}
	h.Set(EverstackApiKey, "")      // canonical present but empty
	h.Set(LegacyMFApiKey, "mf-key") // legacy populated

	if got := GetHTTPHeader(h, EverstackApiKey, LegacyMFApiKey); got != "mf-key" {
		t.Fatalf("empty canonical must fall through to populated legacy: got %q, want %q", got, "mf-key")
	}
}

func TestGetHeader_MetadataStyleGetter(t *testing.T) {
	// GetHeader accepts any func(string) string, e.g. a gRPC metadata lookup.
	md := map[string]string{"x-mf-nonce": "n123"}
	get := func(k string) string { return md[k] }

	if got := GetHeader(get, "x-evs-nonce", "x-mf-nonce"); got != "n123" {
		t.Fatalf("metadata-style fallback: got %q, want %q", got, "n123")
	}
	if got := GetHeader(get, "x-evs-missing", "x-mf-missing"); got != "" {
		t.Fatalf("missing header should return empty, got %q", got)
	}
}

func TestCanonicalHeadersUseEvsPrefix(t *testing.T) {
	// Guardrail: the canonical constants must carry the x-evs- prefix so we don't
	// accidentally regress them back to a legacy brand.
	canonical := map[string]string{
		"EverstackApiKey":     EverstackApiKey,
		"EverstackAPIKey":     EverstackAPIKey,
		"EverstackOrgID":      EverstackOrgID,
		"EverstackTenantID":   EverstackTenantID,
		"EverstackUserID":     EverstackUserID,
		"EverstackLicenseKey": EverstackLicenseKey,
		"EverstackMode":       EverstackMode,
	}
	for name, val := range canonical {
		if len(val) < 6 || val[:6] != "x-evs-" {
			t.Errorf("%s = %q, want an x-evs-* canonical name", name, val)
		}
	}
}
