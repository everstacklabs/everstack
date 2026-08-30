package config

import "time"

// BillingConfig holds configuration for the Billing service
type BillingConfig struct {
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
	// Redis configuration for upgrade session store (optional)
	// If not configured, falls back to in-memory storage (single-pod mode)
	Redis struct {
		// DSN is the Redis connection string (e.g., redis://localhost:6379/0)
		// Takes precedence over Address if both are provided
		DSN string `mapstructure:"dsn" yaml:"dsn"`
		// Address is the Redis server address (e.g., localhost:6379)
		Address string `mapstructure:"address" yaml:"address"`
		// Password for Redis authentication (optional)
		Password string `mapstructure:"password" yaml:"password"`
		// DB is the Redis database number (default: 0)
		DB int `mapstructure:"db" yaml:"db"`
	} `mapstructure:"redis" yaml:"redis"`
	Stripe struct {
		SecretKey     string `mapstructure:"secret_key" yaml:"secret_key"`
		WebhookSecret string `mapstructure:"webhook_secret" yaml:"webhook_secret"`
		PriceIDs      struct {
			BasicMonthly      string `mapstructure:"basic_monthly" yaml:"basic_monthly"`
			BasicYearly       string `mapstructure:"basic_yearly" yaml:"basic_yearly"`
			ProMonthly        string `mapstructure:"pro_monthly" yaml:"pro_monthly"`
			ProYearly         string `mapstructure:"pro_yearly" yaml:"pro_yearly"`
			EnterpriseMonthly string `mapstructure:"enterprise_monthly" yaml:"enterprise_monthly"`
			EnterpriseYearly  string `mapstructure:"enterprise_yearly" yaml:"enterprise_yearly"`
		} `mapstructure:"price_ids" yaml:"price_ids"`
	} `mapstructure:"stripe" yaml:"stripe"`
	// Portal configuration for Stripe Customer Billing Portal
	Portal struct {
		// BusinessName is displayed in the billing portal header
		BusinessName string `mapstructure:"business_name" yaml:"business_name"`
		// DefaultReturnURL is where customers are redirected after using the portal
		DefaultReturnURL string `mapstructure:"default_return_url" yaml:"default_return_url"`
	} `mapstructure:"portal" yaml:"portal"`
	// PlansConfigPath is the path to the plans.json file for Stripe product sync
	// If empty, defaults to "pkg/plans/plans.json"
	PlansConfigPath string `mapstructure:"plans_config_path" yaml:"plans_config_path"`

	// CloudCallbackPrivateKey is the base64-encoded ed25519 private key for signing
	// JWT callbacks to gateways (for subscription status updates, license release notifications, etc.)
	// This key is shared between billing and license services.
	// The corresponding public key (EVS_CLOUD_PUBLIC_KEY) must be configured on the gateway.
	CloudCallbackPrivateKey string `mapstructure:"cloud_callback_private_key" yaml:"cloud_callback_private_key"`
}
