package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// DeriveKey derives a 32-byte encryption key from an instance ID and salt using HKDF-SHA256.
// This provides a deterministic but unique encryption key per instance without requiring
// additional secret management.
//
// Parameters:
//   - instanceID: Unique identifier for this instance (e.g., local_instance_id)
//   - salt: Application-specific salt string (should be hardcoded and versioned)
//
// Returns a 32-byte key suitable for ChaCha20-Poly1305 encryption.
func DeriveKey(instanceID, salt string) ([]byte, error) {
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID cannot be empty")
	}
	if salt == "" {
		return nil, fmt.Errorf("salt cannot be empty")
	}

	// Use HKDF with SHA256 to derive a 32-byte key
	// Info parameter includes the salt for domain separation
	info := []byte(salt)
	reader := hkdf.New(sha256.New, []byte(instanceID), nil, info)
	
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	return key, nil
}

// Encrypt encrypts plaintext using ChaCha20-Poly1305 authenticated encryption.
// Returns a base64-encoded string containing: nonce || ciphertext || tag
//
// Format: base64(12-byte-nonce + encrypted-data + 16-byte-tag)
//
// The nonce is randomly generated for each encryption to ensure semantic security.
// The Poly1305 tag provides authentication, preventing tampering.
func Encrypt(plaintext []byte, key []byte) (string, error) {
	if len(key) != chacha20poly1305.KeySize {
		return "", fmt.Errorf("invalid key size: expected %d bytes, got %d", chacha20poly1305.KeySize, len(key))
	}

	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Generate a random nonce
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and authenticate
	// The result contains: ciphertext || tag
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)

	// Combine nonce + ciphertext for storage
	// Format: nonce || (ciphertext + tag)
	combined := append(nonce, ciphertext...)

	// Base64 encode for text storage
	return base64.StdEncoding.EncodeToString(combined), nil
}

// Decrypt decrypts a base64-encoded ciphertext using ChaCha20-Poly1305.
// Expects the format: base64(nonce || ciphertext || tag)
//
// Returns the plaintext if decryption and authentication succeed.
// Returns an error if the ciphertext has been tampered with or the key is incorrect.
func Decrypt(ciphertext string, key []byte) ([]byte, error) {
	if len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("invalid key size: expected %d bytes, got %d", chacha20poly1305.KeySize, len(key))
	}

	// Decode from base64
	combined, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	nonceSize := aead.NonceSize()
	if len(combined) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short: expected at least %d bytes, got %d", nonceSize, len(combined))
	}

	// Split nonce and ciphertext
	nonce := combined[:nonceSize]
	encryptedData := combined[nonceSize:]

	// Decrypt and verify authentication tag
	plaintext, err := aead.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong key or tampered data): %w", err)
	}

	return plaintext, nil
}
