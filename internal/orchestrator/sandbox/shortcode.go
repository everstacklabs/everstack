package lifecycle

import (
	"crypto/rand"
	"fmt"
)

// shortCodeAlphabet excludes visually ambiguous chars (0/O/1/l/I) so a
// user copy-pasting from a terminal can't typo their way into someone
// else's sandbox. 49 chars × 8 positions ≈ 3.3e13 keyspace; collision
// probability is negligible at any realistic sandbox count, but
// InsertPendingWithShortCode still retries on the rare hit.
const shortCodeAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// shortCodeLength is the rendered length of a sandbox short code. 8
// chars matches what bitly / nanoid users expect and stays under the
// SSH username and DNS label limits with room to spare.
const shortCodeLength = 8

// GenerateShortCode returns a fresh random short code. Uses crypto/rand
// rather than math/rand so the value is unguessable by external callers
// — anyone who can guess a code can target an SSH login at it, so the
// keyspace must be unpredictable, not just unique.
func GenerateShortCode() (string, error) {
	buf := make([]byte, shortCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("shortcode: read random: %w", err)
	}
	out := make([]byte, shortCodeLength)
	for i, b := range buf {
		out[i] = shortCodeAlphabet[int(b)%len(shortCodeAlphabet)]
	}
	return string(out), nil
}
