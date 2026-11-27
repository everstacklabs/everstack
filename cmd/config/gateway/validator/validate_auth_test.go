package validator

import (
	"strings"
	"testing"
)

// A mistyped auth.mode used to be rewritten to "none", which starts the
// gateway with the admin dashboard open to anyone. It must refuse to start.
func TestValidateAndFixRejectsUnknownAuthMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"builtni", "BUILT-IN", "password", "true", "off"} {
		cfg := &Config{Auth: &AuthConfig{Mode: mode}}
		err := ValidateAndFix(cfg)
		if err == nil {
			t.Fatalf("ValidateAndFix(auth.mode=%q) error = nil, want a startup error", mode)
		}
		for _, valid := range []string{"none", "builtin", "oidc"} {
			if !strings.Contains(err.Error(), valid) {
				t.Errorf("error %q does not list valid mode %q", err, valid)
			}
		}
		if cfg.Auth.Mode == "none" {
			t.Errorf("auth.mode=%q was downgraded to \"none\"", mode)
		}
	}
}

func TestValidateAndFixAcceptsKnownAuthModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: "none"},
		{in: "   ", want: "none"},
		{in: "none", want: "none"},
		{in: "builtin", want: "builtin"},
		// Case and surrounding space were always tolerated by the validator,
		// but the server compared the raw value, so " Builtin " silently ran
		// with no authentication. The normalised value is what the server sees.
		{in: " Builtin ", want: "builtin"},
		{in: "OIDC", want: "oidc"},
	}
	for _, tt := range tests {
		cfg := &Config{Auth: &AuthConfig{Mode: tt.in, Builtin: AuthBuiltinConfig{SessionSecret: "configured"}}}
		if err := ValidateAndFix(cfg); err != nil {
			t.Fatalf("ValidateAndFix(auth.mode=%q) error = %v", tt.in, err)
		}
		if cfg.Auth.Mode != tt.want {
			t.Errorf("auth.mode %q normalised to %q, want %q", tt.in, cfg.Auth.Mode, tt.want)
		}
	}
}

func TestValidateAndFixWithoutAuthSection(t *testing.T) {
	t.Parallel()

	if err := ValidateAndFix(&Config{}); err != nil {
		t.Fatalf("ValidateAndFix(no auth section) error = %v", err)
	}
}

func TestNormalizeAuthMode(t *testing.T) {
	t.Parallel()

	if got, err := NormalizeAuthMode(""); err != nil || got != "none" {
		t.Fatalf("NormalizeAuthMode(\"\") = (%q, %v), want (\"none\", nil)", got, err)
	}
	if got, err := NormalizeAuthMode(" BuiltIn "); err != nil || got != "builtin" {
		t.Fatalf("NormalizeAuthMode(\" BuiltIn \") = (%q, %v), want (\"builtin\", nil)", got, err)
	}
	if _, err := NormalizeAuthMode("builtni"); err == nil {
		t.Fatal("NormalizeAuthMode(\"builtni\") error = nil, want error")
	}
}
