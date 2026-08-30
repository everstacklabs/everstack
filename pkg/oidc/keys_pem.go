package oidc

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// LoadKeySetFromPEM builds a KeySet from a PEM-encoded RSA private key
// (PKCS#8 or PKCS#1). Use this to pin the OP signing key from a sealed secret
// so tokens survive restarts and every replica signs with the same key.
func LoadKeySetFromPEM(pemBytes []byte) (*KeySet, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("oidc: no PEM block found in signing key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("oidc: PKCS#8 key is not RSA")
		}
		return NewKeySetFromPrivateKey(rsaKey), nil
	}
	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("oidc: parse RSA private key: %w", err)
	}
	return NewKeySetFromPrivateKey(rsaKey), nil
}
