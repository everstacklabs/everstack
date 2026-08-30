package hosting

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

var slugAdjectives = []string{
	"amber", "bold", "brisk", "calm", "clear", "crisp", "eager", "fair",
	"fleet", "glad", "keen", "kind", "lively", "lunar", "mellow", "noble",
	"quick", "quiet", "rapid", "solar", "spry", "still", "sunny", "swift",
	"tidal", "vivid", "warm", "wise", "witty", "zesty",
}

var slugNouns = []string{
	"aspen", "atlas", "basin", "birch", "bloom", "brook", "cedar", "cliff",
	"comet", "coral", "crane", "delta", "dune", "ember", "fjord", "gale",
	"grove", "harbor", "heron", "lagoon", "maple", "meadow", "mesa", "otter",
	"pine", "reef", "ridge", "river", "spark", "wren",
}

const slugSuffixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// GenerateSlug returns a random human-friendly slug like "swift-heron-x4t9".
// Collisions are handled by the caller (retry on unique violation).
func GenerateSlug() (string, error) {
	adj, err := pick(slugAdjectives)
	if err != nil {
		return "", err
	}
	noun, err := pick(slugNouns)
	if err != nil {
		return "", err
	}
	suffix := make([]byte, 4)
	for i := range suffix {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(slugSuffixAlphabet))))
		if err != nil {
			return "", err
		}
		suffix[i] = slugSuffixAlphabet[n.Int64()]
	}
	return fmt.Sprintf("%s-%s-%s", adj, noun, string(suffix)), nil
}

func pick(list []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
	if err != nil {
		return "", err
	}
	return list[n.Int64()], nil
}
