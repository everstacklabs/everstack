package v1

import (
	"testing"

	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
)

// The OpenAI-compatible /v1 surface accepts an issued key as Bearer by its
// prefix. Every key this generator mints has to match, or a freshly created
// key stops working with the OpenAI SDK.
func TestGeneratedKeysCarryAnIssuedPrefix(t *testing.T) {
	for _, acct := range []string{svcAcctPrefix, usrAcctPrefix} {
		key := acct + apiVersion + "-" + "c2VjcmV0"
		if !apikeylib.HasIssuedKeyPrefix(key) {
			t.Fatalf("generated key %q is not recognised as an issued key", key)
		}
	}
}
