package transport

import "testing"

func TestSecureIssuer(t *testing.T) {
	cases := []struct {
		issuer string
		want   bool
	}{
		{"https://auth.everstack.ai", true},
		{"https://auth.everstack.ai:8443", true},
		{"http://localhost:9000", true},
		{"http://127.0.0.1:9000", true},
		{"http://[::1]:9000", true},
		// A network attacker controlling a plaintext non-loopback issuer could
		// serve forged JWKS and mint accepted id_tokens; these must be refused.
		{"http://auth.everstack.ai", false},
		{"http://attacker.example", false},
		{"ftp://auth.everstack.ai", false},
		{"auth.everstack.ai", false},
		{"", false},
	}
	for _, c := range cases {
		if got := secureIssuer(c.issuer); got != c.want {
			t.Errorf("secureIssuer(%q) = %v, want %v", c.issuer, got, c.want)
		}
	}
}
