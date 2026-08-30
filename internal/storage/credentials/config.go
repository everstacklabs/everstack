package credentials

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

const (
	credentialKeyIDEnv          = "EVS_STORAGE_CREDENTIAL_KEY_ID"
	credentialMasterKeyEnv      = "EVS_STORAGE_CREDENTIAL_MASTER_KEY"
	credentialPreviousKeyEnv    = "EVS_STORAGE_CREDENTIAL_PREVIOUS_KEYS"
	credentialKeyIDConfig       = "secret_manager.storage_credentials.key_id"
	credentialMasterKeyConfig   = "secret_manager.storage_credentials.master_key"
	credentialPreviousKeyConfig = "secret_manager.storage_credentials.previous_keys"
)

// KeyringConfig selects the active envelope key while retaining previous keys
// long enough to rotate existing ciphertext without downtime.
type KeyringConfig struct {
	ActiveKeyID  string
	ActiveSecret string
	PreviousKeys map[string]string
}

func LoadKeyringConfig() (KeyringConfig, error) {
	keyID, err := keyringConfigValue(credentialKeyIDConfig, credentialKeyIDEnv)
	if err != nil {
		return KeyringConfig{}, err
	}
	activeSecret, err := keyringConfigValue(credentialMasterKeyConfig, credentialMasterKeyEnv)
	if err != nil {
		return KeyringConfig{}, err
	}
	rawPreviousKeys, err := keyringConfigValue(credentialPreviousKeyConfig, credentialPreviousKeyEnv)
	if err != nil {
		return KeyringConfig{}, err
	}
	return NewKeyringConfig(keyID, activeSecret, rawPreviousKeys)
}

// NewKeyringConfig validates the typed values produced by the layered gateway
// configuration loader without consulting process-global state.
func NewKeyringConfig(keyID, activeSecret, rawPreviousKeys string) (KeyringConfig, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		keyID = "v1"
	}
	activeSecret = strings.TrimSpace(activeSecret)
	if activeSecret == "" {
		return KeyringConfig{}, fmt.Errorf("storage credential master key is not configured")
	}
	if len([]byte(activeSecret)) < 32 {
		return KeyringConfig{}, fmt.Errorf("storage credential master key must be at least 32 bytes")
	}

	previousKeys := map[string]string{}
	if raw := strings.TrimSpace(rawPreviousKeys); raw != "" {
		if err := json.Unmarshal([]byte(raw), &previousKeys); err != nil {
			return KeyringConfig{}, fmt.Errorf("parse storage credential previous keys: %w", err)
		}
	}
	for previousKeyID, previousSecret := range previousKeys {
		if strings.TrimSpace(previousKeyID) == "" || len([]byte(strings.TrimSpace(previousSecret))) < 32 {
			return KeyringConfig{}, fmt.Errorf("storage credential previous key %q must be at least 32 bytes", previousKeyID)
		}
	}
	delete(previousKeys, keyID)

	return KeyringConfig{
		ActiveKeyID: keyID, ActiveSecret: activeSecret, PreviousKeys: previousKeys,
	}, nil
}

func keyringConfigValue(configKey, envKey string) (string, error) {
	if err := viper.BindEnv(configKey, envKey); err != nil {
		return "", fmt.Errorf("bind storage credential configuration: %w", err)
	}
	return strings.TrimSpace(viper.GetString(configKey)), nil
}

func NewEnvelopeCipherFromConfig(config KeyringConfig) (*EnvelopeCipher, error) {
	keys := make(map[string]string, len(config.PreviousKeys)+1)
	for keyID, secret := range config.PreviousKeys {
		keys[keyID] = secret
	}
	keys[config.ActiveKeyID] = config.ActiveSecret
	return NewEnvelopeCipher(config.ActiveKeyID, keys)
}
