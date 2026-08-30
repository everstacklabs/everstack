package config

import "time"

type ActivationConfig struct {
	PlatformURL  string        `mapstructure:"platform_url" yaml:"platform_url"`
	InstanceSalt string        `mapstructure:"instance_salt" yaml:"instance_salt"`
	Interval     time.Duration `mapstructure:"interval" yaml:"interval"`
	Timeout      time.Duration `mapstructure:"timeout" yaml:"timeout"`
}
