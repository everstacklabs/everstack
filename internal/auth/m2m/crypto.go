package m2m

import (
	"crypto"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
)

// Hash wrappers for RSA signature verification

func newSHA256() hash.Hash {
	return sha256.New()
}

func newSHA384() hash.Hash {
	return sha512.New384()
}

func newSHA512() hash.Hash {
	return sha512.New()
}

// cryptoHashForAlg returns the crypto.Hash constant for the given algorithm.
func cryptoHashForAlg(alg string) crypto.Hash {
	switch alg {
	case "RS256":
		return crypto.SHA256
	case "RS384":
		return crypto.SHA384
	case "RS512":
		return crypto.SHA512
	default:
		return 0
	}
}
