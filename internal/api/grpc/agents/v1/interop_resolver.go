package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	a2aclient "github.com/everstacklabs/everstack/internal/a2a/client"

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

// externalRunTools keeps tenant scoping outside the model-visible schema.
type externalRunTools struct {
	service *interop.RunService
	tenant  string
}

func (r externalRunTools) Start(ctx context.Context, remote, message, key string) (string, error) {
	run, err := r.service.Start(ctx, r.tenant, remote, message, key)
	return externalRunOutput(run, err)
}
func (r externalRunTools) Read(ctx context.Context, id string, refresh, cancel bool) (string, error) {
	run, err := r.service.Read(ctx, r.tenant, id)
	if err == nil && refresh && run.RemoteTaskID != "" && !a2aclient.Terminal(run.State) {
		run, err = r.service.Refresh(ctx, r.tenant, id, cancel)
	}
	return externalRunOutput(run, err)
}
func externalRunOutput(run interop.ExternalRun, err error) (string, error) {
	if err != nil {
		// Repository/driver errors are never model-visible.
		switch {
		case errors.Is(err, interop.ErrConflict):
			return "", errors.New("idempotency key already belongs to different work")
		case errors.Is(err, interop.ErrBusy):
			return "", errors.New("run operation in progress; read this run again without starting another")
		case errors.Is(err, interop.ErrNotFound):
			return "", errors.New("saved remote or run not found")
		case errors.Is(err, interop.ErrRemoteChanged):
			return "", errors.New("remote connection changed; this run cannot be continued")
		}
		return "", errors.New("external agent operation unavailable; do not resend work with a new key")
	}
	// Bound model context while the full supported text stays in run storage.
	if len(run.Output) > 64000 {
		run.Output = run.Output[:64000] + "\n[Output truncated; read the full run in the dashboard.]"
	}
	raw, err := json.Marshal(run)
	if err != nil {
		return "", fmt.Errorf("encode external run: %w", err)
	}
	return string(raw), nil
}
