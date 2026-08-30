package otel

import (
	_ "embed"
)

// DefaultConfig is the embedded default OTEL collector configuration
// This logs telemetry to console (good for development/testing)
//
//go:embed defaults/otel-collector-default.yaml
var DefaultConfig string

// ClickHouseConfig is the embedded OTEL collector configuration with ClickHouse export
// This exports telemetry to ClickHouse for storage and analysis
//
//go:embed defaults/otel-collector-clickhouse.yaml
var ClickHouseConfig string

// GetConfig returns the appropriate OTEL collector configuration
// mode can be "default" or "clickhouse"
func GetConfig(mode string) string {
	switch mode {
	case "clickhouse":
		return ClickHouseConfig
	default:
		return DefaultConfig
	}
}
