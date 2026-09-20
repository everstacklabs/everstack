package openaiapi

import (
	"context"
	"errors"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	artifactpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/artifacts"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/jmoiron/sqlx"
)

type adapterAuthObjects struct{}

func (adapterAuthObjects) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, nil
}
func (adapterAuthObjects) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}
func (adapterAuthObjects) Put(context.Context, string, string, string, io.Reader) (string, error) {
	return "", nil
}
func (adapterAuthObjects) Delete(context.Context, string, string) error { return nil }
func (adapterAuthObjects) Head(context.Context, string, string) (int64, string, error) {
	return 0, "", nil
}
func (adapterAuthObjects) List(context.Context, string, string) ([]storage.BucketObject, error) {
	return nil, nil
}

func deviceArtifactContext(o Ownership, role string) context.Context {
	ctx := contextkeys.WithTenantID(context.Background(), o.InstanceID)
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	ctx = contextkeys.WithUserID(ctx, o.PrincipalID)
	ctx = contextkeys.WithUserRole(ctx, role)
	return contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{
		OrganizationID: o.OrganizationID,
		InstanceID:     o.InstanceID,
	})
}

func TestArtifactAdapterAuthorizesOrganizationThroughVerifiedInstanceScope(t *testing.T) {
	o := Ownership{OrganizationID: "organization-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "member"}
	session := &Session{ID: "session-a", SandboxID: "sandbox-a", Environment: EnvironmentSpec{Type: "self_hosted"}, CleanupStatus: "active"}
	sessions := &restartStore{owner: o, session: session}
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	db := sqlx.NewDb(rawDB, "sqlmock")
	query := regexp.QuoteMeta(`SELECT id, key, filename, content_type, size_bytes, checksum_sha256, COALESCE(metadata->>'openai_file_kind', purpose) AS purpose, created_at
 FROM object_storage_objects WHERE tenant_id = $1 AND config_id = $2 AND reference_type = $3 AND reference_id = $4
 AND metadata->>'openai_instance_id' = $5 AND metadata->>'openai_sandbox_id' = $6 AND deleted_at IS NULL ORDER BY created_at DESC`)
	mock.ExpectQuery(query).
		WithArgs(o.OrganizationID, "config-a", "openai_agents_session", session.ID, o.InstanceID, session.SandboxID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "key", "filename", "content_type", "size_bytes", "checksum_sha256", "purpose", "created_at"}))
	store := artifactpkg.NewFixedStore(db, adapterAuthObjects{}, nil, nil, "bucket-a", "config-a")
	adapter := ArtifactAdapter{Store: store, Sessions: sessions}

	got, err := adapter.List(deviceArtifactContext(o, o.Role), o, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("artifacts = %+v, want none", got)
	}
	mock.ExpectQuery(query).
		WithArgs(o.OrganizationID, "config-a", "openai_agents_session", session.ID, o.InstanceID, session.SandboxID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "key", "filename", "content_type", "size_bytes", "checksum_sha256", "purpose", "created_at"}))
	if err := adapter.Cleanup(deviceArtifactContext(o, o.Role), o, session.ID); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactAdapterRejectsMismatchedVerifiedInstanceScope(t *testing.T) {
	o := Ownership{OrganizationID: "organization-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "member"}
	sessions := &restartStore{owner: o, session: &Session{ID: "session-a", SandboxID: "sandbox-a", Environment: EnvironmentSpec{Type: "self_hosted"}, CleanupStatus: "active"}}
	adapter := ArtifactAdapter{Store: artifactpkg.NewFixedStore(nil, adapterAuthObjects{}, nil, nil, "bucket-a", "config-a"), Sessions: sessions}
	ctx := deviceArtifactContext(o, o.Role)
	ctx = contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{OrganizationID: "organization-b", InstanceID: o.InstanceID})

	if _, err := adapter.List(ctx, o, "session-a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("List() error = %v, want ErrForbidden", err)
	}
}

func TestArtifactStorageContextRejectsForeignInstanceAndMissingEvidence(t *testing.T) {
	o := Ownership{OrganizationID: "organization-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "member"}
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{
			name: "same organization foreign instance",
			ctx: contextkeys.WithRequestInstanceScope(
				deviceArtifactContext(o, o.Role),
				contextkeys.RequestInstanceScope{OrganizationID: o.OrganizationID, InstanceID: "instance-b"},
			),
		},
		{
			name: "verified principal missing instance scope",
			ctx: func() context.Context {
				ctx := contextkeys.WithTenantID(context.Background(), o.InstanceID)
				ctx = contextkeys.WithTenantAuthenticated(ctx)
				return contextkeys.WithUserID(ctx, o.PrincipalID)
			}(),
		},
		{
			name: "instance scope missing verified principal",
			ctx: contextkeys.WithRequestInstanceScope(
				contextkeys.WithTenantID(contextkeys.WithTenantAuthenticated(context.Background()), o.InstanceID),
				contextkeys.RequestInstanceScope{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID},
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := storageContext(test.ctx, o); !errors.Is(err, ErrForbidden) {
				t.Fatalf("storageContext() error = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestArtifactStorageContextAllowsVerifiedSingleTenantWithoutInstanceScope(t *testing.T) {
	o := Ownership{OrganizationID: "tenant-a", InstanceID: "tenant-a", PrincipalID: "user-a", Role: "member"}
	ctx := contextkeys.WithTenantID(context.Background(), o.OrganizationID)
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	ctx = contextkeys.WithUserID(ctx, o.PrincipalID)
	ctx = contextkeys.WithUserRole(ctx, o.Role)

	storageCtx, err := storageContext(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if tenant, err := storageauth.AuthorizeTenant(storageCtx, storageauth.ActionObjectList, o.OrganizationID); err != nil || tenant != o.OrganizationID {
		t.Fatalf("AuthorizeTenant() tenant = %q, error = %v", tenant, err)
	}
}

func TestArtifactAdapterPreservesReaderDeleteDenial(t *testing.T) {
	o := Ownership{OrganizationID: "organization-a", InstanceID: "instance-a", PrincipalID: "user-a", Role: "viewer"}
	sessions := &restartStore{owner: o, session: &Session{ID: "session-a", SandboxID: "sandbox-a", Environment: EnvironmentSpec{Type: "self_hosted"}, CleanupStatus: "active"}}
	adapter := ArtifactAdapter{Store: artifactpkg.NewFixedStore(nil, adapterAuthObjects{}, nil, nil, "bucket-a", "config-a"), Sessions: sessions}

	err := adapter.Delete(deviceArtifactContext(o, o.Role), o, "session-a", "object-a")
	if !errors.Is(err, storageauth.ErrPermissionDenied) {
		t.Fatalf("Delete() error = %v, want storage permission denial", err)
	}
}
