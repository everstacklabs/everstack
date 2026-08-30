package domain

import "time"

// SessionConfig holds session management settings
type SessionConfig struct {
	Secret     string        `yaml:"secret"`
	CookieName string        `yaml:"cookie_name"`
	MaxAge     time.Duration `yaml:"max_age"`
	Secure     bool          `yaml:"secure"`
	HTTPOnly   bool          `yaml:"http_only"`
	SameSite   string        `yaml:"same_site"`
	// Domain sets the cookie Domain attribute for cross-subdomain sharing.
	// For production: ".everstack.ai" shares across app.everstack.ai and *.everstack.ai
	// For local dev:  ".127.0.0.1.sslip.io" shares across *.127.0.0.1.sslip.io
	// If empty, the cookie is only sent to the exact origin that set it.
	Domain string `yaml:"domain"`
}

// InstanceSignedOutCookie marks a browser that explicitly signed out of *this*
// instance. It is host-only (never parent-domain) so it says nothing about the
// cloud or any sibling instance.
//
// It exists because the browser keeps the cloud's parent-domain session cookie
// after an instance sign-out, and the instance's auth fallbacks accept that
// cookie as proof of identity. Without a marker the very next request
// re-authenticates the user and sign-out reads as a no-op. While the marker is
// present the instance refuses those fallbacks and the SPA bounces the user to
// the cloud, where re-entering the instance goes through the relay. Every path
// that mints an instance session clears the marker (see setSessionCookie), so
// a successful re-entry always wins.
const InstanceSignedOutCookie = "evs_instance_signed_out"

// InternalConfig holds the internal auth service configuration (self-hosted only)
type InternalConfig struct {
	Session SessionConfig `yaml:"session"`
}
