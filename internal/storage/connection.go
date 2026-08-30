package storage

import (
	"context"

	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
)

// ConnectionConfig contains non-secret provider connection metadata.
type ConnectionConfig struct {
	Provider       string
	Endpoint       string
	Region         string
	Bucket         string
	PathPrefix     string
	ForcePathStyle bool
}

// ConnectionVerifier validates provider credentials without persisting them or
// writing customer data.
type ConnectionVerifier interface {
	Verify(ctx context.Context, config ConnectionConfig, credentials storagecredentials.ProviderCredentials) error
}
