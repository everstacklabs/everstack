package middleware

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// TestVerifySignature_DualReadHeaderNames asserts the M2M verifier accepts the
// signing headers under both the canonical x-evs-* names and the legacy x-mf-*
// names. The HMAC payload excludes header names, so the same signature verifies
// regardless of which header name carries it.
func TestVerifySignature_DualReadHeaderNames(t *testing.T) {
	key := []byte("test-signing-key")
	method := http.MethodPost
	path := "/v1/example"
	body := []byte(`{"hello":"world"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "nonce-123"
	sig := ComputeSignature(method, path, body, timestamp, nonce, key)

	cases := []struct {
		name          string
		sigHeader     string
		tsHeader      string
		nonceHeader   string
	}{
		{"canonical x-evs-*", HeaderSignature, HeaderTimestamp, HeaderNonce},
		{"legacy x-mf-*", legacyHeaderSignature, legacyHeaderTimestamp, legacyHeaderNonce},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set(tc.sigHeader, sig)
			h.Set(tc.tsHeader, timestamp)
			h.Set(tc.nonceHeader, nonce)

			if err := VerifySignature(method, path, body, h, key, time.Minute); err != nil {
				t.Fatalf("VerifySignature(%s) = %v, want nil", tc.name, err)
			}
		})
	}
}

// TestExtractClientIdentity_DualReadHeaderNames asserts client identity is read
// from either the canonical or legacy header names.
func TestExtractClientIdentity_DualReadHeaderNames(t *testing.T) {
	t.Run("canonical instance id", func(t *testing.T) {
		h := http.Header{}
		h.Set(HeaderInstanceID, "inst-1")
		typ, id, err := ExtractClientIdentity(h)
		if err != nil || typ != "gateway" || id != "inst-1" {
			t.Fatalf("got (%q,%q,%v), want (gateway,inst-1,nil)", typ, id, err)
		}
	})
	t.Run("legacy service token", func(t *testing.T) {
		h := http.Header{}
		h.Set(legacyHeaderServiceToken, "svc-token")
		typ, id, err := ExtractClientIdentity(h)
		if err != nil || typ != "service" || id != "svc-token" {
			t.Fatalf("got (%q,%q,%v), want (service,svc-token,nil)", typ, id, err)
		}
	})
}
