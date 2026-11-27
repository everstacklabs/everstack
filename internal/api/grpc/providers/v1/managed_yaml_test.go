package v1

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/enterprise"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1"
)

// GetConfigYAML / SaveConfigYAML are operator tools that read and replace
// the gateway's on-disk provider file, which carries stored credentials. On
// a managed (cloud) gateway that file is shared by every tenant on the pod,
// so no tenant principal may read or write it.
func TestConfigYAML_DeniedOnManagedGateway(t *testing.T) {
	enterprise.SetManagedGateway(true)
	t.Cleanup(func() { enterprise.SetManagedGateway(false) })

	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(path, []byte("providers: [{api_key: sk-secret}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{configPath: path}

	_, err := s.GetConfigYAML(context.Background(), connect.NewRequest(&providerspb.GetConfigYAMLRequest{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("GetConfigYAML on a managed gateway: err = %v, want PermissionDenied", err)
	}

	_, err = s.SaveConfigYAML(context.Background(), connect.NewRequest(&providerspb.SaveConfigYAMLRequest{YamlContent: "providers: []\n"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("SaveConfigYAML on a managed gateway: err = %v, want PermissionDenied", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "providers: [{api_key: sk-secret}]\n" {
		t.Fatalf("SaveConfigYAML overwrote the shared file on a managed gateway: %q", got)
	}
}
