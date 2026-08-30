package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
)

const envelopeDomain = "everstack/storage-credentials/aes-gcm/v1"

// EnvelopeCipher encrypts credential payloads with a versioned keyring. The
// tenant and opaque reference are authenticated as associated data so copied
// ciphertext cannot be resolved in another tenant or under another reference.
type EnvelopeCipher struct {
	activeKeyID string
	keys        map[string][]byte
}

// NewEnvelopeCipher constructs a versioned AES-256-GCM keyring. Root secrets
// are domain-separated before use as encryption keys. Multiple keys may be
// supplied during rotation, while all new writes use activeKeyID.
func NewEnvelopeCipher(activeKeyID string, rootSecrets map[string]string) (*EnvelopeCipher, error) {
	activeKeyID = strings.TrimSpace(activeKeyID)
	if activeKeyID == "" {
		return nil, fmt.Errorf("storage credential active key id is required")
	}
	if strings.TrimSpace(rootSecrets[activeKeyID]) == "" {
		return nil, fmt.Errorf("storage credential active key is not configured")
	}

	keys := make(map[string][]byte, len(rootSecrets))
	for keyID, rootSecret := range rootSecrets {
		keyID = strings.TrimSpace(keyID)
		rootSecret = strings.TrimSpace(rootSecret)
		if keyID == "" || rootSecret == "" {
			return nil, fmt.Errorf("storage credential keyring contains an empty key id or secret")
		}
		sum := sha256.Sum256([]byte(envelopeDomain + "\x00" + keyID + "\x00" + rootSecret))
		keys[keyID] = sum[:]
	}

	return &EnvelopeCipher{activeKeyID: activeKeyID, keys: keys}, nil
}

func (c *EnvelopeCipher) Seal(tenantID, reference string, plaintext []byte) ([]byte, string, error) {
	if c == nil {
		return nil, "", fmt.Errorf("storage credential cipher is not configured")
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(reference) == "" {
		return nil, "", fmt.Errorf("storage credential tenant and reference are required")
	}

	aead, err := newAEAD(c.keys[c.activeKeyID])
	if err != nil {
		return nil, "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, "", fmt.Errorf("generate storage credential nonce: %w", err)
	}

	ciphertext := aead.Seal(nonce, nonce, plaintext, envelopeAAD(tenantID, reference))
	return ciphertext, c.activeKeyID, nil
}

func (c *EnvelopeCipher) Open(tenantID, reference, keyID string, ciphertext []byte) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("storage credential cipher is not configured")
	}
	key, ok := c.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("storage credential encryption key is unavailable")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < aead.NonceSize() {
		return nil, fmt.Errorf("storage credential ciphertext is invalid")
	}

	nonce := ciphertext[:aead.NonceSize()]
	sealed := ciphertext[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, sealed, envelopeAAD(tenantID, reference))
	if err != nil {
		return nil, fmt.Errorf("storage credential ciphertext authentication failed")
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create storage credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create storage credential AEAD: %w", err)
	}
	return aead, nil
}

func envelopeAAD(tenantID, reference string) []byte {
	return []byte(envelopeDomain + "\x00" + tenantID + "\x00" + reference)
}
