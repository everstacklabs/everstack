package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	storagequery "github.com/everstacklabs/everstack/internal/query/handlers/storage"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/pkg/authz"
	storagepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1"
)

type inertObjectStore struct{}

type leakingObjectStore struct{ inertObjectStore }

func (leakingObjectStore) Put(context.Context, string, string, string, io.Reader) (string, error) {
	return "", errors.New("Authorization: raw-secret X-Amz-Signature=signed-query-value")
}

func (inertObjectStore) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "https://upload.example", nil, nil
}

func (inertObjectStore) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "https://download.example", nil
}

func (inertObjectStore) Put(context.Context, string, string, string, io.Reader) (string, error) {
	return "etag", nil
}

func (inertObjectStore) Delete(context.Context, string, string) error { return nil }

func (inertObjectStore) Head(context.Context, string, string) (int64, string, error) {
	return 0, "", nil
}

func (inertObjectStore) List(context.Context, string, string) ([]storagepkg.BucketObject, error) {
	return nil, nil
}

func TestInternalUploadResponseDoesNotExposeProviderTraffic(t *testing.T) {
	server := CreateServerWithSecurityDeps(context.Background(), leakingObjectStore{}, nil, nil, nil)
	ctx := authenticatedStorageContext(string(authz.RoleOwner))

	_, err := server.UploadObject(ctx, "tenant-1", "artifact", "file.txt", "text/plain", strings.NewReader("data"), 4, "", "")
	if err == nil {
		t.Fatal("UploadObject() error = nil")
	}
	for _, forbidden := range []string{"raw-secret", "signed-query-value", "Authorization:", "X-Amz-Signature="} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("UploadObject() error exposes %q: %v", forbidden, err)
		}
	}
}

type emptyStorageConfigListHandler struct{}

func (emptyStorageConfigListHandler) QueryType() string { return "ListStorageConfigs" }

func (emptyStorageConfigListHandler) Handle(context.Context, query.Query) (interface{}, error) {
	return []storagequery.StorageConfigReadModel{}, nil
}

func authenticatedStorageContext(role string) context.Context {
	ctx := contextkeys.WithTenantAuthenticated(context.Background())
	ctx = contextkeys.WithTenantID(ctx, "tenant-1")
	if role != "" {
		ctx = contextkeys.WithUserRole(ctx, role)
	}
	return ctx
}

func TestConfigureStorageRejectsUnauthenticatedTenantSources(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		req  *storagepb.ConfigureStorageRequest
	}{
		{
			name: "request body tenant",
			ctx:  context.Background(),
			req:  &storagepb.ConfigureStorageRequest{TenantId: "attacker-chosen-tenant"},
		},
		{
			name: "unverified context tenant",
			ctx:  contextkeys.WithTenantID(context.Background(), "injected-tenant"),
			req:  &storagepb.ConfigureStorageRequest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := CreateServer()
			_, err := server.ConfigureStorage(tt.ctx, connect.NewRequest(tt.req))
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("ConfigureStorage() code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodeUnauthenticated, err)
			}
		})
	}
}

func TestStorageRPCsRejectBodyTenantWithoutAuthenticatedContext(t *testing.T) {
	tests := []struct {
		name string
		call func(*Server) error
	}{
		{
			name: "get storage config",
			call: func(s *Server) error {
				_, err := s.GetStorageConfig(context.Background(), connect.NewRequest(&storagepb.GetStorageConfigRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "list storage configs",
			call: func(s *Server) error {
				_, err := s.ListStorageConfigs(context.Background(), connect.NewRequest(&storagepb.ListStorageConfigsRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "update storage config",
			call: func(s *Server) error {
				_, err := s.UpdateStorageConfig(context.Background(), connect.NewRequest(&storagepb.UpdateStorageConfigRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "delete storage config",
			call: func(s *Server) error {
				_, err := s.DeleteStorageConfig(context.Background(), connect.NewRequest(&storagepb.DeleteStorageConfigRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "get presigned upload URL",
			call: func(s *Server) error {
				_, err := s.GetPresignedUploadURL(context.Background(), connect.NewRequest(&storagepb.GetPresignedUploadURLRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "complete upload",
			call: func(s *Server) error {
				_, err := s.CompleteUpload(context.Background(), connect.NewRequest(&storagepb.CompleteUploadRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "get presigned download URL",
			call: func(s *Server) error {
				_, err := s.GetPresignedDownloadURL(context.Background(), connect.NewRequest(&storagepb.GetPresignedDownloadURLRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "delete object",
			call: func(s *Server) error {
				_, err := s.DeleteObject(context.Background(), connect.NewRequest(&storagepb.DeleteObjectRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "list objects",
			call: func(s *Server) error {
				_, err := s.ListObjects(context.Background(), connect.NewRequest(&storagepb.ListObjectsRequest{TenantId: "body-tenant"}))
				return err
			},
		},
		{
			name: "get storage usage",
			call: func(s *Server) error {
				_, err := s.GetStorageUsage(context.Background(), connect.NewRequest(&storagepb.GetStorageUsageRequest{TenantId: "body-tenant"}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(CreateServer())
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("RPC code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodeUnauthenticated, err)
			}
		})
	}
}

func TestStorageRPCsEnforceRolePermissions(t *testing.T) {
	t.Run("member cannot manage connections", func(t *testing.T) {
		ctx := authenticatedStorageContext(string(authz.RoleMember))
		_, err := CreateServer().ConfigureStorage(ctx, connect.NewRequest(&storagepb.ConfigureStorageRequest{}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("ConfigureStorage() code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
		}
	})

	t.Run("viewer cannot write objects", func(t *testing.T) {
		ctx := authenticatedStorageContext(string(authz.RoleViewer))
		_, err := CreateServer().CompleteUpload(ctx, connect.NewRequest(&storagepb.CompleteUploadRequest{}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("CompleteUpload() code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
		}
	})

	t.Run("viewer can read connections", func(t *testing.T) {
		queryBus := query.NewQueryBus()
		queryBus.RegisterHandler(emptyStorageConfigListHandler{})
		ctx := cqrs.WithSystem(authenticatedStorageContext(string(authz.RoleViewer)), &cqrs.System{QueryBus: queryBus})

		resp, err := CreateServer().ListStorageConfigs(ctx, connect.NewRequest(&storagepb.ListStorageConfigsRequest{}))
		if err != nil {
			t.Fatalf("ListStorageConfigs() error = %v", err)
		}
		if got := len(resp.Msg.GetConfigs()); got != 0 {
			t.Fatalf("ListStorageConfigs() returned %d configs, want 0", got)
		}
	})

	t.Run("unknown role fails closed", func(t *testing.T) {
		ctx := authenticatedStorageContext("superadmin")
		_, err := CreateServer().ListStorageConfigs(ctx, connect.NewRequest(&storagepb.ListStorageConfigsRequest{}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("ListStorageConfigs() code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
		}
	})

	t.Run("roleless marker is not a credential", func(t *testing.T) {
		ctx := authenticatedStorageContext("")
		_, err := CreateServer().ListStorageConfigs(ctx, connect.NewRequest(&storagepb.ListStorageConfigsRequest{}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("ListStorageConfigs() code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
		}
	})

	t.Run("tenant API key can manage connections", func(t *testing.T) {
		ctx := contextkeys.WithAPIKeyHash(authenticatedStorageContext(""), "verified-key-hash")
		_, err := CreateServer().ConfigureStorage(ctx, connect.NewRequest(&storagepb.ConfigureStorageRequest{}))
		if connect.CodeOf(err) == connect.CodePermissionDenied || connect.CodeOf(err) == connect.CodeUnauthenticated {
			t.Fatalf("ConfigureStorage() rejected authenticated API key: %v", err)
		}
	})
}

func TestStorageHTTPHandlersAuthorizeBeforeParsingOrDependencies(t *testing.T) {
	t.Run("upload ignores form tenant without authenticated context", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("tenant_id", "attacker-chosen-tenant"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		res := httptest.NewRecorder()

		CreateServer().UploadProxyHandler().ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("upload status = %d, want %d (body=%q)", res.Code, http.StatusUnauthorized, res.Body.String())
		}
	})

	t.Run("sync ignores JSON tenant without authenticated context", func(t *testing.T) {
		body, err := json.Marshal(map[string]string{"tenant_id": "attacker-chosen-tenant"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/sync", bytes.NewReader(body))
		res := httptest.NewRecorder()

		CreateServer().SyncHandler().ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("sync status = %d, want %d (body=%q)", res.Code, http.StatusUnauthorized, res.Body.String())
		}
	})

	t.Run("member cannot run administrative sync", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/sync", nil)
		req = req.WithContext(authenticatedStorageContext(string(authz.RoleMember)))
		res := httptest.NewRecorder()

		CreateServer().SyncHandler().ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("sync status = %d, want %d (body=%q)", res.Code, http.StatusForbidden, res.Body.String())
		}
	})

	t.Run("member may reach upload validation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload", nil)
		req = req.WithContext(authenticatedStorageContext(string(authz.RoleMember)))
		res := httptest.NewRecorder()

		CreateServer().UploadProxyHandler().ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("upload status = %d, want %d (body=%q)", res.Code, http.StatusBadRequest, res.Body.String())
		}
	})
}

func TestInternalStorageHelpersRequireMatchingAuthorizedTenant(t *testing.T) {
	server := CreateServerWithDeps(context.Background(), inertObjectStore{}, nil)
	memberCtx := authenticatedStorageContext(string(authz.RoleMember))

	if server.HasStorageConfig(context.Background(), "tenant-1") {
		t.Fatal("HasStorageConfig() = true for unauthenticated context")
	}
	if server.HasStorageConfig(memberCtx, "tenant-2") {
		t.Fatal("HasStorageConfig() = true for a cross-tenant argument")
	}
	if !server.HasStorageConfig(memberCtx, "tenant-1") {
		t.Fatal("HasStorageConfig() = false for matching authorized tenant")
	}

	_, err := server.UploadObject(memberCtx, "tenant-2", "artifact", "report.txt", "text/plain", bytes.NewBufferString("secret"), 6, "run", "run-1")
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("UploadObject() code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
	}

	viewerCtx := authenticatedStorageContext(string(authz.RoleViewer))
	_, err = server.UploadObject(viewerCtx, "tenant-1", "artifact", "report.txt", "text/plain", bytes.NewBufferString("secret"), 6, "run", "run-1")
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("UploadObject() viewer code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
	}

	_, err = server.GetPresignedDownloadURLForObject(memberCtx, "tenant-2", "object-1")
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("GetPresignedDownloadURLForObject() code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
	}
}
