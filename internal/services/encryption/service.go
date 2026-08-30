package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// Service provides encryption and decryption for sensitive data
type Service struct {
	key []byte
}

// NewService creates a new encryption service
// The secret should be from server.security.api_key_hash_secret
func NewService(secret string) (*Service, error) {
	if secret == "" {
		return nil, fmt.Errorf("encryption secret cannot be empty")
	}

	// Derive a 32-byte key from the secret using SHA-256
	hash := sha256.Sum256([]byte(secret))

	return &Service{
		key: hash[:],
	}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM and returns base64-encoded ciphertext
func (s *Service) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Create nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and append nonce to ciphertext
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Encode to base64 for storage
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext using AES-256-GCM
func (s *Service) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	// Decode from base64
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	// Extract nonce and ciphertext
	nonce, cipherData := data[:nonceSize], data[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, cipherData, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// MustEncrypt encrypts or panics (for cases where encryption must succeed)
func (s *Service) MustEncrypt(plaintext string) string {
	encrypted, err := s.Encrypt(plaintext)
	if err != nil {
		panic(fmt.Sprintf("encryption failed: %v", err))
	}
	return encrypted
}

// MustDecrypt decrypts or panics (for cases where decryption must succeed)
func (s *Service) MustDecrypt(ciphertext string) string {
	decrypted, err := s.Decrypt(ciphertext)
	if err != nil {
		panic(fmt.Sprintf("decryption failed: %v", err))
	}
	return decrypted
}
