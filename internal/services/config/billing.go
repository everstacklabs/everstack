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
	Redis  BillingRedisConfig `mapstructure:"redis" yaml:"redis"`
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

// BillingRedisConfig controls the shared upgrade-session store. Sentinel takes
// precedence over DSN and Address when both Sentinel fields are configured.
type BillingRedisConfig struct {
	Required           bool     `mapstructure:"required" yaml:"required"`
	DSN                string   `mapstructure:"dsn" yaml:"dsn"`
	Address            string   `mapstructure:"address" yaml:"address"`
	Password           string   `mapstructure:"password" yaml:"password"`
	DB                 int      `mapstructure:"db" yaml:"db"`
	SentinelMasterName string   `mapstructure:"sentinel_master_name" yaml:"sentinel_master_name"`
	SentinelAddresses  []string `mapstructure:"sentinel_addresses" yaml:"sentinel_addresses"`
	TLSEnabled         bool     `mapstructure:"tls_enabled" yaml:"tls_enabled"`
	TLSCAFile          string   `mapstructure:"tls_ca_file" yaml:"tls_ca_file"`
	TLSServerName      string   `mapstructure:"tls_server_name" yaml:"tls_server_name"`
}
