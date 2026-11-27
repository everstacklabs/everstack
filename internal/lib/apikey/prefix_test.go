package apikey

import "testing"

func TestHasIssuedKeyPrefix(t *testing.T) {
	for token, want := range map[string]bool{
		"sk-mf-svcacct-api01-abc": true,
		"sk-mf-usracct-api01-abc": true,
		"evs_live_abc":            true,
		"sk-mf-":                  false, // prefix alone is not a key
		"evs_live_":               false,
		"eyJhbGciOiJFZERTQSJ9.e":  false, // device-auth JWT
		"evs_dk_abc":              false, // deployment keys have their own path
		"sk-proj-abc":             false,
		"":                        false,
	} {
		if got := HasIssuedKeyPrefix(token); got != want {
			t.Errorf("HasIssuedKeyPrefix(%q) = %v, want %v", token, got, want)
		}
	}
}
