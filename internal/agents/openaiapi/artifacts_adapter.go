package openaiapi

import (
	"context"
	"errors"
	"time"

	artifactpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/artifacts"
	executorpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/executor"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

type ArtifactDTO struct {
	ObjectID    string    `json:"object_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type,omitempty"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	Kind        string    `json:"kind"`
	CreatedAt   time.Time `json:"created_at"`
}
type ArtifactDownload struct {
	ArtifactDTO
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}
type ArtifactController interface {
	Upload(context.Context, Ownership, string, string, string, []byte, string) (*ArtifactDTO, error)
	List(context.Context, Ownership, string) ([]ArtifactDTO, error)
	Export(context.Context, Ownership, string, string) (*ArtifactDTO, error)
	Download(context.Context, Ownership, string, string) (*ArtifactDownload, error)
	Delete(context.Context, Ownership, string, string) error
	Cleanup(context.Context, Ownership, string) error
}
type ArtifactAdapter struct {
	Store    *artifactpkg.Store
	Executor *executorpkg.Manager
	Sessions Store
}

// storageContext translates the verified host-scoped tenant into the owning
// organization used by storage rows. It preserves the authenticated principal
// and role so storageauth still performs its normal permission check.
func storageContext(ctx context.Context, o Ownership) (context.Context, error) {
	if !contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return nil, ErrForbidden
	}
	tenantID := contextkeys.GetTenantID(ctx)
	requestScope, hasRequestScope := contextkeys.RequestInstanceScopeFromContext(ctx)
	if hasRequestScope {
		if requestScope.OrganizationID != o.OrganizationID || requestScope.InstanceID != o.InstanceID ||
			(tenantID != requestScope.InstanceID && tenantID != requestScope.OrganizationID) {
			return nil, ErrForbidden
		}
	} else if tenantID != o.OrganizationID || o.InstanceID != tenantID {
		return nil, ErrForbidden
	}
	return contextkeys.WithTenantID(ctx, o.OrganizationID), nil
}

func (a ArtifactAdapter) scope(ctx context.Context, o Ownership, id string) (artifactpkg.Scope, error) {
	s, e := a.scopeAny(ctx, o, id)
	if e != nil {
		return s, e
	}
	x, e := a.Sessions.GetSession(ctx, o, id)
	if e != nil {
		return s, e
	}
	if x.CleanupStatus != "active" {
		return artifactpkg.Scope{}, ErrInvalid
	}
	return s, nil
}
func (a ArtifactAdapter) scopeAny(ctx context.Context, o Ownership, id string) (artifactpkg.Scope, error) {
	x, e := a.Sessions.GetSession(ctx, o, id)
	if e != nil {
		return artifactpkg.Scope{}, e
	}
	if x.Environment.Type != "self_hosted" || x.SandboxID == "" {
		return artifactpkg.Scope{}, ErrInvalid
	}
	return artifactpkg.Scope{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID, SessionID: id, SandboxID: x.SandboxID}, nil
}
func dto(a artifactpkg.RetainedArtifact) *ArtifactDTO {
	kind := a.Kind
	if kind == "artifact" {
		kind = "output"
	}
	return &ArtifactDTO{ObjectID: a.ObjectID, Filename: a.Filename, ContentType: a.ContentType, SizeBytes: a.SizeBytes, SHA256: a.SHA256, Kind: kind, CreatedAt: a.CreatedAt}
}
func (a ArtifactAdapter) Upload(ctx context.Context, o Ownership, id, name, ct string, b []byte, dest string) (*ArtifactDTO, error) {
	s, e := a.scope(ctx, o, id)
	if e != nil {
		return nil, e
	}
	ctx, e = storageContext(ctx, o)
	if e != nil {
		return nil, e
	}
	v, e := a.Store.UploadInput(ctx, s, name, ct, b)
	if e == nil && dest != "" {
		_, e = a.Executor.AttachInputReference(ctx, executorpkg.Ownership{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID}, id, s.SandboxID, v.ObjectID, dest, a.Store)
		if e != nil {
			if cleanupErr := a.Store.Delete(ctx, s, v.ObjectID); cleanupErr != nil {
				return nil, errors.Join(e, cleanupErr)
			}
			return nil, e
		}
	}
	if e != nil {
		return nil, e
	}
	return dto(*v), nil
}
func (a ArtifactAdapter) Cleanup(ctx context.Context, o Ownership, id string) error {
	s, e := a.scopeAny(ctx, o, id)
	if e != nil {
		return e
	}
	ctx, e = storageContext(ctx, o)
	if e != nil {
		return e
	}
	return a.Store.DeleteAll(ctx, s)
}
func (a ArtifactAdapter) List(ctx context.Context, o Ownership, id string) ([]ArtifactDTO, error) {
	s, e := a.scope(ctx, o, id)
	if e != nil {
		return nil, e
	}
	ctx, e = storageContext(ctx, o)
	if e != nil {
		return nil, e
	}
	v, e := a.Store.List(ctx, s)
	out := make([]ArtifactDTO, len(v))
	for i := range v {
		out[i] = *dto(v[i])
	}
	return out, e
}
func (a ArtifactAdapter) Export(ctx context.Context, o Ownership, id, path string) (*ArtifactDTO, error) {
	s, e := a.scope(ctx, o, id)
	if e != nil {
		return nil, e
	}
	ctx, e = storageContext(ctx, o)
	if e != nil {
		return nil, e
	}
	v, e := a.Executor.ExportWorkspaceFileReference(ctx, executorpkg.Ownership{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID}, id, s.SandboxID, path, a.Store)
	if e != nil {
		return nil, e
	}
	list, e := a.List(ctx, o, id)
	if e != nil {
		return nil, e
	}
	for _, x := range list {
		if x.ObjectID == v.ObjectID {
			return &x, nil
		}
	}
	return nil, ErrNotFound
}
func (a ArtifactAdapter) Download(ctx context.Context, o Ownership, id, obj string) (*ArtifactDownload, error) {
	s, e := a.scope(ctx, o, id)
	if e != nil {
		return nil, e
	}
	ctx, e = storageContext(ctx, o)
	if e != nil {
		return nil, e
	}
	v, e := a.Store.Download(ctx, s, obj, 15*time.Minute)
	if e != nil {
		return nil, e
	}
	return &ArtifactDownload{ArtifactDTO: *dto(v.RetainedArtifact), URL: v.URL, ExpiresAt: v.ExpiresAt}, nil
}
func (a ArtifactAdapter) Delete(ctx context.Context, o Ownership, id, obj string) error {
	s, e := a.scope(ctx, o, id)
	if e != nil {
		return e
	}
	ctx, e = storageContext(ctx, o)
	if e != nil {
		return e
	}
	return a.Store.Delete(ctx, s, obj)
}
