package config

import "time"

type GatewayDatabase struct {
	DSN             string        `mapstructure:"dsn" yaml:"dsn"`
	MaxOpenConns    int           `mapstructure:"max_open_conns" yaml:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns" yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" yaml:"conn_max_lifetime"`
}

// GatewayCallbackConfig holds configuration for cloud-to-gateway callbacks
type GatewayCallbackConfig struct {
	// CloudPublicKey is the base64-encoded ed25519 public key for verifying
	// signed JWT callbacks from cloud (for automatic license activation)
	CloudPublicKey string `mapstructure:"cloud_public_key" yaml:"cloud_public_key"`
}

type GatewayConfig struct {
	Database GatewayDatabase       `mapstructure:"database" yaml:"database"`
	Callback GatewayCallbackConfig `mapstructure:"callback" yaml:"callback"`
}
