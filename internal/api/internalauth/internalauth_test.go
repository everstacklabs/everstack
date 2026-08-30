package internalauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyAcceptsOnlyTheProcessToken(t *testing.T) {
	if !Verify(token) {
		t.Fatal("the process token must verify against itself")
	}

	// The whole point of this package is that these do not authenticate.
	for _, guess := range []string{
		"",
		"same-origin",
		"none",
		"0000000000000000000000000000000000000000000000000000000000000000",
		token[:len(token)-1],    // truncated
		token + "0",             // extended
		token[:32] + token[:32], // right length, wrong bytes
	} {
		if Verify(guess) {
			t.Errorf("Verify(%q) must be false", guess)
		}
	}
}

func TestTokenIsNotGuessable(t *testing.T) {
	// 32 bytes hex encoded.
	if len(token) != 64 {
		t.Fatalf("expected a 64 char hex token, got %d chars", len(token))
	}
	// A second generation must not collide, which would indicate a constant
	// or a seeded PRNG rather than crypto/rand.
	if other := generateToken(); other == token {
		t.Fatal("two generated tokens collided; the source is not random")
	}
}

func TestSetHeaderRoundTrips(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if IsInternalRequest(req) {
		t.Fatal("a bare request must not be internal")
	}
	SetHeader(req.Header)
	if !IsInternalRequest(req) {
		t.Fatal("a request stamped by SetHeader must be internal")
	}
	if !IsInternalHeader(req.Header) {
		t.Fatal("IsInternalHeader must agree with IsInternalRequest")
	}
}

func TestSpoofedBrowserHeadersAreNotInternal(t *testing.T) {
	// This is the regression. Sec-Fetch-Site is forbidden to scripts but any
	// non browser client sets it freely, so it was never a credential.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "https://app.everstack.ai")
	req.Header.Set("Referer", "https://app.everstack.ai/")
	req.Header.Set("x-tenant-id", "victim-tenant")

	if IsInternalRequest(req) {
		t.Fatal("spoofable browser headers must never count as an internal call")
	}
}

func TestNilSafety(t *testing.T) {
	if IsInternalRequest(nil) {
		t.Fatal("nil request must not be internal")
	}
	if IsInternalHeader(nil) {
		t.Fatal("nil header must not be internal")
	}
}
