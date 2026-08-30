package storage

import (
	"context"
	"io"
	"time"
)

// BucketObject holds metadata returned by a bucket listing.
type BucketObject struct {
	Key          string
	SizeBytes    int64
	LastModified time.Time
}

// ObjectStore defines the interface for S3-compatible object storage operations.
type ObjectStore interface {
	// PutPresignedURL generates a presigned URL for uploading an object.
	PutPresignedURL(ctx context.Context, bucket, key, contentType string, sizeBytes int64, expiry time.Duration) (url string, headers map[string]string, err error)

	// GetPresignedURL generates a presigned URL for downloading an object.
	GetPresignedURL(ctx context.Context, bucket, key string, expiry time.Duration) (url string, err error)

	// Put uploads an object directly (server-side, no presigned URL).
	Put(ctx context.Context, bucket, key, contentType string, body io.Reader) (etag string, err error)

	// Delete removes an object from storage.
	Delete(ctx context.Context, bucket, key string) error

	// Head checks if an object exists and returns its size.
	Head(ctx context.Context, bucket, key string) (sizeBytes int64, contentType string, err error)

	// List returns all objects in the bucket under the given prefix.
	List(ctx context.Context, bucket, prefix string) ([]BucketObject, error)
}
