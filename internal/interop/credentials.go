package interop

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
)

var ErrCredentialUnavailable = errors.New("interop: credential encryption is not configured")
var ErrCredentialReplacement = errors.New("interop: endpoint/protocol changes require an explicit replacement or cleared credential")

type credentialCipher interface {
	Seal(string, string, []byte) ([]byte, string, error)
	Open(string, string, string, []byte) ([]byte, error)
}

func loadRemoteCipher() (credentialCipher, error) {
	key := strings.TrimSpace(os.Getenv("EVS_INTEROP_CREDENTIAL_MASTER_KEY"))
	if key == "" {
		return nil, nil
	}
	if len(key) < 32 {
		return nil, ErrCredentialUnavailable
	}
	id := strings.TrimSpace(os.Getenv("EVS_INTEROP_CREDENTIAL_KEY_ID"))
	if id == "" {
		id = "v1"
	}
	keys := map[string]string{}
	if raw := os.Getenv("EVS_INTEROP_CREDENTIAL_PREVIOUS_KEYS"); raw != "" {
		if json.Unmarshal([]byte(raw), &keys) != nil {
			return nil, ErrCredentialUnavailable
		}
	}
	keys[id] = key
	for keyID, secret := range keys {
		if strings.TrimSpace(keyID) == "" || len(strings.TrimSpace(secret)) < 32 {
			return nil, ErrCredentialUnavailable
		}
		// Reuse the authenticated envelope implementation with a separate key
		// domain; copying a storage credential can never resolve as an agent token.
		keys[keyID] = "everstack/interop/credentials/v1\x00" + strings.TrimSpace(secret)
	}
	return storagecredentials.NewEnvelopeCipher(id, keys)
}
func credentialReference(name, endpoint, protocol, routing string) string {
	// JSON avoids ambiguous delimiters in user-controlled names/URLs.
	raw, _ := json.Marshal([]string{"a2a", name, endpoint, protocol, routing})
	return string(raw)
}
