package config

import "time"

// AuthConfig holds configuration for the Auth service
type AuthConfig struct {
	ExternalURL string `mapstructure:"external_url" yaml:"external_url"`
	HTTP        struct {
		Address  string `mapstructure:"address" yaml:"address"`
		Timeouts struct {
			Read  time.Duration `mapstructure:"read" yaml:"read"`
			Write time.Duration `mapstructure:"write" yaml:"write"`
			Idle  time.Duration `mapstructure:"idle" yaml:"idle"`
		} `mapstructure:"timeouts" yaml:"timeouts"`
	} `mapstructure:"http" yaml:"http"`
	Database struct {
		DSN             string        `mapstructure:"dsn" yaml:"dsn"`
		MaxOpenConns    int           `mapstructure:"max_open_conns" yaml:"max_open_conns"`
		MaxIdleConns    int           `mapstructure:"max_idle_conns" yaml:"max_idle_conns"`
		ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" yaml:"conn_max_lifetime"`
	} `mapstructure:"database" yaml:"database"`
	WorkOS struct {
		APIKey       string `mapstructure:"api_key" yaml:"api_key"`
		ClientID     string `mapstructure:"client_id" yaml:"client_id"`
		ClientSecret string `mapstructure:"client_secret" yaml:"client_secret"`
		RedirectURI  string `mapstructure:"redirect_uri" yaml:"redirect_uri"`
	} `mapstructure:"workos" yaml:"workos"`
	Identity struct {
		Enabled       bool   `mapstructure:"enabled" yaml:"enabled"`
		BaseURL       string `mapstructure:"base_url" yaml:"base_url"`
		InternalToken string `mapstructure:"internal_token" yaml:"internal_token"`
	} `mapstructure:"identity" yaml:"identity"`
	Session struct {
		Secret     string        `mapstructure:"secret" yaml:"secret"`
		CookieName string        `mapstructure:"cookie_name" yaml:"cookie_name"`
		MaxAge     time.Duration `mapstructure:"max_age" yaml:"max_age"`
		Secure     bool          `mapstructure:"secure" yaml:"secure"`
		HTTPOnly   bool          `mapstructure:"http_only" yaml:"http_only"`
		SameSite   string        `mapstructure:"same_site" yaml:"same_site"`
		// Domain sets the Set-Cookie Domain attribute so the session
		// cookie is shared across subdomains (e.g. ".everstack.ai").
		Domain string `mapstructure:"domain" yaml:"domain"`
	} `mapstructure:"session" yaml:"session"`
}
