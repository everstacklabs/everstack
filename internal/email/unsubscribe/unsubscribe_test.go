package unsubscribe

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestTokenRoundTrip(t *testing.T) {
	secret := strings.Repeat("s", 32)
	for _, c := range []struct{ name, email, want, userID string }{
		{"round trip", "ada@example.com", "ada@example.com", "user-1"},
		{"normalization", "  ADA@Example.COM \n", "ada@example.com", "user-1"},
		{"no user id", "ada@example.com", "ada@example.com", ""},
		{"boundary", strings.Repeat("a", 308) + "@example.com", strings.Repeat("a", 308) + "@example.com", "user-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			token, err := Token(secret, c.email, c.userID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(token, "=") {
				t.Fatal("token uses padded base64")
			}
			email, userID, err := Verify([]string{secret}, token)
			if err != nil || email != c.want || userID != c.userID {
				t.Fatalf("Verify = %q, %q, %v", email, userID, err)
			}
		})
	}
}

func TestVerifyRejectsTamperingAndAcceptsPreviousSecret(t *testing.T) {
	secret, previous := strings.Repeat("s", 32), strings.Repeat("p", 32)
	token, err := Token(previous, "ada@example.com", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	for i, value := range []string{"v2", base64.RawURLEncoding.EncodeToString([]byte("other@example.com")), base64.RawURLEncoding.EncodeToString([]byte("user-2")), base64.RawURLEncoding.EncodeToString(make([]byte, 32))} {
		tampered := append([]string(nil), parts...)
		tampered[i] = value
		if _, _, err := Verify([]string{previous}, strings.Join(tampered, ".")); err == nil {
			t.Errorf("segment %d accepted after tampering", i)
		}
	}
	for _, c := range []struct {
		name    string
		secrets []string
		valid   bool
	}{
		{"wrong secret", []string{secret}, false},
		{"previous secret", []string{secret, previous}, true},
		{"empty keys ignored", []string{"", previous, ""}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			email, userID, err := Verify(c.secrets, token)
			if (err == nil) != c.valid {
				t.Fatalf("Verify error = %v, valid = %v", err, c.valid)
			}
			if c.valid && (email != "ada@example.com" || userID != "user-1") {
				t.Fatalf("Verify = %q, %q", email, userID)
			}
		})
	}
	for _, malformed := range []string{"", "v1", token + ".extra", token + "=", strings.Replace(token, ".", ".\n", 1), "v1...", "v1.!.!.!"} {
		if _, _, err := Verify([]string{previous}, malformed); err == nil {
			t.Errorf("accepted malformed token %q", malformed)
		}
	}
}

func TestTokenRejectsInvalidInputs(t *testing.T) {
	for _, secret := range []string{"", "short", strings.Repeat("s", 31)} {
		if _, err := Token(secret, "ada@example.com", "user-1"); !errors.Is(err, ErrSecretTooShort) {
			t.Errorf("Token short secret error = %v", err)
		}
		if _, _, err := Verify([]string{secret}, "invalid"); !errors.Is(err, ErrSecretTooShort) {
			t.Errorf("Verify short secret error = %v", err)
		}
	}
	for _, email := range []string{"", " \n ", strings.Repeat("a", 321)} {
		if _, err := Token(strings.Repeat("s", 32), email, "user-1"); err == nil {
			t.Error("accepted invalid email")
		}
	}
}

func TestURL(t *testing.T) {
	for _, c := range []struct {
		base  string
		valid bool
	}{
		{"https://app.example.com/email/unsubscribe?source=note&t=old", true},
		{"http://localhost:8080/email/unsubscribe", true},
		{"http://127.0.0.1:8080/email/unsubscribe", true},
		{"http://app.example.com/email/unsubscribe", false},
		{"http://localhost.evil.test/email/unsubscribe", false},
		{"/email/unsubscribe", false},
		{"//app.example.com/email/unsubscribe", false},
		{"https:///email/unsubscribe", false},
		{"https://%", false},
		{"javascript:alert(1)", false},
	} {
		t.Run(c.base, func(t *testing.T) {
			got, err := URL(c.base, "a+b&c=d?e")
			if (err == nil) != c.valid {
				t.Fatalf("URL = %q, %v", got, err)
			}
			if c.valid {
				u, err := url.Parse(got)
				if err != nil || u.Query().Get("t") != "a+b&c=d?e" {
					t.Fatalf("token not escaped: %q (%v)", got, err)
				}
				if strings.Contains(c.base, "source=") && u.Query().Get("source") != "note" {
					t.Fatal("existing query was lost")
				}
			}
		})
	}
	h := Headers("https://example.com/unsubscribe?t=token")
	if h["List-Unsubscribe"] != "<https://example.com/unsubscribe?t=token>" || h["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers = %v", h)
	}
}

func TestVerifyIgnoresShortPreviousSecret(t *testing.T) {
	secret := strings.Repeat("s", 32)
	token, err := Token(secret, "sam@example.test", "person")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = Verify([]string{secret, "short"}, token); err != nil {
		t.Fatal(err)
	}
}
