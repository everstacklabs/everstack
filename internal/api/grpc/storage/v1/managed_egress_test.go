package v1

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/storage"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	storageegress "github.com/everstacklabs/everstack/internal/storage/egress"
)

func TestS3ConnectionVerifierEnforcesManagedEgress(t *testing.T) {
	previous := enterprise.ManagedGateway()
	enterprise.SetManagedGateway(true)
	t.Cleanup(func() { enterprise.SetManagedGateway(previous) })

	err := (s3ConnectionVerifier{}).Verify(context.Background(), storage.ConnectionConfig{
		Provider: "s3",
		Endpoint: "https://127.0.0.1:9000",
		Region:   "us-east-1",
		Bucket:   "bucket",
	}, storagecredentials.ProviderCredentials{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
	})
	if !errors.Is(err, storageegress.ErrEndpointDenied) {
		t.Fatalf("managed connection verification error = %v, want ErrEndpointDenied", err)
	}
}
