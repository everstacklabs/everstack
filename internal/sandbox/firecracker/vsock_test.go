package firecracker

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestToolboxMountsPreserveScopedCredentials(t *testing.T) {
	mounts := toolboxMounts([]sandbox.StorageMountConfig{{
		Type:            "r2",
		Bucket:          "bucket-a",
		MountPath:       "/mnt/data",
		Endpoint:        "https://r2.example",
		SubPath:         "tenant-a",
		ReadOnly:        true,
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		SessionToken:    "session-token",
	}})
	if len(mounts) != 1 {
		t.Fatalf("len(mounts) = %d, want 1", len(mounts))
	}
	got := mounts[0]
	if got.Type != "r2" || got.Bucket != "bucket-a" || got.MountPath != "/mnt/data" || got.Endpoint != "https://r2.example" || got.SubPath != "tenant-a" || !got.ReadOnly {
		t.Fatalf("unexpected mount metadata: %+v", got)
	}
	if got.AccessKeyID != "access-key" || got.SecretAccessKey != "secret-key" || got.SessionToken != "session-token" {
		t.Fatalf("scoped credentials not preserved: %+v", got)
	}
}
