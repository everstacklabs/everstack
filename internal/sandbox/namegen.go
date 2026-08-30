package sandbox

import (
	"fmt"
	"math/rand"
)

// adjectives is a curated list of short, memorable adjectives for sandbox names.
var adjectives = []string{
	"bold", "calm", "cool", "cozy", "dark", "deep", "epic", "fair", "fast",
	"firm", "fond", "free", "glad", "gold", "good", "hazy", "icy", "keen",
	"kind", "lazy", "lean", "lime", "live", "loud", "lush", "mild", "neat",
	"nice", "numb", "odd", "pale", "pink", "pure", "rare", "raw", "red",
	"rich", "ripe", "rosy", "safe", "slim", "soft", "tame", "tidy", "tiny",
	"trim", "true", "vast", "warm", "wide", "wild", "wise", "zany", "zen",
	"apt", "big", "dry", "fit", "hip", "hot", "lit", "new", "old", "shy",
	"sly", "top", "wry",
}

// nouns is a curated list of short, memorable nouns for sandbox names.
var nouns = []string{
	"ace", "ant", "ape", "arc", "ark", "ash", "axe", "bay", "bee", "bit",
	"bow", "box", "bud", "bug", "bus", "cap", "cat", "cog", "cow", "cub",
	"cup", "dam", "den", "dew", "dot", "dune", "elk", "elm", "emu", "eve",
	"eye", "fawn", "fig", "fin", "fly", "fog", "fox", "frog", "gem", "gnu",
	"hare", "hawk", "hex", "hive", "hog", "hub", "ice", "imp", "ink", "ion",
	"iris", "ivy", "jar", "jay", "jet", "koi", "lark", "leaf", "lime", "lion",
	"lynx", "mage", "mars", "mesh", "mint", "mist", "mole", "moon", "moth",
	"muse", "newt", "node", "nova", "oak", "opal", "orb", "orca", "owl",
	"paw", "peak", "pine", "plum", "pony", "pond", "puma", "quay", "rain",
	"ram", "ray", "reef", "rook", "rose", "ruby", "sage", "seal", "seed",
	"snow", "star", "swan", "teal", "tern", "tide", "toad", "tree", "vale",
	"veil", "vine", "wasp", "wave", "web", "wick", "wind", "wolf", "wren",
	"yak", "yew", "zap",
}

// GenerateName produces a random adjective-noun name like "bold-fox".
func GenerateName() string {
	a := adjectives[rand.Intn(len(adjectives))]
	n := nouns[rand.Intn(len(nouns))]
	return fmt.Sprintf("%s-%s", a, n)
}

// GenerateUniqueName produces a name not present in the given set.
// Falls back to appending a random suffix after a few tries.
func GenerateUniqueName(existing map[string]struct{}) string {
	for i := 0; i < 50; i++ {
		name := GenerateName()
		if _, ok := existing[name]; !ok {
			return name
		}
	}
	// Extremely unlikely fallback: append random digits
	return fmt.Sprintf("%s-%d", GenerateName(), rand.Intn(9000)+1000)
}
