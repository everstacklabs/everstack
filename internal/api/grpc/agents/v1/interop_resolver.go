package v1

import (
	"context"

	"github.com/everstacklabs/everstack/internal/interop"
)

// interopRemoteResolver adapts the interop store to the tools.RemoteResolver
// interface, tenant-scoped, so the call_external_agent tool can resolve a saved
// remote agent by name.
type interopRemoteResolver struct {
	store  *interop.Store
	tenant string
}

func (r interopRemoteResolver) Resolve(ctx context.Context, name string) (endpoint, authToken string, found bool, err error) {
	ra, err := r.store.GetRemoteAgentByName(ctx, r.tenant, name)
	if err != nil {
		return "", "", false, err
	}
	if ra == nil {
		return "", "", false, nil
	}
	return ra.Endpoint, ra.AuthToken, true, nil
}
