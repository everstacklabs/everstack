package oidc

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// VerifyPKCE checks a code_verifier against the stored code_challenge per
// RFC 7636. Only S256 and plain are supported; an empty method defaults to
// plain. S256 is required for confidential instance clients in production.
func VerifyPKCE(method, challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	switch method {
	case "S256":
		sum := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
	case "plain", "":
		return subtle.ConstantTimeCompare([]byte(verifier), []byte(challenge)) == 1
	default:
		return false
	}
}
