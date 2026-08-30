package utils

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateRandomString returns a cryptographically random hex string of the given byte length.
// The returned string is 2*n characters long.
func GenerateRandomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
