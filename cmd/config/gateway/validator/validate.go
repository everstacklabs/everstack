package validator

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/spf13/viper"
)

// ValidateAndFix runs startup validation on the assembled config. It logs
// warnings for common misconfigurations, and returns an error only for values
// that would make the gateway less secure than the operator asked for: an
// unrecognised auth.mode is refused rather than guessed at.
//
// It may mutate cfg to auto-fix recoverable issues (e.g. generating a random
// session secret when auth.mode=builtin but no secret is configured, or
// normalising the case of auth.mode).
func ValidateAndFix(cfg *Config) error {
	validateDatabase(cfg)
	if err := validateAuth(cfg); err != nil {
		return err
	}
	validateCache(cfg)
	validateSecurity()
	return nil
}

// validateDatabase warns when the Postgres DSN still has the embedded default
// localhost value — a strong signal that the user hasn't configured a real DB.
func validateDatabase(cfg *Config) {
	if cfg.Database == nil {
		return
	}
	dsn := cfg.Database.Postgres.DSN
	if dsn != "" && strings.Contains(dsn, "localhost") {
		logger.Warn("database: using default localhost DSN — set EVS_POSTGRES_DSN for production")
	}
}

// AuthModes lists the accepted values of auth.mode (EVS_AUTH_MODE).
var AuthModes = []string{"none", "builtin", "oidc"}

// NormalizeAuthMode trims and lower-cases an auth.mode value. Empty means the
// documented default, "none". Any other unrecognised value is an error: the
// operator asked for something, and falling back to no authentication would
// silently open the dashboard to anyone.
func NormalizeAuthMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return "none", nil
	}
	for _, valid := range AuthModes {
		if mode == valid {
			return mode, nil
		}
	}
	return "", fmt.Errorf("auth.mode %q is not a recognised authentication mode; set auth.mode (EVS_AUTH_MODE) to one of: %s", raw, strings.Join(AuthModes, ", "))
}

// validateAuth checks auth.mode, normalises it in place so the server sees
// the canonical value, and, when builtin auth is selected, ensures a session
// secret is present (auto-generating one if missing).
func validateAuth(cfg *Config) error {
	if cfg.Auth == nil {
		return nil
	}

	mode, err := NormalizeAuthMode(cfg.Auth.Mode)
	if err != nil {
		return err
	}
	cfg.Auth.Mode = mode
	if mode == "builtin" {
		validateBuiltinAuth(cfg)
	}
	// OIDC validation is handled at connect time.
	return nil
}

// validateBuiltinAuth auto-generates a random session secret when none is
// configured. The secret is ephemeral — sessions won't survive restarts.
func validateBuiltinAuth(cfg *Config) {
	if cfg.Auth.Builtin.SessionSecret != "" {
		return
	}

	secret, err := generateRandomSecret(32)
	if err != nil {
		logger.WithFields("error", err).
			Warn("auth: failed to auto-generate session secret — builtin auth sessions will not work")
		return
	}

	cfg.Auth.Builtin.SessionSecret = secret
	logger.Warn("auth: auto-generated session_secret (sessions won't persist across restarts; set EVS_AUTH_BUILTIN_SESSION_SECRET for production)")
}

// validateCache warns when Redis cache is configured but the address is empty.
func validateCache(cfg *Config) {
	if cfg.Cache == nil {
		return
	}
	if strings.EqualFold(cfg.Cache.Type, "redis") && cfg.Cache.Redis.Address == "" {
		logger.Warn("cache: Redis cache configured but address is empty — set EVS_CACHE_REDIS_ADDRESS")
	}
}

// validateSecurity warns when the API key hash secret still has the baked-in
// default value. We read directly from viper because the security config is
// consumed via viper.GetString at runtime rather than being unmarshalled into
// a struct field.
func validateSecurity() {
	secret := strings.TrimSpace(viper.GetString("server.security.api_key_hash_secret"))
	defaults := []string{
		"change-me-in-production-use-a-random-64-char-hex-string",
		"<random-32-bytes-base64-string>",
		"",
	}
	for _, d := range defaults {
		if secret == d {
			logger.Warn("security: using default api_key_hash_secret — set a random value for production")
			return
		}
	}
}

// generateRandomSecret produces a base64-encoded random secret of the given
// byte length.
func generateRandomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
