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
	// CreditPurchase controls self-serve prepaid credit top-ups
	// (docs/design/prepaid-credits-v2.md S3).
	CreditPurchase BillingCreditPurchaseConfig `mapstructure:"credit_purchase" yaml:"credit_purchase"`
	// Dunning is the failed-renewal policy (docs/design/subscription-lifecycle.md).
	Dunning BillingDunningConfig `mapstructure:"dunning" yaml:"dunning"`
	// StripeWebhook keeps the Stripe webhook endpoint subscribed to every
	// event the code handles. Off by default.
	StripeWebhook BillingStripeWebhookConfig `mapstructure:"stripe_webhook" yaml:"stripe_webhook"`
}

// BillingDunningConfig controls what happens when a subscription renewal
// payment fails.
type BillingDunningConfig struct {
	// AccessMode (EVS_SERVICES_BILLING_DUNNING_ACCESS_MODE) is what the org
	// keeps while Stripe retries: "keep_limits_block_spend" (default: plan
	// limits stay, new spend is blocked) or "suspend_immediately" (Starter
	// limits until the payment succeeds).
	AccessMode string `mapstructure:"access_mode" yaml:"access_mode"`
	// GraceDays (EVS_SERVICES_BILLING_DUNNING_GRACE_DAYS) is how long the
	// renewal is retried before the subscription is canceled and the org
	// drops to Starter. 0 means the default, 7.
	GraceDays int `mapstructure:"grace_days" yaml:"grace_days"`
}

// BillingStripeWebhookConfig gates the webhook endpoint sync.
type BillingStripeWebhookConfig struct {
	// EnsureEvents (EVS_SERVICES_BILLING_STRIPE_WEBHOOK_ENSURE_EVENTS) adds
	// any handled event missing from the endpoint at startup. It never
	// removes an event and never creates an endpoint.
	EnsureEvents bool `mapstructure:"ensure_events" yaml:"ensure_events"`
	// EndpointURL (EVS_SERVICES_BILLING_STRIPE_WEBHOOK_ENDPOINT_URL) is the
	// URL of the existing endpoint to update, e.g.
	// https://billing.everstack.ai/webhooks/stripe.
	EndpointURL string `mapstructure:"endpoint_url" yaml:"endpoint_url"`
}

// BillingCreditPurchaseConfig gates the credit purchase flow. Enabled
// (EVS_SERVICES_BILLING_CREDIT_PURCHASE_ENABLED) defaults to false: it hides
// the Buy UI and refuses PurchaseCredits. Turning it on also needs Stripe Tax
// configured on the account (registrations, origin address), because the
// credit Checkout session always enables automatic tax. Webhook handling,
// refunds, disputes and the reconciliation run whatever its value.
type BillingCreditPurchaseConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
	// TaxCode overrides the Stripe product tax code for credit
	// (EVS_SERVICES_BILLING_CREDIT_PURCHASE_TAX_CODE).
	TaxCode string `mapstructure:"tax_code" yaml:"tax_code"`
	// AppBaseURL is the Cloud app origin Checkout returns to
	// (EVS_SERVICES_BILLING_CREDIT_PURCHASE_APP_BASE_URL). Defaults to the
	// portal return URL.
	AppBaseURL string `mapstructure:"app_base_url" yaml:"app_base_url"`
	// AutoTopUp (EVS_SERVICES_BILLING_CREDIT_PURCHASE_AUTO_TOPUP) offers
	// auto top-up and lets its loop charge saved cards off-session
	// (docs/design/auto-top-up.md). Defaults to false: auto top-up charges
	// no tax until prepaid-credits-v2 S7, so leave it off wherever tax must
	// be collected. Needs off-session card payments enabled on the Stripe
	// account and the payment_intent.payment_failed webhook event.
	AutoTopUp bool `mapstructure:"auto_topup" yaml:"auto_topup"`
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
