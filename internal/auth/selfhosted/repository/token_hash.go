package repository

import (
	"crypto/sha256"
	"encoding/hex"
)

// hashToken returns the hex SHA-256 of an emailed bearer token (magic link,
// invitation). Only the hash is stored: the raw token travels in the link
// alone, so a read of the table yields nothing that signs anyone in. The
// tokens carry 256 bits of randomness, so an unsalted fast hash is enough.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
