package repository

import (
	"regexp"
	"testing"
)

func TestHashToken(t *testing.T) {
	// printf abc | shasum -a 256
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := hashToken("abc"); got != want {
		t.Fatalf("hashToken(abc) = %s, want %s", got, want)
	}
	raw, err := generateMagicToken()
	if err != nil {
		t.Fatal(err)
	}
	if h := hashToken(raw); h == raw || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) {
		t.Fatalf("hashToken(%q) = %q, want 64 lowercase hex different from the raw token", raw, h)
	}
}
