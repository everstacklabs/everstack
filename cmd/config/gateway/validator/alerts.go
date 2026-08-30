package validator

// AlertsConfig is an empty struct that satisfies mapstructure unmarshalling for
// the top-level `alerts:` YAML key. The actual alerts configuration is loaded
// as raw bytes via DefaultConfigs.Alerts and parsed separately by the alerts
// subsystem. This stub prevents mapstructure from failing on unknown keys.
type AlertsConfig struct{}
