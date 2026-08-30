package validator

// BackupConfig is an empty struct that satisfies mapstructure unmarshalling for
// the top-level `backup:` YAML key. Backup configuration is not yet implemented;
// this stub prevents mapstructure from failing on unknown keys when a user
// includes a `backup:` section in their gateway.yaml.
type BackupConfig struct{}
