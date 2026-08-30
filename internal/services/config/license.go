package config

import "time"

type LicenseConfig struct {
	HTTP struct {
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
	Tokens struct {
		AccessTTL     time.Duration `mapstructure:"access_ttl" yaml:"access_ttl"`
		RefreshTTL    time.Duration `mapstructure:"refresh_ttl" yaml:"refresh_ttl"`
		AccessCookie  string        `mapstructure:"access_cookie" yaml:"access_cookie"`
		RefreshCookie string        `mapstructure:"refresh_cookie" yaml:"refresh_cookie"`
	} `mapstructure:"tokens" yaml:"tokens"`
	Events struct {
		DSN string `mapstructure:"dsn" yaml:"dsn"`
	} `mapstructure:"events" yaml:"events"`
	Signing struct {
		Secret string `mapstructure:"secret" yaml:"secret"`
	} `mapstructure:"signing" yaml:"signing"`
	// BillingServiceURL is the URL of the billing service for forwarding usage data
	// When set, the license service forwards usage reports to billing for Stripe meter reporting
	BillingServiceURL string `mapstructure:"billing_service_url" yaml:"billing_service_url"`

	// RateLimit configures rate limiting storage for fingerprint tracking
	// Supports "memory" (default, single instance) or "redis" (horizontal scaling)
	RateLimit RateLimitConfig `mapstructure:"rate_limit" yaml:"rate_limit"`
}

// RateLimitConfig configures rate limiting storage
type RateLimitConfig struct {
	// Type is the storage backend: "memory" or "redis"
	// Default: "memory" (suitable for single instance deployments)
	Type string `mapstructure:"type" yaml:"type"`

	// TTL is how long rate limit entries are kept
	// Default: 24h (fingerprint first-seen tracking)
	TTL time.Duration `mapstructure:"ttl" yaml:"ttl"`

	// KeyPrefix is the Redis key prefix for rate limit data
	// Default: "evs:rl:"
	KeyPrefix string `mapstructure:"key_prefix" yaml:"key_prefix"`

	// Redis configuration (only used when Type is "redis")
	Redis RedisConfig `mapstructure:"redis" yaml:"redis"`
}

// RedisConfig holds Redis connection settings
type RedisConfig struct {
	// DSN is the Redis connection string (e.g., redis://:password@host:port/db)
	// When DSN is provided, it takes precedence over individual settings.
	// Format: redis://[:password@]host[:port][/db]
	DSN string `mapstructure:"dsn" yaml:"dsn"`

	// Address is the Redis server address (host:port)
	// Used when DSN is not provided
	Address string `mapstructure:"address" yaml:"address"`

	// Password for Redis authentication (optional)
	// Used when DSN is not provided
	Password string `mapstructure:"password" yaml:"password"`

	// DB is the Redis database number (0-15)
	// Used when DSN is not provided
	DB int `mapstructure:"db" yaml:"db"`

	// PoolSize is the maximum number of socket connections
	// Default: 10
	PoolSize int `mapstructure:"pool_size" yaml:"pool_size"`

	// MinIdleConns is the minimum number of idle connections
	// Default: 2
	MinIdleConns int `mapstructure:"min_idle_conns" yaml:"min_idle_conns"`

	// MaxRetries is the maximum number of retries before giving up
	// Default: 3
	MaxRetries int `mapstructure:"max_retries" yaml:"max_retries"`

	// DialTimeout is the timeout for establishing new connections
	// Default: 5s
	DialTimeout time.Duration `mapstructure:"dial_timeout" yaml:"dial_timeout"`

	// ReadTimeout is the timeout for socket reads
	// Default: 3s
	ReadTimeout time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`

	// WriteTimeout is the timeout for socket writes
	// Default: 3s
	WriteTimeout time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
}
