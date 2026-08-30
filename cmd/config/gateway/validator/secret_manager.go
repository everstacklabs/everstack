package validator

// SecretManagerConfig contains runtime secret delivery settings. Provider-
// specific fields continue to be consumed by the existing secret-manager
// subsystem, while storage credentials are typed so they can be injected into
// every service from the same layered configuration snapshot.
type SecretManagerConfig struct {
	Type               string                          `mapstructure:"type"`
	StorageCredentials *StorageCredentialKeyringConfig `mapstructure:"storage_credentials"`
	Vault              *VaultSecretManagerConfig       `mapstructure:"vault"`
}

type StorageCredentialKeyringConfig struct {
	Backend      string `mapstructure:"backend"`
	KeyID        string `mapstructure:"key_id"`
	MasterKey    string `mapstructure:"master_key"`
	PreviousKeys string `mapstructure:"previous_keys"`
	PathPrefix   string `mapstructure:"path_prefix"`
}

type VaultSecretManagerConfig struct {
	Address   string `mapstructure:"address"`
	Token     string `mapstructure:"token"`
	Namespace string `mapstructure:"namespace"`
	MountPath string `mapstructure:"mount_path"`
}
