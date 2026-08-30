package previewtoken

import (
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	s, err := NewSigner([]byte("test-secret-32-bytes-exactly-ok!"))
	if err != nil {
		t.Fatal(err)
	}

	claims := Claims{
		SandboxID: "sbx_abc",
		Subdomain: "xK3p9q2A-3000",
		TenantID:  "ten_123",
		Port:      3000,
	}

	token, err := s.Sign(claims, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	got, err := s.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.SandboxID != claims.SandboxID {
		t.Errorf("SandboxID: got %q want %q", got.SandboxID, claims.SandboxID)
	}
	if got.Port != claims.Port {
		t.Errorf("Port: got %d want %d", got.Port, claims.Port)
	}
}

func TestExpiredToken(t *testing.T) {
	s, err := NewSigner([]byte("test-secret-32-bytes-exactly-ok!"))
	if err != nil {
		t.Fatal(err)
	}
	// Build claims with ExpiresAt already in the past, then sign directly.
	claims := Claims{
		SandboxID: "sbx_abc",
		Subdomain: "xK3-3000",
		TenantID:  "ten_1",
		Port:      3000,
		ExpiresAt: time.Now().Add(-time.Hour).Unix(), // past
	}
	token, err := s.signClaims(claims)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Verify(token)
	if err != ErrTokenExpired {
		t.Errorf("want ErrTokenExpired, got %v", err)
	}
}

func TestTamperedSignature(t *testing.T) {
	s, err := NewSigner([]byte("test-secret-32-bytes-exactly-ok!"))
	if err != nil {
		t.Fatal(err)
	}
	claims := Claims{SandboxID: "sbx_abc", Subdomain: "xK3-3000", TenantID: "ten_1", Port: 3000}
	token, err := s.Sign(claims, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Flip last byte of token
	tokenBytes := []byte(token)
	tokenBytes[len(tokenBytes)-1] ^= 0xFF
	_, err = s.Verify(string(tokenBytes))
	if err != ErrTokenInvalid {
		t.Errorf("want ErrTokenInvalid, got %v", err)
	}
}

func TestWrongSecret(t *testing.T) {
	s1, _ := NewSigner([]byte("secret-one-32-bytes-exactly-pad!!"))
	s2, _ := NewSigner([]byte("secret-two-32-bytes-exactly-pad!!"))
	claims := Claims{SandboxID: "sbx_abc", Subdomain: "xK3-3000", TenantID: "ten_1", Port: 3000}
	token, _ := s1.Sign(claims, time.Hour)
	_, err := s2.Verify(token)
	if err != ErrTokenInvalid {
		t.Errorf("want ErrTokenInvalid, got %v", err)
	}
}

func TestRandomSecretGeneration(t *testing.T) {
	s, err := NewSigner(nil) // nil = generate random
	if err != nil {
		t.Fatal(err)
	}
	claims := Claims{SandboxID: "sbx_abc", Subdomain: "xK3-3000", TenantID: "ten_1", Port: 3000}
	token, err := s.Sign(claims, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := s.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.SandboxID != claims.SandboxID {
		t.Error("SandboxID mismatch")
	}
}
