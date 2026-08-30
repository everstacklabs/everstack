package hosting

import "context"

// DirectFile is one file of an in-process publish (no presigned URLs; the
// server writes to object storage directly). Used by the agent runtime's
// publish_site tool.
type DirectFile struct {
	Path        string
	Content     []byte
	ContentType string
}

// Publisher publishes a set of files as a live static site and returns its
// URL. Implemented by the hosting service; consumed by the agent runtime so
// the tools package does not depend on the API layer.
type Publisher interface {
	PublishDirect(ctx context.Context, tenantID, slug string, spaFallback bool, files []DirectFile) (url string, err error)
}

// SiteInfo is a compact, tenant-safe view of a hosted site for listing.
type SiteInfo struct {
	Slug       string `json:"slug"`
	URL        string `json:"url"`
	Status     string `json:"status"`
	FileCount  int32  `json:"file_count"`
	TotalBytes int64  `json:"total_bytes"`
}

// SiteLister lists the sites owned by a tenant. Kept separate from Publisher
// so callers that only publish (the agent runtime tool) do not depend on it.
type SiteLister interface {
	ListSitesForTenant(ctx context.Context, tenantID string) ([]SiteInfo, error)
}
