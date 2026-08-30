package hosting

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Manifest is the per-site pointer document the sites-worker reads from
// R2 at sites/{slug}/manifest.json. Finalize swaps it in a single PUT, so
// readers always see a complete version. Field names are part of the
// contract with infra/cloudflare/sites-worker.
type Manifest struct {
	SiteID               string                   `json:"siteId"`
	Slug                 string                   `json:"slug"`
	Version              int32                    `json:"version"`
	Status               string                   `json:"status"` // "active" or any non-serving state
	ModerationGeneration int64                    `json:"moderationGeneration"`
	SPAFallback          bool                     `json:"spaFallback"`
	Access               string                   `json:"access"`  // "public" | "noindex"
	NoIndex              bool                     `json:"noindex"` // always true for anonymous sites
	ExpiresAt            *time.Time               `json:"expiresAt,omitempty"`
	Files                map[string]ManifestEntry `json:"files"`
}

// ManifestEntry describes one servable file. Keys of Manifest.Files are
// site-absolute paths ("/index.html").
type ManifestEntry struct {
	Key          string `json:"key"` // R2 object key
	ContentType  string `json:"contentType"`
	SizeBytes    int64  `json:"size"`
	SHA256       string `json:"sha256,omitempty"`
	CacheControl string `json:"cacheControl,omitempty"`
}

// ObjectKey returns the R2 key for a file within a site version.
func ObjectKey(slug string, version int32, path string) string {
	return fmt.Sprintf("sites/%s/v%d/%s", slug, version, path)
}

// ManifestKey returns the R2 key of a site's manifest pointer.
func ManifestKey(slug string) string {
	return fmt.Sprintf("sites/%s/manifest.json", slug)
}

// NewToken returns a 32-byte random token, hex encoded. Used for finalize
// and claim tokens; only the sha256 of a token is persisted.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
